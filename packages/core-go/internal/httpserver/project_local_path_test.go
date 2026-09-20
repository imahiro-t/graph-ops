package httpserver

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
)

// DFLT-00080: server-side consumers of a project's local path
// (graph-config.json's projectPaths) other than the project API itself --
// the settings scope and the app-settings writer.
//
// Catalog loading used to be a third consumer, covered here by
// TestLoadCatalogForTicket_*. DFLT-00103 removed GET
// /api/tickets/{id}/executable-nodes, the only handler that loaded a catalog
// for a ticket, and loadCatalogForTicket with it -- over HTTP a catalog is
// now only ever read through the settings API, which resolves the local path
// via settingsScope and is covered below.

func TestSettingsProjectScope_UsesLocalPath(t *testing.T) {
	s, repo := newBareTestServer(t)
	s.cfg.UserExtensionsDir = t.TempDir()
	alpha, _ := repo.CreateProject("Alpha", "")
	local := t.TempDir()
	setLocalPath(t, s, alpha.ID, local)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/node-types/implementation", map[string]any{
		"scope": "project", "project_id": alpha.ID, "text": "alpha rules",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	want := filepath.Join(local, ".graph-ops", "extensions", "node-types", "implementation.md")
	if raw, err := os.ReadFile(want); err != nil || string(raw) != "alpha rules" {
		t.Errorf("override should be written under the local path (%s): %q, %v", want, raw, err)
	}
}

func TestSettingsProjectScope_NoLocalPathIsProjectLocalPathNotSet(t *testing.T) {
	s, repo := newBareTestServer(t)
	s.cfg.UserExtensionsDir = t.TempDir()
	beta, _ := repo.CreateProject("Beta", "")
	cwd, _ := os.Getwd()
	_, cwdGraphOpsErr := os.Stat(filepath.Join(cwd, ".graph-ops"))

	for _, req := range []struct {
		method, path string
		body         map[string]any
	}{
		{http.MethodGet, "/api/settings/node-types/implementation?scope=project&project_id=" + beta.ID, nil},
		{http.MethodPut, "/api/settings/node-types/implementation", map[string]any{"scope": "project", "project_id": beta.ID, "text": "x"}},
		{http.MethodGet, "/api/settings/catalog?scope=project&project_id=" + beta.ID, nil},
		{http.MethodGet, "/api/settings/plan-template?scope=project&project_id=" + beta.ID, nil},
	} {
		rec := doJSON(t, s, req.method, req.path, req.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s: expected 400, got %d: %s", req.method, req.path, rec.Code, rec.Body.String())
			continue
		}
		if code := decodeError(t, rec).Code; code != domain.ErrCodeProjectLocalPathNotSet {
			t.Errorf("%s %s: code = %s, want %s", req.method, req.path, code, domain.ErrCodeProjectLocalPathNotSet)
		}
	}
	// Nothing was written relative to the server process's cwd.
	if _, err := os.Stat(filepath.Join(cwd, ".graph-ops")); (err == nil) != (cwdGraphOpsErr == nil) {
		t.Error("a .graph-ops directory appeared in the server's cwd")
	}
	for _, dir := range []string{s.cfg.WorkDir, s.cfg.HomeDir} {
		if _, err := os.Stat(filepath.Join(dir, ".graph-ops", "extensions")); err == nil {
			t.Errorf("unexpected extensions written under %s", dir)
		}
	}

	// The list endpoints treat such a project as "no team tier" rather than
	// failing, so the settings screen as a whole keeps working.
	for _, path := range []string{"/api/settings/node-types?project_id=" + beta.ID, "/api/settings/skills?project_id=" + beta.ID} {
		if rec := doJSON(t, s, http.MethodGet, path, nil); rec.Code != http.StatusOK {
			t.Errorf("GET %s: expected 200, got %d: %s", path, rec.Code, rec.Body.String())
		}
	}
	// And the global scope is unaffected.
	if rec := doJSON(t, s, http.MethodGet, "/api/settings/node-types/implementation?scope=global", nil); rec.Code != http.StatusOK {
		t.Errorf("global scope GET: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSettingsProjectScope_ExplicitTeamExtensionsDirWinsWithoutLocalPath(t *testing.T) {
	s, repo := newBareTestServer(t)
	s.cfg.UserExtensionsDir = t.TempDir()
	s.cfg.TeamExtensionsDir = t.TempDir()
	beta, _ := repo.CreateProject("Beta", "")
	override := filepath.Join(s.cfg.TeamExtensionsDir, "extensions", "node-types")
	if err := os.MkdirAll(override, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(override, "implementation.md"), []byte("team pinned"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, s, http.MethodGet, "/api/settings/node-types/implementation?scope=project&project_id="+beta.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		TierText string `json:"tier_text"`
	}
	mustDecode(t, rec, &body)
	if body.TierText != "team pinned" {
		t.Errorf("tier_text = %q, want the explicit TeamExtensionsDir override", body.TierText)
	}
}

// PUT /api/settings/app rewrites only its own fields; projectPaths survives.
func TestAppSettings_PutPreservesProjectPaths(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	if _, err := runtimeconfig.SetProjectPath(workDir, homeDir, "proj-aaa", "/home/a/alpha"); err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{"paginationPageSize": 30})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cfg, _, err := runtimeconfig.Load(workDir, homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectPaths["proj-aaa"] != "/home/a/alpha" || cfg.PaginationPageSize != 30 {
		t.Errorf("after PUT: projectPaths=%v pageSize=%d", cfg.ProjectPaths, cfg.PaginationPageSize)
	}
}
