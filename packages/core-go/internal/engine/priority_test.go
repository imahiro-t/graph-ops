package engine

import (
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00059: CreateTicketWithPriority and RefineTicket's priority parameter.
// DFLT-00083: no unset state -- nil at creation means MEDIUM, and there is
// no way to clear a priority.

func TestCreateTicketWithPriority_SetsPriority(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	high := domain.TicketPriorityHigh
	ticket, err := e.CreateTicketWithPriority(projectID, "title", "desc", &high)
	if err != nil {
		t.Fatalf("CreateTicketWithPriority: %v", err)
	}
	if ticket.Priority != domain.TicketPriorityHigh {
		t.Errorf("expected priority HIGH, got %q", ticket.Priority)
	}
}

func TestCreateTicketWithPriority_NilDefaultsToMedium(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	ticket, err := e.CreateTicketWithPriority(projectID, "title", "desc", nil)
	if err != nil {
		t.Fatalf("CreateTicketWithPriority: %v", err)
	}
	if ticket.Priority != domain.TicketPriorityMedium {
		t.Errorf("expected default priority MEDIUM, got %q", ticket.Priority)
	}
}

func TestCreateTicket_DefaultsToMedium(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	ticket, err := e.CreateTicket(projectID, "title", "desc")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if ticket.Priority != domain.TicketPriorityMedium {
		t.Errorf("expected default priority MEDIUM, got %q", ticket.Priority)
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
	if updated.Priority != domain.TicketPriorityHigh {
		t.Errorf("expected priority to stay HIGH, got %q", updated.Priority)
	}
}

func TestRefineTicket_PrioritySet(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	ticket, err := e.CreateTicket(projectID, "title", "desc")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	updated, err := e.RefineTicket(ticket.ID, "", SetPriority(domain.TicketPriorityLow))
	if err != nil {
		t.Fatalf("RefineTicket: %v", err)
	}
	if updated.Priority != domain.TicketPriorityLow {
		t.Errorf("expected priority LOW, got %q", updated.Priority)
	}
}
