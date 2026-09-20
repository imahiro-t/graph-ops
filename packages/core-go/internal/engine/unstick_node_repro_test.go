package engine

import (
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// TestStaleClaim_UnreachableViaGetExecutableNodes documents the actual root
// cause of the DFLT-00020 field report ("a review node several hops
// downstream of its loop_back_to target never gets re-offered by
// get-executable"): the topology itself is not the problem (see
// TestGetExecutableNodes_SkipLevelLoopBackRewindsIntermediateNodes below --
// a single caller driving that same topology does get the failing review
// node back, once the rework and the rewound nodes between them have
// actually re-run) -- the problem is that
// GetExecutableNodes claims (flips to IN PROGRESS/IN REVIEW) every node it
// returns in one call, and if that node is never actually completed by a
// worker (e.g. because a second, unrelated GetExecutableNodes call --
// another agent polling for context while working a different node in the
// same batch -- claims it out from under the intended dispatch), it is
// excluded from every future GetExecutableNodes call by its own claimed
// status, yet the ticket never becomes Blocked (nothing failed, nothing hit
// an iteration limit), so ReopenNodes' "must be Blocked" precondition is
// never satisfied either. This test reproduces exactly that stuck state and
// confirms UnstickNode is the sanctioned way out of it.
func TestStaleClaim_UnreachableViaGetExecutableNodes(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	e.CompleteNode(exec[0].ID, true, nil) // plan
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	e.CompleteNode(exec[0].ID, true, nil) // plan_review

	patch := &Patch{ExtraNodes: []ExtraNode{
		{ID: "plan_approval", Type: "approval_gate", Name: "Plan Approval", DependsOn: []string{"plan_review"}},
		{ID: "impl", Type: "implementation", Name: "Implementation", DependsOn: []string{"plan_approval"}},
		{ID: "gherkin_test", Type: "gherkin_test", Name: "Gherkin Test Execution", DependsOn: []string{"impl"}},
		{ID: "test_review", Type: "review", Name: "Test Result Review", DependsOn: []string{"gherkin_test"}, LoopBackTo: "impl"},
	}}
	if err := e.ExpandGraph(ticket.ID, cat, patch); err != nil {
		t.Fatalf("ExpandGraph: %v", err)
	}

	planApproval := nodeByConfigID(t, repo, ticket.ID, "plan_approval")
	e.CompleteNode(planApproval.ID, true, nil)

	exec, _ = e.GetExecutableNodes(ticket.ID, cat) // claims impl
	implID := exec[0].ID
	e.CompleteNode(implID, true, nil)

	exec, _ = e.GetExecutableNodes(ticket.ID, cat) // claims gherkin_test
	e.CompleteNode(exec[0].ID, true, nil)

	// This call claims test_review (flips it to IN REVIEW) -- simulating an
	// unrelated poll (e.g. a sibling subagent checking context) that never
	// goes on to actually work the node or call CompleteNode on it.
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	if len(exec) != 1 || exec[0].Type != domain.NodeTypeReview {
		t.Fatalf("expected test_review to be claimed by this call, got %+v", exec)
	}
	testReviewID := exec[0].ID

	// No CompleteNode call happens for test_review here -- this is the bug:
	// its claim is now permanent as far as GetExecutableNodes is concerned.
	for i := 0; i < 3; i++ {
		exec, _ = e.GetExecutableNodes(ticket.ID, cat)
		if len(exec) != 0 {
			t.Fatalf("expected nothing else executable while test_review sits claimed, got %+v", exec)
		}
	}
	stuck, _ := repo.GetNode(testReviewID)
	if stuck.Status != domain.NodeInReview {
		t.Fatalf("expected test_review to remain claimed (IN REVIEW), got %s", stuck.Status)
	}
	got, _ := repo.GetTicket(ticket.ID)
	if got.Blocked {
		t.Fatalf("ticket must not be Blocked in this scenario (nothing failed, no iteration limit hit), got Blocked=true")
	}
	if _, err := e.ReopenNodes(ticket.ID, []string{testReviewID}); err == nil {
		t.Fatalf("expected ReopenNodes to refuse (ticket is not Blocked), but it succeeded")
	}

	// The fix: UnstickNode resets the stale claim without requiring Blocked.
	unstuck, err := e.UnstickNode(testReviewID)
	if err != nil {
		t.Fatalf("UnstickNode: %v", err)
	}
	if unstuck.Status != domain.NodeTODO {
		t.Fatalf("expected test_review to be TODO after UnstickNode, got %s", unstuck.Status)
	}
	if unstuck.IterationCount != 0 {
		t.Fatalf("UnstickNode must not touch iteration_count, got %d", unstuck.IterationCount)
	}

	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	if len(exec) != 1 || exec[0].ID != testReviewID {
		t.Fatalf("expected test_review to be re-offered after UnstickNode, got %+v", exec)
	}
}

// TestGetExecutableNodes_SkipLevelLoopBackRewindsIntermediateNodes pins the
// order a skip-level loop-back (a review node several hops downstream of its
// own loop_back_to target) puts the graph back into. The failing reviewer is
// NOT executable again until the target's rework and every node the rewind
// took out with it have re-run: CompleteNode resets both the target and the
// DONE nodes on the success path between the two (here code_review and
// gherkin_test), so allNonLoopPrereqsDone keeps everything but the target
// from being handed out.
//
// Before DFLT-00101 this same graph behaved the opposite way, and this test
// asserted it as correct: only impl was reset, so gherkin_test stayed DONE and
// test_review came back in the very same GetExecutableNodes call that had just
// reset impl -- re-judging the previous run's test results before the rework
// existed, and letting code_review/gherkin_test be skipped entirely for the
// new implementation (BUG-02).
func TestGetExecutableNodes_SkipLevelLoopBackRewindsIntermediateNodes(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	e.CompleteNode(exec[0].ID, true, nil) // plan
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	e.CompleteNode(exec[0].ID, true, nil) // plan_review

	patch := &Patch{ExtraNodes: []ExtraNode{
		{ID: "plan_approval", Type: "approval_gate", Name: "Plan Approval", DependsOn: []string{"plan_review"}},
		{ID: "impl", Type: "implementation", Name: "Implementation", DependsOn: []string{"plan_approval"}},
		{ID: "code_review", Type: "review_gate", GateRef: "code_review", DependsOn: []string{"impl"}, LoopBackTo: "impl"},
		{ID: "gherkin_test", Type: "gherkin_test", Name: "Gherkin Test Execution", DependsOn: []string{"code_review"}},
		{ID: "test_review", Type: "review", Name: "Test Result Review", DependsOn: []string{"gherkin_test"}, LoopBackTo: "impl"},
	}}
	if err := e.ExpandGraph(ticket.ID, cat, patch); err != nil {
		t.Fatalf("ExpandGraph: %v", err)
	}

	planApproval := nodeByConfigID(t, repo, ticket.ID, "plan_approval")
	e.CompleteNode(planApproval.ID, true, nil)

	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	implID := exec[0].ID
	e.CompleteNode(implID, true, nil) // impl #1

	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	codeReviewID := exec[0].ID
	e.CompleteNode(codeReviewID, true, nil) // code_review

	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	gherkinTestID := exec[0].ID
	e.CompleteNode(gherkinTestID, true, nil) // gherkin_test

	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	testReviewID := exec[0].ID

	res, err := e.CompleteNode(testReviewID, false, nil) // FAIL, loop_back_to impl
	if err != nil {
		t.Fatalf("CompleteNode(test_review, fail): %v", err)
	}
	if res.NextStatus != "AWAITING FIX" || !res.LoopedBack {
		t.Fatalf("expected loop-back, got %+v", res)
	}
	if got, _ := repo.GetNode(testReviewID); got.Status != domain.NodeAwaitingFix {
		t.Fatalf("expected test_review to be AWAITING FIX right after looping back, got %s", got.Status)
	}
	if got, _ := repo.GetNode(implID); got.Status != domain.NodeTODO || got.IterationCount != 1 {
		t.Fatalf("expected impl to be reset to TODO at iteration_count 1, got %s / %d", got.Status, got.IterationCount)
	}
	// The nodes between the loop target and the failing reviewer: reset, but
	// with their own iteration counts untouched (only the target's counts).
	for _, id := range []string{codeReviewID, gherkinTestID} {
		got, _ := repo.GetNode(id)
		if got.Status != domain.NodeTODO {
			t.Errorf("expected %s (between impl and test_review) to be reset to TODO, got %s", got.Name, got.Status)
		}
		if got.IterationCount != 0 {
			t.Errorf("expected %s's iteration_count to stay 0, got %d", got.Name, got.IterationCount)
		}
	}
	// Upstream of the loop target is untouched: the rewind starts AT the
	// target, it does not walk back past it.
	if got, _ := repo.GetNode(planApproval.ID); got.Status != domain.NodeDone {
		t.Errorf("expected plan_approval (upstream of the loop target) to stay DONE, got %s", got.Status)
	}

	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	if len(exec) != 1 || exec[0].ID != implID {
		t.Fatalf("expected impl alone to be executable right after the loop-back, got %+v", exec)
	}

	// ... and the rest come back in graph order as the rework proceeds.
	e.CompleteNode(implID, true, nil) // impl #2
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	if len(exec) != 1 || exec[0].ID != codeReviewID {
		t.Fatalf("expected code_review to be re-offered after impl's rework, got %+v", exec)
	}
	e.CompleteNode(codeReviewID, true, nil)
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	if len(exec) != 1 || exec[0].ID != gherkinTestID {
		t.Fatalf("expected gherkin_test to be re-offered after code_review, got %+v", exec)
	}
	e.CompleteNode(gherkinTestID, true, nil)
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	if len(exec) != 1 || exec[0].ID != testReviewID {
		t.Fatalf("expected test_review to be re-offered last, after the re-run test it judges, got %+v", exec)
	}
	if exec[0].Status != domain.NodeInReview {
		t.Errorf("expected the re-offered test_review to be claimed IN REVIEW (from AWAITING FIX), got %s", exec[0].Status)
	}
}
