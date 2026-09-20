package httpserver

import (
	"net/http"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00103 / BUG-05: PATCH /api/tickets/{id} and PATCH /api/nodes/{id}
// used to write whatever they were given -- any string as a status or a node
// type, an empty title, a negative max_iterations -- and the node PATCH was
// the one node-mutating path that never re-derived the owning ticket's
// status.

// newPatchTicket returns a server and a ticket with a known title/status to
// PATCH against.
func newPatchTicket(t *testing.T) (*Server, string) {
	t.Helper()
	s, repo, projectID := newTestServer(t)
	tk, err := repo.CreateTicket(projectID, domain.Ticket{
		Title: "元のタイトル", Description: "d", Status: domain.TicketTODO,
	})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	return s, tk.ID
}

func ticketFromAPI(t *testing.T, s *Server, ticketID string) map[string]any {
	t.Helper()
	rec := doJSON(t, s, http.MethodGet, "/api/tickets/"+ticketID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET ticket: %d %s", rec.Code, rec.Body.String())
	}
	return decodeObject(t, rec)
}

// A rejected PATCH must leave the row exactly as it was. Validation runs
// before repo.UpdateTicket for precisely this reason, so the check is on
// every field at once: no partial write, not even of the fields that were
// valid.
func TestUpdateTicket_RejectsInvalidInputAndChangesNothing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     map[string]any
		wantCode domain.ErrorCode
	}{
		{"unknown status", map[string]any{"status": "TOTALLY MADE UP"}, domain.ErrCodeValidation},
		{"lowercase status", map[string]any{"status": "done"}, domain.ErrCodeValidation},
		{"empty status", map[string]any{"status": ""}, domain.ErrCodeValidation},
		{"CLOSED needs the close endpoint", map[string]any{"status": string(domain.TicketClosed)}, domain.ErrCodeValidation},
		{"empty title", map[string]any{"title": ""}, domain.ErrCodeTitleRequired},
		{"whitespace-only title", map[string]any{"title": "   \t\n "}, domain.ErrCodeTitleRequired},
		{
			// The valid half of the body must not sneak through either.
			"valid description alongside an invalid status",
			map[string]any{"description": "書き換えたい説明", "status": "NOPE"},
			domain.ErrCodeValidation,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ticketID := newPatchTicket(t)
			before := ticketFromAPI(t, s, ticketID)

			rec := doJSON(t, s, http.MethodPatch, "/api/tickets/"+ticketID, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if got := decodeError(t, rec).Code; got != tc.wantCode {
				t.Errorf("error code = %q, want %q", got, tc.wantCode)
			}

			after := ticketFromAPI(t, s, ticketID)
			for _, field := range []string{"title", "description", "status"} {
				if before[field] != after[field] {
					t.Errorf("%s changed despite the rejection: %v -> %v", field, before[field], after[field])
				}
			}
		})
	}
}

// Every status the domain defines except CLOSED is still settable, so the
// validation closes the hole without narrowing what legitimately works.
func TestUpdateTicket_AcceptsEveryKnownStatusExceptClosed(t *testing.T) {
	for _, status := range []domain.TicketStatus{
		domain.TicketTODO, domain.TicketRefined, domain.TicketInProgress,
		domain.TicketInReview, domain.TicketInRelease, domain.TicketDone,
	} {
		t.Run(string(status), func(t *testing.T) {
			s, ticketID := newPatchTicket(t)
			rec := doJSON(t, s, http.MethodPatch, "/api/tickets/"+ticketID, map[string]any{"status": string(status)})
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			if got := ticketFromAPI(t, s, ticketID)["status"]; got != string(status) {
				t.Errorf("persisted status = %v, want %q", got, status)
			}
		})
	}
}

// newPatchNode returns a server, a ticket and one TODO node on it.
func newPatchNode(t *testing.T) (*Server, store.GraphRepository, string, string) {
	t.Helper()
	s, repo, projectID := newTestServer(t)
	tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	n, err := repo.CreateNode(domain.GraphNode{
		TicketID: tk.ID, Name: "実装", Type: domain.NodeTypeImplementation,
		Status: domain.NodeTODO, MaxIterations: 3,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	return s, repo, tk.ID, n.ID
}

func TestUpdateNode_RejectsInvalidInputAndChangesNothing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     map[string]any
		wantCode domain.ErrorCode
	}{
		{"unknown status", map[string]any{"status": "MADE UP"}, domain.ErrCodeValidation},
		{"lowercase status", map[string]any{"status": "done"}, domain.ErrCodeValidation},
		{"max_iterations zero", map[string]any{"max_iterations": 0}, domain.ErrCodeInvalidMaxIterations},
		{"max_iterations negative", map[string]any{"max_iterations": -5}, domain.ErrCodeInvalidMaxIterations},
		{"withdrawn name", map[string]any{"name": "別の名前"}, domain.ErrCodeValidation},
		{"withdrawn type", map[string]any{"type": "totally-made-up-type"}, domain.ErrCodeValidation},
		{"withdrawn iteration_count", map[string]any{"iteration_count": 99}, domain.ErrCodeValidation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, ticketID, nodeID := newPatchNode(t)
			before, err := repo.GetNode(nodeID)
			if err != nil || before == nil {
				t.Fatalf("GetNode: %v", err)
			}

			rec := doJSON(t, s, http.MethodPatch, "/api/nodes/"+nodeID, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if got := decodeError(t, rec).Code; got != tc.wantCode {
				t.Errorf("error code = %q, want %q", got, tc.wantCode)
			}

			after, err := repo.GetNode(nodeID)
			if err != nil || after == nil {
				t.Fatalf("GetNode: %v", err)
			}
			if after.Name != before.Name || after.Type != before.Type ||
				after.Status != before.Status || after.MaxIterations != before.MaxIterations ||
				after.IterationCount != before.IterationCount {
				t.Errorf("node changed despite the rejection: %+v -> %+v", *before, *after)
			}
			// The ticket must not have moved either.
			if got := ticketFromAPI(t, s, ticketID)["status"]; got != string(domain.TicketTODO) {
				t.Errorf("ticket status = %v after a rejected node PATCH, want %q", got, domain.TicketTODO)
			}
		})
	}
}

// A node status this endpoint accepts is one the engine can still reason
// about -- and setting one now re-derives the ticket's status, which this
// path used to skip entirely.
func TestUpdateNode_SyncsTicketStatus(t *testing.T) {
	s, repo, ticketID, nodeID := newPatchNode(t)

	rec := doJSON(t, s, http.MethodPatch, "/api/nodes/"+nodeID, map[string]any{
		"status": string(domain.NodeInProgress),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := ticketFromAPI(t, s, ticketID)["status"]; got != string(domain.TicketInProgress) {
		t.Errorf("ticket status = %v after its only node went IN PROGRESS, want %q", got, domain.TicketInProgress)
	}

	// DONE additionally requires the graph to have been expanded (see
	// deriveTicketStatus), which a hand-built test graph has not been --
	// stamping it is what makes "every node is DONE" mean the whole ticket
	// is, rather than just the seed.
	expandedAt := "2026-01-01T00:00:00Z"
	if _, err := repo.UpdateTicket(ticketID, store.TicketPatch{GraphExpandedAt: &expandedAt}); err != nil {
		t.Fatalf("UpdateTicket: %v", err)
	}

	rec = doJSON(t, s, http.MethodPatch, "/api/nodes/"+nodeID, map[string]any{
		"status": string(domain.NodeDone),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := ticketFromAPI(t, s, ticketID)["status"]; got != string(domain.TicketDone) {
		t.Errorf("ticket status = %v after its only node went DONE, want %q", got, domain.TicketDone)
	}
}

// Syncing must not resurrect a withdrawn ticket or lose why it was
// withdrawn: syncTicketStatus returns early for a CLOSED ticket, and nothing
// on this path writes closed_reason.
func TestUpdateNode_LeavesAClosedTicketClosed(t *testing.T) {
	s, _, ticketID, nodeID := newPatchNode(t)

	rec := doJSON(t, s, http.MethodPost, "/api/tickets/"+ticketID+"/close", map[string]any{
		"reason": "上位チケットに統合したため",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("close = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodPatch, "/api/nodes/"+nodeID, map[string]any{
		"status": string(domain.NodeDone),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	ticket := ticketFromAPI(t, s, ticketID)
	if got := ticket["status"]; got != string(domain.TicketClosed) {
		t.Errorf("ticket status = %v, want %q -- a closed ticket must not be resurrected", got, domain.TicketClosed)
	}
	if got := ticket["closed_reason"]; got != "上位チケットに統合したため" {
		t.Errorf("closed_reason = %v, want it preserved", got)
	}
}

// A missing node is still a 404 NODE_NOT_FOUND, not the 400 the engine's
// other errors map to.
func TestUpdateNode_MissingNodeIs404(t *testing.T) {
	s, _, _ := newTestServer(t)
	rec := doJSON(t, s, http.MethodPatch, "/api/nodes/node-does-not-exist", map[string]any{
		"status": string(domain.NodeDone),
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec).Code; got != domain.ErrCodeNodeNotFound {
		t.Errorf("error code = %q, want %q", got, domain.ErrCodeNodeNotFound)
	}
}

func TestParseTicketAndNodeStatus(t *testing.T) {
	for _, s := range []string{"TODO", "REFINED", "IN PROGRESS", "IN REVIEW", "IN RELEASE", "DONE", "CLOSED"} {
		if got, err := domain.ParseTicketStatus(s); err != nil || string(got) != s {
			t.Errorf("ParseTicketStatus(%q) = %q, %v", s, got, err)
		}
	}
	for _, s := range []string{"", "todo", "IN  PROGRESS", "ARCHIVED", "DONE "} {
		if _, err := domain.ParseTicketStatus(s); err == nil {
			t.Errorf("ParseTicketStatus(%q) accepted an unknown status", s)
		}
	}
	for _, s := range []string{"TODO", "IN PROGRESS", "IN REVIEW", "DONE", "REJECTED", "AWAITING FIX"} {
		if got, err := domain.ParseNodeStatus(s); err != nil || string(got) != s {
			t.Errorf("ParseNodeStatus(%q) = %q, %v", s, got, err)
		}
	}
	for _, s := range []string{"", "done", "REFINED", "AWAITING-FIX"} {
		if _, err := domain.ParseNodeStatus(s); err == nil {
			t.Errorf("ParseNodeStatus(%q) accepted an unknown status", s)
		}
	}
}
