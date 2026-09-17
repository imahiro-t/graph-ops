package store

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// preLabelSchemaDDL is schemaDDL as it was before DFLT-00084: everything up
// to the labels section.
func preLabelSchemaDDL(t *testing.T) string {
	t.Helper()
	const marker = "-- Labels (DFLT-00084)"
	i := strings.Index(schemaDDL, marker)
	if i < 0 {
		t.Fatal("schemaDDL no longer contains the labels marker this test anchors on")
	}
	return schemaDDL[:i]
}

func sqliteTableExists(t *testing.T, repo *SQLiteRepository, table string) bool {
	t.Helper()
	return countRows(t, repo.db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table) > 0
}

// TestSQLiteInit_AddsLabelTablesToPreLabelDB: Init on a DB created without
// the label tables adds them, existing tickets read back unchanged with
// labels [], a second Init keeps labels and links, and a migrated ticket can
// be labeled.
func TestSQLiteInit_AddsLabelTablesToPreLabelDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "old.db")
	old, err := NewSQLiteRepository(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	if _, err := old.db.Exec(preLabelSchemaDDL(t)); err != nil {
		t.Fatalf("applying pre-label schema: %v", err)
	}
	if sqliteTableExists(t, old, "labels") || sqliteTableExists(t, old, "ticket_labels") {
		t.Fatal("test setup: the old schema must not have label tables")
	}
	if _, err := old.db.Exec(`INSERT INTO projects (id, name, prefix, ticket_seq, created_at, updated_at) VALUES ('proj-old', 'Old', 'OLD', 2, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("inserting project: %v", err)
	}
	for _, row := range []struct{ id, title, status, priority string }{
		{"OLD-1", "one", "TODO", "HIGH"},
		{"OLD-2", "two", "IN PROGRESS", "LOW"},
	} {
		if _, err := old.db.Exec(
			`INSERT INTO tickets (id, project_id, title, description, status, auto_executable, blocked, node_seq, priority, created_at, updated_at)
			 VALUES (?, 'proj-old', ?, 'desc', ?, 1, 0, 0, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
			row.id, row.title, row.status, row.priority); err != nil {
			t.Fatalf("inserting ticket: %v", err)
		}
	}
	old.db.Close()

	repo, err := NewSQLiteRepository(dbPath)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	t.Cleanup(func() { repo.db.Close() })
	if err := repo.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !sqliteTableExists(t, repo, "labels") || !sqliteTableExists(t, repo, "ticket_labels") {
		t.Fatal("Init must create labels and ticket_labels")
	}
	for id, want := range map[string]domain.Ticket{
		"OLD-1": {Title: "one", Description: "desc", Status: "TODO", Priority: "HIGH"},
		"OLD-2": {Title: "two", Description: "desc", Status: "IN PROGRESS", Priority: "LOW"},
	} {
		got, err := repo.GetTicket(id)
		if err != nil || got == nil {
			t.Fatalf("GetTicket(%s): %v, %v", id, got, err)
		}
		if got.Title != want.Title || got.Description != want.Description || got.Status != want.Status || got.Priority != want.Priority {
			t.Errorf("%s changed by the migration: %+v", id, got)
		}
		assertLabelNames(t, id, got.Labels)
	}
	list, err := repo.ListTicketsByProject("proj-old")
	if err != nil || len(list) != 2 {
		t.Fatalf("ListTicketsByProject: %v, %v", list, err)
	}
	for _, tk := range list {
		assertLabelNames(t, "list "+tk.ID, tk.Labels)
	}

	bug := mustCreateLabel(t, repo, "proj-old", "バグ", "red")
	setLabels(t, repo, "OLD-1", bug.ID)

	if err := repo.Init(); err != nil {
		t.Fatalf("second Init: %v", err)
	}
	assertLabelNames(t, "OLD-1 after second Init", getTicketLabels(t, repo, "OLD-1"), "バグ")
	if got, _ := repo.GetLabel(bug.ID); got == nil {
		t.Error("the label must survive a second Init")
	}
}
