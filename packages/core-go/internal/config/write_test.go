package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveDocumentAt_RoundTripsAndCreatesDirs(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nested", "workflow.yaml")

	doc := Document{
		Version: 1,
		ReviewGates: map[string]ReviewGateDef{
			"code_review": {Criteria: "be nice"},
		},
		MaxIterations: intPtr(5),
	}
	if err := SaveDocumentAt(path, doc); err != nil {
		t.Fatalf("SaveDocumentAt: %v", err)
	}

	got, err := LoadDocumentAt(path)
	if err != nil {
		t.Fatalf("LoadDocumentAt: %v", err)
	}
	if got.Version != 1 || got.ReviewGates["code_review"].Criteria != "be nice" {
		t.Errorf("round-tripped doc = %+v", got)
	}
	if got.MaxIterations == nil || *got.MaxIterations != 5 {
		t.Errorf("max_iterations = %v", got.MaxIterations)
	}
}

func TestLoadDocumentAt_MissingFileIsZeroValueNotError(t *testing.T) {
	got, err := LoadDocumentAt(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("expected no error for a missing file, got %v", err)
	}
	if got.Version != 0 || len(got.ReviewGates) != 0 {
		t.Errorf("expected zero-value Document, got %+v", got)
	}
}

func TestWriteExtensionText_SaveThenClearDeletesFile(t *testing.T) {
	root := t.TempDir()

	if err := WriteExtensionText(root, NodeTypesSubdir, "implementation.md", "extra instructions"); err != nil {
		t.Fatalf("WriteExtensionText: %v", err)
	}
	got, ok := readExtensionText(root, NodeTypesSubdir, "implementation.md")
	if !ok || got != "extra instructions" {
		t.Fatalf("got %q, ok=%v", got, ok)
	}

	// Saving empty text clears the override (falls back to lower layers).
	if err := WriteExtensionText(root, NodeTypesSubdir, "implementation.md", ""); err != nil {
		t.Fatalf("WriteExtensionText(clear): %v", err)
	}
	path := filepath.Join(root, extensionsSubdir, NodeTypesSubdir, "implementation.md")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected file to be removed, stat err = %v", err)
	}
}

func TestWriteExtensionText_RejectsUnsafeName(t *testing.T) {
	root := t.TempDir()
	if err := WriteExtensionText(root, NodeTypesSubdir, "../../escape.md", "x"); err == nil {
		t.Fatal("expected an error for a path-traversal node type name")
	}
}

func TestListKnownNodeTypes_IncludesBuiltinsAndCustomTypes(t *testing.T) {
	catalog := Catalog{Nodes: []NodeDef{
		{ID: "n1", Type: "implementation"},
		{ID: "n2", Type: "custom_lint"},
	}}
	got := ListKnownNodeTypes(catalog)

	seen := make(map[string]bool, len(got))
	for _, t := range got {
		seen[t] = true
	}
	for _, want := range []string{"plan", "review", "implementation", "documentation", "release", "custom_lint"} {
		if !seen[want] {
			t.Errorf("expected %q in %v", want, got)
		}
	}

	// No duplicates.
	if len(seen) != len(got) {
		t.Errorf("expected no duplicates, got %v", got)
	}
}

// Every node type the plugin ships a default instructions file for must be
// listed in builtinNodeTypes, or the settings UI's picker hides it until
// some catalog node happens to reference it -- which is exactly how
// "documentation" went missing. Checking against the embedded defaults
// rather than a hand-written list means adding a new defaults/node-types/
// file (and forgetting builtinNodeTypes) fails here instead of shipping.
//
// The reverse direction is deliberately not asserted: a builtin type may
// legitimately have no default text (defaultNodeTypeContent returns "" for
// it), e.g. a purely manual node type.
func TestBuiltinNodeTypes_CoverEveryShippedDefaultFile(t *testing.T) {
	entries, err := defaultNodeTypeFS.ReadDir("defaults/node-types")
	if err != nil {
		t.Fatalf("reading embedded defaults: %v", err)
	}
	unclaimed := make(map[string]bool, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			unclaimed[e.Name()] = true
		}
	}
	if len(unclaimed) == 0 {
		t.Fatal("no default node type files embedded; the test is not checking anything")
	}
	for _, nodeType := range builtinNodeTypes {
		delete(unclaimed, nodeTypeDefaultFile(nodeType))
	}
	for name := range unclaimed {
		t.Errorf("defaults/node-types/%s has no matching entry in builtinNodeTypes", name)
	}
}
