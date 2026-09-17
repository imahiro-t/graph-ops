package httpserver

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/config"
)

// markdownTemplateCase is one of the two Markdown templates the settings
// UI's テンプレート tab edits through GET/PUT /api/settings/<kind>-template.
type markdownTemplateCase struct {
	kind           string // "plan" | "review" -- also the extensions/ subdir
	path           string // API path
	defaultHeading string // first heading of the English plugin default
	resolve        func(config.Roots) string
}

var markdownTemplateCases = []markdownTemplateCase{
	{kind: "plan", path: "/api/settings/plan-template", defaultHeading: "# Purpose", resolve: config.ResolvePlanTemplate},
	{kind: "review", path: "/api/settings/review-template", defaultHeading: "# Verdict", resolve: config.ResolveReviewTemplate},
}

type templateTextBody struct {
	TierText   string `json:"tier_text"`
	MergedText string `json:"merged_text"`
}

func getTemplateState(t *testing.T, s *Server, path string) templateTextBody {
	t.Helper()
	rec := doJSON(t, s, http.MethodGet, path, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s expected 200, got %d: %s", path, rec.Code, rec.Body.String())
	}
	var body templateTextBody
	mustDecode(t, rec, &body)
	return body
}

func putTemplateText(t *testing.T, s *Server, path string, payload map[string]any) templateTextBody {
	t.Helper()
	rec := doJSON(t, s, http.MethodPut, path, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT %s expected 200, got %d: %s", path, rec.Code, rec.Body.String())
	}
	var body templateTextBody
	mustDecode(t, rec, &body)
	return body
}

func overrideFile(root, kind string) string {
	return filepath.Join(root, "extensions", kind, "template.md")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func projectTeamRoot(t *testing.T, s *Server, projectID string) string {
	t.Helper()
	return filepath.Join(testProjectLocalPath(t, s, projectID), ".graph-ops")
}

// Scenario: 上書きがないとき、GET は空の tier_text と英語デフォルトの
// merged_text を返す.
func TestSettingsMarkdownTemplate_NoOverrideReturnsEnglishDefault(t *testing.T) {
	for _, tc := range markdownTemplateCases {
		t.Run(tc.kind, func(t *testing.T) {
			s, _, _ := newSettingsTestServer(t)

			body := getTemplateState(t, s, tc.path+"?scope=global")
			if body.TierText != "" {
				t.Errorf("tier_text = %q, want empty", body.TierText)
			}
			if !strings.HasPrefix(body.MergedText, tc.defaultHeading) {
				t.Errorf("merged_text should start with %q, got %q", tc.defaultHeading, body.MergedText)
			}
			if body.MergedText != tc.resolve(config.Roots{}) {
				t.Errorf("merged_text does not match the plugin default")
			}
		})
	}
}

// Scenario: 全体設定のスコープで保存すると、ユーザー層の上書きファイルに
// 書かれて GET に反映される / 空文字で保存すると上書きファイルが消え、
// 下の層に戻る. Also checks the CLI-side resolver reads the same file.
func TestSettingsMarkdownTemplate_GlobalScopeSaveAndClear(t *testing.T) {
	for _, tc := range markdownTemplateCases {
		t.Run(tc.kind, func(t *testing.T) {
			s, _, projA := newSettingsTestServer(t)
			userRoot := s.cfg.UserExtensionsDir
			teamRoot := projectTeamRoot(t, s, projA)
			const text = "# ユーザーの見出し\n本文"

			body := putTemplateText(t, s, tc.path, map[string]any{"scope": "global", "text": text})
			if body.TierText != text || body.MergedText != text {
				t.Errorf("PUT response = %+v, want tier/merged %q", body, text)
			}
			data, err := os.ReadFile(overrideFile(userRoot, tc.kind))
			if err != nil || string(data) != text {
				t.Errorf("user override file = (%q, %v), want %q", data, err, text)
			}
			if fileExists(overrideFile(teamRoot, tc.kind)) {
				t.Errorf("team override file should not have been written")
			}

			body = getTemplateState(t, s, tc.path+"?scope=global")
			if body.TierText != text || body.MergedText != text {
				t.Errorf("GET after save = %+v, want tier/merged %q", body, text)
			}

			// What get-plan-template/get-review-template resolve is the same file.
			if got := tc.resolve(config.Roots{UserDir: userRoot}); got != text {
				t.Errorf("config resolver = %q, want the saved override", got)
			}

			body = putTemplateText(t, s, tc.path, map[string]any{"scope": "global", "text": ""})
			if body.TierText != "" {
				t.Errorf("tier_text after clear = %q, want empty", body.TierText)
			}
			if !strings.HasPrefix(body.MergedText, tc.defaultHeading) {
				t.Errorf("merged_text after clear should fall back to %q, got %q", tc.defaultHeading, body.MergedText)
			}
			if fileExists(overrideFile(userRoot, tc.kind)) {
				t.Errorf("user override file should have been removed by an empty save")
			}
		})
	}
}

// Scenario: plan / review の上書きは内容をチェックせずに保存できる.
func TestSettingsMarkdownTemplate_AcceptsAnyContent(t *testing.T) {
	contents := []string{
		"# 自由な見出し\n任意の本文",
		"見出しのない、ただの文章",
		"<!-- HTML コメントと <b>タグ</b> を含む本文 -->",
	}
	for _, tc := range markdownTemplateCases {
		for i, text := range contents {
			t.Run(fmt.Sprintf("%s_%d", tc.kind, i), func(t *testing.T) {
				s, _, _ := newSettingsTestServer(t)
				body := putTemplateText(t, s, tc.path, map[string]any{"scope": "global", "text": text})
				if body.TierText != text {
					t.Errorf("tier_text = %q, want %q", body.TierText, text)
				}
			})
		}
	}
}

// Scenario: プロジェクト単位設定のスコープで保存するとチーム層に書かれ、
// merged_text はユーザー層よりチーム層を優先する / チーム層の上書きを空保存で
// 解除すると、merged_text はユーザー層の上書きに戻る.
func TestSettingsMarkdownTemplate_ProjectScopeTeamWinsOverUser(t *testing.T) {
	for _, tc := range markdownTemplateCases {
		t.Run(tc.kind, func(t *testing.T) {
			s, _, projA := newSettingsTestServer(t)
			userRoot := s.cfg.UserExtensionsDir
			teamRoot := projectTeamRoot(t, s, projA)

			putTemplateText(t, s, tc.path, map[string]any{"scope": "global", "text": "# ユーザーの見出し"})

			body := putTemplateText(t, s, tc.path, map[string]any{"scope": "project", "project_id": projA, "text": "# チームの見出し"})
			if body.TierText != "# チームの見出し" || body.MergedText != "# チームの見出し" {
				t.Errorf("project PUT response = %+v, want team heading for both", body)
			}
			data, err := os.ReadFile(overrideFile(teamRoot, tc.kind))
			if err != nil || string(data) != "# チームの見出し" {
				t.Errorf("team override file = (%q, %v)", data, err)
			}
			data, err = os.ReadFile(overrideFile(userRoot, tc.kind))
			if err != nil || string(data) != "# ユーザーの見出し" {
				t.Errorf("user override file changed: (%q, %v)", data, err)
			}
			if got := tc.resolve(config.Roots{UserDir: userRoot, TeamDir: teamRoot}); got != "# チームの見出し" {
				t.Errorf("config resolver = %q, want the team override", got)
			}

			// A global-scope preview never includes a project's team tier.
			global := getTemplateState(t, s, tc.path+"?scope=global")
			if global.TierText != "# ユーザーの見出し" || global.MergedText != "# ユーザーの見出し" {
				t.Errorf("global GET = %+v, want user heading for both", global)
			}

			body = putTemplateText(t, s, tc.path, map[string]any{"scope": "project", "project_id": projA, "text": ""})
			if body.TierText != "" {
				t.Errorf("tier_text after clearing team override = %q, want empty", body.TierText)
			}
			if body.MergedText != "# ユーザーの見出し" {
				t.Errorf("merged_text after clearing team override = %q, want the user override", body.MergedText)
			}
		})
	}
}

// Scenario: 不正なスコープやリクエストは 4xx で拒否され、ファイルは書かれない.
func TestSettingsMarkdownTemplate_InvalidScopeRejected(t *testing.T) {
	// The spec calls the scope error INVALID_SCOPE; the code every settings
	// endpoint actually returns for it is domain.ErrCodeInvalidScope, whose
	// wire value is INVALID_SETTINGS_SCOPE.
	type req struct {
		name   string
		method string
		query  string
		body   map[string]any
		status int
		code   string
	}
	reqs := []req{
		{name: "get_unknown_scope", method: http.MethodGet, query: "?scope=foo", status: http.StatusBadRequest, code: "INVALID_SETTINGS_SCOPE"},
		{name: "get_project_without_id", method: http.MethodGet, query: "?scope=project", status: http.StatusBadRequest, code: "INVALID_SETTINGS_SCOPE"},
		{name: "put_unknown_scope", method: http.MethodPut, body: map[string]any{"scope": "foo", "text": "# x"}, status: http.StatusBadRequest, code: "INVALID_SETTINGS_SCOPE"},
		{name: "put_project_without_id", method: http.MethodPut, body: map[string]any{"scope": "project", "text": "# x"}, status: http.StatusBadRequest, code: "INVALID_SETTINGS_SCOPE"},
		{name: "put_unknown_project", method: http.MethodPut, body: map[string]any{"scope": "project", "project_id": "no-such", "text": "# x"}, status: http.StatusNotFound, code: "PROJECT_NOT_FOUND"},
	}
	for _, tc := range markdownTemplateCases {
		for _, rq := range reqs {
			t.Run(tc.kind+"_"+rq.name, func(t *testing.T) {
				s, _, projA := newSettingsTestServer(t)
				rec := doJSON(t, s, rq.method, tc.path+rq.query, rq.body)
				if rec.Code != rq.status {
					t.Fatalf("expected %d, got %d: %s", rq.status, rec.Code, rec.Body.String())
				}
				if got := string(decodeError(t, rec).Code); got != rq.code {
					t.Errorf("error code = %q, want %q", got, rq.code)
				}
				if fileExists(overrideFile(s.cfg.UserExtensionsDir, tc.kind)) || fileExists(overrideFile(projectTeamRoot(t, s, projA), tc.kind)) {
					t.Errorf("no override file should have been written")
				}
			})
		}
	}
}

// Scenario: JSON として読めない body は 400 で拒否される.
func TestSettingsMarkdownTemplate_MalformedJSONRejected(t *testing.T) {
	for _, tc := range markdownTemplateCases {
		t.Run(tc.kind, func(t *testing.T) {
			s, _, _ := newSettingsTestServer(t)
			req := httptest.NewRequest(http.MethodPut, tc.path, bytes.NewReader([]byte(`{"scope": "global", "text": `)))
			req.Host = testHost
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(csrfHeaderName, "1")
			rec := httptest.NewRecorder()
			s.Routes().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
			if fileExists(overrideFile(s.cfg.UserExtensionsDir, tc.kind)) {
				t.Errorf("no override file should have been written")
			}
		})
	}
}

// Scenario: language: ja を設定していても、上書きがなければ merged_text は
// 英語デフォルトのまま.
func TestSettingsMarkdownTemplate_JaLanguageStillReturnsEnglishDefault(t *testing.T) {
	for _, tc := range markdownTemplateCases {
		t.Run(tc.kind, func(t *testing.T) {
			s, _, _ := newSettingsTestServer(t)
			if err := config.SaveDocumentAt(config.UserDocumentPath(s.cfg.UserExtensionsDir), config.Document{Language: "ja"}); err != nil {
				t.Fatalf("SaveDocumentAt: %v", err)
			}

			body := getTemplateState(t, s, tc.path+"?scope=global")
			if !strings.HasPrefix(body.MergedText, tc.defaultHeading) {
				t.Errorf("merged_text should stay the English default starting with %q, got %q", tc.defaultHeading, body.MergedText)
			}
			jaPath := filepath.Join("..", "..", "..", "plugin", "defaults", "locales", "ja", tc.kind, "template.md")
			ja, err := os.ReadFile(jaPath)
			if err != nil {
				t.Fatalf("reading %s: %v", jaPath, err)
			}
			if body.MergedText == strings.TrimSpace(string(ja)) {
				t.Errorf("merged_text must not be the Japanese template")
			}
		})
	}
}

// Scenario: レポートテンプレートの API は、今までどおり html フィールドと
// 必須マーカーの検証を使う -- a "text" field is not read by the report PUT.
func TestSettingsReportTemplate_TextFieldIsIgnored(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)
	rec := doJSON(t, s, http.MethodPut, "/api/settings/report-template", map[string]any{
		"scope": "global", "text": validReportHTML,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (empty html clears), got %d: %s", rec.Code, rec.Body.String())
	}
	body := getTemplateState(t, s, "/api/settings/report-template?scope=global")
	if body.TierText != "" {
		t.Errorf("report tier_text = %q, want empty: only the html field may set it", body.TierText)
	}
}
