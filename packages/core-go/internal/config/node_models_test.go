package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// DFLT-00375 completion criterion 1: the plugin default assigns report to
// haiku, gherkin_test/release to sonnet and every other type to inherit.
func TestLoadWithRoots_DefaultNodeModels(t *testing.T) {
	cat, err := LoadWithRoots(t.TempDir(), "", "")
	if err != nil {
		t.Fatalf("LoadWithRoots: %v", err)
	}
	want := map[string]string{
		"report": "haiku", "gherkin_test": "sonnet", "release": "sonnet",
		"plan": "inherit", "investigation": "inherit", "gherkin_spec": "inherit",
		"implementation": "inherit", "review": "inherit", "review_gate": "inherit",
		"documentation": "inherit", "deliverable": "inherit",
		"custom_lint": "inherit", "approval_gate": "inherit",
	}
	for typ, w := range want {
		if got := cat.ModelFor(typ); got != w {
			t.Errorf("ModelFor(%q) = %q, want %q", typ, got, w)
		}
	}
}

// Completion criterion 2: per-key, later tier wins (team over user over the
// default), and custom types can be assigned too.
func TestLoadWithRoots_NodeModelsOverride(t *testing.T) {
	userDir, teamDir := t.TempDir(), t.TempDir()
	writeTierFile(t, userDir, userConfigFile, "version: 1\nnode_models:\n  report: sonnet\n  gherkin_test: inherit\n")
	writeTierFile(t, teamDir, teamConfigFile, "version: 1\nnode_models:\n  report: opus\n  security_scan: sonnet\n")
	cat, err := LoadWithRoots(userDir, teamDir, "")
	if err != nil {
		t.Fatalf("LoadWithRoots: %v", err)
	}
	for typ, w := range map[string]string{
		"report":        "opus",    // team beats user
		"gherkin_test":  "inherit", // user beats the default
		"release":       "sonnet",  // default untouched
		"security_scan": "sonnet",  // custom type
		"custom_lint":   "inherit", // nobody assigns it
	} {
		if got := cat.ModelFor(typ); got != w {
			t.Errorf("ModelFor(%q) = %q, want %q", typ, got, w)
		}
	}
}

func TestLoadWithRoots_UserNodeModelsOverride(t *testing.T) {
	userDir := t.TempDir()
	writeTierFile(t, userDir, userConfigFile, "version: 1\nnode_models:\n  report: inherit\n")
	cat, err := LoadWithRoots(userDir, "", "")
	if err != nil {
		t.Fatalf("LoadWithRoots: %v", err)
	}
	if got := cat.ModelFor("report"); got != "inherit" {
		t.Errorf("report = %q, want inherit", got)
	}
	if got := cat.ModelFor("gherkin_test"); got != "sonnet" {
		t.Errorf("gherkin_test = %q, want sonnet", got)
	}
}

func TestLoadWithRoots_RejectsInvalidNodeModel(t *testing.T) {
	for _, v := range []string{"gpt-4", "fable", `""`, "Opus"} {
		for _, tier := range []string{"user", "team"} {
			t.Run(tier+"/"+v, func(t *testing.T) {
				userDir, teamDir := t.TempDir(), t.TempDir()
				doc := "version: 1\nnode_models:\n  report: " + v + "\n"
				if tier == "user" {
					writeTierFile(t, userDir, userConfigFile, doc)
				} else {
					writeTierFile(t, teamDir, teamConfigFile, doc)
				}
				_, err := LoadWithRoots(userDir, teamDir, "")
				if err == nil || !strings.Contains(err.Error(), "node_models") || !strings.Contains(err.Error(), `"report"`) {
					t.Fatalf("err = %v, want a node_models error naming report", err)
				}
			})
		}
	}
}

func TestNodeModelWarnings(t *testing.T) {
	doc := Document{NodeModels: map[string]string{"report": "", "plan": "inherit", "b": "fable", "a": "gpt-4"}}
	ws := NodeModelWarnings(doc)
	if len(ws) != 3 {
		t.Fatalf("warnings = %+v, want 3", ws)
	}
	wantTypes := []string{"a", "b", "report"}
	for i, w := range ws {
		if w.Code != WarnNodeModelInvalid || w.NodeType != wantTypes[i] || w.NodeModel == nil {
			t.Errorf("warning %d = %+v", i, w)
		}
	}
	raw, _ := json.Marshal(ws[2])
	if !strings.Contains(string(raw), `"node_model":""`) || !strings.Contains(string(raw), `"node_type":"report"`) {
		t.Errorf("JSON = %s, want the empty value kept", raw)
	}
	if !strings.Contains(ws[1].Message(), `"fable"`) {
		t.Errorf("Message() = %q", ws[1].Message())
	}
	if NodeModelWarnings(Document{}) == nil || len(NodeModelWarnings(Document{})) != 0 {
		t.Errorf("no node_models should give an empty list")
	}
}

// The value survives a save/load round trip of a tier document.
func TestSaveDocumentAt_KeepsNodeModels(t *testing.T) {
	path := t.TempDir() + "/config.yaml"
	if err := SaveDocumentAt(path, Document{Version: 1, NodeModels: map[string]string{"report": "inherit"}}); err != nil {
		t.Fatalf("SaveDocumentAt: %v", err)
	}
	doc, err := LoadDocumentAt(path)
	if err != nil {
		t.Fatalf("LoadDocumentAt: %v", err)
	}
	if doc.NodeModels["report"] != "inherit" {
		t.Errorf("NodeModels = %v", doc.NodeModels)
	}
}
