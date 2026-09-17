package store

import (
	"database/sql"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00048 adds tickets.priority. DFLT-00083 removes the "unset" state:
// Init backfills NULL priorities to domain.DefaultTicketPriority (MEDIUM),
// and CreateTicket never writes a NULL/empty one. There is deliberately no
// NULL -> MEDIUM substitution at read time; these tests pin the migration
// itself.

func TestInit_FreshSQLiteDBHasTicketPriorityColumn(t *testing.T) {
	repo := newTestRepo(t)

	if !sqliteColumnNames(t, repo.db, "tickets")["priority"] {
		t.Errorf("tickets.priority must exist in a fresh schema, got columns %v", sqliteColumnNames(t, repo.db, "tickets"))
	}
}

// priorityRow is one ticket's stored priority (NULL-aware) and updated_at,
// read straight from the table so the assertions see what Init wrote, not
// what scanTicket makes of it.
type priorityRow struct {
	priority  sql.NullString
	updatedAt string
}

const legacyTicketUpdatedAt = "2026-01-02T03:04:05Z"

// insertLegacyPriorityTickets inserts, with raw SQL that bypasses
// CreateTicket's default, three tickets under projectID: "-N" with a NULL
// priority (as written before DFLT-00083), "-H" with HIGH and "-L" with LOW.
// It returns their IDs keyed by "N"/"H"/"L".
func insertLegacyPriorityTickets(t *testing.T, db *sql.DB, projectID, prefix string) map[string]string {
	t.Helper()
	ids := map[string]string{"N": prefix + "-90001", "H": prefix + "-90002", "L": prefix + "-90003"}
	rows := []struct {
		id       string
		priority any
	}{
		{ids["N"], nil},
		{ids["H"], string(domain.TicketPriorityHigh)},
		{ids["L"], string(domain.TicketPriorityLow)},
	}
	for _, r := range rows {
		if _, err := db.Exec(
			`INSERT INTO tickets (id, project_id, title, description, status, auto_executable, blocked, node_seq, assignee_name, priority, created_at, updated_at)
			 VALUES (?, ?, 'legacy', '', 'TODO', 1, 0, 0, NULL, ?, ?, ?)`,
			r.id, projectID, r.priority, legacyTicketUpdatedAt, legacyTicketUpdatedAt,
		); err != nil {
			t.Fatalf("inserting legacy ticket %s: %v", r.id, err)
		}
	}
	return ids
}

func readPriorityRows(t *testing.T, db *sql.DB, ids map[string]string) map[string]priorityRow {
	t.Helper()
	out := map[string]priorityRow{}
	for key, id := range ids {
		var row priorityRow
		if err := db.QueryRow(`SELECT priority, updated_at FROM tickets WHERE id = ?`, id).Scan(&row.priority, &row.updatedAt); err != nil {
			t.Fatalf("reading ticket %s: %v", id, err)
		}
		out[key] = row
	}
	return out
}

// assertNullPriorityBackfill runs init twice over the legacy rows and checks
// the DFLT-00083 migration contract: only the NULL row becomes MEDIUM, HIGH
// and LOW are untouched, updated_at never changes, and a second run leaves
// everything exactly as the first did. Shared by the SQLite and MySQL tests.
func assertNullPriorityBackfill(t *testing.T, db *sql.DB, init func() error, ids map[string]string) {
	t.Helper()

	before := readPriorityRows(t, db, ids)
	if before["N"].priority.Valid {
		t.Fatalf("precondition: ticket N should start with a NULL priority, got %+v", before["N"].priority)
	}

	if err := init(); err != nil {
		t.Fatalf("Init (first migration run): %v", err)
	}
	first := readPriorityRows(t, db, ids)
	want := map[string]domain.TicketPriority{
		"N": domain.TicketPriorityMedium,
		"H": domain.TicketPriorityHigh,
		"L": domain.TicketPriorityLow,
	}
	for key, p := range want {
		got := first[key]
		if !got.priority.Valid || got.priority.String != string(p) {
			t.Errorf("ticket %s: priority = %+v after Init, want %s", key, got.priority, p)
		}
		if got.updatedAt != legacyTicketUpdatedAt {
			t.Errorf("ticket %s: updated_at changed by the migration: %q -> %q", key, legacyTicketUpdatedAt, got.updatedAt)
		}
	}

	if err := init(); err != nil {
		t.Fatalf("Init (second migration run) should be idempotent: %v", err)
	}
	second := readPriorityRows(t, db, ids)
	for key := range ids {
		if second[key] != first[key] {
			t.Errorf("ticket %s: second Init changed the row: %+v -> %+v", key, first[key], second[key])
		}
	}
}

func TestSQLiteRepository_InitBackfillsNullTicketPriority(t *testing.T) {
	repo, proj := newTestRepoWithProject(t)
	ids := insertLegacyPriorityTickets(t, repo.db, proj.ID, proj.Prefix)

	assertNullPriorityBackfill(t, repo.db, repo.Init, ids)

	// The migrated ticket reads back through the normal path as MEDIUM too.
	got, err := repo.GetTicket(ids["N"])
	if err != nil || got == nil || got.Priority != domain.TicketPriorityMedium {
		t.Fatalf("GetTicket after backfill: %v, %+v", err, got)
	}
}

// TestSQLiteRepository_CreateTicketWithoutPriorityStoresDefault covers the
// store-level safety net: a direct CreateTicket call that leaves Priority
// empty still stores MEDIUM, never NULL.
func TestSQLiteRepository_CreateTicketWithoutPriorityStoresDefault(t *testing.T) {
	repo, proj := newTestRepoWithProject(t)

	created, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "no priority", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if created.Priority != domain.TicketPriorityMedium {
		t.Errorf("created.Priority = %q, want MEDIUM", created.Priority)
	}
	var stored sql.NullString
	if err := repo.db.QueryRow(`SELECT priority FROM tickets WHERE id = ?`, created.ID).Scan(&stored); err != nil {
		t.Fatalf("reading stored priority: %v", err)
	}
	if !stored.Valid || stored.String != string(domain.TicketPriorityMedium) {
		t.Errorf("stored priority = %+v, want MEDIUM (not NULL)", stored)
	}
}
