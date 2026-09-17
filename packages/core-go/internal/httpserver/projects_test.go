package httpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
)

// newBareTestServer is like newTestServer but does NOT create any project or
// select a current one -- for tests that specifically exercise the
// no-project-yet state (creating the first project, POST /api/tickets
// failing with NO_CURRENT_PROJECT, etc).
//
// WorkDir and HomeDir are sandboxed temp dirs, so the graph-config.json the
// project API reads/writes (projectPaths, DFLT-00080) never touches the
// developer's real one or the package directory.
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
	cfg := Config{ArtifactsDir: t.TempDir(), WorkDir: t.TempDir(), HomeDir: t.TempDir()}
	return New(repo, eng, cfg), repo
}

// projectPathsOnDisk loads s's graph-config.json projectPaths.
func projectPathsOnDisk(t *testing.T, s *Server) map[string]string {
	t.Helper()
	cfg, _, err := runtimeconfig.Load(s.cfg.WorkDir, s.cfg.HomeDir)
	if err != nil {
		t.Fatalf("runtimeconfig.Load: %v", err)
	}
	return cfg.ProjectPaths
}

func setLocalPath(t *testing.T, s *Server, projectID, path string) {
	t.Helper()
	if _, err := runtimeconfig.SetProjectPath(s.cfg.WorkDir, s.cfg.HomeDir, projectID, path); err != nil {
		t.Fatalf("SetProjectPath: %v", err)
	}
}

func decodeProject(t *testing.T, body []byte) (projectResponse, map[string]any) {
	t.Helper()
	var p projectResponse
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	return p, raw
}

// Scenario: a project can be created by specifying a name, prefix, and
// local path -- the path goes to graph-config.json, not the DB.
func TestHandleCreateProject_ExplicitPrefixAndLocalPath(t *testing.T) {
	s, repo := newBareTestServer(t)

	rec := doJSON(t, s, http.MethodPost, "/api/projects", map[string]any{
		"name": "Sample Project", "prefix": "SMPL", "local_path": "/work/gamma",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	p, raw := decodeProject(t, rec.Body.Bytes())
	if p.Name != "Sample Project" || p.Prefix != "SMPL" || p.LocalPath != "/work/gamma" {
		t.Errorf("unexpected project: %+v", p)
	}
	if _, has := raw["work_dir"]; has {
		t.Errorf("response must not carry work_dir: %s", rec.Body.String())
	}
	if got := projectPathsOnDisk(t, s)[p.ID]; got != "/work/gamma" {
		t.Errorf("projectPaths[%s] = %q, want /work/gamma", p.ID, got)
	}
	if got, _ := repo.GetProject(p.ID); got == nil {
		t.Error("project should exist in the DB")
	}
}

func TestHandleCreateProject_WithoutLocalPath(t *testing.T) {
	s, _ := newBareTestServer(t)
	rec := doJSON(t, s, http.MethodPost, "/api/projects", map[string]any{"name": "Epsilon"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	p, raw := decodeProject(t, rec.Body.Bytes())
	if v, ok := raw["local_path"]; !ok || v != "" {
		t.Errorf("expected local_path \"\" in response, got %s", rec.Body.String())
	}
	if _, ok := projectPathsOnDisk(t, s)[p.ID]; ok {
		t.Error("no projectPaths entry should be created without local_path")
	}
}

// Replaces the removed store-level "relative path is rejected" test: the
// absolute-path rule now lives at the API layer (the store has no path).
func TestHandleCreateProject_RelativeLocalPathIs400(t *testing.T) {
	s, repo := newBareTestServer(t)
	rec := doJSON(t, s, http.MethodPost, "/api/projects", map[string]any{"name": "Zeta", "local_path": "relative/zeta"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if apiErr := decodeError(t, rec); apiErr.Code != domain.ErrCodeValidation {
		t.Errorf("expected %s, got %s", domain.ErrCodeValidation, apiErr.Code)
	}
	if projects, _ := repo.ListProjects(); len(projects) != 0 {
		t.Errorf("no project should be created, got %+v", projects)
	}
}

// Scenario: the DB insert succeeds but saving the local path fails -> 500
// with a message saying the project was created anyway.
func TestHandleCreateProject_LocalPathSaveFailureIs500AndSaysCreated(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based write failure cannot be simulated as root")
	}
	s, repo := newBareTestServer(t)
	var logBuf bytes.Buffer
	var mu sync.Mutex
	s.logger = slog.New(slog.NewTextHandler(&lockedWriter{w: &logBuf, mu: &mu}, nil))
	// No graph-config.json exists, so Save would create $HOME/.graph-ops;
	// a read-only HomeDir makes that fail.
	if err := os.Chmod(s.cfg.HomeDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(s.cfg.HomeDir, 0o700) })

	rec := doJSON(t, s, http.MethodPost, "/api/projects", map[string]any{"name": "Theta", "local_path": "/work/theta"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	apiErr := decodeError(t, rec)
	if !strings.Contains(apiErr.Message, "Theta") || !strings.Contains(apiErr.Message, "was created") {
		t.Errorf("message should say the project was created: %q", apiErr.Message)
	}
	// A dedicated code lets the Web UI tell "already created" apart from a
	// plain INTERNAL_ERROR (and so avoid a duplicate create).
	if apiErr.Code != domain.ErrCodeProjectCreatedLocalPathNotSaved {
		t.Errorf("expected code %s, got %s", domain.ErrCodeProjectCreatedLocalPathNotSaved, apiErr.Code)
	}
	projects, _ := repo.ListProjects()
	if len(projects) != 1 || projects[0].Name != "Theta" {
		t.Fatalf("project Theta should exist in the DB, got %+v", projects)
	}
	mu.Lock()
	defer mu.Unlock()
	logged := logBuf.String()
	if !strings.Contains(logged, "project_local_path_save_failed_after_create") || !strings.Contains(logged, projects[0].ID) {
		t.Errorf("expected a save-failure log line naming the project, got %q", logged)
	}
}

// Scenario: omitting the prefix auto-generates one from the alphanumeric
// characters in the project name.
func TestHandleCreateProject_AutoPrefixFromAlnumName(t *testing.T) {
	s, _ := newBareTestServer(t)

	rec := doJSON(t, s, http.MethodPost, "/api/projects", map[string]any{
		"name": "My Project",
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
		"name": "Sample", "prefix": "ABCDEF",
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
		"name": "P1", "prefix": "ABCDE",
	})

	rec := doJSON(t, s, http.MethodPost, "/api/projects", map[string]any{
		"name": "Another Project", "prefix": "abcde",
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
	created, err := repo.CreateProject("P", "FIXED")
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

func getProjectLocalPath(t *testing.T, s *Server, id string) string {
	t.Helper()
	rec := doJSON(t, s, http.MethodGet, "/api/projects/"+id, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET project: %d %s", rec.Code, rec.Body.String())
	}
	p, _ := decodeProject(t, rec.Body.Bytes())
	return p.LocalPath
}

func TestHandleUpdateProject_SetChangeRepeatAndClearLocalPath(t *testing.T) {
	s, repo := newBareTestServer(t)
	beta, _ := repo.CreateProject("Beta", "")

	// set on an unset project
	rec := doJSON(t, s, http.MethodPatch, "/api/projects/"+beta.ID, map[string]any{"local_path": "/work/beta"})
	if rec.Code != http.StatusOK {
		t.Fatalf("set: %d %s", rec.Code, rec.Body.String())
	}
	if p, _ := decodeProject(t, rec.Body.Bytes()); p.LocalPath != "/work/beta" {
		t.Errorf("PATCH response local_path = %q", p.LocalPath)
	}
	if got := getProjectLocalPath(t, s, beta.ID); got != "/work/beta" {
		t.Errorf("GET after set = %q", got)
	}

	// same value again is harmless
	rec = doJSON(t, s, http.MethodPatch, "/api/projects/"+beta.ID, map[string]any{"local_path": "/work/beta"})
	if rec.Code != http.StatusOK || projectPathsOnDisk(t, s)[beta.ID] != "/work/beta" {
		t.Fatalf("repeat: %d %s / %v", rec.Code, rec.Body.String(), projectPathsOnDisk(t, s))
	}

	// change
	doJSON(t, s, http.MethodPatch, "/api/projects/"+beta.ID, map[string]any{"local_path": "/work/beta2"})
	if got := getProjectLocalPath(t, s, beta.ID); got != "/work/beta2" {
		t.Errorf("GET after change = %q", got)
	}
	if paths := projectPathsOnDisk(t, s); len(paths) != 1 || paths[beta.ID] != "/work/beta2" {
		t.Errorf("projectPaths after change = %v", paths)
	}

	// name only leaves the path alone
	rec = doJSON(t, s, http.MethodPatch, "/api/projects/"+beta.ID, map[string]any{"name": "Beta Renamed"})
	if p, _ := decodeProject(t, rec.Body.Bytes()); p.Name != "Beta Renamed" || p.LocalPath != "/work/beta2" {
		t.Errorf("name-only PATCH = %+v", p)
	}

	// relative is rejected and nothing changes
	rec = doJSON(t, s, http.MethodPatch, "/api/projects/"+beta.ID, map[string]any{"local_path": "relative/beta"})
	if rec.Code != http.StatusBadRequest || decodeError(t, rec).Code != domain.ErrCodeValidation {
		t.Errorf("relative: %d %s", rec.Code, rec.Body.String())
	}
	if projectPathsOnDisk(t, s)[beta.ID] != "/work/beta2" {
		t.Error("relative PATCH must not change the stored path")
	}

	// clear
	doJSON(t, s, http.MethodPatch, "/api/projects/"+beta.ID, map[string]any{"local_path": ""})
	if got := getProjectLocalPath(t, s, beta.ID); got != "" {
		t.Errorf("GET after clear = %q", got)
	}
	if _, ok := projectPathsOnDisk(t, s)[beta.ID]; ok {
		t.Error("entry should be removed after clear")
	}
}

func TestHandleUpdateProject_UnknownProjectIs404AndWritesNothing(t *testing.T) {
	s, _ := newBareTestServer(t)
	rec := doJSON(t, s, http.MethodPatch, "/api/projects/proj-missing", map[string]any{"local_path": "/work/missing"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, ok := projectPathsOnDisk(t, s)["proj-missing"]; ok {
		t.Error("no entry should be written for an unknown project")
	}
}

func TestHandleDeleteProject_RemovesLocalPathEntry(t *testing.T) {
	s, repo := newBareTestServer(t)
	beta, _ := repo.CreateProject("Beta", "")
	setLocalPath(t, s, beta.ID, "/work/beta")

	rec := doJSON(t, s, http.MethodDelete, "/api/projects/"+beta.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, ok := projectPathsOnDisk(t, s)[beta.ID]; ok {
		t.Error("projectPaths entry should be removed on delete")
	}
}

func TestHandleDeleteProject_CleanupFailureStillSucceedsAndLogs(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based write failure cannot be simulated as root")
	}
	s, repo := newBareTestServer(t)
	var logBuf bytes.Buffer
	var mu sync.Mutex
	s.logger = slog.New(slog.NewTextHandler(&lockedWriter{w: &logBuf, mu: &mu}, nil))
	beta, _ := repo.CreateProject("Beta", "")
	setLocalPath(t, s, beta.ID, "/work/beta")
	// Saves go through a temp file + rename in the config file's directory,
	// so making that directory read-only is what makes the cleanup fail.
	dir := filepath.Dir(runtimeconfig.ResolvePath(s.cfg.WorkDir, s.cfg.HomeDir))
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	rec := doJSON(t, s, http.MethodDelete, "/api/projects/"+beta.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 despite cleanup failure, got %d: %s", rec.Code, rec.Body.String())
	}
	if got, _ := repo.GetProject(beta.ID); got != nil {
		t.Error("project should be deleted from the DB")
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(logBuf.String(), "project_local_path_cleanup_failed") || !strings.Contains(logBuf.String(), beta.ID) {
		t.Errorf("expected a cleanup-failure log line, got %q", logBuf.String())
	}
}

// A graph-config.json that cannot be parsed makes projectLocalPath fall back
// to "not set" (Claude launch / catalog loading keep working), but the reason
// is logged instead of being swallowed.
func TestProjectLocalPath_UnreadableConfigFallsBackAndLogs(t *testing.T) {
	s, repo := newBareTestServer(t)
	var logBuf bytes.Buffer
	var mu sync.Mutex
	s.logger = slog.New(slog.NewTextHandler(&lockedWriter{w: &logBuf, mu: &mu}, nil))
	beta, _ := repo.CreateProject("Beta", "")
	path := runtimeconfig.ResolvePath(s.cfg.WorkDir, s.cfg.HomeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := s.projectLocalPath(beta.ID); got != "" {
		t.Errorf("expected \"\" for an unreadable config, got %q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(logBuf.String(), "project_local_path_load_failed") || !strings.Contains(logBuf.String(), beta.ID) {
		t.Errorf("expected a load-failure log line, got %q", logBuf.String())
	}
}

type lockedWriter struct {
	w  *bytes.Buffer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// Scenario: previously-created projects can be listed, each with this
// environment's local_path ("" when unset) and no work_dir.
func TestHandleListProjects(t *testing.T) {
	s, repo := newBareTestServer(t)
	a, _ := repo.CreateProject("Project A", "")
	repo.CreateProject("Project B", "")
	setLocalPath(t, s, a.ID, "/work/alpha")

	rec := doJSON(t, s, http.MethodGet, "/api/projects", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var projects []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &projects)
	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projects))
	}
	want := map[string]string{"Project A": "/work/alpha", "Project B": ""}
	for _, p := range projects {
		if lp, ok := p["local_path"]; !ok || lp != want[p["name"].(string)] {
			t.Errorf("%v: local_path = %v, want %q", p["name"], lp, want[p["name"].(string)])
		}
		if _, has := p["work_dir"]; has {
			t.Errorf("%v must not carry work_dir", p["name"])
		}
	}
}

// Scenario: the current project can be switched, and GET /api/current-project
// returns null when none is selected yet. Both carry local_path.
func TestCurrentProjectGetAndSwitch(t *testing.T) {
	s, repo := newBareTestServer(t)

	rec := doJSON(t, s, http.MethodGet, "/api/current-project", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "null\n" {
		t.Fatalf("expected null before any project is selected, got %q", rec.Body.String())
	}

	a, _ := repo.CreateProject("Project A", "")
	b, _ := repo.CreateProject("Project B", "")
	setLocalPath(t, s, a.ID, "/work/alpha")

	for _, tc := range []struct {
		id, localPath string
	}{{a.ID, "/work/alpha"}, {b.ID, ""}} {
		rec = doJSON(t, s, http.MethodPut, "/api/current-project", map[string]any{"project_id": tc.id})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		put, raw := decodeProject(t, rec.Body.Bytes())
		if put.ID != tc.id || put.LocalPath != tc.localPath {
			t.Errorf("PUT response = %+v, want id %s local_path %q", put, tc.id, tc.localPath)
		}
		if _, has := raw["work_dir"]; has {
			t.Error("PUT response must not carry work_dir")
		}

		rec = doJSON(t, s, http.MethodGet, "/api/current-project", nil)
		cur, _ := decodeProject(t, rec.Body.Bytes())
		if cur.ID != tc.id || cur.LocalPath != tc.localPath {
			t.Fatalf("GET current = %+v, want id %s local_path %q", cur, tc.id, tc.localPath)
		}
	}
}

// Plan review condition 2: PATCH /api/projects/{id} (projectPaths) and PUT
// /api/settings/app (paginationPageSize) hitting the server at the same time
// must never lose each other's update in graph-config.json.
func TestConcurrentLocalPathAndAppSettingsWritesDoNotLoseUpdates(t *testing.T) {
	s, repo := newBareTestServer(t)
	proj, _ := repo.CreateProject("AAA", "")
	if _, err := runtimeconfig.Save(s.cfg.WorkDir, s.cfg.HomeDir, runtimeconfig.FileConfig{PaginationPageSize: 20}); err != nil {
		t.Fatal(err)
	}

	const rounds = 25
	var wg sync.WaitGroup
	errs := make(chan string, rounds*2)
	for i := 0; i < rounds; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			rec := doJSON(t, s, http.MethodPatch, "/api/projects/"+proj.ID, map[string]any{"local_path": fmt.Sprintf("/work/p%d", i%2+1)})
			if rec.Code != http.StatusOK {
				errs <- fmt.Sprintf("PATCH %d: %s", rec.Code, rec.Body.String())
			}
		}(i)
		go func() {
			defer wg.Done()
			rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{"paginationPageSize": 50})
			if rec.Code != http.StatusOK {
				errs <- fmt.Sprintf("PUT %d: %s", rec.Code, rec.Body.String())
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}

	cfg, _, err := runtimeconfig.Load(s.cfg.WorkDir, s.cfg.HomeDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.ProjectPaths[proj.ID]; got != "/work/p1" && got != "/work/p2" {
		t.Errorf("projectPaths[%s] = %q, want /work/p1 or /work/p2", proj.ID, got)
	}
	if cfg.PaginationPageSize != 50 {
		t.Errorf("paginationPageSize = %d, want 50", cfg.PaginationPageSize)
	}
}

// Scenario: switching projects shows only that project's tickets in the
// list, and ?all=true fetches across every project.
func TestHandleListTickets_ScopedToCurrentProject(t *testing.T) {
	s, repo := newBareTestServer(t)
	a, _ := repo.CreateProject("Project A", "")
	b, _ := repo.CreateProject("Project B", "")
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

// DFLT-00025 regression guard: create-ticket (CLI) prefers the project whose
// local path contains its cwd, but POST /api/tickets must keep resolving
// only via the current project, whatever the server process's cwd is. A
// project whose local path is exactly the server's cwd is registered to make
// sure it is not picked up.
func TestHandleCreateTicket_IgnoresServerCwdUsesCurrentProject(t *testing.T) {
	s, repo := newBareTestServer(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	cwdProj, err := repo.CreateProject("GraphOps", "")
	if err != nil {
		t.Fatalf("CreateProject(cwd): %v", err)
	}
	setLocalPath(t, s, cwdProj.ID, cwd)
	current, err := repo.CreateProject("TECH BLOG", "")
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
	cwdProj, err := repo.CreateProject("GraphOps", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	setLocalPath(t, s, cwdProj.ID, cwd)
	if _, err := repo.CreateProject("TECH BLOG", ""); err != nil {
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
	proj, _ := repo.CreateProject("P", "ABCDE")
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
// local path (resolution priority: explicit project_id).
func TestResolveLaunchWorkDir_ExplicitProjectID(t *testing.T) {
	s, repo := newBareTestServer(t)
	proj, _ := repo.CreateProject("P", "")
	setLocalPath(t, s, proj.ID, "/work/project-c")

	got := s.resolveLaunchWorkDir(proj.ID, "")
	if got != "/work/project-c" {
		t.Errorf("expected /work/project-c, got %q", got)
	}
}

func TestResolveLaunchWorkDir_ViaTicketProject(t *testing.T) {
	s, repo := newBareTestServer(t)
	proj, _ := repo.CreateProject("P", "")
	setLocalPath(t, s, proj.ID, "/work/project-c")
	ticket, _ := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO})

	got := s.resolveLaunchWorkDir("", ticket.ID)
	if got != "/work/project-c" {
		t.Errorf("expected /work/project-c via ticket's project, got %q", got)
	}
}

// A project with no local path in this environment falls back to
// TerminalWorkDir instead of launching in "" (DFLT-00080).
func TestResolveLaunchWorkDir_ProjectWithoutLocalPathFallsBack(t *testing.T) {
	s, repo := newBareTestServer(t)
	s.cfg.TerminalWorkDir = "/work/terminal"
	proj, _ := repo.CreateProject("Beta", "")
	ticket, _ := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO})

	if got := s.resolveLaunchWorkDir(proj.ID, ""); got != "/work/terminal" {
		t.Errorf("project_id without local path: got %q, want /work/terminal", got)
	}
	if got := s.resolveLaunchWorkDir("", ticket.ID); got != "/work/terminal" {
		t.Errorf("ticket without local path: got %q, want /work/terminal", got)
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
