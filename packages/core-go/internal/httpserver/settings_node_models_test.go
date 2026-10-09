package httpserver

import (
	"net/http"
	"os"
	"testing"

	"github.com/graph-ops/core-go/internal/config"
)

type settingsNodeModelsResponse struct {
	MergedCatalog struct {
		NodeModels map[string]string `json:"node_models"`
	} `json:"merged_catalog"`
	TierDocument struct {
		NodeModels map[string]string `json:"node_models"`
	} `json:"tier_document"`
	Warnings []map[string]any `json:"warnings"`
}

// DFLT-00375: the settings screen has no node_models editor, but the
// document it PUTs back (the loaded tier_document) carries node_models, and
// saving must keep it.
func TestSettingsCatalog_NodeModelsRoundTrip(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)
	path := config.UserDocumentPath(s.cfg.UserExtensionsDir)
	if err := os.WriteFile(path, []byte("version: 1\nnode_models:\n  report: inherit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, s, http.MethodGet, "/api/settings/catalog", nil)
	var get settingsNodeModelsResponse
	mustDecode(t, rec, &get)
	if get.TierDocument.NodeModels["report"] != "inherit" {
		t.Fatalf("tier_document.node_models = %v", get.TierDocument.NodeModels)
	}
	if get.MergedCatalog.NodeModels["report"] != "inherit" || get.MergedCatalog.NodeModels["gherkin_test"] != "sonnet" {
		t.Errorf("merged_catalog.node_models = %v", get.MergedCatalog.NodeModels)
	}

	// The screen sends the tier document back, with its own change.
	rec = doJSON(t, s, http.MethodPut, "/api/settings/catalog", map[string]any{
		"document": map[string]any{"version": 1, "max_iterations": 4, "node_models": get.TierDocument.NodeModels},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	doc, err := config.LoadDocumentAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc.NodeModels["report"] != "inherit" {
		t.Errorf("saved node_models = %v, want report: inherit kept", doc.NodeModels)
	}
}

func TestSettingsCatalog_InvalidNodeModelIsReportedAndRejected(t *testing.T) {
	s, _, _ := newSettingsTestServer(t)
	path := config.UserDocumentPath(s.cfg.UserExtensionsDir)
	original := "version: 1\nnode_models:\n  report: fable\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, s, http.MethodGet, "/api/settings/catalog", nil)
	var get settingsNodeModelsResponse
	mustDecode(t, rec, &get)
	if len(get.Warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", get.Warnings)
	}
	if w := get.Warnings[0]; w["code"] != config.WarnNodeModelInvalid || w["node_type"] != "report" || w["node_model"] != "fable" {
		t.Errorf("warning = %v", w)
	}

	rec = doJSON(t, s, http.MethodPut, "/api/settings/catalog", map[string]any{
		"document": map[string]any{"version": 1, "node_models": map[string]string{"report": "gpt-4"}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if raw, _ := os.ReadFile(path); string(raw) != original {
		t.Errorf("the user document changed on a rejected PUT:\n%s", raw)
	}
}
