package httpserver

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// newBareTestServer is like newTestServer but does NOT create any project or
// select a current one -- for tests that specifically exercise the
// no-project-yet state (creating the first project, POST /api/tickets
// failing with NO_CURRENT_PROJECT, etc).
func newBareTestServer(t *testing.T) (*Server, store.GraphRepository) {
	t.Helper()
	repo, err := store.NewSQLiteRepository(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	if err := repo.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	eng := engine.New(repo)
	cfg := Config{ArtifactsDir: t.TempDir()}
	return New(repo, eng, cfg), repo
}

// Scenario: a project can be created by specifying a name, prefix, and
// working folder.
func TestHandleCreateProject_ExplicitPrefix(t *testing.T) {
	s, _ := newBareTestServer(t)
	workDir := t.TempDir()

	rec := doJSON(t, s, http.MethodPost, "/api/projects", map[string]any{
		"name": "Sample Project", "prefix": "SMPL", "work_dir": workDir,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var p domain.Project
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Name != "Sample Project" || p.Prefix != "SMPL" || p.WorkDir != workDir {
		t.Errorf("unexpected project: %+v", p)
	}
}

// Scenario: omitting the prefix auto-generates one from the alphanumeric
// characters in the project name.
func TestHandleCreateProject_AutoPrefixFromAlnumName(t *testing.T) {
	s, _ := newBareTestServer(t)

	rec := doJSON(t, s, http.MethodPost, "/api/projects", map[string]any{
		"name": "My Project", "work_dir": t.TempDir(),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var p domain.Project
	json.Unmarshal(rec.Body.Bytes(), &p)
	if p.Prefix != "MYPRO" {
		t.Errorf("expected MYPRO, got %q", p.Prefix)
	}
}

// Scenario: an explicitly-specified prefix longer than 5 alphanumeric
// characters, or containing non-alphanumeric characters, is an error.
func TestHandleCreateProject_InvalidPrefixFormat(t *testing.T) {
	s, _ := newBareTestServer(t)

	rec := doJSON(t, s, http.MethodPost, "/api/projects", map[string]any{
		"name": "Sample", "prefix": "ABCDEF", "work_dir": t.TempDir(),
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	apiErr := decodeError(t, rec)
	if apiErr.Code != domain.ErrCodeInvalidPrefix {
		t.Errorf("expected %s, got %s", domain.ErrCodeInvalidPrefix, apiErr.Code)
	}
}

// Scenario: an explicitly-specified prefix that duplicates an existing
// project's prefix case-insensitively is an error.
func TestHandleCreateProject_DuplicatePrefixCaseInsensitive(t *testing.T) {
	s, _ := newBareTestServer(t)
	doJSON(t, s, http.MethodPost, "/api/projects", map[string]any{
		"name": "P1", "prefix": "ABCDE", "work_dir": t.TempDir(),
	})

	rec := doJSON(t, s, http.MethodPost, "/api/projects", map[string]any{
		"name": "Another Project", "prefix": "abcde", "work_dir": t.TempDir(),
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	apiErr := decodeError(t, rec)
	if apiErr.Code != domain.ErrCodePrefixTaken {
		t.Errorf("expected %s, got %s", domain.ErrCodePrefixTaken, apiErr.Code)
	}
}

// Scenario: a project's prefix cannot be changed after creation.
// PATCH /api/projects/{id} has no prefix field in its body struct at all, so
// even attempting to send one is simply ignored.
func TestHandleUpdateProject_CannotChangePrefix(t *testing.T) {
	s, repo := newBareTestServer(t)
	created, err := repo.CreateProject("P", "FIXED", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	rec := doJSON(t, s, http.MethodPatch, "/api/projects/"+created.ID, map[string]any{
		"prefix": "OTHER", "name": "renamed",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated domain.Project
	json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated.Prefix != "FIXED" {
		t.Errorf("expected prefix to remain FIXED, got %q", updated.Prefix)
	}
	if updated.Name != "renamed" {
		t.Errorf("expected name updated, got %q", updated.Name)
	}
}

// Scenario: previously-created projects can be listed.
func TestHandleListProjects(t *testing.T) {
	s, repo := newBareTestServer(t)
	repo.CreateProject("Project A", "", t.TempDir())
	repo.CreateProject("Project B", "", t.TempDir())

	rec := doJSON(t, s, http.MethodGet, "/api/projects", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var projects []domain.Project
	json.Unmarshal(rec.Body.Bytes(), &projects)
	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projects))
	}
}

// Scenario: the current project can be switched, and GET /api/current-project
// returns null when none is selected yet.
func TestCurrentProjectGetAndSwitch(t *testing.T) {
	s, repo := newBareTestServer(t)

	rec := doJSON(t, s, http.MethodGet, "/api/current-project", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "null\n" {
		t.Fatalf("expected null before any project is selected, got %q", rec.Body.String())
	}

	a, _ := repo.CreateProject("Project A", "", t.TempDir())
	b, _ := repo.CreateProject("Project B", "", t.TempDir())

	rec = doJSON(t, s, http.MethodPut, "/api/current-project", map[string]any{"project_id": a.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/current-project", nil)
	var cur domain.Project
	json.Unmarshal(rec.Body.Bytes(), &cur)
	if cur.ID != a.ID {
		t.Fatalf("expected current project %q, got %q", a.ID, cur.ID)
	}

	rec = doJSON(t, s, http.MethodPut, "/api/current-project", map[string]any{"project_id": b.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/current-project", nil)
	json.Unmarshal(rec.Body.Bytes(), &cur)
	if cur.ID != b.ID {
		t.Fatalf("expected current project %q, got %q", b.ID, cur.ID)
	}
}

// Scenario: switching projects shows only that project's tickets in the
// list, and ?all=true fetches across every project.
func TestHandleListTickets_ScopedToCurrentProject(t *testing.T) {
	s, repo := newBareTestServer(t)
	a, _ := repo.CreateProject("Project A", "", t.TempDir())
	b, _ := repo.CreateProject("Project B", "", t.TempDir())
	ta, _ := repo.CreateTicket(a.ID, domain.Ticket{Title: "A-1", Status: domain.TicketTODO})
	tb, _ := repo.CreateTicket(b.ID, domain.Ticket{Title: "B-1", Status: domain.TicketTODO})

	repo.SetCurrentProjectID(a.ID)
	rec := doJSON(t, s, http.MethodGet, "/api/tickets", nil)
	var list []domain.Ticket
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].ID != ta.ID {
		t.Fatalf("expected only ticket %q for current project A, got %+v", ta.ID, list)
	}

	repo.SetCurrentProjectID(b.ID)
	rec = doJSON(t, s, http.MethodGet, "/api/tickets", nil)
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].ID != tb.ID {
		t.Fatalf("expected only ticket %q for current project B, got %+v", tb.ID, list)
	}

	rec = doJSON(t, s, http.MethodGet, "/api/tickets?all=true", nil)
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 2 {
		t.Fatalf("expected 2 tickets with ?all=true, got %d", len(list))
	}
}

// Scenario: creating a ticket when no project exists at all is an error.
func TestHandleCreateTicket_NoCurrentProjectFails(t *testing.T) {
	s, _ := newBareTestServer(t)

	rec := doJSON(t, s, http.MethodPost, "/api/tickets", map[string]any{"title": "t"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	apiErr := decodeError(t, rec)
	if apiErr.Code != domain.ErrCodeNoCurrentProject {
		t.Errorf("expected %s, got %s", domain.ErrCodeNoCurrentProject, apiErr.Code)
	}
}

// DFLT-00025 regression guard: create-ticket (CLI) now prefers the project
// whose work_dir contains its cwd, but POST /api/tickets must keep resolving
// only via the current project, whatever the server process's cwd is. A
// project whose work_dir is exactly the server's cwd is registered to make
// sure it is not picked up.
func TestHandleCreateTicket_IgnoresServerCwdUsesCurrentProject(t *testing.T) {
	s, repo := newBareTestServer(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	cwdProj, err := repo.CreateProject("GraphOps", "", cwd)
	if err != nil {
		t.Fatalf("CreateProject(cwd): %v", err)
	}
	current, err := repo.CreateProject("TECH BLOG", "", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject(current): %v", err)
	}
	if err := repo.SetCurrentProjectID(current.ID); err != nil {
		t.Fatalf("SetCurrentProjectID: %v", err)
	}

	rec := doJSON(t, s, http.MethodPost, "/api/tickets", map[string]any{"title": "t"})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("expected success, got %d: %s", rec.Code, rec.Body.String())
	}
	var ticket domain.Ticket
	if err := json.Unmarshal(rec.Body.Bytes(), &ticket); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ticket.ProjectID != current.ID {
		t.Fatalf("expected ticket in current project %s, got %s (cwd project %s)", current.ID, ticket.ProjectID, cwdProj.ID)
	}
}

// DFLT-00025 regression guard: with projects registered (including one
// matching the server's cwd) but none selected, POST /api/tickets still
// fails with NO_CURRENT_PROJECT.
func TestHandleCreateTicket_ProjectsButNoCurrentProjectFails(t *testing.T) {
	s, repo := newBareTestServer(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if _, err := repo.CreateProject("GraphOps", "", cwd); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := repo.CreateProject("TECH BLOG", "", t.TempDir()); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	rec := doJSON(t, s, http.MethodPost, "/api/tickets", map[string]any{"title": "t"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if apiErr := decodeError(t, rec); apiErr.Code != domain.ErrCodeNoCurrentProject {
		t.Errorf("expected %s, got %s", domain.ErrCodeNoCurrentProject, apiErr.Code)
	}
}

// Scenario: ticket IDs are numbered in "prefix-sequence (5 digits,
// zero-padded)" format, incrementing on successive creations.
func TestHandleCreateTicket_IDFormatAndIncrement(t *testing.T) {
	s, repo := newBareTestServer(t)
	proj, _ := repo.CreateProject("P", "ABCDE", t.TempDir())
	repo.SetCurrentProjectID(proj.ID)

	rec := doJSON(t, s, http.MethodPost, "/api/tickets", map[string]any{"title": "t1"})
	var t1 domain.Ticket
	json.Unmarshal(rec.Body.Bytes(), &t1)
	if t1.ID != "ABCDE-00001" {
		t.Errorf("expected ABCDE-00001, got %q", t1.ID)
	}

	rec = doJSON(t, s, http.MethodPost, "/api/tickets", map[string]any{"title": "t2"})
	var t2 domain.Ticket
	json.Unmarshal(rec.Body.Bytes(), &t2)
	if t2.ID != "ABCDE-00002" {
		t.Errorf("expected ABCDE-00002, got %q", t2.ID)
	}
}

// Scenario: selecting a project launches a terminal session targeting its
// working folder (work_dir resolution priority: explicit project_id).
func TestResolveLaunchWorkDir_ExplicitProjectID(t *testing.T) {
	s, repo := newBareTestServer(t)
	proj, _ := repo.CreateProject("P", "", "/work/project-c")

	got := s.resolveLaunchWorkDir(proj.ID, "")
	if got != "/work/project-c" {
		t.Errorf("expected /work/project-c, got %q", got)
	}
}

func TestResolveLaunchWorkDir_ViaTicketProject(t *testing.T) {
	s, repo := newBareTestServer(t)
	proj, _ := repo.CreateProject("P", "", "/work/project-c")
	ticket, _ := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO})

	got := s.resolveLaunchWorkDir("", ticket.ID)
	if got != "/work/project-c" {
		t.Errorf("expected /work/project-c via ticket's project, got %q", got)
	}
}

func TestResolveLaunchWorkDir_FallsBackToGlobalConfig(t *testing.T) {
	s, _ := newBareTestServer(t)
	s.cfg.TerminalWorkDir = "/fallback"

	got := s.resolveLaunchWorkDir("", "")
	if got != "/fallback" {
		t.Errorf("expected fallback /fallback, got %q", got)
	}
}
