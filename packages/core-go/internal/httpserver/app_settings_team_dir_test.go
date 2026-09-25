package httpserver

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/graph-ops/core-go/internal/runtimeconfig"
)

// --- DFLT-00153: the app settings edit the team settings directory
// (teamExtensionsDir), no longer the personal one (userExtensionsDir). ---

// teamDirBody is a complete PUT body (owned fields are a full replacement)
// with the given teamExtensionsDir, plus any extra fields.
func teamDirBody(teamDir any, extra map[string]any) map[string]any {
	body := map[string]any{
		"dbBackend":          "sqlite",
		"dbPath":             "/tmp/graph.db",
		"artifactsDir":       "/tmp/artifacts",
		"teamExtensionsDir":  teamDir,
		"paginationPageSize": 10,
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

// TestAppSettings_PutSavesTeamExtensionsDir: a saved teamExtensionsDir lands
// in config.json and GET's file block shows it.
func TestAppSettings_PutSavesTeamExtensionsDir(t *testing.T) {
	s, _, homeDir := newAppSettingsTestServer(t)
	team := filepath.Join(homeDir, "shared-team")

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", teamDirBody("  "+team+"  ", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := readConfigJSON(t, runtimeconfig.HomeConfigPath(homeDir)).TeamExtensionsDir; got != team {
		t.Errorf("config.json teamExtensionsDir = %q, want %q (trimmed)", got, team)
	}

	rec = doJSON(t, s, http.MethodGet, "/api/settings/app", nil)
	var got appSettingsResponse
	mustDecode(t, rec, &got)
	if got.File.TeamExtensionsDir != team {
		t.Errorf("GET file.teamExtensionsDir = %q, want %q", got.File.TeamExtensionsDir, team)
	}
}

// TestAppSettings_PutEmptyTeamExtensionsDirRemovesOnlyThatKey: saving it
// empty (or whitespace only) removes the key, and every other key --
// including the personal userExtensionsDir and projectPaths, which this
// endpoint does not own -- survives.
func TestAppSettings_PutEmptyTeamExtensionsDirRemovesOnlyThatKey(t *testing.T) {
	for _, empty := range []string{"", "   "} {
		t.Run("value "+`"`+empty+`"`, func(t *testing.T) {
			s, _, homeDir := newAppSettingsTestServer(t)
			homePath := runtimeconfig.HomeConfigPath(homeDir)
			userExt := filepath.Join(homeDir, "my-ext")
			writeConfigJSON(t, homePath, runtimeconfig.FileConfig{
				DBPath:            "/tmp/graph.db",
				UserExtensionsDir: userExt,
				TeamExtensionsDir: filepath.Join(homeDir, "old-team"),
				ProjectPaths:      map[string]string{"proj-1": "/src/proj"},
			})

			rec := doJSON(t, s, http.MethodPut, "/api/settings/app", teamDirBody(empty, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			raw := readRawConfigBytes(t, homePath)
			if containsKey(raw, "teamExtensionsDir") {
				t.Errorf("config.json still has teamExtensionsDir: %s", raw)
			}
			cfg := readConfigJSON(t, homePath)
			if cfg.DBPath != "/tmp/graph.db" || cfg.UserExtensionsDir != userExt || cfg.ProjectPaths["proj-1"] != "/src/proj" {
				t.Errorf("other keys were not kept: %+v", cfg)
			}
		})
	}
}

// TestAppSettings_PutWithoutTeamExtensionsDirKeepsIt: a body that leaves the
// field out altogether (a client from before DFLT-00153, which never sends
// it) keeps the saved value -- only an explicit "" removes it -- while the
// other owned fields in the body are still saved.
func TestAppSettings_PutWithoutTeamExtensionsDirKeepsIt(t *testing.T) {
	s, _, homeDir := newAppSettingsTestServer(t)
	homePath := runtimeconfig.HomeConfigPath(homeDir)
	team := filepath.Join(homeDir, "old-team")
	writeConfigJSON(t, homePath, runtimeconfig.FileConfig{
		DBPath:            "/tmp/before.db",
		TeamExtensionsDir: team,
	})

	body := teamDirBody(nil, nil)
	delete(body, "teamExtensionsDir")
	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	cfg := readConfigJSON(t, homePath)
	if cfg.TeamExtensionsDir != team {
		t.Errorf("config.json teamExtensionsDir = %q, want the stored %q kept", cfg.TeamExtensionsDir, team)
	}
	if cfg.DBPath != "/tmp/graph.db" {
		t.Errorf("config.json dbPath = %q, want the submitted /tmp/graph.db", cfg.DBPath)
	}
}

// TestAppSettings_PutNeverWritesUserExtensionsDir: userExtensionsDir is not
// an owned field any more -- a body that still carries one (an older client)
// neither changes nor clears the stored value.
func TestAppSettings_PutNeverWritesUserExtensionsDir(t *testing.T) {
	s, _, homeDir := newAppSettingsTestServer(t)
	homePath := runtimeconfig.HomeConfigPath(homeDir)
	userExt := filepath.Join(homeDir, "my-ext")
	writeConfigJSON(t, homePath, runtimeconfig.FileConfig{UserExtensionsDir: userExt})

	for _, sent := range []string{filepath.Join(homeDir, "other-ext"), ""} {
		rec := doJSON(t, s, http.MethodPut, "/api/settings/app", teamDirBody("", map[string]any{"userExtensionsDir": sent}))
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if got := readConfigJSON(t, homePath).UserExtensionsDir; got != userExt {
			t.Errorf("after sending userExtensionsDir %q: config.json has %q, want the stored %q", sent, got, userExt)
		}
	}
}

// TestAppSettings_PutRejectsRelativeTeamExtensionsDir: a relative path would
// depend on the directory the process was started in, so it is a 400 and
// nothing is written.
func TestAppSettings_PutRejectsRelativeTeamExtensionsDir(t *testing.T) {
	for _, rel := range []string{"shared/team", "./team", "~/team"} {
		t.Run(rel, func(t *testing.T) {
			s, _, homeDir := newAppSettingsTestServer(t)
			homePath := runtimeconfig.HomeConfigPath(homeDir)
			writeConfigJSON(t, homePath, runtimeconfig.FileConfig{DBPath: "/tmp/before.db"})
			before := readRawConfigBytes(t, homePath)

			rec := doJSON(t, s, http.MethodPut, "/api/settings/app", teamDirBody(rel, nil))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
			if got := decodeError(t, rec).Code; got != "VALIDATION_ERROR" {
				t.Errorf("error code = %q, want VALIDATION_ERROR", got)
			}
			if after := readRawConfigBytes(t, homePath); after != before {
				t.Errorf("config.json changed on a rejected PUT:\n%s\n->\n%s", before, after)
			}
		})
	}
}

// TestAppSettings_EffectiveTeamExtensionsDir: GET's effective block reports
// the team tier actually in effect -- "" when unset or when it is the user
// root itself (no team tier), the directory otherwise.
func TestAppSettings_EffectiveTeamExtensionsDir(t *testing.T) {
	cases := []struct {
		name string
		team func(s *Server) string
		want func(s *Server) string
	}{
		{"unset", func(*Server) string { return "" }, func(*Server) string { return "" }},
		{"same as the user root", func(s *Server) string { return s.cfg.UserExtensionsDir }, func(*Server) string { return "" }},
		{"separate directory", func(*Server) string { return "/srv/team-graph-ops" }, func(*Server) string { return "/srv/team-graph-ops" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newAppSettingsTestServer(t)
			s.cfg.TeamExtensionsDir = tc.team(s)

			rec := doJSON(t, s, http.MethodGet, "/api/settings/app", nil)
			var got appSettingsResponse
			mustDecode(t, rec, &got)
			if want := tc.want(s); got.Effective.TeamExtensionsDir != want {
				t.Errorf("effective.teamExtensionsDir = %q, want %q", got.Effective.TeamExtensionsDir, want)
			}
			if got.Effective.UserExtensionsDir != s.cfg.UserExtensionsDir {
				t.Errorf("effective.userExtensionsDir = %q, want %q", got.Effective.UserExtensionsDir, s.cfg.UserExtensionsDir)
			}
		})
	}
}

// containsKey reports whether the JSON object raw has a top-level key.
func containsKey(raw, key string) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return false
	}
	_, ok := m[key]
	return ok
}
