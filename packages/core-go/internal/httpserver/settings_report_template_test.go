package httpserver

import (
	"net/http"
	"testing"
)

// validReportHTML (a minimal HTML document containing every marker
// config.ValidateReportHTML requires) is already declared in tickets_test.go
// -- reused here rather than redeclared, but note it uses
// data-report-template="t", not "custom".

// Scenario: 必須マーカーを含むレポートテンプレートを保存・反映でき、空に
// すると既定テンプレートにフォールバックする.
func TestSettingsReportTemplate_SaveAndClear(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/report-template", map[string]any{
		"html": validReportHTML,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/settings/report-template", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		TierText   string `json:"tier_text"`
		MergedText string `json:"merged_text"`
	}
	mustDecode(t, rec, &body)
	if body.TierText != validReportHTML {
		t.Errorf("tier_text = %q", body.TierText)
	}
	if !contains(body.MergedText, `data-report-template="t"`) {
		t.Errorf("merged_text does not reflect override: %q", body.MergedText)
	}

	// Clearing (empty html) falls back to the plugin default template.
	rec = doJSON(t, s, http.MethodPut, "/api/settings/report-template", map[string]any{"html": ""})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT clear expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/settings/report-template", nil)
	mustDecode(t, rec, &body)
	if body.TierText != "" {
		t.Errorf("tier_text after clear = %q, want empty", body.TierText)
	}
	if contains(body.MergedText, `data-report-template="t"`) {
		t.Errorf("merged_text still contains cleared override: %q", body.MergedText)
	}
	if !contains(body.MergedText, `data-report-template="graph-ops-default"`) {
		t.Errorf("merged_text does not fall back to the plugin default: %q", body.MergedText)
	}
}

// Scenario: 必須マーカーが1つ欠けたHTMLを保存しようとすると400 +
// INVALID_REPORT_TEMPLATE になり、ファイルは書き込まれない.
func TestSettingsReportTemplate_MissingMarkerRejectedAndNotWritten(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)

	invalidHTML := `<!doctype html>
<html data-report-template="t" data-report-version="1">
<body>
<header data-report-section="header">H</header>
<section data-report-section="summary">S</section>
<section data-report-section="results">R</section>
</body>
</html>` // missing data-report-section="footer"

	rec := doJSON(t, s, http.MethodPut, "/api/settings/report-template", map[string]any{
		"html": invalidHTML,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec).Code; got != "INVALID_REPORT_TEMPLATE" {
		t.Errorf("error code = %q", got)
	}

	// Nothing was written: tier_text is still empty.
	rec = doJSON(t, s, http.MethodGet, "/api/settings/report-template", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		TierText string `json:"tier_text"`
	}
	mustDecode(t, rec, &body)
	if body.TierText != "" {
		t.Errorf("tier_text after rejected save = %q, want empty (nothing should have been written)", body.TierText)
	}
}
