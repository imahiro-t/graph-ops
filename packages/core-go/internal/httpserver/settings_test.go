package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
)

// newSettingsTestServer is newTestServer, but with cfg.UserExtensionsDir
// pinned to a fresh temp dir (so "global" scope tests never touch the real
// $HOME/.graph-ops) and cfg.TeamExtensionsDir left empty, so
// scope=project resolution goes through the project's own local path (see
// settingsScope/resolveSettingsScope) rather than any explicit override.
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
	if err := repo.SetCurrentProjectID(proj.ID); err != nil {
		t.Fatalf("SetCurrentProjectID: %v", err)
	}
	eng := engine.New(repo)
	cfg := Config{ArtifactsDir: t.TempDir(), UserExtensionsDir: t.TempDir(), HomeDir: t.TempDir()}
	s := New(repo, eng, cfg)
	if _, err := runtimeconfig.SetProjectPath(cfg.WorkDir, cfg.HomeDir, proj.ID, t.TempDir()); err != nil {
		t.Fatalf("SetProjectPath: %v", err)
	}
	return s, repo, proj.ID
}

// Scenario: 全体設定スコープでノード種別の指示文を追加・保存できる -- and
// clearing it back to "" falls back to the plugin default (no override).
func TestSettingsNodeType_GlobalScopeSaveAndClear(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/node-types/gherkin_spec", map[string]any{
		"scope": "global", "text": "Always write scenario titles in JPY context.",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/settings/node-types/gherkin_spec?scope=global", nil)
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
	rec = doJSON(t, s, http.MethodPut, "/api/settings/node-types/gherkin_spec", map[string]any{
		"scope": "global", "text": "",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT clear expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/settings/node-types/gherkin_spec?scope=global", nil)
	mustDecode(t, rec, &body)
	if body.TierText != "" {
		t.Errorf("tier_text after clear = %q, want empty", body.TierText)
	}
	if contains(body.MergedText, "Always write scenario titles in JPY context.") {
		t.Errorf("merged_text still contains cleared override: %q", body.MergedText)
	}
}

// Scenario: プロジェクト単位設定スコープでノード種別の指示文を追加・保存でき、
// 別プロジェクトには反映されない -- and the write actually lands under that
// project's local path/.graph-ops/, matching the plan's design fix for
// per-project team-tier resolution.
func TestSettingsNodeType_ProjectScopeIsolatedPerProject(t *testing.T) {
	s, repo, projA := newSettingsTestServer(t)
	workDirB := t.TempDir()
	projB, err := repo.CreateProject("Project B", "PB")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := runtimeconfig.SetProjectPath(s.cfg.WorkDir, s.cfg.HomeDir, projB.ID, workDirB); err != nil {
		t.Fatalf("SetProjectPath(B): %v", err)
	}

	projectA, err := repo.GetProject(projA)
	if err != nil || projectA == nil {
		t.Fatalf("GetProject(A): %v", err)
	}

	rec := doJSON(t, s, http.MethodPut, "/api/settings/node-types/implementation", map[string]any{
		"scope": "project", "project_id": projA, "text": "project-A specific instructions",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// The override file actually lives under project A's local path.
	expected := filepath.Join(testProjectLocalPath(t, s, projectA.ID), ".graph-ops", "extensions", "node-types", "implementation.md")
	raw, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("expected override file at %s: %v", expected, err)
	}
	if string(raw) != "project-A specific instructions" {
		t.Errorf("file content = %q", string(raw))
	}

	// Project B sees no override.
	rec = doJSON(t, s, http.MethodGet, "/api/settings/node-types/implementation?scope=project&project_id="+projB.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		TierText string `json:"tier_text"`
	}
	mustDecode(t, rec, &body)
	if body.TierText != "" {
		t.Errorf("project B tier_text = %q, want empty (isolated from project A)", body.TierText)
	}
}

// Scenario: プロジェクトが選択されていない状態ではプロジェクト単位設定タブが
// 無効化される -- server-side: scope=project without project_id fails
// validation instead of guessing a project.
func TestSettingsScope_ProjectWithoutProjectIDIsRejected(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)
	rec := doJSON(t, s, http.MethodGet, "/api/settings/node-types/plan?scope=project", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec).Code; got != "INVALID_SETTINGS_SCOPE" {
		t.Errorf("error code = %q", got)
	}
}

// Scenario: workflow.nodes を保存しようとすると、スキーマ骨格はプラグイン
// 既定のみが定めるためバリデーションエラーになり保存されない.
func TestSettingsCatalog_WorkflowNodesRejected(t *testing.T) {
	s, _, projA := newSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/catalog", map[string]any{
		"scope": "project", "project_id": projA,
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
	s, _, projA := newSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/catalog", map[string]any{
		"scope": "project", "project_id": projA,
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

// Scenario: 最大イテレーション数に0以下の値を入力するとバリデーションエラー
// になる.
func TestSettingsCatalog_InvalidMaxIterationsRejected(t *testing.T) {
	s, _, projA := newSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/catalog", map[string]any{
		"scope": "project", "project_id": projA,
		"document": map[string]any{
			"version": 1,
			"review_gates": map[string]any{
				"code_review": map[string]any{"max_iterations": 0},
			},
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec).Code; got != "INVALID_MAX_ITERATIONS" {
		t.Errorf("error code = %q", got)
	}
}

// Scenario: a valid workflow/review-gate edit saves successfully and is
// reflected in the merged catalog.
func TestSettingsCatalog_ValidSaveRoundTrips(t *testing.T) {
	s, _, projA := newSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/catalog", map[string]any{
		"scope": "project", "project_id": projA,
		"document": map[string]any{
			"version": 1,
			"review_gates": map[string]any{
				"code_review": map[string]any{"max_iterations": 5},
			},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/settings/catalog?scope=project&project_id="+projA, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		MergedCatalog struct {
			ReviewGates map[string]struct {
				MaxIterations *int `json:"max_iterations"`
			} `json:"review_gates"`
		} `json:"merged_catalog"`
	}
	mustDecode(t, rec, &body)
	gate, ok := body.MergedCatalog.ReviewGates["code_review"]
	if !ok || gate.MaxIterations == nil || *gate.MaxIterations != 5 {
		t.Errorf("merged code_review gate = %+v (ok=%v)", gate, ok)
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

func putLanguage(t *testing.T, s *Server, scope, projectID, language string) {
	t.Helper()
	body := map[string]any{
		"scope":    scope,
		"document": map[string]any{"version": 1, "language": language},
	}
	if projectID != "" {
		body["project_id"] = projectID
	}
	rec := doJSON(t, s, http.MethodPut, "/api/settings/catalog", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT settings/catalog (scope=%s, language=%s) expected 200, got %d: %s", scope, language, rec.Code, rec.Body.String())
	}
}

// Scenario: グローバルスコープの merged_catalog は user 層の language のみで
// 解決され、team 層の language には一切影響されない -- even a team-tier
// language that would (if wrongly consulted) leave the skeleton English
// (here "en", for which no locale file ships) must not override the user
// tier's "ja".
func TestSettingsCatalog_GlobalScopeIgnoresTeamLanguage(t *testing.T) {
	s, _, projA := newSettingsTestServer(t)

	putLanguage(t, s, "global", "", "ja")
	putLanguage(t, s, "project", projA, "en")

	rec := doJSON(t, s, http.MethodGet, "/api/settings/catalog?scope=global", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body settingsCatalogGetResponse
	mustDecode(t, rec, &body)
	if body.ResolvedLanguage != "ja" {
		t.Errorf("resolved_language = %q, want ja (team's language must not leak into global scope)", body.ResolvedLanguage)
	}
	if got := nodeName(t, body.MergedCatalog, "impl"); got != "実装" {
		t.Errorf("merged_catalog impl name = %q, want 実装", got)
	}
}

// Scenario: プロジェクトスコープで team 層のみに language を設定した場合、
// merged_catalog は日本語化されるが、team を含まない inherited_catalog は
// 英語のまま.
func TestSettingsCatalog_ProjectScopeTeamOnlyLanguage(t *testing.T) {
	s, _, projA := newSettingsTestServer(t)

	putLanguage(t, s, "project", projA, "ja")

	rec := doJSON(t, s, http.MethodGet, "/api/settings/catalog?scope=project&project_id="+projA, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body settingsCatalogGetResponse
	mustDecode(t, rec, &body)
	if got := nodeName(t, body.MergedCatalog, "impl"); got != "実装" {
		t.Errorf("merged_catalog impl name = %q, want 実装 (team-tier language)", got)
	}
	if got := nodeName(t, body.InheritedCatalog, "impl"); got != "Implementation" {
		t.Errorf("inherited_catalog impl name = %q, want the English default (inherited_catalog excludes team)", got)
	}
}

// Scenario: プロジェクトスコープで user 層のみに language を設定した場合、
// merged_catalog・inherited_catalog の両方が日本語化される（どちらも user
// 層を含むため）.
func TestSettingsCatalog_ProjectScopeUserOnlyLanguage(t *testing.T) {
	s, _, projA := newSettingsTestServer(t)

	putLanguage(t, s, "global", "", "ja")

	rec := doJSON(t, s, http.MethodGet, "/api/settings/catalog?scope=project&project_id="+projA, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body settingsCatalogGetResponse
	mustDecode(t, rec, &body)
	if got := nodeName(t, body.MergedCatalog, "impl"); got != "実装" {
		t.Errorf("merged_catalog impl name = %q, want 実装 (user-tier language)", got)
	}
	if got := nodeName(t, body.InheritedCatalog, "impl"); got != "実装" {
		t.Errorf("inherited_catalog impl name = %q, want 実装 (inherited_catalog still includes user tier)", got)
	}
}

// Scenario: PUT /settings/catalog で language を含むドキュメントを保存した
// 直後のレスポンス merged_catalog が、追加の GET なしにその場で日本語化
// されている.
func TestSettingsCatalog_PutWithLanguageReflectsImmediately(t *testing.T) {
	s, _, projA := newSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/catalog", map[string]any{
		"scope": "project", "project_id": projA,
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
	s, _, projA := newSettingsTestServer(t)
	rec := doJSON(t, s, http.MethodPut, "/api/settings/catalog", map[string]any{
		"scope": "project", "project_id": projA,
		"document": map[string]any{"version": 1, "language": "ja"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Scenario: GET /settings/node-types (handleListSettingsNodeTypes) still
// works once team/user language settings are present, both with and without
// a project_id -- a regression check for the read-order fix this handler
// needed (userDoc/teamDoc must be read before resolving the localized
// default; see settings.go's doc comment on that function).
func TestSettingsNodeTypes_SucceedsWithLanguageSet(t *testing.T) {
	s, _, projA := newSettingsTestServer(t)
	putLanguage(t, s, "global", "", "ja")
	putLanguage(t, s, "project", projA, "ja")

	rec := doJSON(t, s, http.MethodGet, "/api/settings/node-types", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET (no project_id) expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/settings/node-types?project_id="+projA, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET (with project_id) expected 200, got %d: %s", rec.Code, rec.Body.String())
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
