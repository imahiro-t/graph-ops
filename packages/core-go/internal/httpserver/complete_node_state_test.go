package httpserver

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00102 / BUG-04 over HTTP. Two things are pinned here that the
// engine-level tests cannot see: which status code each refusal becomes, and
// that the inline `artifacts` array is not left behind by a refused call.
//
// handleCompleteNode used to answer every engine failure with a flat 400,
// which told a caller nothing it could act on -- a node that does not exist
// and a node that is simply finished already are different problems with
// different fixes.

// newNodeAt creates a ticket with one node in the given state.
func newNodeAt(t *testing.T, repo store.GraphRepository, projectID string, status domain.NodeStatus) domain.GraphNode {
	t.Helper()
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := repo.CreateNode(domain.GraphNode{
		ID: "node-" + engine.NewArtifactID(), TicketID: ticket.ID, Name: "Impl",
		Type: domain.NodeTypeImplementation, Status: status, MaxIterations: 3,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	return node
}

func errorCodeOf(t *testing.T, body []byte) string {
	t.Helper()
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal error body %s: %v", body, err)
	}
	return payload.Error.Code
}

// TestHandleCompleteNode_StatusCodeTable is the HTTP half of completion
// criterion 5's "check both the CLI and the HTTP route".
func TestHandleCompleteNode_StatusCodeTable(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   domain.NodeStatus // "" means: do not create the node at all
		wantCode int
		wantErr  string
	}{
		{"IN PROGRESS completes", domain.NodeInProgress, http.StatusOK, ""},
		{"IN REVIEW completes", domain.NodeInReview, http.StatusOK, ""},
		{"TODO conflicts", domain.NodeTODO, http.StatusConflict, "INVALID_NODE_STATE"},
		{"DONE conflicts", domain.NodeDone, http.StatusConflict, "INVALID_NODE_STATE"},
		{"REJECTED conflicts", domain.NodeRejected, http.StatusConflict, "INVALID_NODE_STATE"},
		{"AWAITING FIX conflicts", domain.NodeAwaitingFix, http.StatusConflict, "INVALID_NODE_STATE"},
		{"a missing node is not found", "", http.StatusNotFound, "NODE_NOT_FOUND"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, projectID := newTestServer(t)
			nodeID := "node-does-not-exist"
			if tc.status != "" {
				nodeID = newNodeAt(t, repo, projectID, tc.status).ID
			}

			rec := postCompleteNode(t, s, nodeID, map[string]any{"passed": true})
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantErr == "" {
				return
			}
			if got := errorCodeOf(t, rec.Body.Bytes()); got != tc.wantErr {
				t.Errorf("error code = %q, want %q", got, tc.wantErr)
			}
			if tc.status != "" {
				node, _ := repo.GetNode(nodeID)
				if node.Status != tc.status {
					t.Errorf("a refused call changed the node to %s; it must stay %s", node.Status, tc.status)
				}
			}
		})
	}
}

// TestHandleCompleteNode_ManualNodeCompletesFromTODO: the Web UI offers
// approve/reject on exactly the TODO approval gates, so this route has to
// accept one. Refusing it would break approvals from the browser entirely.
func TestHandleCompleteNode_ManualNodeCompletesFromTODO(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	gate, err := repo.CreateNode(domain.GraphNode{
		ID: "node-" + engine.NewArtifactID(), TicketID: ticket.ID, Name: "Approval",
		Type: domain.NodeTypeApprovalGate, Status: domain.NodeTODO, IsManual: true, MaxIterations: 3,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	rec := postCompleteNode(t, s, gate.ID, map[string]any{"passed": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// Approving the same gate twice -- once in the browser, once from a
	// terminal -- is the case the SKILL.md warning used to be the only
	// defence against. The second one is now a 409.
	rec = postCompleteNode(t, s, gate.ID, map[string]any{"passed": true})
	if rec.Code != http.StatusConflict {
		t.Fatalf("the second approval got %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if got := errorCodeOf(t, rec.Body.Bytes()); got != "INVALID_NODE_STATE" {
		t.Errorf("error code = %q, want INVALID_NODE_STATE", got)
	}
	node, _ := repo.GetNode(gate.ID)
	if node.Status != domain.NodeDone {
		t.Errorf("gate status = %s, want DONE", node.Status)
	}
}

// TestHandleCompleteNode_RefusedCallStoresNoInlineArtifact: the inline
// artifacts array is validated by this handler and then written by the engine,
// which used to write it before looking at the node at all. A 409 must leave
// nothing behind.
func TestHandleCompleteNode_RefusedCallStoresNoInlineArtifact(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	node := newNodeAt(t, repo, projectID, domain.NodeDone)

	rec := postCompleteNode(t, s, node.ID, map[string]any{
		"passed":    true,
		"artifacts": []map[string]any{{"name": "note", "type": "text", "content": "all good"}},
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	arts, err := repo.ListArtifactsByNode(node.ID)
	if err != nil {
		t.Fatalf("ListArtifactsByNode: %v", err)
	}
	if len(arts) != 0 {
		t.Errorf("a refused call stored %d artifact(s): %+v", len(arts), arts)
	}
}

// TestHandleCompleteNode_ClosedTicketConflicts: a closed ticket takes no more
// work, whatever state its nodes are in.
func TestHandleCompleteNode_ClosedTicketConflicts(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	node := newNodeAt(t, repo, projectID, domain.NodeInProgress)
	closed := domain.TicketClosed
	if _, err := repo.UpdateTicket(node.TicketID, store.TicketPatch{Status: &closed}); err != nil {
		t.Fatalf("closing the ticket: %v", err)
	}

	rec := postCompleteNode(t, s, node.ID, map[string]any{"passed": true})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if got := errorCodeOf(t, rec.Body.Bytes()); got != "INVALID_NODE_STATE" {
		t.Errorf("error code = %q, want INVALID_NODE_STATE", got)
	}
}
