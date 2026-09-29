package store

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

// A DB created before DFLT-00329 has no decision columns on nodes. Init adds
// them, twice in a row safely, and an existing node reads back undecided.
func TestSQLiteInit_AddsNodeDecisionColumnsIdempotently(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "old.db")
	old, err := NewSQLiteRepository(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Init(); err != nil {
		t.Fatal(err)
	}
	for _, col := range nodeDecisionColumns {
		if _, err := old.db.Exec(`ALTER TABLE nodes DROP COLUMN ` + col); err != nil {
			t.Fatalf("dropping %s: %v", col, err)
		}
	}
	for _, stmt := range []string{
		`INSERT INTO projects (id, name, prefix, ticket_seq, created_at, updated_at) VALUES ('proj-old', 'Old', 'OLD', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO tickets (id, project_id, title, description, status, auto_executable, blocked, node_seq, priority, created_at, updated_at)
		 VALUES ('OLD-00001', 'proj-old', 'existing', '', 'IN REVIEW', 1, 0, 1, 'MEDIUM', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO nodes (id, ticket_id, name, type, status, is_manual, created_at, updated_at)
		 VALUES ('OLD-00001-01', 'OLD-00001', 'approval', 'approval_gate', 'DONE', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
	} {
		if _, err := old.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	old.db.Close()

	repo, err := NewSQLiteRepository(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.db.Close() })
	for i := 1; i <= 2; i++ {
		if err := repo.Init(); err != nil {
			t.Fatalf("Init #%d: %v", i, err)
		}
	}
	cols := sqliteColumnNames(t, repo.db, "nodes")
	for _, col := range nodeDecisionColumns {
		if !cols[col] {
			t.Errorf("Init did not add nodes.%s", col)
		}
	}
	n, err := repo.GetNode("OLD-00001-01")
	if err != nil || n == nil || n.DecidedByName != nil || n.DecidedAt != nil || n.DecidedByAutopilot != nil || n.DecidedByNameIsFallback != nil {
		t.Fatalf("existing node after migration = %+v, %v; want readable and undecided", n, err)
	}
}

func TestAddNodeDecisionColumns_ConcurrentAddIsNotAnError(t *testing.T) {
	added := map[string]bool{}
	exists := func() (map[string]bool, error) {
		out := map[string]bool{}
		for k, v := range added {
			out[k] = v
		}
		return out, nil
	}
	err := addNodeDecisionColumns("test", exists, func(col, _ string) error {
		added[col] = true // another Init got there first
		return errors.New("duplicate column name: " + col)
	}, sqliteNodeDecisionColumnTypes)
	if err != nil {
		t.Fatalf("an ALTER that lost the race must not fail Init: %v", err)
	}
}

// The MySQL counterpart, with two Inits racing each other: columns dropped
// from a fresh schema come back, and neither Init fails.
func TestMySQLInit_AddsNodeDecisionColumnsConcurrently(t *testing.T) {
	repo := newTestMySQLRepo(t)
	for _, col := range nodeDecisionColumns {
		if _, err := repo.db.Exec("ALTER TABLE nodes DROP COLUMN " + col); err != nil {
			t.Fatalf("dropping %s: %v", col, err)
		}
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = repo.Init()
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Init #%d: %v", i, err)
		}
	}
	for _, col := range nodeDecisionColumns {
		if ok, err := repo.mysqlColumnExists("nodes", col); err != nil || !ok {
			t.Errorf("nodes.%s after Init: %v, %v", col, ok, err)
		}
	}
}
