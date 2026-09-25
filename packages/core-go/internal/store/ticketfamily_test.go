package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00142: tickets.parent_ticket_id, the children listing and the
// migration that adds the column to an existing DB.

// assertParentChildContract is the backend-independent part: a child created
// with ParentTicketID reads back with it (GetTicket and the project listing),
// ListChildTickets returns the children in creation order and nothing for a
// childless ticket, and deleting the parent leaves the child with no parent.
func assertParentChildContract(t *testing.T, repo GraphRepository, projectID string) {
	t.Helper()
	parent, err := repo.CreateTicket(projectID, domain.Ticket{Title: "P", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket(parent): %v", err)
	}
	if parent.ParentTicketID != nil {
		t.Fatalf("a ticket created without a parent has parent %q", *parent.ParentTicketID)
	}
	c1, err := repo.CreateTicket(projectID, domain.Ticket{Title: "子1", Status: domain.TicketTODO, ParentTicketID: strPtr(parent.ID)})
	if err != nil {
		t.Fatalf("CreateTicket(child 1): %v", err)
	}
	c2, err := repo.CreateTicket(projectID, domain.Ticket{Title: "子2", Status: domain.TicketDone, ParentTicketID: strPtr(parent.ID)})
	if err != nil {
		t.Fatalf("CreateTicket(child 2): %v", err)
	}
	if c1.ParentTicketID == nil || *c1.ParentTicketID != parent.ID {
		t.Fatalf("CreateTicket returned parent %v, want %s", c1.ParentTicketID, parent.ID)
	}
	got, err := repo.GetTicket(c2.ID)
	if err != nil || got == nil || got.ParentTicketID == nil || *got.ParentTicketID != parent.ID {
		t.Fatalf("GetTicket(child 2) = %+v, %v; want parent %s", got, err, parent.ID)
	}

	children, err := ListChildTickets(repo, parent)
	if err != nil {
		t.Fatalf("ListChildTickets: %v", err)
	}
	if len(children) != 2 || children[0].ID != c1.ID || children[1].ID != c2.ID {
		t.Fatalf("children = %+v, want [%s %s] in creation order", children, c1.ID, c2.ID)
	}
	if children[1].Status != domain.TicketDone || children[0].Title != "子1" {
		t.Errorf("children carry the wrong fields: %+v", children)
	}
	none, err := ListChildTickets(repo, c1)
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("ListChildTickets(childless) = %#v, %v; want an empty non-nil slice", none, err)
	}

	listed, err := repo.ListTicketsByProject(projectID)
	if err != nil {
		t.Fatalf("ListTicketsByProject: %v", err)
	}
	for _, tk := range listed {
		switch tk.ID {
		case c1.ID, c2.ID:
			if tk.ParentTicketID == nil || *tk.ParentTicketID != parent.ID {
				t.Errorf("listing: %s has parent %v, want %s", tk.ID, tk.ParentTicketID, parent.ID)
			}
		case parent.ID:
			if tk.ParentTicketID != nil {
				t.Errorf("listing: the parent has parent %v", *tk.ParentTicketID)
			}
		}
	}

	if err := repo.DeleteTicket(parent.ID); err != nil {
		t.Fatalf("DeleteTicket(parent): %v", err)
	}
	after, err := repo.GetTicket(c1.ID)
	if err != nil || after == nil {
		t.Fatalf("the child must survive its parent's deletion: %+v, %v", after, err)
	}
	if after.ParentTicketID != nil {
		t.Fatalf("after deleting the parent, parent_ticket_id = %q, want NULL", *after.ParentTicketID)
	}
}

func TestSQLiteRepository_TicketParentAndChildren(t *testing.T) {
	repo, proj := newTestRepoWithProject(t)
	assertParentChildContract(t, repo, proj.ID)
}

func TestHTTPRepository_TicketParentAndChildren(t *testing.T) {
	_, srv := startPlugin(t, testToken)
	repo := openHTTP(t, srv.URL, testToken)
	proj, err := repo.CreateProject("HTTP", "HTTP")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, ok := any(repo).(TicketChildLister); ok {
		t.Fatal("HTTPRepository must not implement TicketChildLister (the protocol has no children endpoint)")
	}
	assertParentChildContract(t, repo, proj.ID)
}

func TestTicketJSON_ParentTicketIDIsAlwaysSerialized(t *testing.T) {
	raw, err := json.Marshal(domain.Ticket{ID: "X-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"parent_ticket_id":null`) {
		t.Fatalf("a ticket with no parent must serialize parent_ticket_id as null: %s", raw)
	}
}

// --- HTTP data source: protocol 1.1 vs 1.0 ---

func TestHTTPRepository_CreateTicketSendsParentOnlyWhenSet(t *testing.T) {
	p, srv := startPlugin(t, testToken)
	repo := openHTTP(t, srv.URL, testToken)
	proj, err := repo.CreateProject("HTTP", "HTTP")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	parent, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "P", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	p.ResetRequests()
	if _, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "C", Status: domain.TicketTODO, ParentTicketID: strPtr(parent.ID)}); err != nil {
		t.Fatalf("CreateTicket(child): %v", err)
	}
	if _, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "N", Status: domain.TicketTODO}); err != nil {
		t.Fatalf("CreateTicket(no parent): %v", err)
	}
	reqs := p.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	var withParent, without map[string]any
	if err := json.Unmarshal(reqs[0].Body, &withParent); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(reqs[1].Body, &without); err != nil {
		t.Fatal(err)
	}
	if withParent["parent_ticket_id"] != parent.ID {
		t.Errorf("create body parent_ticket_id = %v, want %s (body %s)", withParent["parent_ticket_id"], parent.ID, reqs[0].Body)
	}
	if _, present := without["parent_ticket_id"]; present {
		t.Errorf("a ticket without a parent must be sent without the key, as under 1.0: %s", reqs[1].Body)
	}
}

func TestHTTPRepository_Protocol10RefusesParentBeforeSending(t *testing.T) {
	p, srv := startPlugin(t, testToken)
	p.Version = "1.0"
	repo := openHTTP(t, srv.URL, testToken)
	proj, err := repo.CreateProject("Old", "OLD")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	// Without a parent, 1.0 works as before.
	plain, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "plain", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket without a parent on 1.0: %v", err)
	}
	if plain.ParentTicketID != nil {
		t.Fatalf("parent_ticket_id = %v, want nil", *plain.ParentTicketID)
	}

	p.ResetRequests()
	_, err = repo.CreateTicket(proj.ID, domain.Ticket{Title: "child", Status: domain.TicketTODO, ParentTicketID: strPtr(plain.ID)})
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeParentTicketUnsupported {
		t.Fatalf("CreateTicket with a parent on 1.0 = %v, want PARENT_TICKET_UNSUPPORTED", err)
	}
	if reqs := p.Requests(); len(reqs) != 0 {
		t.Fatalf("nothing may be sent to a 1.0 plugin, got %+v", reqs)
	}
}

// --- migration ---

// preParentSchemaDDL is schemaDDL as it was before DFLT-00142: tickets had no
// parent_ticket_id.
func preParentSchemaDDL(t *testing.T) string {
	t.Helper()
	const line = "\tparent_ticket_id TEXT REFERENCES tickets(id) ON DELETE SET NULL,\n"
	if !strings.Contains(schemaDDL, line) {
		t.Fatal("schemaDDL no longer contains the parent_ticket_id line this test removes")
	}
	return strings.Replace(schemaDDL, line, "", 1)
}

func TestSQLiteInit_AddsTicketParentColumnIdempotently(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "old.db")
	old, err := NewSQLiteRepository(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	if _, err := old.db.Exec(preParentSchemaDDL(t)); err != nil {
		t.Fatalf("applying the pre-DFLT-00142 schema: %v", err)
	}
	if _, err := old.db.Exec(`INSERT INTO projects (id, name, prefix, ticket_seq, created_at, updated_at) VALUES ('proj-old', 'Old', 'OLD', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec(`INSERT INTO tickets (id, project_id, title, description, status, auto_executable, blocked, node_seq, priority, created_at, updated_at)
		VALUES ('OLD-00001', 'proj-old', 'existing', '', 'TODO', 1, 0, 0, 'MEDIUM', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if sqliteColumnNames(t, old.db, "tickets")["parent_ticket_id"] {
		t.Fatal("test setup: the old schema must not have parent_ticket_id")
	}
	old.db.Close()

	repo, err := NewSQLiteRepository(dbPath)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	t.Cleanup(func() { repo.db.Close() })
	for i := 1; i <= 2; i++ {
		if err := repo.Init(); err != nil {
			t.Fatalf("Init #%d: %v", i, err)
		}
	}
	if !sqliteColumnNames(t, repo.db, "tickets")["parent_ticket_id"] {
		t.Fatal("Init must add tickets.parent_ticket_id")
	}
	var indexes int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_tickets_parent' AND tbl_name = 'tickets'`).Scan(&indexes); err != nil || indexes != 1 {
		t.Fatalf("idx_tickets_parent count = %d (err %v), want 1", indexes, err)
	}
	existing, err := repo.GetTicket("OLD-00001")
	if err != nil || existing == nil || existing.ParentTicketID != nil {
		t.Fatalf("existing ticket after migration = %+v, %v; want it readable with a NULL parent", existing, err)
	}
	// The migrated column carries the same ON DELETE SET NULL as a fresh one.
	assertParentChildContract(t, repo, "proj-old")
}

func TestSQLiteInit_FreshSchemaHasTicketParentIndex(t *testing.T) {
	repo := newTestRepo(t)
	var indexes int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_tickets_parent'`).Scan(&indexes); err != nil || indexes != 1 {
		t.Fatalf("idx_tickets_parent count = %d (err %v), want 1", indexes, err)
	}
}

func TestEnsureTicketParentColumn_ConcurrentAddIsNotAnError(t *testing.T) {
	calls := 0
	exists := func() (bool, error) {
		calls++
		return calls > 1, nil // missing at first, present after the failed ALTER
	}
	err := ensureTicketParentColumn("test", exists, func() error { return errors.New("duplicate column name: parent_ticket_id") })
	if err != nil {
		t.Fatalf("an ALTER that lost the race must not fail Init: %v", err)
	}
	err = ensureTicketParentColumn("test", func() (bool, error) { return false, nil }, func() error { return errors.New("disk full") })
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("a real ALTER failure must be returned, got %v", err)
	}
}
