package store

import (
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00046: tickets.graph_expanded_at records the moment engine.ExpandGraph
// first succeeds for a ticket, so deriveTicketStatus can tell "only the seed
// is DONE" apart from "the expanded graph is DONE" without threading
// config.Catalog through every store method. These tests pin the storage
// contract both backends share: a fresh schema already has the column, and
// UpdateTicket/scanTicket round-trip it.

func TestSQLite_FreshDBHasTicketGraphExpandedAtColumn(t *testing.T) {
	repo := newTestRepo(t)
	if cols := sqliteColumnNames(t, repo.db, "tickets"); !cols["graph_expanded_at"] {
		t.Errorf("tickets.graph_expanded_at must exist in a fresh schema, got columns %v", cols)
	}
}

func TestSQLite_UpdateTicketRoundTripsGraphExpandedAt(t *testing.T) {
	repo, proj := newTestRepoWithProject(t)
	ticket, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Description: "d", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if got, _ := repo.GetTicket(ticket.ID); got.GraphExpandedAt != nil {
		t.Errorf("expected a new ticket to have no graph_expanded_at, got %+v", got.GraphExpandedAt)
	}

	expandedAt := "2026-01-01T00:00:00Z"
	updated, err := repo.UpdateTicket(ticket.ID, TicketPatch{GraphExpandedAt: &expandedAt})
	if err != nil {
		t.Fatalf("UpdateTicket: %v", err)
	}
	if updated.GraphExpandedAt == nil || *updated.GraphExpandedAt != expandedAt {
		t.Errorf("expected graph_expanded_at %q, got %+v", expandedAt, updated.GraphExpandedAt)
	}

	got, err := repo.GetTicket(ticket.ID)
	if err != nil || got == nil || got.GraphExpandedAt == nil || *got.GraphExpandedAt != expandedAt {
		t.Fatalf("expected graph_expanded_at to round-trip through GetTicket, got %v, %+v", err, got)
	}
}

func TestMySQL_FreshDBHasTicketGraphExpandedAtColumn(t *testing.T) {
	repo := newTestMySQLRepo(t)
	if exists, err := repo.mysqlColumnExists("tickets", "graph_expanded_at"); err != nil || !exists {
		t.Errorf("tickets.graph_expanded_at must exist after Init (exists=%v, err=%v)", exists, err)
	}
}

func TestMySQL_UpdateTicketRoundTripsGraphExpandedAt(t *testing.T) {
	repo := newTestMySQLRepo(t)
	proj, err := repo.CreateProject("Graph Expanded At", "GEAT")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	ticket, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Description: "d", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	expandedAt := "2026-01-01T00:00:00Z"
	updated, err := repo.UpdateTicket(ticket.ID, TicketPatch{GraphExpandedAt: &expandedAt})
	if err != nil {
		t.Fatalf("UpdateTicket: %v", err)
	}
	if updated.GraphExpandedAt == nil || *updated.GraphExpandedAt != expandedAt {
		t.Errorf("expected graph_expanded_at %q, got %+v", expandedAt, updated.GraphExpandedAt)
	}
}
