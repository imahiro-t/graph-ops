package main

// Test for DFLT-00145: create-project and use-project save the home config
// (projectPaths, currentProjectId), and both saves must keep the top-level
// keys this version does not know. This is the automated form of the manual
// check: a v0.9.0 binary switching projects erased v0.10.0's
// autopilotSettings.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/graph-ops/core-go/internal/runtimeconfig"
)

// unknownKeysJSON: see the runtimeconfig package's test of the same name --
// values any decode-and-re-encode would change.
const unknownKeysJSON = `"autopilotSettings": {"proj-1": {"mode": "auto", "maxParallel": 2}},
	"futureList": [1, "two", {"three": 3}],
	"futureBigInt": 9007199254740993,
	"futureNull": null,
	"futureFloat": 1.0,
	"futureRawChars": "https://example.com/?a=1&b=<2>",
	"futureEscaped": "a \u003c b \u0026 c",
	"futureNested": {"cmd": "x && y > z", "esc": "\u003ctag\u003e"}`

var unknownKeyNames = []string{
	"autopilotSettings", "futureList", "futureBigInt", "futureNull",
	"futureFloat", "futureRawChars", "futureEscaped", "futureNested",
}

func homeConfigDoc(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, raw)
	}
	return doc
}

func assertUnknownKeysUnchanged(t *testing.T, before, after []byte) {
	t.Helper()
	b, a := homeConfigDoc(t, before), homeConfigDoc(t, after)
	for _, k := range unknownKeyNames {
		av, ok := a[k]
		if !ok {
			t.Errorf("%q was dropped:\n%s", k, after)
			continue
		}
		var cb, ca bytes.Buffer
		if err := json.Compact(&cb, b[k]); err != nil {
			t.Fatal(err)
		}
		if err := json.Compact(&ca, av); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(cb.Bytes(), ca.Bytes()) {
			t.Errorf("%q changed: before %s, after %s", k, cb.Bytes(), ca.Bytes())
		}
		decode := func(raw []byte) any {
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			var v any
			if err := dec.Decode(&v); err != nil {
				t.Fatal(err)
			}
			return v
		}
		if db, da := decode(b[k]), decode(av); !reflect.DeepEqual(db, da) {
			t.Errorf("%q changed in meaning: %#v vs %#v", k, db, da)
		}
	}
}

func TestCreateAndUseProject_KeepUnknownConfigKeys(t *testing.T) {
	repo := newTestRepo(t)
	rc := sandboxRC(t)
	path := runtimeconfig.HomeConfigPath(rc.HomeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	before := []byte(`{` + unknownKeysJSON + `}`)
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()

	captureStdout(t, func() {
		if err := cmdCreateProject(repo, rc, []string{"Scenario", "--workdir", workDir}); err != nil {
			t.Fatalf("cmdCreateProject: %v", err)
		}
	})
	projects, err := repo.ListProjects()
	if err != nil || len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d (err=%v)", len(projects), err)
	}
	id := projects[0].ID
	wantPaths := map[string]string{id: workDir}
	if got := savedProjectPaths(t, rc); !reflect.DeepEqual(got, wantPaths) {
		t.Errorf("projectPaths = %v, want %v", got, wantPaths)
	}
	afterCreate, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertUnknownKeysUnchanged(t, before, afterCreate)

	captureStdout(t, func() {
		if err := cmdUseProject(repo, rc, []string{id}); err != nil {
			t.Fatalf("cmdUseProject: %v", err)
		}
	})
	if got := savedCurrentProjectID(t, rc); got == nil || *got != id {
		t.Errorf("currentProjectId = %v, want %q", got, id)
	}
	if got := savedProjectPaths(t, rc); !reflect.DeepEqual(got, wantPaths) {
		t.Errorf("projectPaths after use-project = %v, want %v", got, wantPaths)
	}
	afterUse, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertUnknownKeysUnchanged(t, before, afterUse)
}
