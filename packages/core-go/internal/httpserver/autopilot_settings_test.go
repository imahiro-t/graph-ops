package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
)

// DFLT-00142 phase 2: GET/PUT /api/projects/{id}/autopilot-settings.

func autopilotSettingsPath(projectID string) string {
	return "/api/projects/" + projectID + "/autopilot-settings"
}

// withTeamAutopilot points s at a fresh team extensions directory holding
// content as autopilot.yaml, and returns the directory.
func withTeamAutopilot(t *testing.T, s *Server, content string) string {
	t.Helper()
	dir := t.TempDir()
	writeTeamAutopilot(t, dir, content)
	s.cfg.TeamExtensionsDir = dir
	return dir
}

func writeTeamAutopilot(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, autopilot.TeamFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setLocalAutopilot stores local values for projectID directly in the home
// config, bypassing the API (the "given the file already holds" step).
func setLocalAutopilot(t *testing.T, s *Server, projectID string, local autopilot.LocalSettings) {
	t.Helper()
	if _, _, err := runtimeconfig.UpdateHome(s.cfg.HomeDir, func(cfg *runtimeconfig.FileConfig) error {
		if cfg.AutopilotSettings == nil {
			cfg.AutopilotSettings = map[string]autopilot.LocalSettings{}
		}
		cfg.AutopilotSettings[projectID] = local
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func homeConfigBytes(t *testing.T, s *Server) string {
	t.Helper()
	return readRawConfigBytes(t, runtimeconfig.HomeConfigPath(s.cfg.HomeDir))
}

func decodeAutopilotSettings(t *testing.T, rec *httptest.ResponseRecorder) autopilot.Effective {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var eff autopilot.Effective
	if err := json.Unmarshal(rec.Body.Bytes(), &eff); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body.String())
	}
	return eff
}

func apItem(t *testing.T, eff autopilot.Effective, key string) autopilot.Item {
	t.Helper()
	for _, it := range eff.Items {
		if it.Key == key {
			return it
		}
	}
	t.Fatalf("no item %q", key)
	return autopilot.Item{}
}

// doAutopilotRaw sends body verbatim (so a test can send exact JSON literals such
// as 2.5 or "10"), with or without the CSRF header.
func doAutopilotRaw(s *Server, method, path, body string, csrf bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = testHost
	req.Header.Set("Content-Type", "application/json")
	if csrf {
		req.Header.Set(csrfHeaderName, "1")
	}
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	return rec
}

func TestAutopilotSettings_GetDefaults(t *testing.T) {
	s, _, pid := newTestServer(t)
	eff := decodeAutopilotSettings(t, doJSON(t, s, http.MethodGet, autopilotSettingsPath(pid), nil))
	if eff.ProjectID != pid || eff.Settings != autopilot.Defaults() || len(eff.Warnings) != 0 || eff.TeamFile != "" {
		t.Fatalf("eff = %+v", eff)
	}
	for _, it := range eff.Items {
		if it.Source != autopilot.SourceDefault || it.Locked || it.Local != nil || it.Team != nil {
			t.Errorf("item %+v", it)
		}
	}
	// The body carries "local": null / "team": null explicitly.
	body := doJSON(t, s, http.MethodGet, autopilotSettingsPath(pid), nil).Body.String()
	if !strings.Contains(body, `"local":null`) || !strings.Contains(body, `"team":null`) || !strings.Contains(body, `"warnings":[]`) {
		t.Errorf("body = %s", body)
	}
}

func TestAutopilotSettings_UnknownProject(t *testing.T) {
	s, _, _ := newTestServer(t)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		var body map[string]any
		if method == http.MethodPut {
			body = map[string]any{"maxTickets": 3}
		}
		rec := doJSON(t, s, method, autopilotSettingsPath("proj-nope"), body)
		if rec.Code != http.StatusNotFound || decodeError(t, rec).Code != domain.ErrCodeProjectNotFound {
			t.Errorf("%s: %d %s", method, rec.Code, rec.Body.String())
		}
	}
}

func TestAutopilotSettings_PutPartialUpdateKeepsEverythingElse(t *testing.T) {
	s, _, pid := newTestServer(t)
	if _, _, err := runtimeconfig.UpdateHome(s.cfg.HomeDir, func(cfg *runtimeconfig.FileConfig) error {
		cfg.DBBackend = "sqlite"
		cfg.PaginationPageSize = 25
		cfg.AutopilotSettings = map[string]autopilot.LocalSettings{
			pid:          {autopilot.KeyMaxDepth: 2},
			"proj-other": {autopilot.KeyOnFailure: "continue"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := readConfigJSON(t, runtimeconfig.HomeConfigPath(s.cfg.HomeDir))

	eff := decodeAutopilotSettings(t, doJSON(t, s, http.MethodPut, autopilotSettingsPath(pid), map[string]any{"maxTickets": 30}))
	if eff.Settings.MaxTickets != 30 || eff.Settings.MaxDepth != 2 || apItem(t, eff, "maxTickets").Source != autopilot.SourceLocal {
		t.Fatalf("eff = %+v", eff.Settings)
	}

	after := readConfigJSON(t, runtimeconfig.HomeConfigPath(s.cfg.HomeDir))
	if !reflect.DeepEqual(after.AutopilotSettings[pid], autopilot.LocalSettings{"maxTickets": float64(30), "maxDepth": float64(2)}) {
		t.Errorf("saved = %+v", after.AutopilotSettings[pid])
	}
	if !reflect.DeepEqual(after.AutopilotSettings["proj-other"], before.AutopilotSettings["proj-other"]) {
		t.Errorf("another project's settings changed: %+v", after.AutopilotSettings)
	}
	after.AutopilotSettings, before.AutopilotSettings = nil, nil
	if !reflect.DeepEqual(after, before) {
		t.Errorf("other fields changed:\nbefore %+v\nafter  %+v", before, after)
	}

	// A fresh GET shows the saved value.
	got := decodeAutopilotSettings(t, doJSON(t, s, http.MethodGet, autopilotSettingsPath(pid), nil))
	if got.Settings.MaxTickets != 30 {
		t.Errorf("GET after PUT = %+v", got.Settings)
	}
}

func TestAutopilotSettings_PutNullRemovesLocalValue(t *testing.T) {
	s, _, pid := newTestServer(t)
	setLocalAutopilot(t, s, pid, autopilot.LocalSettings{autopilot.KeyMaxDepth: 2})
	eff := decodeAutopilotSettings(t, doAutopilotRaw(s, http.MethodPut, autopilotSettingsPath(pid), `{"maxDepth": null}`, true))
	if eff.Settings.MaxDepth != 3 || apItem(t, eff, "maxDepth").Source != autopilot.SourceDefault {
		t.Fatalf("eff = %+v", eff.Settings)
	}
	cfg := readConfigJSON(t, runtimeconfig.HomeConfigPath(s.cfg.HomeDir))
	if _, ok := cfg.AutopilotSettings[pid]; ok {
		t.Errorf("an emptied project entry must be dropped: %+v", cfg.AutopilotSettings)
	}
	if strings.Contains(homeConfigBytes(t, s), "autopilotSettings") {
		t.Errorf("an emptied map must be omitted: %s", homeConfigBytes(t, s))
	}
}

func TestAutopilotSettings_PutLockedKeyIsRefusedWhole(t *testing.T) {
	for _, value := range []string{"10", "12"} {
		t.Run(value, func(t *testing.T) {
			s, _, pid := newTestServer(t)
			withTeamAutopilot(t, s, "version: 1\nprojects:\n  "+pid+":\n    maxTickets: 10\n")
			before := homeConfigBytes(t, s)
			rec := doAutopilotRaw(s, http.MethodPut, autopilotSettingsPath(pid), `{"maxTickets": `+value+`, "maxDepth": 5}`, true)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
			}
			e := decodeError(t, rec)
			if e.Code != domain.ErrCodeAutopilotSettingLocked || !reflect.DeepEqual(e.Details["keys"], []any{"maxTickets"}) {
				t.Fatalf("error = %+v", e)
			}
			if homeConfigBytes(t, s) != before {
				t.Errorf("config.json changed")
			}
		})
	}
}

func TestAutopilotSettings_PreLockLocalValueSurvivesAndReturns(t *testing.T) {
	s, _, pid := newTestServer(t)
	setLocalAutopilot(t, s, pid, autopilot.LocalSettings{autopilot.KeyMaxTickets: 7})
	teamDir := withTeamAutopilot(t, s, "projects:\n  "+pid+":\n    maxTickets: 10\n")

	eff := decodeAutopilotSettings(t, doJSON(t, s, http.MethodGet, autopilotSettingsPath(pid), nil))
	it := apItem(t, eff, "maxTickets")
	if it.Value != float64(10) || !it.Locked || it.Local != float64(7) || it.Team != float64(10) || it.Source != autopilot.SourceTeamProject {
		t.Fatalf("item = %+v", it)
	}

	decodeAutopilotSettings(t, doJSON(t, s, http.MethodPut, autopilotSettingsPath(pid), map[string]any{"maxDepth": 4}))
	cfg := readConfigJSON(t, runtimeconfig.HomeConfigPath(s.cfg.HomeDir))
	if cfg.AutopilotSettings[pid]["maxTickets"] != float64(7) || cfg.AutopilotSettings[pid]["maxDepth"] != float64(4) {
		t.Fatalf("saved = %+v", cfg.AutopilotSettings[pid])
	}

	writeTeamAutopilot(t, teamDir, "projects: {}\n")
	eff = decodeAutopilotSettings(t, doJSON(t, s, http.MethodGet, autopilotSettingsPath(pid), nil))
	if eff.Settings.MaxTickets != 7 || apItem(t, eff, "maxTickets").Locked {
		t.Fatalf("after unlocking: %+v", eff.Settings)
	}
}

func TestAutopilotSettings_PutInvalidValues(t *testing.T) {
	cases := []struct{ key, value string }{
		{"mainReflection", `"squash"`},
		{"permissionMode", `"default"`},
		{"permissionMode", `"plan"`},
		{"maxTickets", `0`},
		{"maxTickets", `101`},
		{"maxTickets", `"10"`},
		{"maxTickets", `2.5`},
		{"maxDepth", `-1`},
		{"maxDepth", `11`},
		{"onFailure", `"retry"`},
		{"stallTimeoutMinutes", `14`},
		{"stallTimeoutMinutes", `1441`},
		{"autoApproveGates", `"yes"`},
		{"autoApproveGates", `1`},
		{"autoCreateTickets", `"false"`},
		{"notASetting", `1`},
	}
	s, _, pid := newTestServer(t)
	setLocalAutopilot(t, s, pid, autopilot.LocalSettings{autopilot.KeyMaxDepth: 2})
	before := homeConfigBytes(t, s)
	for _, c := range cases {
		// A valid companion key shows that nothing is saved partially.
		companion := `"onFailure": "continue"`
		if c.key == "onFailure" {
			companion = `"maxTickets": 30`
		}
		rec := doAutopilotRaw(s, http.MethodPut, autopilotSettingsPath(pid), `{"`+c.key+`": `+c.value+`, `+companion+`}`, true)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s=%s: status %d", c.key, c.value, rec.Code)
			continue
		}
		e := decodeError(t, rec)
		if e.Code != domain.ErrCodeValidation || !reflect.DeepEqual(e.Details["keys"], []any{c.key}) || !strings.Contains(e.Message, c.key) {
			t.Errorf("%s=%s: error %+v", c.key, c.value, e)
		}
	}
	for _, body := range []string{`[]`, `not json`, `null`} {
		if rec := doAutopilotRaw(s, http.MethodPut, autopilotSettingsPath(pid), body, true); rec.Code != http.StatusBadRequest || decodeError(t, rec).Code != domain.ErrCodeValidation {
			t.Errorf("body %s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if homeConfigBytes(t, s) != before {
		t.Errorf("config.json changed by a refused PUT")
	}
}

func TestAutopilotSettings_PutWithoutCSRFHeaderIsRefused(t *testing.T) {
	s, _, pid := newTestServer(t)
	before := homeConfigBytes(t, s)
	rec := doAutopilotRaw(s, http.MethodPut, autopilotSettingsPath(pid), `{"maxTickets": 30}`, false)
	if rec.Code != http.StatusForbidden || decodeError(t, rec).Code != domain.ErrCodeCSRFHeaderRequired {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if homeConfigBytes(t, s) != before {
		t.Errorf("config.json changed")
	}
}

func TestAutopilotSettings_TeamWarningsAndBypass(t *testing.T) {
	s, _, pid := newTestServer(t)
	withTeamAutopilot(t, s, "defaults:\n  permissionMode: bypassPermissions\n  maxTickets: 1000\n")
	eff := decodeAutopilotSettings(t, doJSON(t, s, http.MethodGet, autopilotSettingsPath(pid), nil))
	codes := map[string]bool{}
	for _, w := range eff.Warnings {
		codes[w.Code+"/"+w.Key] = true
	}
	if !codes[autopilot.WarnTeamBypassIgnored+"/permissionMode"] || !codes[autopilot.WarnInvalidValue+"/maxTickets"] {
		t.Fatalf("warnings = %+v", eff.Warnings)
	}
	if apItem(t, eff, "permissionMode").Locked {
		t.Error("an ignored bypassPermissions must not lock the key")
	}
	// ...so it can be saved locally.
	eff = decodeAutopilotSettings(t, doJSON(t, s, http.MethodPut, autopilotSettingsPath(pid), map[string]any{"permissionMode": "bypassPermissions"}))
	if eff.Settings.PermissionMode != "bypassPermissions" {
		t.Fatalf("settings = %+v", eff.Settings)
	}
}
