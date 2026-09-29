package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// A DB created before DFLT-00327 has neither the nodes' claim columns nor
// processing_sessions. Init adds both, twice in a row safely, and a node
// already IN PROGRESS there reads back as a claim without a record (legacy).
func TestSQLiteInit_AddsNodeClaimColumnsAndSessionsIdempotently(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "old.db")
	old, err := NewSQLiteRepository(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Init(); err != nil {
		t.Fatal(err)
	}
	// Turn the fresh schema back into the pre-DFLT-00327 one.
	for _, col := range nodeClaimColumns {
		if _, err := old.db.Exec(`ALTER TABLE nodes DROP COLUMN ` + col); err != nil {
			t.Fatalf("dropping %s: %v", col, err)
		}
	}
	if _, err := old.db.Exec(`DROP TABLE processing_sessions`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec(`INSERT INTO projects (id, name, prefix, ticket_seq, created_at, updated_at) VALUES ('proj-old', 'Old', 'OLD', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec(`INSERT INTO tickets (id, project_id, title, description, status, auto_executable, blocked, node_seq, priority, created_at, updated_at)
		VALUES ('OLD-00001', 'proj-old', 'existing', '', 'IN PROGRESS', 1, 0, 1, 'MEDIUM', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec(`INSERT INTO nodes (id, ticket_id, name, type, status, created_at, updated_at)
		VALUES ('OLD-00001-01', 'OLD-00001', 'impl', 'implementation', 'IN PROGRESS', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
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
	for _, col := range nodeClaimColumns {
		if !cols[col] {
			t.Errorf("Init did not add nodes.%s", col)
		}
	}
	if !sqliteColumnNames(t, repo.db, "processing_sessions")["run_id"] {
		t.Error("Init did not create processing_sessions")
	}
	n, err := repo.GetNode("OLD-00001-01")
	if err != nil || n == nil || n.ClaimToken != nil || n.ClaimedByName != nil {
		t.Fatalf("existing node after migration = %+v, %v; want readable with no claim", n, err)
	}
	if err := repo.SaveProcessingSession(domain.ProcessingSession{ID: "s1", ProjectID: "proj-old", TicketID: "OLD-00001", StartedAt: "x", Heartbeat: "x"}); err != nil {
		t.Fatalf("SaveProcessingSession after migration: %v", err)
	}
	// Sessions go with their ticket.
	if err := repo.DeleteTicket("OLD-00001"); err != nil {
		t.Fatal(err)
	}
	if s, _ := repo.GetProcessingSession("s1"); s != nil {
		t.Errorf("a deleted ticket's session was kept: %+v", s)
	}
}

func TestAddNodeClaimColumns_ConcurrentAddIsNotAnError(t *testing.T) {
	added := map[string]bool{}
	exists := func() (map[string]bool, error) {
		out := map[string]bool{}
		for k, v := range added {
			out[k] = v
		}
		return out, nil
	}
	err := addNodeClaimColumns("test", exists, func(col, _ string) error {
		added[col] = true // another Init got there first
		return errors.New("duplicate column name: " + col)
	}, sqliteNodeClaimColumnTypes)
	if err != nil {
		t.Fatalf("an ALTER that lost the race must not fail Init: %v", err)
	}
	err = addNodeClaimColumns("test", func() (map[string]bool, error) { return map[string]bool{}, nil }, func(string, string) error { return errors.New("disk full") }, sqliteNodeClaimColumnTypes)
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("a real ALTER failure must be returned, got %v", err)
	}
}

// On a DB that already has every claim column -- every graph-engine call
// after the first -- the migration is one read of the columns and nothing
// else (a remote MySQL pays a round trip per query).
func TestAddNodeClaimColumns_MigratedDBIsOneRead(t *testing.T) {
	reads := 0
	all := map[string]bool{}
	for _, c := range nodeClaimColumns {
		all[c] = true
	}
	err := addNodeClaimColumns("test", func() (map[string]bool, error) { reads++; return all, nil },
		func(col, _ string) error { t.Errorf("ALTER for %s on a migrated DB", col); return nil }, sqliteNodeClaimColumnTypes)
	if err != nil || reads != 1 {
		t.Fatalf("err=%v reads=%d, want nil and 1", err, reads)
	}
}

// The MySQL counterpart: columns dropped from a fresh schema come back with
// Init, and processing_sessions is created.
func TestMySQLInit_AddsNodeClaimColumnsAndSessions(t *testing.T) {
	repo := newTestMySQLRepo(t)
	for _, col := range nodeClaimColumns {
		if _, err := repo.db.Exec("ALTER TABLE nodes DROP COLUMN " + col); err != nil {
			t.Fatalf("dropping %s: %v", col, err)
		}
	}
	if _, err := repo.db.Exec("DROP TABLE processing_sessions"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		if err := repo.Init(); err != nil {
			t.Fatalf("Init #%d: %v", i, err)
		}
	}
	for _, col := range nodeClaimColumns {
		if ok, err := repo.mysqlColumnExists("nodes", col); err != nil || !ok {
			t.Errorf("nodes.%s after Init: %v, %v", col, ok, err)
		}
	}
	if ok, err := repo.mysqlColumnExists("processing_sessions", "heartbeat"); err != nil || !ok {
		t.Errorf("processing_sessions after Init: %v, %v", ok, err)
	}
}
