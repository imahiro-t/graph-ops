package httpserver

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00330: PATCH /api/tickets/{id} takes an optional if_updated_at.

func TestUpdateTicket_IfUpdatedAt(t *testing.T) {
	setup := func(t *testing.T) (*Server, domain.Ticket, domain.Label, domain.Label) {
		t.Helper()
		s, repo, projectID := newTestServer(t)
		bug := apiCreateLabel(t, s, projectID, "bug", "red")
		feature := apiCreateLabel(t, s, projectID, "feature", "green")
		tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
		if err != nil {
			t.Fatal(err)
		}
		rec := doJSON(t, s, http.MethodPatch, "/api/tickets/"+tk.ID, map[string]any{"label_ids": []string{bug.ID}})
		expectStatus(t, rec, http.StatusOK)
		if err := json.Unmarshal(rec.Body.Bytes(), &tk); err != nil {
			t.Fatal(err)
		}
		return s, tk, bug, feature
	}
	current := func(t *testing.T, s *Server, id string) domain.Ticket {
		t.Helper()
		rec := doJSON(t, s, http.MethodGet, "/api/tickets/"+id, nil)
		expectStatus(t, rec, http.StatusOK)
		var tk domain.Ticket
		if err := json.Unmarshal(rec.Body.Bytes(), &tk); err != nil {
			t.Fatal(err)
		}
		return tk
	}

	t.Run("a matching if_updated_at is 200", func(t *testing.T) {
		s, tk, _, feature := setup(t)
		rec := doJSON(t, s, http.MethodPatch, "/api/tickets/"+tk.ID, map[string]any{"label_ids": []string{feature.ID}, "if_updated_at": tk.UpdatedAt})
		expectStatus(t, rec, http.StatusOK)
		var got domain.Ticket
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Labels) != 1 || got.Labels[0].ID != feature.ID || got.UpdatedAt == tk.UpdatedAt {
			t.Fatalf("response = %+v", got)
		}
	})

	t.Run("a mismatched if_updated_at is 409 TICKET_CHANGED and writes nothing", func(t *testing.T) {
		s, tk, bug, feature := setup(t)
		rec := doJSON(t, s, http.MethodPatch, "/api/tickets/"+tk.ID, map[string]any{"label_ids": []string{feature.ID}, "if_updated_at": "2000-01-01T00:00:00Z"})
		expectErrorCode(t, rec, http.StatusConflict, domain.ErrCodeTicketChanged)
		got := current(t, s, tk.ID)
		if len(got.Labels) != 1 || got.Labels[0].ID != bug.ID || got.UpdatedAt != tk.UpdatedAt {
			t.Fatalf("a refused PATCH changed the ticket: %+v", got)
		}
	})

	t.Run("an omitted if_updated_at writes unconditionally", func(t *testing.T) {
		s, tk, _, _ := setup(t)
		rec := doJSON(t, s, http.MethodPatch, "/api/tickets/"+tk.ID, map[string]any{"priority": "HIGH"})
		expectStatus(t, rec, http.StatusOK)
		if got := current(t, s, tk.ID); got.Priority != domain.TicketPriorityHigh {
			t.Fatalf("priority = %s", got.Priority)
		}
	})

	t.Run("an empty if_updated_at is 400 VALIDATION_ERROR", func(t *testing.T) {
		s, tk, bug, feature := setup(t)
		rec := doJSON(t, s, http.MethodPatch, "/api/tickets/"+tk.ID, map[string]any{"label_ids": []string{feature.ID}, "if_updated_at": ""})
		expectErrorCode(t, rec, http.StatusBadRequest, domain.ErrCodeValidation)
		got := current(t, s, tk.ID)
		if len(got.Labels) != 1 || got.Labels[0].ID != bug.ID || got.UpdatedAt != tk.UpdatedAt {
			t.Fatalf("a refused PATCH changed the ticket: %+v", got)
		}
	})
}
