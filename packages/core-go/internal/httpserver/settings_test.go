package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// newSettingsTestServer is newTestServer with cfg.UserExtensionsDir pinned to
// a fresh temp dir, so these tests never touch the real $HOME/.graph-ops. The
// settings API has one tier -- the user tier (DFLT-00124) -- so that is the
// only root they need.
func newSettingsTestServer(t *testing.T) (*Server, store.GraphRepository, string) {
	t.Helper()
	repo, err := store.NewSQLiteRepository(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	if err := repo.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	proj, err := repo.CreateProject("Test Project", "TEST")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	eng := engine.New(repo)
	cfg := Config{ArtifactsDir: t.TempDir(), UserExtensionsDir: t.TempDir(), HomeDir: t.TempDir()}
	return New(repo, eng, cfg), repo, proj.ID
}

// Scenario: ノード種別の指示文を追加・保存できる -- and clearing it back to
// "" falls back to the plugin default (no override).
func TestSettingsNodeType_SaveAndClear(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/node-types/gherkin_spec", map[string]any{
		"text": "Always write scenario titles in JPY context.",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/settings/node-types/gherkin_spec", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		TierText   string `json:"tier_text"`
		MergedText string `json:"merged_text"`
	}
	mustDecode(t, rec, &body)
	if body.TierText != "Always write scenario titles in JPY context." {
		t.Errorf("tier_text = %q", body.TierText)
	}
	if !contains(body.MergedText, "Always write scenario titles in JPY context.") {
		t.Errorf("merged_text missing override: %q", body.MergedText)
	}

	// Clearing (empty text) falls back to the plugin default.
	rec = doJSON(t, s, http.MethodPut, "/api/settings/node-types/gherkin_spec", map[string]any{"text": ""})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT clear expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/settings/node-types/gherkin_spec", nil)
	mustDecode(t, rec, &body)
	if body.TierText != "" {
		t.Errorf("tier_text after clear = %q, want empty", body.TierText)
	}
	if contains(body.MergedText, "Always write scenario titles in JPY context.") {
		t.Errorf("merged_text still contains cleared override: %q", body.MergedText)
	}
}

// TestSettingsNodeType_WritesUnderTheUserRoot is completion criterion 7's
// "the settings screen edits one tier" half: the override file lands under
// cfg.UserExtensionsDir, never under a project's local path (which is where
// the deleted per-project scope used to put it).
func TestSettingsNodeType_WritesUnderTheUserRoot(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/node-types/implementation", map[string]any{
		"text": "user-tier instructions",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	expected := filepath.Join(s.cfg.UserExtensionsDir, "extensions", "node-types", "implementation.md")
	raw, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("expected override file at %s: %v", expected, err)
	}
	if string(raw) != "user-tier instructions" {
		t.Errorf("file content = %q", string(raw))
	}
}

// TestSettings_TeamTierIsNeverWritten pins the other side of the same
// decision: teamExtensionsDir is a shared directory this screen does not
// edit, so configuring one changes nothing about where a save lands.
func TestSettings_TeamTierIsNeverWritten(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)
	teamDir := t.TempDir()
	s.cfg.TeamExtensionsDir = teamDir

	rec := doJSON(t, s, http.MethodPut, "/api/settings/node-types/implementation", map[string]any{
		"text": "user-tier instructions",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(teamDir, "extensions", "node-types", "implementation.md")); !os.IsNotExist(err) {
		t.Errorf("the team directory was written, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.cfg.UserExtensionsDir, "extensions", "node-types", "implementation.md")); err != nil {
		t.Errorf("the user root should hold the override: %v", err)
	}
}

// TestSettingsAPI_TakesNoScopeOrProjectID is completion criterion 7's
// "does not accept it" half, seen from the wire: no GET response carries
// scope / project_id / team_root_resolved, and sending either on a PUT
// changes nothing about where the save goes.
func TestSettingsAPI_TakesNoScopeOrProjectID(t *testing.T) {
	s, _, projA := newSettingsTestServer(t)

	gets := []string{
		"/api/settings/catalog",
		"/api/settings/node-types",
		"/api/settings/node-types/plan",
		"/api/settings/skills",
		"/api/settings/skills/process-ticket",
		"/api/settings/report-template",
		"/api/settings/plan-template",
		"/api/settings/review-template",
	}
	for _, path := range gets {
		t.Run(path, func(t *testing.T) {
			rec := doJSON(t, s, http.MethodGet, path, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s expected 200, got %d: %s", path, rec.Code, rec.Body.String())
			}
			var raw map[string]any
			mustDecode(t, rec, &raw)
			for _, field := range []string{"scope", "project_id", "team_root_resolved"} {
				if _, present := raw[field]; present {
					t.Errorf("GET %s response still carries %q", path, field)
				}
			}
		})
	}

	// A PUT carrying the old fields: they are unknown to the body struct, so
	// encoding/json drops them, and the save lands in the user tier exactly
	// as it would without them. The criterion is that the server has no code
	// reading them -- not that an old client gets a 400.
	puts := []struct {
		path string
		body map[string]any
	}{
		{"/api/settings/node-types/plan", map[string]any{"scope": "project", "project_id": projA, "text": "x"}},
		{"/api/settings/skills/process-ticket", map[string]any{"scope": "project", "project_id": projA, "text": "x"}},
		{"/api/settings/plan-template", map[string]any{"scope": "project", "project_id": projA, "text": "x"}},
		{"/api/settings/review-template", map[string]any{"scope": "project", "project_id": projA, "text": "x"}},
	}
	for _, tc := range puts {
		t.Run("PUT "+tc.path, func(t *testing.T) {
			rec := doJSON(t, s, http.MethodPut, tc.path, tc.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("PUT %s expected 200, got %d: %s", tc.path, rec.Code, rec.Body.String())
			}
			var raw map[string]any
			mustDecode(t, rec, &raw)
			for _, field := range []string{"scope", "project_id", "team_root_resolved"} {
				if _, present := raw[field]; present {
					t.Errorf("PUT %s response still carries %q", tc.path, field)
				}
			}
		})
	}
}

// Scenario: workflow.nodes を保存しようとすると、スキーマ骨格はプラグイン
// 既定のみが定めるためバリデーションエラーになり保存されない.
func TestSettingsCatalog_WorkflowNodesRejected(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/catalog", map[string]any{
		"document": map[string]any{
			"version": 1,
			"workflow": map[string]any{
				"nodes": []map[string]any{
					{"id": "a", "type": "implementation"},
				},
			},
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec).Code; got != "WORKFLOW_NODES_LOCKED" {
		t.Errorf("error code = %q", got)
	}
}

// Scenario: workflow.seed を保存しようとしても同様に拒否される.
func TestSettingsCatalog_WorkflowSeedRejected(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/catalog", map[string]any{
		"document": map[string]any{
			"version":  1,
			"workflow": map[string]any{"seed": []string{"plan"}},
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec).Code; got != "WORKFLOW_NODES_LOCKED" {
		t.Errorf("error code = %q", got)
	}
}

// Scenario: the workflow-wide review iteration limit only accepts 3, 4 or 5
// (DFLT-00140); anything else is a 400 INVALID_MAX_ITERATIONS and nothing is
// written.
func TestSettingsCatalog_InvalidMaxIterationsRejected(t *testing.T) {
	for _, v := range []int{0, 2, 6} {
		t.Run(strconv.Itoa(v), func(t *testing.T) {
			s, _, _ := newSettingsTestServer(t)
			path := config.UserDocumentPath(s.cfg.UserExtensionsDir)
			original := "version: 1\nmax_iterations: 4\n"
			if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
				t.Fatal(err)
			}

			rec := doJSON(t, s, http.MethodPut, "/api/settings/catalog", map[string]any{
				"document": map[string]any{"version": 1, "max_iterations": v},
			})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
			apiErr := decodeError(t, rec)
			if apiErr.Code != "INVALID_MAX_ITERATIONS" {
				t.Errorf("error code = %q", apiErr.Code)
			}
			if !strings.Contains(apiErr.Message, "3, 4 or 5") {
				t.Errorf("error message %q does not say 3, 4 or 5", apiErr.Message)
			}
			if raw, _ := os.ReadFile(path); string(raw) != original {
				t.Errorf("the user document changed on a rejected PUT:\n%s", raw)
			}
		})
	}
}

// settingsMaxIterationsResponse is the part of the settings GET/PUT
// responses the max_iterations tests look at.
type settingsMaxIterationsResponse struct {
	MergedCatalog struct {
		MaxIterations int                       `json:"max_iterations"`
		ReviewGates   map[string]map[string]any `json:"review_gates"`
	} `json:"merged_catalog"`
	InheritedCatalog struct {
		MaxIterations int `json:"max_iterations"`
	} `json:"inherited_catalog"`
	TierDocument map[string]any   `json:"tier_document"`
	Warnings     []map[string]any `json:"warnings"`
}

// readUserDocumentGeneric parses the user tier's config.yaml as a generic map
// so a test can tell "key absent" from "key present".
func readUserDocumentGeneric(t *testing.T, s *Server) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(config.UserDocumentPath(s.cfg.UserExtensionsDir))
	if err != nil {
		t.Fatalf("read user document: %v", err)
	}
	var out map[string]any
	if err := yaml.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse user document: %v", err)
	}
	return out
}

// Scenario: 3/4/5 save successfully, come back in merged_catalog, and land at
// the top level of the user tier's config.yaml.
func TestSettingsCatalog_ValidSaveRoundTrips(t *testing.T) {
	for _, v := range []int{3, 4, 5} {
		t.Run(strconv.Itoa(v), func(t *testing.T) {
			s, _, _ := newSettingsTestServer(t)

			rec := doJSON(t, s, http.MethodPut, "/api/settings/catalog", map[string]any{
				"document": map[string]any{"version": 1, "max_iterations": v},
			})
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			var put settingsMaxIterationsResponse
			mustDecode(t, rec, &put)
			if put.MergedCatalog.MaxIterations != v {
				t.Errorf("PUT merged_catalog.max_iterations = %d, want %d", put.MergedCatalog.MaxIterations, v)
			}
			if got := readUserDocumentGeneric(t, s)["max_iterations"]; got != v {
				t.Errorf("config.yaml max_iterations = %v, want %d", got, v)
			}

			rec = doJSON(t, s, http.MethodGet, "/api/settings/catalog", nil)
			var get settingsMaxIterationsResponse
			mustDecode(t, rec, &get)
			if get.MergedCatalog.MaxIterations != v || get.InheritedCatalog.MaxIterations != 3 {
				t.Errorf("GET merged/inherited max_iterations = %d/%d, want %d/3", get.MergedCatalog.MaxIterations, get.InheritedCatalog.MaxIterations, v)
			}
		})
	}
}

// Scenario: GET with nothing set reports 3 merged and inherited, and no
// warnings (as an empty array, not null).
func TestSettingsCatalog_GetMaxIterationsDefaults(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)
	rec := doJSON(t, s, http.MethodGet, "/api/settings/catalog", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body settingsMaxIterationsResponse
	mustDecode(t, rec, &body)
	if body.MergedCatalog.MaxIterations != 3 || body.InheritedCatalog.MaxIterations != 3 {
		t.Errorf("merged/inherited max_iterations = %d/%d, want 3/3", body.MergedCatalog.MaxIterations, body.InheritedCatalog.MaxIterations)
	}
	if body.Warnings == nil || len(body.Warnings) != 0 {
		t.Errorf("warnings = %#v, want an empty array", body.Warnings)
	}
}

// Scenario: omitting max_iterations, or sending null, clears the user tier's
// value so the inherited 3 applies again.
func TestSettingsCatalog_PutWithoutMaxIterationsClearsIt(t *testing.T) {
	for _, form := range []string{"omitted", "null"} {
		t.Run(form, func(t *testing.T) {
			s, _, _ := newSettingsTestServer(t)
			if err := os.WriteFile(config.UserDocumentPath(s.cfg.UserExtensionsDir), []byte("version: 1\nmax_iterations: 4\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			doc := map[string]any{"version": 1}
			if form == "null" {
				doc["max_iterations"] = nil
			}
			rec := doJSON(t, s, http.MethodPut, "/api/settings/catalog", map[string]any{"document": doc})
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			var put settingsMaxIterationsResponse
			mustDecode(t, rec, &put)
			if put.MergedCatalog.MaxIterations != 3 {
				t.Errorf("merged_catalog.max_iterations = %d, want 3", put.MergedCatalog.MaxIterations)
			}
			if _, ok := readUserDocumentGeneric(t, s)["max_iterations"]; ok {
				t.Errorf("config.yaml still has a top-level max_iterations")
			}
		})
	}
}

// Scenario: a per-gate max_iterations left in the user tier is reported in
// GET's warnings and never shown in a gate definition; saving drops it.
func TestSettingsCatalog_LegacyPerGateMaxIterationsWarnsAndIsDroppedOnSave(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)
	if err := os.WriteFile(config.UserDocumentPath(s.cfg.UserExtensionsDir), []byte(`version: 1
review_gates:
  code_review:
    criteria: keep me
    max_iterations: 4
`), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, s, http.MethodGet, "/api/settings/catalog", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var get settingsMaxIterationsResponse
	mustDecode(t, rec, &get)
	// Structured, so the UI can translate it: a code plus the gate id, and no
	// English sentence (DFLT-00140 accessibility review).
	if len(get.Warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", get.Warnings)
	}
	if w := get.Warnings[0]; w["code"] != config.WarnLegacyGateMaxIterations || w["gate_id"] != "code_review" || len(w) != 2 {
		t.Errorf("warning = %v, want {code: %s, gate_id: code_review} only", w, config.WarnLegacyGateMaxIterations)
	}
	for id, gate := range get.MergedCatalog.ReviewGates {
		if _, ok := gate["max_iterations"]; ok {
			t.Errorf("merged gate %q exposes max_iterations", id)
		}
	}
	tierGates, _ := get.TierDocument["review_gates"].(map[string]any)
	if gate, _ := tierGates["code_review"].(map[string]any); gate == nil || gate["max_iterations"] != nil {
		t.Errorf("tier_document code_review = %v, want it without max_iterations", tierGates["code_review"])
	}

	// Save the tier document back as the UI would.
	rec = doJSON(t, s, http.MethodPut, "/api/settings/catalog", map[string]any{"document": get.TierDocument})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	saved := readUserDocumentGeneric(t, s)
	gate := saved["review_gates"].(map[string]any)["code_review"].(map[string]any)
	if _, ok := gate["max_iterations"]; ok {
		t.Errorf("the saved config.yaml still has code_review.max_iterations: %v", saved)
	}
	if gate["criteria"] != "keep me" {
		t.Errorf("the rest of the gate was lost: %v", gate)
	}
	rec = doJSON(t, s, http.MethodGet, "/api/settings/catalog", nil)
	var after settingsMaxIterationsResponse
	mustDecode(t, rec, &after)
	if len(after.Warnings) != 0 {
		t.Errorf("warnings after saving = %v, want none", after.Warnings)
	}
}

// A hand-edited out-of-range top-level max_iterations (e.g. 7) is returned
// as-is in tier_document and reported in GET's warnings as a structured
// MAX_ITERATIONS_OUT_OF_RANGE with the value, so the screen can show it
// instead of silently displaying "inherit". Saving a valid value clears it.
func TestSettingsCatalog_OutOfRangeMaxIterationsIsReportedInWarnings(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)
	if err := os.WriteFile(config.UserDocumentPath(s.cfg.UserExtensionsDir), []byte("version: 1\nmax_iterations: 7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, s, http.MethodGet, "/api/settings/catalog", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var get settingsMaxIterationsResponse
	mustDecode(t, rec, &get)
	if v, _ := get.TierDocument["max_iterations"].(float64); v != 7 {
		t.Errorf("tier_document.max_iterations = %v, want 7", get.TierDocument["max_iterations"])
	}
	if len(get.Warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", get.Warnings)
	}
	if w := get.Warnings[0]; w["code"] != config.WarnMaxIterationsOutOfRange || w["value"] != float64(7) || len(w) != 2 {
		t.Errorf("warning = %v, want {code: %s, value: 7} only", w, config.WarnMaxIterationsOutOfRange)
	}

	rec = doJSON(t, s, http.MethodPut, "/api/settings/catalog", map[string]any{"document": map[string]any{"version": 1, "max_iterations": 4}})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/settings/catalog", nil)
	var after settingsMaxIterationsResponse
	mustDecode(t, rec, &after)
	if len(after.Warnings) != 0 {
		t.Errorf("warnings after saving 4 = %v, want none", after.Warnings)
	}
}

// Scenario: a max_iterations sent inside a gate definition is ignored, not
// validated and not written.
func TestSettingsCatalog_PerGateMaxIterationsInPutIsIgnored(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)
	rec := doJSON(t, s, http.MethodPut, "/api/settings/catalog", map[string]any{
		"document": map[string]any{
			"version": 1,
			"review_gates": map[string]any{
				"code_review": map[string]any{"criteria": "c", "max_iterations": 9},
			},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	gate := readUserDocumentGeneric(t, s)["review_gates"].(map[string]any)["code_review"].(map[string]any)
	if _, ok := gate["max_iterations"]; ok {
		t.Errorf("code_review.max_iterations was written: %v", gate)
	}
}

// --- Language locale resolution (DFLT-00051, execution plan section 1.6/3.1) ---

type catalogNodeDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type catalogDTO struct {
	Nodes []catalogNodeDTO `json:"nodes"`
}

type settingsCatalogGetResponse struct {
	MergedCatalog    catalogDTO `json:"merged_catalog"`
	InheritedCatalog catalogDTO `json:"inherited_catalog"`
	ResolvedLanguage string     `json:"resolved_language"`
}

func nodeName(t *testing.T, cat catalogDTO, id string) string {
	t.Helper()
	for _, n := range cat.Nodes {
		if n.ID == id {
			return n.Name
		}
	}
	t.Fatalf("node %q not found in catalog %+v", id, cat)
	return ""
}

func putLanguage(t *testing.T, s *Server, language string) {
	t.Helper()
	rec := doJSON(t, s, http.MethodPut, "/api/settings/catalog", map[string]any{
		"document": map[string]any{"version": 1, "language": language},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT settings/catalog (language=%s) expected 200, got %d: %s", language, rec.Code, rec.Body.String())
	}
}

// Scenario: merged_catalog は user 層の language で解決される.
func TestSettingsCatalog_UserLanguageLocalizesMergedCatalog(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)

	putLanguage(t, s, "ja")

	rec := doJSON(t, s, http.MethodGet, "/api/settings/catalog", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body settingsCatalogGetResponse
	mustDecode(t, rec, &body)
	if body.ResolvedLanguage != "ja" {
		t.Errorf("resolved_language = %q, want ja", body.ResolvedLanguage)
	}
	if got := nodeName(t, body.MergedCatalog, "impl"); got != "実装" {
		t.Errorf("merged_catalog impl name = %q, want 実装", got)
	}
	// inherited_catalog is the plugin default alone, so it stays English --
	// that is what "what you'd fall back to if you cleared this" means.
	if got := nodeName(t, body.InheritedCatalog, "impl"); got != "Implementation" {
		t.Errorf("inherited_catalog impl name = %q, want the English default", got)
	}
}

// TestSettingsCatalog_MergedPreviewExcludesTheTeamTier is plan review
// condition F-2 made testable: an agent additionally sees the team tier, this
// preview deliberately does not, and the doc comment on
// handleGetSettingsCatalog says so.
func TestSettingsCatalog_MergedPreviewExcludesTheTeamTier(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)
	teamDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(teamDir, "workflow.yaml"), []byte(`version: 1
max_iterations: 5
review_gates:
  code_review:
    criteria: from-team
`), 0o644); err != nil {
		t.Fatalf("write workflow.yaml: %v", err)
	}
	if err := os.WriteFile(config.UserDocumentPath(s.cfg.UserExtensionsDir), []byte("version: 1\nmax_iterations: 4\n"), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}
	s.cfg.TeamExtensionsDir = teamDir

	rec := doJSON(t, s, http.MethodGet, "/api/settings/catalog", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		MergedCatalog struct {
			MaxIterations int `json:"max_iterations"`
			ReviewGates   map[string]struct {
				Criteria string `json:"criteria"`
			} `json:"review_gates"`
		} `json:"merged_catalog"`
		InheritedCatalog struct {
			MaxIterations int `json:"max_iterations"`
		} `json:"inherited_catalog"`
	}
	mustDecode(t, rec, &body)
	if gate := body.MergedCatalog.ReviewGates["code_review"]; gate.Criteria == "from-team" {
		t.Error("merged_catalog includes the team tier; this preview covers the plugin default plus the user tier only")
	}
	if body.MergedCatalog.MaxIterations != 4 || body.InheritedCatalog.MaxIterations != 3 {
		t.Errorf("merged/inherited max_iterations = %d/%d, want 4/3 (team tier excluded)", body.MergedCatalog.MaxIterations, body.InheritedCatalog.MaxIterations)
	}

	// The engine still merges it, which is exactly why the preview's
	// description matters.
	cat, err := config.LoadWithRoots(s.cfg.UserExtensionsDir, teamDir, "")
	if err != nil {
		t.Fatalf("LoadWithRoots: %v", err)
	}
	gate, ok := cat.ReviewGates["code_review"]
	if !ok || gate.Criteria != "from-team" {
		t.Errorf("the team tier must still reach an agent, got %+v (ok=%v)", gate, ok)
	}
	if cat.MaxIterations != 5 {
		t.Errorf("agent-side max_iterations = %d, want the team tier's 5", cat.MaxIterations)
	}
}

// Scenario: PUT /settings/catalog で language を含むドキュメントを保存した
// 直後のレスポンス merged_catalog が、追加の GET なしにその場で日本語化
// されている.
func TestSettingsCatalog_PutWithLanguageReflectsImmediately(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/catalog", map[string]any{
		"document": map[string]any{"version": 1, "language": "ja"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		MergedCatalog catalogDTO `json:"merged_catalog"`
	}
	mustDecode(t, rec, &body)
	if got := nodeName(t, body.MergedCatalog, "impl"); got != "実装" {
		t.Errorf("PUT response merged_catalog impl name = %q, want 実装 without a follow-up GET", got)
	}
}

// Scenario: language を含むドキュメントは（workflow.nodes/seed と異なり）
// 拒否されずに保存できる -- validateNoWorkflowOverride's exemption for
// Document.Language.
func TestSettingsCatalog_LanguageFieldNotRejected(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)
	rec := doJSON(t, s, http.MethodPut, "/api/settings/catalog", map[string]any{
		"document": map[string]any{"version": 1, "language": "ja"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Scenario: GET /settings/node-types (handleListSettingsNodeTypes) still
// works once a language setting is present -- a regression check for the
// read-order fix this handler needed (userDoc must be read before resolving
// the localized default; see settings.go's doc comment on that function).
func TestSettingsNodeTypes_SucceedsWithLanguageSet(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)
	putLanguage(t, s, "ja")

	rec := doJSON(t, s, http.MethodGet, "/api/settings/node-types", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Types []struct {
			Type string `json:"type"`
		} `json:"types"`
	}
	mustDecode(t, rec, &body)
	if len(body.Types) == 0 {
		t.Errorf("expected at least one node type, got none")
	}
}

func mustDecode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
