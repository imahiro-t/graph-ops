package store

import (
	"testing"
)

// DFLT-00048 adds tickets.priority. This pins down that a fresh schema
// always has it, so a pre-existing ticket with no priority set reads back
// as nil (see domain.Ticket's Priority field doc comment).

func TestInit_FreshSQLiteDBHasTicketPriorityColumn(t *testing.T) {
	repo := newTestRepo(t)

	if !sqliteColumnNames(t, repo.db, "tickets")["priority"] {
		t.Errorf("tickets.priority must exist in a fresh schema, got columns %v", sqliteColumnNames(t, repo.db, "tickets"))
	}
}
