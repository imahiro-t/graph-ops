package httpserver

import (
	"net/http"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00329: the Web UI's approve / reject records who decided, resolved by
// the server from its own home config -- never from the request.
func TestHandleCompleteNode_RecordsTheServerSideDecider(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	setMyName(t, s.cfg.HomeDir, "Server Side")
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketInReview})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := repo.CreateNode(domain.GraphNode{TicketID: ticket.ID, Name: "approval", Type: domain.NodeTypeApprovalGate, Status: domain.NodeTODO, MaxIterations: 3, IsManual: true})
	if err != nil {
		t.Fatal(err)
	}
	// A client naming somebody else is ignored.
	rec := postCompleteNode(t, s, gate.ID, map[string]any{
		"passed": true, "decided_by_name": "Mallory", "decider": "Mallory", "name": "Mallory",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}
	got, _ := repo.GetNode(gate.ID)
	if got.DecidedByName == nil || *got.DecidedByName != "Server Side" || got.DecidedByNameIsFallback == nil || *got.DecidedByNameIsFallback ||
		got.DecidedAt == nil || got.DecidedByAutopilot == nil || *got.DecidedByAutopilot {
		t.Fatalf("decision = %v / %v / %v / %v, want Server Side, not a fallback, not the autopilot", got.DecidedByName, got.DecidedByNameIsFallback, got.DecidedAt, got.DecidedByAutopilot)
	}

	// The same gate decided again -- a second member's click that arrives
	// after the first -- is a 409 that writes nothing.
	rec = postCompleteNode(t, s, gate.ID, map[string]any{"passed": false, "artifacts": []map[string]any{{"name": "rejection_reason", "type": "text", "content": "late"}}})
	if rec.Code != http.StatusConflict {
		t.Fatalf("second decision: %d %s, want 409", rec.Code, rec.Body.String())
	}
	if e := decodeError(t, rec); e.Code != domain.ErrCodeInvalidNodeState {
		t.Fatalf("code = %s, want INVALID_NODE_STATE", e.Code)
	}
	arts, _ := repo.ListArtifactsByNode(gate.ID)
	if len(arts) != 0 {
		t.Fatalf("the refused rejection left %d artifacts", len(arts))
	}

	// get-ticket's API shows the decision.
	rec = doJSON(t, s, http.MethodGet, "/api/tickets/"+ticket.ID, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"decided_by_name":"Server Side"`) || !strings.Contains(rec.Body.String(), `"decided_at":`) {
		t.Fatalf("GET ticket = %d %s", rec.Code, rec.Body.String())
	}
}

func TestStatusForError_NewConflictCodes(t *testing.T) {
	for _, code := range []domain.ErrorCode{domain.ErrCodeTicketStatusChanged, domain.ErrCodeConcurrentWriteConflict} {
		if got := statusForError(domain.NewAPIError(code, "x"), http.StatusInternalServerError); got != http.StatusConflict {
			t.Errorf("%s -> %d, want 409", code, got)
		}
	}
}
