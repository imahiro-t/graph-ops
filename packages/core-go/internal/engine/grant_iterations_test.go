package engine

import (
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// --- DFLT-00101 / BUG-14: a ticket blocked by an iteration limit had no
// sanctioned way back. ReopenNodes refuses the whole call if any node would
// pass its budget, so reopening the loop target -- which has used its last
// round (iteration_count == max_iterations-1 since DFLT-00140), that being
// why the ticket blocked -- always failed. GrantIterations raises the ceiling
// so the rest of the recovery can run. ---

// blockedAtIterationLimit drives the default workflow to a `test_review`
// failure with `impl` already out of budget, i.e. exactly the state BUG-14 is
// about: the ticket blocked, impl DONE at its ceiling, test_review left
// IN REVIEW.
func blockedAtIterationLimit(t *testing.T, e *GraphEngine, projectID string) (ticketID string, implID string, testReviewID string) {
	t.Helper()
	ticketID, _ = defaultWorkflowAtTestReview(t, e, projectID)
	impl := nodeByConfigID(t, e.repo, ticketID, "impl")
	atLimit := impl.MaxIterations - 1 // the last allowed round
	if _, err := e.repo.UpdateNode(impl.ID, store.NodePatch{IterationCount: &atLimit}); err != nil {
		t.Fatalf("UpdateNode(impl iteration_count): %v", err)
	}
	testReview := nodeByConfigID(t, e.repo, ticketID, "test_review")
	res, err := e.CompleteNode(testReview.ID, false, nil)
	if err != nil {
		t.Fatalf("CompleteNode(test_review, fail): %v", err)
	}
	if res.NextStatus != "BLOCKED" {
		t.Fatalf("precondition: expected the ticket to block, got %+v", res)
	}
	return ticketID, impl.ID, testReview.ID
}

func TestGrantIterations_RaisesBudgetAndLeavesEverythingElseAlone(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, implID, _ := blockedAtIterationLimit(t, e, projectID)
	before, _ := repo.GetNode(implID)

	// The same id twice: a duplicate must collapse, not stack two grants.
	updated, err := e.GrantIterations(ticketID, []string{implID, implID}, 2)
	if err != nil {
		t.Fatalf("GrantIterations: %v", err)
	}
	if len(updated) != 1 || updated[0].ID != implID {
		t.Fatalf("expected the one node back, got %+v", updated)
	}
	if updated[0].MaxIterations != before.MaxIterations+2 {
		t.Errorf("expected max_iterations %d, got %d", before.MaxIterations+2, updated[0].MaxIterations)
	}
	if updated[0].IterationCount != before.IterationCount {
		t.Errorf("iteration_count must not change: was %d, now %d", before.IterationCount, updated[0].IterationCount)
	}
	if updated[0].Status != before.Status {
		t.Errorf("status must not change: was %s, now %s", before.Status, updated[0].Status)
	}
	// Granting budget is not recovery on its own -- the ticket stays blocked
	// until reopen-nodes runs.
	ticket, _ := repo.GetTicket(ticketID)
	if !ticket.Blocked {
		t.Errorf("expected the ticket to stay blocked after a grant alone")
	}
}

func TestGrantIterations_RejectsBadInputWithoutWriting(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, implID, _ := blockedAtIterationLimit(t, e, projectID)

	// A node in a different ticket, to exercise the ownership check.
	other, _ := e.CreateTicket(projectID, "other", "")
	otherCat := baseCatalog(t)
	otherExec, _ := e.GetExecutableNodes(other.ID, otherCat)
	foreignID := otherExec[0].ID

	cases := []struct {
		name    string
		nodeIDs []string
		extra   int
		wantErr string
	}{
		{"zero extra", []string{implID}, 0, "at least 1"},
		{"negative extra", []string{implID}, -3, "at least 1"},
		{"absurd extra", []string{implID}, maxIterationsGrantPerCall + 1, "at most"},
		{"no ids", nil, 1, "no node ids"},
		{"blank id", []string{"   "}, 1, "empty node id"},
		{"unknown id", []string{"DOES-NOT-EXIST"}, 1, "does not belong"},
		{"foreign id", []string{foreignID}, 1, "does not belong"},
		{"one bad id among good ones", []string{implID, "DOES-NOT-EXIST"}, 1, "does not belong"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before, _ := repo.GetNode(implID)
			beforeForeign, _ := repo.GetNode(foreignID)
			beforeTicket, _ := repo.GetTicket(ticketID)

			_, err := e.GrantIterations(ticketID, tc.nodeIDs, tc.extra)
			if err == nil {
				t.Fatalf("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected the error to mention %q, got %v", tc.wantErr, err)
			}
			// Nothing written, not even for the valid id in the mixed case.
			after, _ := repo.GetNode(implID)
			if after.MaxIterations != before.MaxIterations || after.IterationCount != before.IterationCount || after.Status != before.Status {
				t.Errorf("impl must be untouched: %+v -> %+v", before, after)
			}
			afterForeign, _ := repo.GetNode(foreignID)
			if afterForeign.MaxIterations != beforeForeign.MaxIterations {
				t.Errorf("the other ticket's node must be untouched")
			}
			afterTicket, _ := repo.GetTicket(ticketID)
			if afterTicket.Blocked != beforeTicket.Blocked {
				t.Errorf("the ticket's blocked flag must be untouched")
			}
		})
	}
}

func TestGrantIterations_UnknownTicket(t *testing.T) {
	e, _, _ := newTestEngine(t)
	if _, err := e.GrantIterations("NOPE-00001", []string{"n1"}, 1); err == nil {
		t.Fatalf("expected an error for an unknown ticket")
	}
}

// TestGrantIterations_IterationLimitRecoveryPath walks the documented recovery
// end to end: reopen-nodes refuses while the budget is gone, a grant makes the
// same call succeed, and unstick-node clears the reviewer that was left
// IN REVIEW (reopen-nodes only ever touches DONE/REJECTED nodes), after which
// the ticket executes again.
func TestGrantIterations_IterationLimitRecoveryPath(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, implID, testReviewID := blockedAtIterationLimit(t, e, projectID)
	cat := baseCatalog(t)

	if _, err := e.ReopenNodes(ticketID, []string{implID}); err == nil {
		t.Fatalf("expected reopen-nodes to refuse while impl is out of budget")
	} else if !strings.Contains(err.Error(), "max_iterations") {
		t.Errorf("expected the refusal to name max_iterations, got %v", err)
	}
	if got, _ := repo.GetNode(implID); got.Status != domain.NodeDone {
		t.Fatalf("the refused reopen must have written nothing, got impl %s", got.Status)
	}

	if _, err := e.GrantIterations(ticketID, []string{implID}, 1); err != nil {
		t.Fatalf("GrantIterations: %v", err)
	}
	if _, err := e.ReopenNodes(ticketID, []string{implID}); err != nil {
		t.Fatalf("expected reopen-nodes to succeed once the budget was raised: %v", err)
	}
	ticket, _ := repo.GetTicket(ticketID)
	if ticket.Blocked {
		t.Errorf("expected reopen-nodes to clear the blocked flag")
	}
	if got, _ := repo.GetNode(implID); got.Status != domain.NodeTODO {
		t.Errorf("expected impl to be TODO after the reopen, got %s", got.Status)
	}
	// The failing reviewer is IN REVIEW, which reopen-nodes never collects --
	// hence the third step.
	if got, _ := repo.GetNode(testReviewID); got.Status != domain.NodeInReview {
		t.Fatalf("expected test_review to still be IN REVIEW, got %s", got.Status)
	}
	if _, err := e.UnstickNode(testReviewID); err != nil {
		t.Fatalf("UnstickNode(test_review): %v", err)
	}
	if got, _ := repo.GetNode(testReviewID); got.Status != domain.NodeTODO {
		t.Errorf("expected test_review to be TODO after unstick-node, got %s", got.Status)
	}

	exec, err := e.GetExecutableNodes(ticketID, cat)
	if err != nil {
		t.Fatalf("GetExecutableNodes: %v", err)
	}
	var sawImpl bool
	for _, n := range exec {
		if n.ID == implID {
			sawImpl = true
		}
	}
	if !sawImpl {
		t.Fatalf("expected the recovered ticket to offer impl again, got %v", execConfigIDs(exec))
	}
}
