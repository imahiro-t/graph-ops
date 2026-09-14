package store

import (
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00043: tickets.closed_reason backs CloseTicket's optional --reason.
// These tests pin the storage contract both backends share: a fresh schema
// already has the column, and UpdateTicket/scanTicket round-trip it
// (including overwriting a previous value, and clearing it back to empty
// text).

func TestSQLite_FreshDBHasTicketClosedReasonColumn(t *testing.T) {
	repo := newTestRepo(t)
	if cols := sqliteColumnNames(t, repo.db, "tickets"); !cols["closed_reason"] {
		t.Errorf("tickets.closed_reason must exist in a fresh schema, got columns %v", cols)
	}
}

func TestSQLite_UpdateTicketRoundTripsClosedReason(t *testing.T) {
	repo, proj := newTestRepoWithProject(t)
	ticket, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Description: "d", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if got, _ := repo.GetTicket(ticket.ID); got.ClosedReason != nil {
		t.Errorf("expected a new ticket to have no closed_reason, got %+v", got.ClosedReason)
	}

	reason := "対応せずクローズ"
	closed := domain.TicketClosed
	updated, err := repo.UpdateTicket(ticket.ID, TicketPatch{Status: &closed, ClosedReason: &reason})
	if err != nil {
		t.Fatalf("UpdateTicket: %v", err)
	}
	if updated.Status != domain.TicketClosed || updated.ClosedReason == nil || *updated.ClosedReason != reason {
		t.Errorf("expected status CLOSED and reason %q, got %+v", reason, updated)
	}

	// A second close overwrites the reason, even with empty text.
	empty := ""
	updated2, err := repo.UpdateTicket(ticket.ID, TicketPatch{Status: &closed, ClosedReason: &empty})
	if err != nil {
		t.Fatalf("UpdateTicket (overwrite): %v", err)
	}
	if updated2.ClosedReason == nil || *updated2.ClosedReason != "" {
		t.Errorf("expected closed_reason to be overwritten with empty text, got %+v", updated2.ClosedReason)
	}
}

func TestMySQL_FreshDBHasTicketClosedReasonColumn(t *testing.T) {
	repo := newTestMySQLRepo(t)
	if exists, err := repo.mysqlColumnExists("tickets", "closed_reason"); err != nil || !exists {
		t.Errorf("tickets.closed_reason must exist after Init (exists=%v, err=%v)", exists, err)
	}
}

func TestMySQL_UpdateTicketRoundTripsClosedReason(t *testing.T) {
	repo := newTestMySQLRepo(t)
	proj, err := repo.CreateProject("Closed Reason", "CLRS", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	ticket, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Description: "d", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	reason := "対応せずクローズ"
	closed := domain.TicketClosed
	updated, err := repo.UpdateTicket(ticket.ID, TicketPatch{Status: &closed, ClosedReason: &reason})
	if err != nil {
		t.Fatalf("UpdateTicket: %v", err)
	}
	if updated.Status != domain.TicketClosed || updated.ClosedReason == nil || *updated.ClosedReason != reason {
		t.Errorf("expected status CLOSED and reason %q, got %+v", reason, updated)
	}
}
