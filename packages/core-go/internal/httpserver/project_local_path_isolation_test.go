package httpserver

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"

	_ "modernc.org/sqlite"
)

// Completion criterion 6 (DFLT-00080): two environments sharing one DB can
// give the same project different local paths without affecting each other.
// Each "environment" is its own Server with its own WorkDir/HomeDir (hence
// its own graph-config.json), both backed by the same SQLite file.

type sharedEnv struct {
	s    *Server
	repo store.GraphRepository
}

func newSharedDBEnvs(t *testing.T) (dbPath string, a, b sharedEnv) {
	t.Helper()
	dbPath = filepath.Join(t.TempDir(), "shared.db")
	mk := func() sharedEnv {
		repo, err := store.NewSQLiteRepository(dbPath)
		if err != nil {
			t.Fatalf("NewSQLiteRepository: %v", err)
		}
		if err := repo.Init(); err != nil {
			t.Fatalf("Init: %v", err)
		}
		cfg := Config{
			ArtifactsDir:      t.TempDir(),
			WorkDir:           t.TempDir(),
			HomeDir:           t.TempDir(),
			UserExtensionsDir: t.TempDir(),
			TerminalWorkDir:   "/terminal-fallback",
		}
		return sharedEnv{s: New(repo, engine.New(repo), cfg), repo: repo}
	}
	return dbPath, mk(), mk()
}

func listLocalPath(t *testing.T, s *Server, projectID string) string {
	t.Helper()
	rec := doJSON(t, s, http.MethodGet, "/api/projects", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/projects: %d %s", rec.Code, rec.Body.String())
	}
	var list []projectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	for _, p := range list {
		if p.ID == projectID {
			return p.LocalPath
		}
	}
	t.Fatalf("project %s not listed", projectID)
	return ""
}

func patchLocalPath(t *testing.T, s *Server, projectID, path string) {
	t.Helper()
	rec := doJSON(t, s, http.MethodPatch, "/api/projects/"+projectID, map[string]any{"local_path": path})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH local_path=%q: %d %s", path, rec.Code, rec.Body.String())
	}
}

func createSharedProject(t *testing.T, env sharedEnv) projectResponse {
	t.Helper()
	rec := doJSON(t, env.s, http.MethodPost, "/api/projects", map[string]any{"name": "Shared"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/projects: %d %s", rec.Code, rec.Body.String())
	}
	var p projectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProjectLocalPathIsolation_APIReturnsOwnPath(t *testing.T) {
	_, a, b := newSharedDBEnvs(t)
	shared := createSharedProject(t, a)

	patchLocalPath(t, a.s, shared.ID, "/home/a/shared")
	patchLocalPath(t, b.s, shared.ID, "/home/b/shared")

	if got := listLocalPath(t, a.s, shared.ID); got != "/home/a/shared" {
		t.Errorf("env A local_path = %q", got)
	}
	if got := listLocalPath(t, b.s, shared.ID); got != "/home/b/shared" {
		t.Errorf("env B local_path = %q", got)
	}
}

// Two environments sharing one DB resolve the same project's local path
// independently: each one's Claude launch directory comes from its own
// graph-config.json, and neither is disturbed by the other writing its own.
//
// This used to assert the same thing for catalog resolution as well
// (loadCatalogForTicket). DFLT-00103 removed the only HTTP handler that
// loaded a catalog for a ticket, and that helper with it, so the launch
// directory is now the whole of what this pair of environments resolves
// per-ticket.
func TestProjectLocalPathIsolation_LaunchDirUsesOwnPath(t *testing.T) {
	_, a, b := newSharedDBEnvs(t)
	shared := createSharedProject(t, a)
	ticket, err := a.repo.CreateTicket(shared.ID, domain.Ticket{Title: "T-1", Status: domain.TicketTODO})
	if err != nil {
		t.Fatal(err)
	}

	for _, env := range []sharedEnv{a, b} {
		local := t.TempDir()
		patchLocalPath(t, env.s, shared.ID, local)

		if got := env.s.resolveLaunchWorkDir("", ticket.ID); got != local {
			t.Errorf("launch dir = %q, want %q", got, local)
		}
	}
	// Re-check A after B wrote its own path: A still resolves to its own.
	aLocal := a.s.projectLocalPath(shared.ID)
	if got := a.s.resolveLaunchWorkDir("", ticket.ID); got != aLocal || got == b.s.projectLocalPath(shared.ID) {
		t.Errorf("env A launch dir changed after env B's PATCH: %q", got)
	}
}

func TestProjectLocalPathIsolation_ChangeAndClearDoNotTouchOtherEnv(t *testing.T) {
	_, a, b := newSharedDBEnvs(t)
	shared := createSharedProject(t, a)
	patchLocalPath(t, a.s, shared.ID, "/home/a/shared")
	patchLocalPath(t, b.s, shared.ID, "/home/b/shared")

	bPath := runtimeconfig.ResolvePath(b.s.cfg.WorkDir, b.s.cfg.HomeDir)
	before, err := os.ReadFile(bPath)
	if err != nil {
		t.Fatal(err)
	}

	patchLocalPath(t, a.s, shared.ID, "/home/a/shared2")
	patchLocalPath(t, a.s, shared.ID, "")

	after, err := os.ReadFile(bPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("env B's graph-config.json changed:\nbefore %s\nafter  %s", before, after)
	}
	if got := listLocalPath(t, b.s, shared.ID); got != "/home/b/shared" {
		t.Errorf("env B local_path = %q, want /home/b/shared", got)
	}
	if got := listLocalPath(t, a.s, shared.ID); got != "" {
		t.Errorf("env A local_path after clear = %q, want \"\"", got)
	}
}

func TestProjectLocalPathIsolation_DBHoldsNoPaths(t *testing.T) {
	dbPath, a, b := newSharedDBEnvs(t)
	shared := createSharedProject(t, a)
	patchLocalPath(t, a.s, shared.ID, "/home/a/shared")
	patchLocalPath(t, b.s, shared.ID, "/home/b/shared")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT * FROM projects`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	for _, c := range cols {
		if c == "work_dir" {
			t.Error("projects table must not have a work_dir column")
		}
	}
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i, v := range vals {
			if strings.Contains(v.String, "/home/a/shared") || strings.Contains(v.String, "/home/b/shared") {
				t.Errorf("projects.%s holds a local path: %q", cols[i], v.String)
			}
		}
	}
}
