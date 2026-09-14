package engine

import (
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00059: CreateTicketWithPriority and RefineTicket's priority parameter.

func TestCreateTicketWithPriority_SetsPriority(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	high := domain.TicketPriorityHigh
	ticket, err := e.CreateTicketWithPriority(projectID, "title", "desc", &high)
	if err != nil {
		t.Fatalf("CreateTicketWithPriority: %v", err)
	}
	if ticket.Priority == nil || *ticket.Priority != domain.TicketPriorityHigh {
		t.Errorf("expected priority HIGH, got %+v", ticket.Priority)
	}
}

func TestCreateTicketWithPriority_NilLeavesPriorityUnset(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	ticket, err := e.CreateTicketWithPriority(projectID, "title", "desc", nil)
	if err != nil {
		t.Fatalf("CreateTicketWithPriority: %v", err)
	}
	if ticket.Priority != nil {
		t.Errorf("expected no priority, got %+v", ticket.Priority)
	}
}

func TestCreateTicketWithPriority_InvalidValueErrorsAndCreatesNothing(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	invalid := domain.TicketPriority("URGENT")
	if _, err := e.CreateTicketWithPriority(projectID, "title", "desc", &invalid); err == nil {
		t.Fatalf("expected an error for an invalid priority")
	}
	tickets, err := repo.ListTicketsByProject(projectID)
	if err != nil {
		t.Fatalf("ListTicketsByProject: %v", err)
	}
	if len(tickets) != 0 {
		t.Errorf("a rejected priority must not create a ticket, got %d", len(tickets))
	}
}

func TestRefineTicket_PriorityNoChangeLeavesItUntouched(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	high := domain.TicketPriorityHigh
	ticket, err := e.CreateTicketWithPriority(projectID, "title", "desc", &high)
	if err != nil {
		t.Fatalf("CreateTicketWithPriority: %v", err)
	}
	updated, err := e.RefineTicket(ticket.ID, "new description", NoPriorityChange())
	if err != nil {
		t.Fatalf("RefineTicket: %v", err)
	}
	if updated.Priority == nil || *updated.Priority != domain.TicketPriorityHigh {
		t.Errorf("expected priority to stay HIGH, got %+v", updated.Priority)
	}
}

func TestRefineTicket_PrioritySet(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	ticket, err := e.CreateTicket(projectID, "title", "desc")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	updated, err := e.RefineTicket(ticket.ID, "", SetPriority(domain.TicketPriorityMedium))
	if err != nil {
		t.Fatalf("RefineTicket: %v", err)
	}
	if updated.Priority == nil || *updated.Priority != domain.TicketPriorityMedium {
		t.Errorf("expected priority MEDIUM, got %+v", updated.Priority)
	}
}

func TestRefineTicket_PriorityClear(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	high := domain.TicketPriorityHigh
	ticket, err := e.CreateTicketWithPriority(projectID, "title", "desc", &high)
	if err != nil {
		t.Fatalf("CreateTicketWithPriority: %v", err)
	}
	updated, err := e.RefineTicket(ticket.ID, "", ClearPriority())
	if err != nil {
		t.Fatalf("RefineTicket: %v", err)
	}
	if updated.Priority != nil {
		t.Errorf("expected priority to be cleared, got %+v", updated.Priority)
	}
}
