package httpserver

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00068: $HOME/.graph-ops is the user tier's root and must never be
// used as a project's (team tier's) settings directory. HOME is pinned to a
// temp dir so the developer's real ~/.graph-ops is never touched.

func TestSettingsProjectScope_UnderHomeWritesToProjectNotHome(t *testing.T) {
	s, repo := newBareTestServer(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	userRoot := filepath.Join(home, ".graph-ops")
	if err := os.MkdirAll(userRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	s.cfg.UserExtensionsDir = "" // default: $HOME/.graph-ops
	alpha, _ := repo.CreateProject("Alpha", "")
	local := filepath.Join(home, "alpha")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	setLocalPath(t, s, alpha.ID, local)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/node-types/implementation", map[string]any{
		"scope": "project", "project_id": alpha.ID, "text": "alpha rules",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	want := filepath.Join(local, ".graph-ops", "extensions", "node-types", "implementation.md")
	if raw, err := os.ReadFile(want); err != nil || string(raw) != "alpha rules" {
		t.Errorf("override should be written under the project's local path (%s): %q, %v", want, raw, err)
	}
	if _, err := os.Stat(filepath.Join(userRoot, "extensions", "node-types", "implementation.md")); err == nil {
		t.Error("project-scoped override was written into $HOME/.graph-ops")
	}
}

func TestSettingsProjectScope_LocalPathIsHomeIsRejected(t *testing.T) {
	s, repo := newBareTestServer(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	userRoot := filepath.Join(home, ".graph-ops")
	if err := os.MkdirAll(userRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	s.cfg.UserExtensionsDir = ""
	proj, _ := repo.CreateProject("Home", "")
	setLocalPath(t, s, proj.ID, home)

	for _, req := range []struct {
		method, path string
		body         map[string]any
	}{
		{http.MethodGet, "/api/settings/node-types/implementation?scope=project&project_id=" + proj.ID, nil},
		{http.MethodPut, "/api/settings/node-types/implementation", map[string]any{"scope": "project", "project_id": proj.ID, "text": "x"}},
	} {
		rec := doJSON(t, s, req.method, req.path, req.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s: expected 400, got %d: %s", req.method, req.path, rec.Code, rec.Body.String())
			continue
		}
		if code := decodeError(t, rec).Code; code != domain.ErrCodeProjectTeamRootIsUserRoot {
			t.Errorf("%s %s: code = %s, want %s", req.method, req.path, code, domain.ErrCodeProjectTeamRootIsUserRoot)
		}
	}
	if _, err := os.Stat(filepath.Join(userRoot, "extensions", "node-types", "implementation.md")); err == nil {
		t.Error("project-scoped PUT wrote into $HOME/.graph-ops")
	}

	// The list endpoints treat it as "no team tier" and keep working.
	for _, path := range []string{"/api/settings/node-types?project_id=" + proj.ID, "/api/settings/skills?project_id=" + proj.ID} {
		if rec := doJSON(t, s, http.MethodGet, path, nil); rec.Code != http.StatusOK {
			t.Errorf("GET %s: expected 200, got %d: %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestSettingsProjectScope_TeamOverrideEqualToUserRootIsRejected(t *testing.T) {
	s, repo := newBareTestServer(t)
	shared := t.TempDir()
	s.cfg.UserExtensionsDir = shared
	s.cfg.TeamExtensionsDir = shared + string(filepath.Separator)
	proj, _ := repo.CreateProject("Beta", "")

	rec := doJSON(t, s, http.MethodPut, "/api/settings/node-types/implementation", map[string]any{
		"scope": "project", "project_id": proj.ID, "text": "x",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := decodeError(t, rec).Code; code != domain.ErrCodeProjectTeamRootIsUserRoot {
		t.Errorf("code = %s, want %s", code, domain.ErrCodeProjectTeamRootIsUserRoot)
	}
}
