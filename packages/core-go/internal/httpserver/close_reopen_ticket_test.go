package httpserver

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00043: HTTP handlers around engine.CloseTicket/engine.ReopenTicket.
// Engine-level behavior (deriving the reopened status, rejecting a reopen of
// a non-CLOSED ticket, etc.) is covered by internal/engine's own tests; these
// pin the handler wiring (routes, status codes, request/response shape).

func TestHandleCloseTicket_WithReason(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Description: "d", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	rec := doJSON(t, s, http.MethodPost, "/api/tickets/"+ticket.ID+"/close", map[string]any{"reason": "対応不要"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got domain.Ticket
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Status != domain.TicketClosed {
		t.Errorf("expected status CLOSED, got %+v", got)
	}
	if got.ClosedReason == nil || *got.ClosedReason != "対応不要" {
		t.Errorf("expected closed_reason to be saved, got %+v", got.ClosedReason)
	}
}

func TestHandleCloseTicket_NoBodyClosesWithEmptyReason(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	rec := doJSON(t, s, http.MethodPost, "/api/tickets/"+ticket.ID+"/close", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got domain.Ticket
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Status != domain.TicketClosed {
		t.Errorf("expected status CLOSED, got %+v", got)
	}
}

func TestHandleReopenTicket_MovesTicketOutOfClosed(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if rec := doJSON(t, s, http.MethodPost, "/api/tickets/"+ticket.ID+"/close", nil); rec.Code != http.StatusOK {
		t.Fatalf("close: expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, s, http.MethodPost, "/api/tickets/"+ticket.ID+"/reopen", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got domain.Ticket
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Status == domain.TicketClosed {
		t.Errorf("expected the ticket to no longer be CLOSED, got %+v", got)
	}
}

func TestHandleReopenTicket_RejectsWhenNotClosed(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	rec := doJSON(t, s, http.MethodPost, "/api/tickets/"+ticket.ID+"/reopen", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a non-CLOSED ticket, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}
