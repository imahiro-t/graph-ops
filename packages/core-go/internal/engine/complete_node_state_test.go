package engine

import (
	"errors"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// Tests for DFLT-00102 / BUG-04: CompleteNode used to close a node without
// ever looking at the state it was in, so a DONE node could be failed a second
// time (blocking a finished ticket) and a TODO node nobody had run could be
// marked DONE -- and either way the call's artifacts were already written by
// the time it might have been refused.

// assertInvalidNodeState fails the test unless err is an INVALID_NODE_STATE
// APIError. Shared with engine_test.go.
func assertInvalidNodeState(t *testing.T, err error) *domain.APIError {
	t.Helper()
	if err == nil {
		t.Fatal("expected an INVALID_NODE_STATE error, got nil")
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected a *domain.APIError, got %T: %v", err, err)
	}
	if apiErr.Code != domain.ErrCodeInvalidNodeState {
		t.Fatalf("error code = %s, want %s (%v)", apiErr.Code, domain.ErrCodeInvalidNodeState, err)
	}
	return apiErr
}

// setNodeStatus (awaiting_fix_test.go) drives a node straight to the status a
// test needs, bypassing the engine, so states that would otherwise take a
// whole run to reach -- REJECTED, AWAITING FIX, DONE -- can be set up
// directly. What is under test is what CompleteNode does when it finds a node
// sitting in one of them.

func setNodeManual(t *testing.T, repo store.GraphRepository, nodeID string, manual bool) {
	t.Helper()
	if _, err := repo.UpdateNode(nodeID, store.NodePatch{IsManual: &manual}); err != nil {
		t.Fatalf("setting node %s is_manual=%v: %v", nodeID, manual, err)
	}
}

// TestCompleteNode_StatusTable is the state table completion criterion 2 fixes:
// which (is_manual, status) pairs may complete and which are refused.
//
// The manual rows are not a nicety. A manual node is never claimed --
// GetExecutableNodes skips it and a human judges it where it sits, at TODO --
// so holding manual nodes to the automatic rule would refuse every approval
// the Web UI and `complete-node <gate> true` send, breaking the approval flow
// outright.
func TestCompleteNode_StatusTable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		manual  bool
		status  domain.NodeStatus
		allowed bool
	}{
		{"automatic/IN PROGRESS", false, domain.NodeInProgress, true},
		{"automatic/IN REVIEW", false, domain.NodeInReview, true},
		{"automatic/TODO", false, domain.NodeTODO, false},
		{"automatic/DONE", false, domain.NodeDone, false},
		{"automatic/REJECTED", false, domain.NodeRejected, false},
		{"automatic/AWAITING FIX", false, domain.NodeAwaitingFix, false},
		{"manual/TODO", true, domain.NodeTODO, true},
		{"manual/IN PROGRESS", true, domain.NodeInProgress, true},
		{"manual/IN REVIEW", true, domain.NodeInReview, true},
		{"manual/DONE", true, domain.NodeDone, false},
		{"manual/REJECTED", true, domain.NodeRejected, false},
		{"manual/AWAITING FIX", true, domain.NodeAwaitingFix, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, repo, projectID := newTestEngine(t)
			cat := baseCatalog(t)
			ticket, _ := e.CreateTicket(projectID, "title", "")
			exec, err := e.GetExecutableNodes(ticket.ID, cat)
			if err != nil {
				t.Fatalf("GetExecutableNodes: %v", err)
			}
			node := exec[0]
			setNodeManual(t, repo, node.ID, tc.manual)
			setNodeStatus(t, repo, node.ID, tc.status)

			_, err = e.CompleteNode(node.ID, true, nil)
			if tc.allowed {
				if err != nil {
					t.Fatalf("CompleteNode from %s (is_manual=%v) = %v, want success", tc.status, tc.manual, err)
				}
				got, _ := repo.GetNode(node.ID)
				if got.Status != domain.NodeDone {
					t.Fatalf("node status = %s, want DONE", got.Status)
				}
				return
			}
			apiErr := assertInvalidNodeState(t, err)
			// The message names where the node actually is, so a reader
			// of a failed run can tell which of the two recoveries
			// applies without going back to the database.
			if !strings.Contains(apiErr.Message, string(tc.status)) {
				t.Errorf("message %q does not name the current status %s", apiErr.Message, tc.status)
			}
			got, _ := repo.GetNode(node.ID)
			if got.Status != tc.status {
				t.Errorf("a refused completion changed the node to %s; it must stay %s", got.Status, tc.status)
			}
		})
	}
}

// TestCompleteNode_RefusedCallWritesNoArtifacts is the half of completion
// criterion 2 that the status table alone does not cover: the refusal has to
// happen before anything is written. CompleteNode used to create the call's
// artifacts first and only then act on the verdict, so even a call that should
// never have been accepted left its artifacts behind on the node.
func TestCompleteNode_RefusedCallWritesNoArtifacts(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")
	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	node := exec[0]
	setNodeStatus(t, repo, node.ID, domain.NodeTODO)

	first, second := "one", "two"
	_, err := e.CompleteNode(node.ID, true, []domain.Artifact{
		{Name: "a", Type: domain.ArtifactText, Content: &first},
		{Name: "b", Type: domain.ArtifactText, Content: &second},
	})
	assertInvalidNodeState(t, err)

	arts, err := repo.ListArtifactsByNode(node.ID)
	if err != nil {
		t.Fatalf("ListArtifactsByNode: %v", err)
	}
	if len(arts) != 0 {
		t.Fatalf("a refused completion wrote %d artifact(s): %+v", len(arts), arts)
	}
	got, _ := repo.GetNode(node.ID)
	if got.Status != domain.NodeTODO {
		t.Errorf("node status = %s, want TODO", got.Status)
	}
	ticketAfter, _ := repo.GetTicket(ticket.ID)
	if ticketAfter.Blocked {
		t.Error("a refused completion blocked the ticket")
	}
}

// TestCompleteNode_FailingADoneNodeIsRefused is the case that motivated the
// whole check: re-failing an already finished node used to block a ticket
// whose work was done, with nothing in the record to show that the second
// verdict was never meant to count.
func TestCompleteNode_FailingADoneNodeIsRefused(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")
	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	node := exec[0]
	if _, err := e.CompleteNode(node.ID, true, nil); err != nil {
		t.Fatalf("CompleteNode: %v", err)
	}

	_, err := e.CompleteNode(node.ID, false, nil)
	assertInvalidNodeState(t, err)

	got, _ := repo.GetNode(node.ID)
	if got.Status != domain.NodeDone {
		t.Errorf("node status = %s, want DONE", got.Status)
	}
	ticketAfter, _ := repo.GetTicket(ticket.ID)
	if ticketAfter.Blocked {
		t.Error("re-failing a DONE node blocked the ticket")
	}
}

// TestCompleteNode_ApprovalGateDoubleProcessingIsRefused is the case the
// SKILL.md warning ("don't call complete-node if the Web UI already handled
// it") used to be the only defence against.
func TestCompleteNode_ApprovalGateDoubleProcessingIsRefused(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	gate := runUntilApprovalGate(t, e, repo, ticket.ID, cat)
	if gate.Status != domain.NodeTODO || !gate.IsManual {
		t.Fatalf("expected a manual gate at TODO, got %s / is_manual=%v", gate.Status, gate.IsManual)
	}

	if _, err := e.CompleteNode(gate.ID, true, nil); err != nil {
		t.Fatalf("approving the gate: %v", err)
	}
	_, err := e.CompleteNode(gate.ID, true, nil)
	assertInvalidNodeState(t, err)

	got, _ := repo.GetNode(gate.ID)
	if got.Status != domain.NodeDone {
		t.Errorf("gate status = %s, want DONE", got.Status)
	}
}

// TestCompleteNode_ErrorMessageNamesStateAndRecovery keeps the refusal
// actionable: the message has to say where the node is, where it would have
// to be, and how to get it there.
func TestCompleteNode_ErrorMessageNamesStateAndRecovery(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")
	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	node := exec[0]
	setNodeStatus(t, repo, node.ID, domain.NodeDone)

	_, err := e.CompleteNode(node.ID, true, nil)
	apiErr := assertInvalidNodeState(t, err)
	for _, want := range []string{"DONE", "IN PROGRESS", "IN REVIEW", "unstick-node", "reopen-nodes"} {
		if !strings.Contains(apiErr.Message, want) {
			t.Errorf("message %q does not mention %q", apiErr.Message, want)
		}
	}
}

// TestCompleteNode_MissingNodeIsNodeNotFound: the error carries a code so the
// HTTP layer can turn it into a 404 instead of the flat 400 every
// complete-node failure used to get.
func TestCompleteNode_MissingNodeIsNodeNotFound(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	if _, err := e.CreateTicket(projectID, "title", ""); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	_, err := e.CompleteNode("NO-SUCH-NODE-99", true, nil)
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeNodeNotFound {
		t.Fatalf("CompleteNode on a missing node = %v, want a NODE_NOT_FOUND APIError", err)
	}
}

// TestCompleteNode_BlockedTicketStillCompletesRunningNode and its prerequisite
// counterpart below pin the two things checkCompletable deliberately does not
// look at. With parallel branches, one branch's failure blocks the ticket
// while a sibling node is still legitimately running; refusing that sibling's
// completion would throw away work that was correctly done and already in
// flight when the block landed.
func TestCompleteNode_BlockedTicketStillCompletesRunningNode(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")
	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	node := exec[0]

	blocked := true
	if _, err := repo.UpdateTicket(ticket.ID, store.TicketPatch{Blocked: &blocked}); err != nil {
		t.Fatalf("blocking the ticket: %v", err)
	}

	if _, err := e.CompleteNode(node.ID, true, nil); err != nil {
		t.Fatalf("CompleteNode on a blocked ticket's running node: %v", err)
	}
	got, _ := repo.GetNode(node.ID)
	if got.Status != domain.NodeDone {
		t.Errorf("node status = %s, want DONE", got.Status)
	}
}

func TestCompleteNode_UndonesPrerequisiteStillCompletesRunningNode(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	plan := exec[0]
	if _, err := e.CompleteNode(plan.ID, true, nil); err != nil {
		t.Fatalf("completing plan: %v", err)
	}
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	review := exec[0]

	// Knock the prerequisite back out of DONE while its successor runs.
	setNodeStatus(t, repo, plan.ID, domain.NodeTODO)

	if _, err := e.CompleteNode(review.ID, true, nil); err != nil {
		t.Fatalf("CompleteNode with an undone prerequisite: %v", err)
	}
	got, _ := repo.GetNode(review.ID)
	if got.Status != domain.NodeDone {
		t.Errorf("node status = %s, want DONE", got.Status)
	}
}

// runUntilApprovalGate drives the default workflow far enough to reach
// plan_approval, the first manual gate: seed plan, plan_review, then expand
// into the full template. The gate comes back at TODO, which is where a manual
// node waits -- GetExecutableNodes never claims one.
func runUntilApprovalGate(t *testing.T, e *GraphEngine, repo store.GraphRepository, ticketID string, cat config.Catalog) domain.GraphNode {
	t.Helper()
	for _, step := range []string{"plan", "plan_review"} {
		exec, err := e.GetExecutableNodes(ticketID, cat)
		if err != nil {
			t.Fatalf("GetExecutableNodes before %s: %v", step, err)
		}
		if len(exec) != 1 {
			t.Fatalf("expected exactly %s to be executable, got %+v", step, exec)
		}
		if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil {
			t.Fatalf("completing %s: %v", step, err)
		}
	}
	if err := e.ExpandGraph(ticketID, cat, nil); err != nil {
		t.Fatalf("ExpandGraph: %v", err)
	}
	return nodeByConfigID(t, repo, ticketID, "plan_approval")
}
