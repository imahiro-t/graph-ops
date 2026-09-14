package store

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// sqliteColumnNames returns table's column names via PRAGMA table_info. Used
// by tests across this package to assert a fresh schema already has a given
// column.
func sqliteColumnNames(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scanning PRAGMA table_info(%s): %v", table, err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating PRAGMA table_info(%s): %v", table, err)
	}
	return cols
}

func newTestRepo(t *testing.T) *SQLiteRepository {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	repo, err := NewSQLiteRepository(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	if err := repo.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return repo
}

// newTestRepoWithProject is newTestRepo plus a ready-to-use project, for
// tests that only care about ticket/node behavior and don't want to repeat
// the CreateProject boilerplate.
func newTestRepoWithProject(t *testing.T) (*SQLiteRepository, domain.Project) {
	t.Helper()
	repo := newTestRepo(t)
	proj, err := repo.CreateProject("Test Project", "TEST", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return repo, proj
}

func TestTicketCRUD(t *testing.T) {
	repo, proj := newTestRepoWithProject(t)

	created, err := repo.CreateTicket(proj.ID, domain.Ticket{
		Title: "t", Description: "d", Status: domain.TicketTODO,
		AutoExecutable: true,
	})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if created.CreatedAt == "" || created.UpdatedAt == "" {
		t.Fatalf("expected timestamps to be set, got %+v", created)
	}
	if created.ProjectID != proj.ID {
		t.Errorf("expected project_id %q, got %q", proj.ID, created.ProjectID)
	}

	got, err := repo.GetTicket(created.ID)
	if err != nil || got == nil {
		t.Fatalf("GetTicket: %v, %+v", err, got)
	}
	if got.Title != "t" || !got.AutoExecutable {
		t.Errorf("round-tripped ticket mismatch: %+v", got)
	}

	newTitle := "updated"
	updated, err := repo.UpdateTicket(created.ID, TicketPatch{Title: &newTitle})
	if err != nil {
		t.Fatalf("UpdateTicket: %v", err)
	}
	if updated.Title != "updated" {
		t.Errorf("title not updated: %+v", updated)
	}
	if updated.UpdatedAt == "" {
		t.Errorf("updated_at should be set")
	}

	list, err := repo.ListTickets()
	if err != nil || len(list) != 1 {
		t.Fatalf("ListTickets: %v, len=%d", err, len(list))
	}

	byProject, err := repo.ListTicketsByProject(proj.ID)
	if err != nil || len(byProject) != 1 {
		t.Fatalf("ListTicketsByProject: %v, len=%d", err, len(byProject))
	}

	if err := repo.DeleteTicket(created.ID); err != nil {
		t.Fatalf("DeleteTicket: %v", err)
	}
	got, _ = repo.GetTicket(created.ID)
	if got != nil {
		t.Errorf("expected ticket to be gone after delete")
	}
}

// TestTicketPriorityCRUD covers DFLT-00048's completion criteria: a ticket
// created without a priority reads back unset (nil), can be set to each of
// the three levels, changed between them, and explicitly cleared back to
// unset -- exercising TicketPatch.Priority's double-pointer three-state
// contract (nil patch = unchanged, *patch == nil = clear, *patch != nil =
// set) the same way TestTicketCRUD exercises Title.
func TestTicketPriorityCRUD(t *testing.T) {
	repo, proj := newTestRepoWithProject(t)

	created, err := repo.CreateTicket(proj.ID, domain.Ticket{
		Title: "優先度テスト", Status: domain.TicketTODO, AutoExecutable: true,
	})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if created.Priority != nil {
		t.Errorf("a newly created ticket should have no priority set, got %+v", created.Priority)
	}

	high := domain.TicketPriorityHigh
	highPtr := &high
	updated, err := repo.UpdateTicket(created.ID, TicketPatch{Priority: &highPtr})
	if err != nil {
		t.Fatalf("UpdateTicket(priority=HIGH): %v", err)
	}
	if updated.Priority == nil || *updated.Priority != domain.TicketPriorityHigh {
		t.Fatalf("expected priority HIGH, got %+v", updated.Priority)
	}

	got, err := repo.GetTicket(created.ID)
	if err != nil || got == nil || got.Priority == nil || *got.Priority != domain.TicketPriorityHigh {
		t.Fatalf("GetTicket after setting priority: %v, %+v", err, got)
	}

	// Change to another value.
	low := domain.TicketPriorityLow
	lowPtr := &low
	updated, err = repo.UpdateTicket(created.ID, TicketPatch{Priority: &lowPtr})
	if err != nil {
		t.Fatalf("UpdateTicket(priority=LOW): %v", err)
	}
	if updated.Priority == nil || *updated.Priority != domain.TicketPriorityLow {
		t.Fatalf("expected priority LOW after change, got %+v", updated.Priority)
	}

	// A patch that doesn't touch Priority at all must leave it unchanged.
	otherTitle := "優先度テスト(更新)"
	updated, err = repo.UpdateTicket(created.ID, TicketPatch{Title: &otherTitle})
	if err != nil {
		t.Fatalf("UpdateTicket(title only): %v", err)
	}
	if updated.Priority == nil || *updated.Priority != domain.TicketPriorityLow {
		t.Errorf("priority should be untouched by an unrelated patch, got %+v", updated.Priority)
	}

	// Explicitly clear back to unset.
	var clearedPtr *domain.TicketPriority
	updated, err = repo.UpdateTicket(created.ID, TicketPatch{Priority: &clearedPtr})
	if err != nil {
		t.Fatalf("UpdateTicket(priority=nil): %v", err)
	}
	if updated.Priority != nil {
		t.Errorf("expected priority to be cleared, got %+v", updated.Priority)
	}
	got, err = repo.GetTicket(created.ID)
	if err != nil || got == nil || got.Priority != nil {
		t.Fatalf("GetTicket after clearing priority: %v, %+v", err, got)
	}
}

// TestTicketIDFormat covers completion criterion 6: ticket IDs are
// "<prefix>-<seq:05d>" and increment per project.
func TestTicketIDFormat(t *testing.T) {
	repo, proj := newTestRepoWithProject(t)

	first, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t1", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if first.ID != proj.Prefix+"-00001" {
		t.Errorf("expected %s-00001, got %q", proj.Prefix, first.ID)
	}

	second, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t2", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if second.ID != proj.Prefix+"-00002" {
		t.Errorf("expected %s-00002, got %q", proj.Prefix, second.ID)
	}
}

// TestNodeIDFormat covers completion criterion 7: node IDs are
// "<ticketID>-<seq:02d>" (a ticket is capped at 99 nodes), with the counter
// reset per ticket.
func TestNodeIDFormat(t *testing.T) {
	repo, proj := newTestRepoWithProject(t)
	ticket, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	n1, err := repo.CreateNode(domain.GraphNode{TicketID: ticket.ID, Name: "a", Type: domain.NodeTypePlan, Status: domain.NodeTODO, MaxIterations: 3})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if n1.ID != ticket.ID+"-01" {
		t.Errorf("expected %s-01, got %q", ticket.ID, n1.ID)
	}

	n2, err := repo.CreateNode(domain.GraphNode{TicketID: ticket.ID, Name: "b", Type: domain.NodeTypeReview, Status: domain.NodeTODO, MaxIterations: 3})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if n2.ID != ticket.ID+"-02" {
		t.Errorf("expected %s-02, got %q", ticket.ID, n2.ID)
	}

	// A second ticket's node counter starts over from 1.
	ticket2, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t2", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	m1, err := repo.CreateNode(domain.GraphNode{TicketID: ticket2.ID, Name: "a", Type: domain.NodeTypePlan, Status: domain.NodeTODO, MaxIterations: 3})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if m1.ID != ticket2.ID+"-01" {
		t.Errorf("expected %s-01, got %q", ticket2.ID, m1.ID)
	}
}

func TestCreateTicketUnknownProjectFails(t *testing.T) {
	repo := newTestRepo(t)
	_, err := repo.CreateTicket("no-such-project", domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err == nil {
		t.Fatal("expected an error for an unknown project")
	}
	apiErr, ok := err.(*domain.APIError)
	if !ok || apiErr.Code != domain.ErrCodeProjectNotFound {
		t.Fatalf("expected ErrCodeProjectNotFound, got %v", err)
	}
}

func TestNodeGateAndCriteriaRoundTrip(t *testing.T) {
	repo, proj := newTestRepoWithProject(t)
	ticket, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	gate := "security_review"
	criteria := "Review from an OWASP Top 10 perspective."
	node, err := repo.CreateNode(domain.GraphNode{
		TicketID: ticket.ID, Name: "Security Review",
		Type: domain.NodeTypeReviewGate, Status: domain.NodeTODO,
		MaxIterations: 3, GateID: &gate, Criteria: &criteria,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if node.GateID == nil || *node.GateID != gate {
		t.Errorf("gate_id not persisted: %+v", node)
	}
	if node.Criteria == nil || *node.Criteria != criteria {
		t.Errorf("criteria not persisted: %+v", node)
	}

	got, err := repo.GetNode(node.ID)
	if err != nil || got == nil {
		t.Fatalf("GetNode: %v, %+v", err, got)
	}
	if got.Criteria == nil || *got.Criteria != criteria {
		t.Errorf("criteria not round-tripped via GetNode: %+v", got)
	}
}

func TestEdgeAndArtifactCRUD(t *testing.T) {
	repo, proj := newTestRepoWithProject(t)
	ticket, _ := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	n1, _ := repo.CreateNode(domain.GraphNode{TicketID: ticket.ID, Name: "a", Type: domain.NodeTypePlan, Status: domain.NodeTODO, MaxIterations: 3})
	n2, _ := repo.CreateNode(domain.GraphNode{TicketID: ticket.ID, Name: "b", Type: domain.NodeTypeReview, Status: domain.NodeTODO, MaxIterations: 3})

	edge, err := repo.CreateEdge(domain.GraphEdge{ID: "e1", TicketID: ticket.ID, FromNodeID: n1.ID, ToNodeID: n2.ID})
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	if edge.Condition != domain.EdgeAlways {
		t.Errorf("expected default condition 'always', got %q", edge.Condition)
	}

	edges, err := repo.ListEdgesByTicket(ticket.ID)
	if err != nil || len(edges) != 1 {
		t.Fatalf("ListEdgesByTicket: %v, len=%d", err, len(edges))
	}

	content := "plan body"
	art, err := repo.CreateArtifact(domain.Artifact{ID: "a1", TicketID: ticket.ID, NodeID: n1.ID, Name: "plan.md", Type: domain.ArtifactText, Content: &content})
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if art.Content == nil || *art.Content != content {
		t.Errorf("artifact content mismatch: %+v", art)
	}

	byNode, err := repo.ListArtifactsByNode(n1.ID)
	if err != nil || len(byNode) != 1 {
		t.Fatalf("ListArtifactsByNode: %v, len=%d", err, len(byNode))
	}

	if err := repo.ClearEdgesByTicket(ticket.ID); err != nil {
		t.Fatalf("ClearEdgesByTicket: %v", err)
	}
	edges, _ = repo.ListEdgesByTicket(ticket.ID)
	if len(edges) != 0 {
		t.Errorf("expected edges cleared, got %d", len(edges))
	}
}

// TestEmptyListsSerializeAsJSONArraysNotNull guards against a real regression:
// a nil Go slice marshals to JSON `null`, but the web UI unconditionally
// calls .filter()/.map()/.length on ticket.nodes/edges/artifacts, so a fresh
// (unrefined) ticket with zero nodes/edges/artifacts must come back as `[]`,
// not `null`, or the whole page crashes on render.
func TestEmptyListsSerializeAsJSONArraysNotNull(t *testing.T) {
	repo, proj := newTestRepoWithProject(t)
	ticket, _ := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO})

	detail, err := repo.GetTicketDetail(ticket.ID)
	if err != nil || detail == nil {
		t.Fatalf("GetTicketDetail: %v, %+v", err, detail)
	}

	b, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, field := range []string{"nodes", "edges", "artifacts"} {
		if raw[field] == nil {
			t.Errorf("field %q serialized as JSON null, want []", field)
		}
	}
}

func TestGetTicketDetailAssemblesGraph(t *testing.T) {
	repo, proj := newTestRepoWithProject(t)
	ticket, _ := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	n1, _ := repo.CreateNode(domain.GraphNode{TicketID: ticket.ID, Name: "a", Type: domain.NodeTypePlan, Status: domain.NodeTODO, MaxIterations: 3})
	repo.CreateEdge(domain.GraphEdge{ID: "e1", TicketID: ticket.ID, FromNodeID: n1.ID, ToNodeID: n1.ID})

	detail, err := repo.GetTicketDetail(ticket.ID)
	if err != nil || detail == nil {
		t.Fatalf("GetTicketDetail: %v, %+v", err, detail)
	}
	if len(detail.Nodes) != 1 || len(detail.Edges) != 1 {
		t.Errorf("expected 1 node and 1 edge, got %d nodes, %d edges", len(detail.Nodes), len(detail.Edges))
	}
}

// --- Projects ---

func TestCreateProjectExplicitPrefix(t *testing.T) {
	repo := newTestRepo(t)
	workDir := t.TempDir()
	proj, err := repo.CreateProject("Sample Project", "SMPL", workDir)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if proj.Name != "Sample Project" || proj.Prefix != "SMPL" || proj.WorkDir != workDir {
		t.Errorf("unexpected project: %+v", proj)
	}
}

func TestCreateProjectAutoPrefixAndDedup(t *testing.T) {
	repo := newTestRepo(t)
	p1, err := repo.CreateProject("My Project", "", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p1.Prefix != "MYPRO" {
		t.Errorf("expected MYPRO, got %q", p1.Prefix)
	}

	p2, err := repo.CreateProject("MyProject2", "", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if strings.EqualFold(p2.Prefix, p1.Prefix) {
		t.Errorf("expected a de-duplicated prefix, got the same one: %q", p2.Prefix)
	}
}

func TestCreateProjectRejectsRelativeWorkDir(t *testing.T) {
	repo := newTestRepo(t)
	_, err := repo.CreateProject("P", "PPPPP", "relative/path")
	if err == nil {
		t.Fatal("expected an error for a relative work_dir")
	}
}

func TestCreateProjectRejectsInvalidExplicitPrefix(t *testing.T) {
	repo := newTestRepo(t)
	_, err := repo.CreateProject("P", "ABCDEF", t.TempDir())
	apiErr, ok := err.(*domain.APIError)
	if !ok || apiErr.Code != domain.ErrCodeInvalidPrefix {
		t.Fatalf("expected ErrCodeInvalidPrefix, got %v", err)
	}
}

func TestCreateProjectRejectsDuplicateExplicitPrefixCaseInsensitive(t *testing.T) {
	repo := newTestRepo(t)
	if _, err := repo.CreateProject("P1", "ABCDE", t.TempDir()); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	_, err := repo.CreateProject("P2", "abcde", t.TempDir())
	apiErr, ok := err.(*domain.APIError)
	if !ok || apiErr.Code != domain.ErrCodePrefixTaken {
		t.Fatalf("expected ErrCodePrefixTaken, got %v", err)
	}
}

func TestUpdateProjectCannotChangePrefix(t *testing.T) {
	repo := newTestRepo(t)
	proj, err := repo.CreateProject("P", "FIXED", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	// ProjectPatch has no Prefix field at all -- there is no way to pass
	// one through UpdateProject, which is itself the enforcement of the
	// completion criterion: "a project's prefix cannot be changed after
	// creation".
	newName := "renamed"
	updated, err := repo.UpdateProject(proj.ID, ProjectPatch{Name: &newName})
	if err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	if updated.Prefix != "FIXED" {
		t.Errorf("expected prefix to remain FIXED, got %q", updated.Prefix)
	}
	if updated.Name != "renamed" {
		t.Errorf("expected name updated, got %q", updated.Name)
	}
}

func TestListProjectsAndSwitching(t *testing.T) {
	repo := newTestRepo(t)
	a, err := repo.CreateProject("Project A", "AAAAA", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject A: %v", err)
	}
	b, err := repo.CreateProject("Project B", "BBBBB", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject B: %v", err)
	}

	list, err := repo.ListProjects()
	if err != nil || len(list) != 2 {
		t.Fatalf("ListProjects: %v, len=%d", err, len(list))
	}

	if err := repo.SetCurrentProjectID(a.ID); err != nil {
		t.Fatalf("SetCurrentProjectID: %v", err)
	}
	cur, err := repo.GetCurrentProjectID()
	if err != nil || cur != a.ID {
		t.Fatalf("expected current project %q, got %q (err=%v)", a.ID, cur, err)
	}

	if err := repo.SetCurrentProjectID(b.ID); err != nil {
		t.Fatalf("SetCurrentProjectID: %v", err)
	}
	cur, err = repo.GetCurrentProjectID()
	if err != nil || cur != b.ID {
		t.Fatalf("expected current project %q, got %q (err=%v)", b.ID, cur, err)
	}
}

// TestDeleteProject_CascadesTicketsNodesAndClearsCurrentProject covers
// DeleteProject's transaction: the project's tickets (and, transitively,
// their nodes) must be gone, and app_state.current_project_id must no
// longer point at the deleted project.
func TestDeleteProject_CascadesTicketsNodesAndClearsCurrentProject(t *testing.T) {
	repo, proj := newTestRepoWithProject(t)
	ticket, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := repo.CreateNode(domain.GraphNode{TicketID: ticket.ID, Name: "a", Type: domain.NodeTypePlan, Status: domain.NodeTODO, MaxIterations: 3})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	if err := repo.SetCurrentProjectID(proj.ID); err != nil {
		t.Fatalf("SetCurrentProjectID: %v", err)
	}

	if err := repo.DeleteProject(proj.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	if got, err := repo.GetProject(proj.ID); err != nil || got != nil {
		t.Errorf("expected project to be gone, got %+v (err=%v)", got, err)
	}
	if got, err := repo.GetTicket(ticket.ID); err != nil || got != nil {
		t.Errorf("expected ticket to be gone (cascaded), got %+v (err=%v)", got, err)
	}
	if got, err := repo.GetNode(node.ID); err != nil || got != nil {
		t.Errorf("expected node to be gone (cascaded), got %+v (err=%v)", got, err)
	}
	if cur, err := repo.GetCurrentProjectID(); err != nil || cur != "" {
		t.Errorf("expected current project to be cleared, got %q (err=%v)", cur, err)
	}
}

// TestDeleteProject_NonExistentIsNoop mirrors DeleteTicket's own contract:
// deleting an id that doesn't exist is not an error.
func TestDeleteProject_NonExistentIsNoop(t *testing.T) {
	repo := newTestRepo(t)
	if err := repo.DeleteProject("proj-does-not-exist"); err != nil {
		t.Errorf("expected no error deleting a non-existent project, got %v", err)
	}
}

// TestDeleteProject_LeavesOtherProjectsCurrentProjectAlone covers the case
// where the deleted project is NOT the current one: current_project_id must
// survive untouched.
func TestDeleteProject_LeavesOtherProjectsCurrentProjectAlone(t *testing.T) {
	repo := newTestRepo(t)
	a, _ := repo.CreateProject("A", "AAAAA", t.TempDir())
	b, _ := repo.CreateProject("B", "BBBBB", t.TempDir())
	if err := repo.SetCurrentProjectID(a.ID); err != nil {
		t.Fatalf("SetCurrentProjectID: %v", err)
	}

	if err := repo.DeleteProject(b.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	if cur, err := repo.GetCurrentProjectID(); err != nil || cur != a.ID {
		t.Errorf("expected current project to remain %q, got %q (err=%v)", a.ID, cur, err)
	}
}

func TestGetCurrentProjectIDDefaultsToEmpty(t *testing.T) {
	repo := newTestRepo(t)
	cur, err := repo.GetCurrentProjectID()
	if err != nil {
		t.Fatalf("GetCurrentProjectID: %v", err)
	}
	if cur != "" {
		t.Errorf("expected empty current project before any is selected, got %q", cur)
	}
}

// TestTicketsScopedPerProject covers completion criterion 4: switching the
// current project changes which tickets ListTicketsByProject returns.
func TestTicketsScopedPerProject(t *testing.T) {
	repo := newTestRepo(t)
	a, _ := repo.CreateProject("A", "AAAAA", t.TempDir())
	b, _ := repo.CreateProject("B", "BBBBB", t.TempDir())

	ta, err := repo.CreateTicket(a.ID, domain.Ticket{Title: "A-1", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket A: %v", err)
	}
	tb, err := repo.CreateTicket(b.ID, domain.Ticket{Title: "B-1", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket B: %v", err)
	}

	listA, err := repo.ListTicketsByProject(a.ID)
	if err != nil || len(listA) != 1 || listA[0].ID != ta.ID {
		t.Fatalf("expected only ticket %q for project A, got %+v (err=%v)", ta.ID, listA, err)
	}
	listB, err := repo.ListTicketsByProject(b.ID)
	if err != nil || len(listB) != 1 || listB[0].ID != tb.ID {
		t.Fatalf("expected only ticket %q for project B, got %+v (err=%v)", tb.ID, listB, err)
	}

	all, err := repo.ListTickets()
	if err != nil || len(all) != 2 {
		t.Fatalf("expected 2 tickets across all projects, got %d (err=%v)", len(all), err)
	}
}

// TestNewSQLiteRepository_CreatesMissingParentDirectory covers DFLT-00027's
// first-run case: the default DB path now lives under $HOME/.graph-ops, a
// directory that does not exist until something creates it. sql.Open
// connects lazily, so a missing parent would not fail in NewSQLiteRepository
// at all -- it would surface later as an opaque "unable to open database
// file" from the first query, which is why this asserts through Init (the
// first statement to actually touch the file) as well.
func TestNewSQLiteRepository_CreatesMissingParentDirectory(t *testing.T) {
	// Two levels deep, so the test fails if only the immediate parent is
	// created rather than the whole chain.
	dir := filepath.Join(t.TempDir(), ".graph-ops", "nested")
	dbPath := filepath.Join(dir, "graph.db")

	repo, err := NewSQLiteRepository(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	if err := repo.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("os.Stat(%s): %v", dir, err)
	}
	if !info.IsDir() {
		t.Fatalf("%s was created but is not a directory", dir)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("os.Stat(%s): %v, want the db file to exist", dbPath, err)
	}
}

// TestNewSQLiteRepository_CreatesParentDirectoryUserOnly pins the mode of the
// directories NewSQLiteRepository creates. One DB file holds every project's
// tickets and artifact content, and under the default configuration it sits
// in $HOME/.graph-ops alongside config.json (which can carry a MySQL
// password), so its directory must not be readable or traversable by group or
// other. The mode has to match cmd/graph-engine's loadRuntimeConfig: MkdirAll
// never re-modes an existing directory, so in a fresh environment whichever
// of the two runs first is the one that decides it.
func TestNewSQLiteRepository_CreatesParentDirectoryUserOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits have no equivalent on windows")
	}
	root := t.TempDir()
	// Two levels deep, so the intermediate parent MkdirAll has to create
	// along the way is checked as well as the directory holding the file.
	parent := filepath.Join(root, ".graph-ops")
	dir := filepath.Join(parent, "nested")

	if _, err := NewSQLiteRepository(filepath.Join(dir, "graph.db")); err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}

	for _, path := range []string{parent, dir} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("os.Stat(%s): %v", path, err)
		}
		// Exact rather than "no group/other bits", so a future loosening
		// fails here. umask only clears bits and 0o700's owner bits survive
		// every umask realistically in use (022, 002).
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("%s has mode %#o, want 0700 (if the owner bits are the ones missing, check the umask)", path, got)
		}
	}
}

// TestNewSQLiteRepository_ParentDirectoryCreationFailureNamesThePath checks
// that a directory that cannot be created is reported with its path rather
// than as a bare errno -- the same reasoning as the artifacts directory in
// cmd/graph-engine: this runs before every command.
func TestNewSQLiteRepository_ParentDirectoryCreationFailureNamesThePath(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), ".graph-ops")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	_, err := NewSQLiteRepository(filepath.Join(blocked, "graph.db"))
	if err == nil {
		t.Fatal("expected an error when the db's parent directory cannot be created")
	}
	if !strings.Contains(err.Error(), blocked) {
		t.Errorf("error %q does not name the directory %q it failed on", err, blocked)
	}
}
