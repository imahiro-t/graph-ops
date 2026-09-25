package store

import (
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00142 on MySQL (skipped without GRAPH_TEST_MYSQL_HOST; run
// dev/mysql/test.sh).

func TestMySQLRepository_TicketParentAndChildren(t *testing.T) {
	repo := newTestMySQLRepo(t)
	proj, err := repo.CreateProject("Family", "FAM")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	assertParentChildContract(t, repo, proj.ID)
}

// A parent and child in one project: DeleteProject's single
// "DELETE FROM tickets WHERE project_id = ?" must not trip over the
// self-referencing foreign key.
func TestMySQLRepository_DeleteProjectWithParentAndChild(t *testing.T) {
	repo := newTestMySQLRepo(t)
	proj, err := repo.CreateProject("Family", "FAM")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	parent, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "P", Status: domain.TicketTODO})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "C", Status: domain.TicketTODO, ParentTicketID: strPtr(parent.ID)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteProject(proj.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if left, err := repo.ListTicketsByProject(proj.ID); err != nil || len(left) != 0 {
		t.Fatalf("tickets left after DeleteProject: %+v, %v", left, err)
	}
}

func mysqlIndexColumnCount(t *testing.T, repo *MySQLRepository, table, index string) int {
	t.Helper()
	var n int
	if err := repo.db.QueryRow(
		`SELECT COUNT(*) FROM INFORMATION_SCHEMA.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND INDEX_NAME = ?`,
		table, index,
	).Scan(&n); err != nil {
		t.Fatalf("reading index %s: %v", index, err)
	}
	return n
}

func TestMySQLRepository_InitAddsTicketParentColumnIdempotently(t *testing.T) {
	repo := newTestMySQLRepo(t)
	// Turn the table back into its pre-DFLT-00142 shape.
	for _, stmt := range []string{
		`ALTER TABLE tickets DROP FOREIGN KEY fk_tickets_parent`,
		`ALTER TABLE tickets DROP INDEX idx_tickets_parent`,
		`ALTER TABLE tickets DROP COLUMN parent_ticket_id`,
	} {
		if _, err := repo.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if has, err := repo.mysqlColumnExists("tickets", "parent_ticket_id"); err != nil || has {
		t.Fatalf("test setup: parent_ticket_id should be gone (has=%v, err=%v)", has, err)
	}
	proj, err := repo.CreateProject("Old", "OLD")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := repo.db.Exec(`INSERT INTO tickets (id, project_id, title, description, status, auto_executable, blocked, node_seq, priority, created_at, updated_at)
		VALUES ('OLD-09999', ?, 'existing', '', 'TODO', 1, 0, 0, 'MEDIUM', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, proj.ID); err != nil {
		t.Fatalf("inserting an old-schema ticket: %v", err)
	}

	for i := 1; i <= 2; i++ {
		if err := repo.Init(); err != nil {
			t.Fatalf("Init #%d: %v", i, err)
		}
	}
	if has, err := repo.mysqlColumnExists("tickets", "parent_ticket_id"); err != nil || !has {
		t.Fatalf("Init must add parent_ticket_id (has=%v, err=%v)", has, err)
	}
	if n := mysqlIndexColumnCount(t, repo, "tickets", "idx_tickets_parent"); n != 1 {
		t.Fatalf("idx_tickets_parent has %d rows in STATISTICS, want exactly 1", n)
	}
	if has, err := repo.mysqlForeignKeyExists("tickets", "fk_tickets_parent"); err != nil || !has {
		t.Fatalf("Init must add fk_tickets_parent (has=%v, err=%v)", has, err)
	}
	existing, err := repo.GetTicket("OLD-09999")
	if err != nil || existing == nil || existing.ParentTicketID != nil {
		t.Fatalf("existing ticket after migration = %+v, %v; want it readable with a NULL parent", existing, err)
	}
	assertParentChildContract(t, repo, proj.ID)
}
