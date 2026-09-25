package httpserver

// Tests for DFLT-00145: the two places the Web UI server saves the home
// config itself -- PUT /api/settings/app and the cleanup after
// DELETE /api/projects/{id} -- keep the top-level keys this version does not
// know (another version's settings, such as v0.10.0's autopilotSettings).

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/graph-ops/core-go/internal/runtimeconfig"
)

const addedLaterSettingsJSON = `{"proj-1": {"mode": "auto", "maxParallel": 2}}`

func writeRawHomeConfig(t *testing.T, homeDir, raw string) {
	t.Helper()
	path := runtimeconfig.HomeConfigPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readRawHomeConfig(t *testing.T, homeDir string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(runtimeconfig.HomeConfigPath(homeDir))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, raw)
	}
	return doc
}

func assertAutopilotSettingsKept(t *testing.T, doc map[string]json.RawMessage) {
	t.Helper()
	got, ok := doc["addedLaterSettings"]
	if !ok {
		t.Fatal("addedLaterSettings was dropped from the home config")
	}
	var want, have bytes.Buffer
	if err := json.Compact(&want, []byte(addedLaterSettingsJSON)); err != nil {
		t.Fatal(err)
	}
	if err := json.Compact(&have, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want.Bytes(), have.Bytes()) {
		t.Errorf("addedLaterSettings = %s, want %s", have.Bytes(), want.Bytes())
	}
}

func TestAppSettings_PutKeepsUnknownConfigKeys(t *testing.T) {
	s, _, homeDir := newAppSettingsTestServer(t)
	writeRawHomeConfig(t, homeDir, `{"addedLaterSettings": `+addedLaterSettingsJSON+`}`)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{"myName": "alice", "paginationPageSize": 10})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	doc := readRawHomeConfig(t, homeDir)
	if got := string(doc["myName"]); got != `"alice"` {
		t.Errorf("myName = %s, want \"alice\"", got)
	}
	assertAutopilotSettingsKept(t, doc)
}

func TestHandleDeleteProject_CleanupKeepsUnknownConfigKeys(t *testing.T) {
	s, repo := newBareTestServer(t)
	alpha, err := repo.CreateProject("Alpha", "")
	if err != nil {
		t.Fatal(err)
	}
	writeRawHomeConfig(t, s.cfg.HomeDir, `{
		"projectPaths": {"`+alpha.ID+`": "/work/alpha"},
		"currentProjectId": "`+alpha.ID+`",
		"addedLaterSettings": `+addedLaterSettingsJSON+`
	}`)

	if rec := doJSON(t, s, http.MethodDelete, "/api/projects/"+alpha.ID, nil); rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	doc := readRawHomeConfig(t, s.cfg.HomeDir)
	if v, ok := doc["projectPaths"]; ok {
		t.Errorf("projectPaths = %s, want the key gone (its only entry was deleted)", v)
	}
	if got := string(doc["currentProjectId"]); got != `""` {
		t.Errorf("currentProjectId = %s, want \"\"", got)
	}
	assertAutopilotSettingsKept(t, doc)
}
