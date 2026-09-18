package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestIsAStandaloneModule checks the sample stays independent of
// graph-engine: it has its own go.mod and imports nothing from
// github.com/graph-ops/core-go (a plugin only depends on the published
// protocol, never on graph-engine's internals).
func TestIsAStandaloneModule(t *testing.T) {
	mod, err := os.ReadFile("go.mod")
	if err != nil || !strings.Contains(string(mod), "module github.com/graph-ops/examples/jira-datasource") {
		t.Fatalf("go.mod = %q (%v)", mod, err)
	}
	if strings.Contains(string(mod), "graph-ops/core-go") {
		t.Fatal("go.mod depends on graph-ops/core-go")
	}
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, f := range files {
		parsed, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasPrefix(path, "github.com/graph-ops/core-go") {
				t.Errorf("%s imports %s", f, path)
			}
		}
	}
}
