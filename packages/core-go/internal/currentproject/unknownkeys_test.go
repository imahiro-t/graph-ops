package currentproject

// Tests for DFLT-00145: switching, clearing or inheriting the current project
// saves the home config, and that save must keep the keys this version does
// not know. Switching projects is exactly how a v0.9.0 UI server erased the
// v0.10.0 autopilotSettings.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
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

func (e env) write(t *testing.T, raw string) []byte {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(e.path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.path(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return []byte(raw)
}

func (e env) read(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(e.path())
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func topLevel(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, raw)
	}
	return doc
}

// assertValuesUnchanged: each key's value is byte-equal after json.Compact
// and equal when decoded with UseNumber.
func assertValuesUnchanged(t *testing.T, before, after []byte, keys []string) {
	t.Helper()
	b, a := topLevel(t, before), topLevel(t, after)
	for _, k := range keys {
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
		if db, da := decodeUseNumber(t, b[k]), decodeUseNumber(t, av); !reflect.DeepEqual(db, da) {
			t.Errorf("%q changed in meaning: %#v vs %#v", k, db, da)
		}
	}
}

func decodeUseNumber(t *testing.T, raw []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestSet_KeepsUnknownKeys is the reported scenario: switching projects.
func TestSet_KeepsUnknownKeys(t *testing.T) {
	e := newEnv(t)
	before := e.write(t, `{"currentProjectId": "proj-old", `+unknownKeysJSON+`}`)

	if err := Set(e.home, "proj-new"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	after := e.read(t)
	if got := string(topLevel(t, after)["currentProjectId"]); got != `"proj-new"` {
		t.Errorf("currentProjectId = %s, want \"proj-new\"", got)
	}
	assertValuesUnchanged(t, before, after, unknownKeyNames)
}

func TestClear_KeepsUnknownKeys(t *testing.T) {
	e := newEnv(t)
	before := e.write(t, `{"currentProjectId": "proj-old", `+unknownKeysJSON+`}`)

	if err := Clear(e.home); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	after := e.read(t)
	if got := string(topLevel(t, after)["currentProjectId"]); got != `""` {
		t.Errorf("currentProjectId = %s, want \"\"", got)
	}
	assertValuesUnchanged(t, before, after, unknownKeyNames)
}

func TestGet_InheritanceKeepsUnknownKeys(t *testing.T) {
	e := newEnv(t)
	before := e.write(t, `{`+unknownKeysJSON+`}`)
	repo := &stubRepo{id: "proj-db"}

	if got := mustGet(t, e, repo); got != "proj-db" {
		t.Errorf("Get = %q, want %q", got, "proj-db")
	}

	after := e.read(t)
	if got := string(topLevel(t, after)["currentProjectId"]); got != `"proj-db"` {
		t.Errorf("currentProjectId = %s, want \"proj-db\"", got)
	}
	assertValuesUnchanged(t, before, after, unknownKeyNames)
}
