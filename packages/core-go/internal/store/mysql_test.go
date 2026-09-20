package store

import (
	"os"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// mysqlTestConfig builds a store.Config from GRAPH_TEST_MYSQL_* env vars.
// MySQLRepository can only be exercised against a real MySQL server (there
// is no pure-Go in-memory MySQL the way modernc.org/sqlite gives SQLite),
// so -- per DFLT-00020's execution plan -- these tests skip entirely unless
// a real server is configured for them to run against; the corresponding
// manual test checklist (see the ticket's Gherkin/plan artifacts) is the
// fallback verification path when no such server is available (e.g. in
// this sandbox/CI environment).
func mysqlTestConfig(t *testing.T) Config {
	t.Helper()
	host := os.Getenv("GRAPH_TEST_MYSQL_HOST")
	if host == "" {
		t.Skip("GRAPH_TEST_MYSQL_HOST not set; skipping MySQLRepository tests (see mysqlTestConfig's doc comment)")
	}
	port := 3306
	if p := os.Getenv("GRAPH_TEST_MYSQL_PORT"); p != "" {
		var err error
		if port, err = parsePort(p); err != nil {
			t.Fatalf("invalid GRAPH_TEST_MYSQL_PORT %q: %v", p, err)
		}
	}
	return Config{
		Backend:       "mysql",
		MySQLHost:     host,
		MySQLPort:     port,
		MySQLDatabase: os.Getenv("GRAPH_TEST_MYSQL_DATABASE"),
		MySQLUser:     os.Getenv("GRAPH_TEST_MYSQL_USER"),
		MySQLPassword: os.Getenv("GRAPH_TEST_MYSQL_PASSWORD"),
		// Deliberately no test-only lenient default: an unset
		// GRAPH_TEST_MYSQL_TLS normalizes to verify-full (the same
		// default production gets), matching this ticket's execution
		// plan (T-5) that the test suite must exercise the same secure
		// default a real deployment gets, not a separately relaxed one.
		MySQLTLSMode:   os.Getenv("GRAPH_TEST_MYSQL_TLS"),
		MySQLTLSCAFile: os.Getenv("GRAPH_TEST_MYSQL_TLS_CA"),
	}
}

func parsePort(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, os.ErrInvalid
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// newTestMySQLRepo opens (and initializes) a fresh MySQLRepository against
// the configured test server, and truncates every table first so each test
// starts from an empty database -- unlike SQLite's newTestRepo, there is no
// t.TempDir() to isolate a MySQL test into its own file.
func newTestMySQLRepo(t *testing.T) *MySQLRepository {
	t.Helper()
	cfg := mysqlTestConfig(t)
	repo, err := NewMySQLRepository(cfg)
	if err != nil {
		t.Fatalf("NewMySQLRepository: %v", err)
	}
	if err := repo.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, table := range []string{"ticket_labels", "labels", "artifacts", "edges", "nodes", "tickets", "app_state", "projects"} {
		if _, err := repo.db.Exec("DELETE FROM " + table); err != nil {
			t.Fatalf("cleaning table %s before test: %v", table, err)
		}
	}
	t.Cleanup(func() { repo.db.Close() })
	return repo
}

// TestMySQLRepository_InitIsIdempotent covers the Gherkin scenarios "テーブ
// ルが存在しないMySQLに対して起動すると、スキーマが自動作成される" and "既に
// テーブルが存在するMySQLに対して再起動しても、既存データは壊れない": calling
// Init twice in a row must succeed both times, against both an empty
// database and one that already has the schema.
func TestMySQLRepository_InitIsIdempotent(t *testing.T) {
	cfg := mysqlTestConfig(t)
	repo, err := NewMySQLRepository(cfg)
	if err != nil {
		t.Fatalf("NewMySQLRepository: %v", err)
	}
	defer repo.db.Close()

	if err := repo.Init(); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	if err := repo.Init(); err != nil {
		t.Fatalf("second Init should be idempotent: %v", err)
	}
}

// TestMySQLRepository_ProjectTicketNodeEdgeArtifactCRUD exercises the same
// CRUD contract SQLiteRepository's tests do for every entity the Gherkin
// Scenario Outline ("MySQLRepositoryはGraphRepositoryの主要CRUD操作を
// SQLiteRepositoryと同等に行える") lists: Project, Ticket, Node, Edge,
// Artifact.
func TestMySQLRepository_ProjectTicketNodeEdgeArtifactCRUD(t *testing.T) {
	repo := newTestMySQLRepo(t)

	proj, err := repo.CreateProject("MySQL Test Project", "MYSQ")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if got, err := repo.GetProject(proj.ID); err != nil || got == nil || got.Name != proj.Name {
		t.Fatalf("GetProject: %v, %+v", err, got)
	}

	ticket, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Description: "d", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if ticket.ID != proj.Prefix+"-00001" {
		t.Errorf("expected ticket ID %s-00001, got %q", proj.Prefix, ticket.ID)
	}
	newTitle := "updated"
	updatedTicket, err := repo.UpdateTicket(ticket.ID, TicketPatch{Title: &newTitle})
	if err != nil || updatedTicket.Title != newTitle {
		t.Fatalf("UpdateTicket: %v, %+v", err, updatedTicket)
	}

	// DFLT-00048/DFLT-00083: priority defaults to MEDIUM and round-trips
	// through set/change the same way on MySQL as on SQLite (see
	// TestTicketPriorityCRUD).
	if ticket.Priority != domain.TicketPriorityMedium {
		t.Errorf("a newly created ticket should default to MEDIUM, got %q", ticket.Priority)
	}
	high := domain.TicketPriorityHigh
	updatedTicket, err = repo.UpdateTicket(ticket.ID, TicketPatch{Priority: &high})
	if err != nil || updatedTicket.Priority != domain.TicketPriorityHigh {
		t.Fatalf("UpdateTicket(priority=HIGH): %v, %+v", err, updatedTicket)
	}
	low := domain.TicketPriorityLow
	updatedTicket, err = repo.UpdateTicket(ticket.ID, TicketPatch{Priority: &low})
	if err != nil || updatedTicket.Priority != domain.TicketPriorityLow {
		t.Fatalf("UpdateTicket(priority=LOW): %v, %+v", err, updatedTicket)
	}

	node, err := repo.CreateNode(domain.GraphNode{TicketID: ticket.ID, Name: "n", Type: domain.NodeTypePlan, Status: domain.NodeTODO, MaxIterations: 3})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if node.ID != ticket.ID+"-01" {
		t.Errorf("expected node ID %s-01, got %q", ticket.ID, node.ID)
	}
	node2, err := repo.CreateNode(domain.GraphNode{TicketID: ticket.ID, Name: "n2", Type: domain.NodeTypeReview, Status: domain.NodeTODO, MaxIterations: 3})
	if err != nil {
		t.Fatalf("CreateNode 2: %v", err)
	}

	edge, err := repo.CreateEdge(domain.GraphEdge{ID: "e-" + node.ID, TicketID: ticket.ID, FromNodeID: node.ID, ToNodeID: node2.ID})
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	if edge.Condition != domain.EdgeAlways {
		t.Errorf("expected default condition %q, got %q", domain.EdgeAlways, edge.Condition)
	}
	edges, err := repo.ListEdgesByTicket(ticket.ID)
	if err != nil || len(edges) != 1 {
		t.Fatalf("ListEdgesByTicket: %v, len=%d", err, len(edges))
	}

	content := "artifact body"
	art, err := repo.CreateArtifact(domain.Artifact{ID: "a-" + node.ID, TicketID: ticket.ID, NodeID: node.ID, Name: "x", Type: domain.ArtifactText, Content: &content})
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if art.Content == nil || *art.Content != content {
		t.Errorf("artifact content mismatch: %+v", art)
	}

	detail, err := repo.GetTicketDetail(ticket.ID)
	if err != nil || detail == nil || len(detail.Nodes) != 2 || len(detail.Edges) != 1 || len(detail.Artifacts) != 1 {
		t.Fatalf("GetTicketDetail: %v, %+v", err, detail)
	}

	if err := repo.SetCurrentProjectID(proj.ID); err != nil {
		t.Fatalf("SetCurrentProjectID: %v", err)
	}
	if cur, err := repo.GetCurrentProjectID(); err != nil || cur != proj.ID {
		t.Fatalf("GetCurrentProjectID: %v, got %q want %q", err, cur, proj.ID)
	}
	// Upsert path (ON DUPLICATE KEY UPDATE): setting it again must update
	// in place, not fail on a duplicate primary key.
	proj2, err := repo.CreateProject("Second", "SCND")
	if err != nil {
		t.Fatalf("CreateProject 2: %v", err)
	}
	if err := repo.SetCurrentProjectID(proj2.ID); err != nil {
		t.Fatalf("SetCurrentProjectID (upsert): %v", err)
	}
	if cur, err := repo.GetCurrentProjectID(); err != nil || cur != proj2.ID {
		t.Fatalf("GetCurrentProjectID after upsert: %v, got %q want %q", err, cur, proj2.ID)
	}

	if err := repo.DeleteNode(node2.ID); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	if got, err := repo.GetNode(node2.ID); err != nil || got != nil {
		t.Errorf("expected node deleted, got %+v (err=%v)", got, err)
	}

	if err := repo.DeleteProject(proj.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if got, err := repo.GetTicket(ticket.ID); err != nil || got != nil {
		t.Errorf("expected ticket cascaded away with its project, got %+v (err=%v)", got, err)
	}
}

// TestMySQLRepository_InitDropsLegacyWorkDirColumnAndKeepsData is the MySQL
// counterpart of TestSQLiteInit_DropsLegacyWorkDirColumnAndKeepsData
// (DFLT-00080): starting from a projects table that still has the old
// `work_dir TEXT NOT NULL` column (re-added here after a normal Init, which
// is exactly the pre-DFLT-00080 table shape), Init run twice must drop the
// column, keep the project and its ticket, and leave CreateProject working.
// Skipped like every other MySQL test when GRAPH_TEST_MYSQL_HOST is unset.
func TestMySQLRepository_InitDropsLegacyWorkDirColumnAndKeepsData(t *testing.T) {
	repo := newTestMySQLRepo(t)
	if _, err := repo.db.Exec(`ALTER TABLE projects ADD COLUMN work_dir TEXT NOT NULL AFTER prefix`); err != nil {
		t.Fatalf("re-adding legacy work_dir column: %v", err)
	}
	if _, err := repo.db.Exec(
		`INSERT INTO projects (id, name, prefix, work_dir, ticket_seq, created_at, updated_at) VALUES ('proj-legacy', 'Alpha', 'ALPHA', '/home/a/alpha', 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("inserting legacy project: %v", err)
	}
	ticket, err := repo.CreateTicket("proj-legacy", domain.Ticket{Title: "legacy ticket", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket on legacy schema: %v", err)
	}

	for i := 1; i <= 2; i++ {
		if err := repo.Init(); err != nil {
			t.Fatalf("Init #%d: %v", i, err)
		}
	}

	if exists, err := repo.mysqlColumnExists("projects", "work_dir"); err != nil || exists {
		t.Fatalf("projects.work_dir should have been dropped (exists=%v, err=%v)", exists, err)
	}
	got, err := repo.GetProject("proj-legacy")
	if err != nil || got == nil || got.Name != "Alpha" || got.Prefix != "ALPHA" {
		t.Fatalf("project after migration: %v, %+v", err, got)
	}
	if gotTicket, err := repo.GetTicket(ticket.ID); err != nil || gotTicket == nil || gotTicket.Title != "legacy ticket" {
		t.Errorf("ticket after migration: %v, %+v", err, gotTicket)
	}
	if _, err := repo.CreateProject("Beta", ""); err != nil {
		t.Errorf("CreateProject after migration: %v", err)
	}
}

// Same race as TestSQLiteInit_LegacyWorkDirDroppedConcurrentlyIsNotAnError,
// on a shared MySQL: another member's Init drops work_dir between this
// Init's column check and its DROP (which then fails with error 1091).
func TestMySQLRepository_InitLegacyWorkDirDroppedConcurrentlyIsNotAnError(t *testing.T) {
	repo := newTestMySQLRepo(t)
	if _, err := repo.db.Exec(`ALTER TABLE projects ADD COLUMN work_dir TEXT NOT NULL AFTER prefix`); err != nil {
		t.Fatalf("re-adding legacy work_dir column: %v", err)
	}
	other, err := NewMySQLRepository(mysqlTestConfig(t))
	if err != nil {
		t.Fatalf("NewMySQLRepository: %v", err)
	}
	t.Cleanup(func() { other.db.Close() })

	setLegacyWorkDirDropHook(t, func() {
		if err := other.Init(); err != nil {
			t.Errorf("concurrent Init: %v", err)
		}
	})

	if err := repo.Init(); err != nil {
		t.Fatalf("Init should treat the already-dropped column as success, got %v", err)
	}
	if exists, err := repo.mysqlColumnExists("projects", "work_dir"); err != nil || exists {
		t.Fatalf("projects.work_dir should be gone (exists=%v, err=%v)", exists, err)
	}
}

// TestMySQLRepository_InitBackfillsNullTicketPriority is the MySQL half of
// the DFLT-00083 migration test (see assertNullPriorityBackfill): NULL
// priorities become MEDIUM, HIGH/LOW and updated_at are untouched, and a
// second Init changes nothing.
func TestMySQLRepository_InitBackfillsNullTicketPriority(t *testing.T) {
	repo := newTestMySQLRepo(t)
	proj, err := repo.CreateProject("Priority Backfill", "PRIB")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	ids := insertLegacyPriorityTickets(t, repo.db, proj.ID, proj.Prefix)

	assertNullPriorityBackfill(t, repo.db, repo.Init, ids)
}

// TestMySQLRepository_UpdateTicketFillsDefaultForLegacyNullPriority is the
// MySQL half of assertUpdateTicketFillsLegacyPriority.
func TestMySQLRepository_UpdateTicketFillsDefaultForLegacyNullPriority(t *testing.T) {
	repo := newTestMySQLRepo(t)
	proj, err := repo.CreateProject("Priority Update", "PRIU")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	ids := insertLegacyPriorityTickets(t, repo.db, proj.ID, proj.Prefix)

	assertUpdateTicketFillsLegacyPriority(t, repo.db, repo.UpdateTicket, ids)
}

// TestMySQLRepository_InitIssuesNoUpdateWhenNoPriorityNeedsBackfill is the
// MySQL half of the DFLT-00100 guard (CHK-07): MySQL's Init ran the
// backfill's unindexed, whole-table UPDATE on every command, holding a write
// lock each time. countBackfillUpdates observes the UPDATE directly, since
// MySQL has no counterpart to SQLite's total_changes().
func TestMySQLRepository_InitIssuesNoUpdateWhenNoPriorityNeedsBackfill(t *testing.T) {
	repo := newTestMySQLRepo(t)
	proj, err := repo.CreateProject("Priority Guard", "PRIG")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	// CreateTicket always stores an explicit priority, so nothing is left
	// to migrate -- the steady state of any DB used after DFLT-00083.
	if _, err := repo.CreateTicket(proj.ID, domain.Ticket{
		Title: "explicit", Status: domain.TicketTODO, Priority: domain.TicketPriorityHigh,
	}); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	var updates int
	testHookTicketPriorityBackfillUpdate = func() { updates++ }
	t.Cleanup(func() { testHookTicketPriorityBackfillUpdate = nil })

	if err := repo.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if updates != 0 {
		t.Errorf("Init issued %d backfill UPDATEs on a DB with nothing to migrate, want 0", updates)
	}

	// The guard must not have disabled the migration: a legacy row still
	// gets one UPDATE, and only one.
	insertLegacyPriorityTickets(t, repo.db, proj.ID, proj.Prefix)
	if err := repo.Init(); err != nil {
		t.Fatalf("Init (with rows to migrate): %v", err)
	}
	if updates != 1 {
		t.Errorf("Init issued %d backfill UPDATEs with legacy rows present, want 1", updates)
	}
	if err := repo.Init(); err != nil {
		t.Fatalf("Init (after the backfill): %v", err)
	}
	if updates != 1 {
		t.Errorf("Init issued another backfill UPDATE after the rows were migrated (%d total), want 1", updates)
	}
}
