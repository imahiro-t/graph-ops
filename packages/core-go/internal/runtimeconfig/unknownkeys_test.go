package runtimeconfig

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Tests for DFLT-00145: saving the home config keeps the top-level keys this
// version of FileConfig does not know, so another graph-engine version's
// settings (v0.10.0's autopilotSettings, say) survive a save made by this one.

// unknownKeysJSON is the set of unknown keys the tests below write into a
// config file: a nested object shaped like autopilotSettings, plus values that
// any decode-and-re-encode would change -- an integer past 2^53, a null, a 1.0
// that must not become 1, raw "<" ">" "&" that must not become \u003c etc.,
// and \u003c \u0026 escapes that must not become raw characters (also one
// level down). It is a fragment of object members, to be spliced into a file
// next to known keys.
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

const autopilotOnlyJSON = `"autopilotSettings": {"proj-1": {"mode": "auto", "maxParallel": 2}}`

func topLevel(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the saved file is not a JSON object: %v\n%s", err, raw)
	}
	return doc
}

func compactBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatalf("json.Compact(%s): %v", raw, err)
	}
	return buf.Bytes()
}

func decodeUseNumber(t *testing.T, raw []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

// assertValuesUnchanged checks that every key in keys has the same value in
// after as in before: equal bytes once both are json.Compact-ed (so nothing
// was re-escaped or re-formatted beyond whitespace), and equal when decoded
// with UseNumber (so the meaning is the same too).
func assertValuesUnchanged(t *testing.T, before, after []byte, keys []string) {
	t.Helper()
	b, a := topLevel(t, before), topLevel(t, after)
	for _, k := range keys {
		bv, ok := b[k]
		if !ok {
			t.Fatalf("test setup: %q is not in the original file", k)
		}
		av, ok := a[k]
		if !ok {
			t.Errorf("%q was dropped from the saved file:\n%s", k, after)
			continue
		}
		if cb, ca := compactBytes(t, bv), compactBytes(t, av); !bytes.Equal(cb, ca) {
			t.Errorf("%q changed: before %s, after %s", k, cb, ca)
		}
		if db, da := decodeUseNumber(t, bv), decodeUseNumber(t, av); !reflect.DeepEqual(db, da) {
			t.Errorf("%q changed in meaning: before %#v, after %#v", k, db, da)
		}
	}
}

// topLevelKeyOrder returns the top-level keys of raw in the order they appear.
func topLevelKeyOrder(t *testing.T, raw []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not an object: %v %v", tok, err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("dec.Token: %v", err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("dec.Decode: %v", err)
		}
	}
	return keys
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile: %v", err)
	}
	return raw
}

func update(t *testing.T, home string, fn func(cfg *FileConfig)) []byte {
	t.Helper()
	_, path, err := UpdateHome(home, func(cfg *FileConfig) error {
		fn(cfg)
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateHome: %v", err)
	}
	return readFile(t, path)
}

// --- 1. unknown keys are kept ----------------------------------------------

func TestUpdateHome_KeepsUnknownKeysUnchanged(t *testing.T) {
	home := t.TempDir()
	before := []byte(`{"dbBackend": "sqlite", ` + unknownKeysJSON + `}`)
	writeHomeConfig(t, home, string(before))

	after := update(t, home, func(cfg *FileConfig) { cfg.DBBackend = "mysql" })

	if got := string(topLevel(t, after)["dbBackend"]); got != `"mysql"` {
		t.Errorf("dbBackend = %s, want \"mysql\"", got)
	}
	assertValuesUnchanged(t, before, after, unknownKeyNames)

	// The same facts, spelled out on the saved bytes.
	for _, want := range []string{
		`9007199254740993`,                 // not rounded through float64
		`1.0`,                              // not normalized to 1
		`"https://example.com/?a=1&b=<2>"`, // raw characters not re-escaped
		`"a \u003c b \u0026 c"`,            // escapes kept as written
		`"\u003ctag\u003e"`,                // ... one level down too
		`"x && y > z"`,
	} {
		if !bytes.Contains(after, []byte(want)) {
			t.Errorf("saved file does not contain %s:\n%s", want, after)
		}
	}
}

func TestUpdateHome_UnknownKeysSurviveRepeatedSaves(t *testing.T) {
	home := t.TempDir()
	before := []byte(`{"dbBackend": "sqlite", ` + unknownKeysJSON + `}`)
	writeHomeConfig(t, home, string(before))

	update(t, home, func(cfg *FileConfig) { cfg.DBBackend = "mysql" })
	after := update(t, home, func(cfg *FileConfig) { cfg.MyName = "alice" })

	assertValuesUnchanged(t, before, after, unknownKeyNames)
	doc := topLevel(t, after)
	if string(doc["dbBackend"]) != `"mysql"` || string(doc["myName"]) != `"alice"` {
		t.Errorf("dbBackend = %s, myName = %s; want \"mysql\" and \"alice\"", doc["dbBackend"], doc["myName"])
	}
}

// UpdateHome's signature and FileConfig's shape are what every caller already
// uses; the unknown keys are handled without either changing.
var _ func(string, func(*FileConfig) error) (FileConfig, string, error) = UpdateHome

func TestUpdateHome_CallersDoNotSeeUnknownKeys(t *testing.T) {
	home := t.TempDir()
	before := []byte(`{` + autopilotOnlyJSON + `}`)
	writeHomeConfig(t, home, string(before))

	after := update(t, home, func(cfg *FileConfig) { cfg.MyName = "bob" })

	if got := string(topLevel(t, after)["myName"]); got != `"bob"` {
		t.Errorf("myName = %s, want \"bob\"", got)
	}
	assertValuesUnchanged(t, before, after, []string{"autopilotSettings"})
}

// --- 2. known keys behave as before ----------------------------------------

func TestUpdateHome_EmptiedKnownKeyIsRemoved(t *testing.T) {
	home := t.TempDir()
	before := []byte(`{"myName": "alice", ` + autopilotOnlyJSON + `}`)
	writeHomeConfig(t, home, string(before))

	after := update(t, home, func(cfg *FileConfig) { cfg.MyName = "" })

	if v, ok := topLevel(t, after)["myName"]; ok {
		t.Errorf("myName = %s, want the key gone", v)
	}
	assertValuesUnchanged(t, before, after, []string{"autopilotSettings"})
}

func TestUpdateHome_CurrentProjectIDNilAndEmptyStayDistinct(t *testing.T) {
	cases := []struct {
		name    string
		set     *string
		wantRaw string // "" means the key must be absent
	}{
		{"nil", nil, ""},
		{"empty", strPtr(""), `""`},
		{"id", strPtr("proj-1"), `"proj-1"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			before := []byte(`{"currentProjectId": "proj-old", ` + autopilotOnlyJSON + `}`)
			writeHomeConfig(t, home, string(before))

			after := update(t, home, func(cfg *FileConfig) { cfg.CurrentProjectID = tc.set })

			v, ok := topLevel(t, after)["currentProjectId"]
			switch {
			case tc.wantRaw == "" && ok:
				t.Errorf("currentProjectId = %s, want the key absent", v)
			case tc.wantRaw != "" && string(v) != tc.wantRaw:
				t.Errorf("currentProjectId = %s (present %v), want %s", v, ok, tc.wantRaw)
			}
			assertValuesUnchanged(t, before, after, []string{"autopilotSettings"})
		})
	}
}

func TestUpdateHome_KnownKeysAreEscapedAsBefore(t *testing.T) {
	home := t.TempDir()
	writeHomeConfig(t, home, `{`+autopilotOnlyJSON+`}`)
	const cmd = "open -a Terminal <dir> && echo ok"

	after := update(t, home, func(cfg *FileConfig) { cfg.TerminalCommand = cmd })

	want, err := json.MarshalIndent(FileConfig{TerminalCommand: cmd}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if got, w := topLevel(t, after)["terminalCommand"], topLevel(t, want)["terminalCommand"]; !bytes.Equal(got, w) {
		t.Errorf("terminalCommand = %s, want %s (json.MarshalIndent's escaping)", got, w)
	}
	cfg, err := LoadHomeConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TerminalCommand != cmd {
		t.Errorf("read back TerminalCommand = %q, want %q", cfg.TerminalCommand, cmd)
	}
}

// --- 3. keys that differ from a known key only in case --------------------

func TestUpdateHome_CaseVariantOfKnownKeyIsRewrittenOnce(t *testing.T) {
	home := t.TempDir()
	writeHomeConfig(t, home, `{"DBBackend": "mysql"}`)

	cfg, err := LoadHomeConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBBackend != "mysql" {
		t.Fatalf("DBBackend = %q, want %q", cfg.DBBackend, "mysql")
	}

	after := update(t, home, func(cfg *FileConfig) { cfg.DBBackend = "sqlite" })

	if keys := topLevelKeyOrder(t, after); !reflect.DeepEqual(keys, []string{"dbBackend"}) {
		t.Errorf("keys = %v, want only dbBackend\n%s", keys, after)
	}
	if cfg, _ := LoadHomeConfig(home); cfg.DBBackend != "sqlite" {
		t.Errorf("read back DBBackend = %q, want %q", cfg.DBBackend, "sqlite")
	}
}

func TestUpdateHome_CorrectAndCaseVariantKeysCollapseToOne(t *testing.T) {
	home := t.TempDir()
	writeHomeConfig(t, home, `{"dbBackend": "sqlite", "DBBackend": "mysql"}`)

	cfg, err := LoadHomeConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBBackend != "mysql" {
		t.Fatalf("DBBackend = %q, want the later %q (encoding/json's rule)", cfg.DBBackend, "mysql")
	}

	after := update(t, home, func(cfg *FileConfig) { cfg.MyName = "alice" })

	keys := topLevelKeyOrder(t, after)
	if !reflect.DeepEqual(keys, []string{"dbBackend", "myName"}) {
		t.Errorf("keys = %v, want [dbBackend myName]\n%s", keys, after)
	}
	if got := string(topLevel(t, after)["dbBackend"]); got != `"mysql"` {
		t.Errorf("dbBackend = %s, want \"mysql\"", got)
	}
	if cfg, _ := LoadHomeConfig(home); cfg.DBBackend != "mysql" {
		t.Errorf("read back DBBackend = %q, want %q", cfg.DBBackend, "mysql")
	}
}

// --- 4. the shape of the output --------------------------------------------

func TestUpdateHome_NoUnknownKeysMeansTheSameBytesAsBefore(t *testing.T) {
	home := t.TempDir()
	writeHomeConfig(t, home, `{"dbBackend": "sqlite", "dbPath": "/tmp/g/graph.db", "port": 8080}`)

	after := update(t, home, func(cfg *FileConfig) { cfg.Port = 9090 })

	want, err := json.MarshalIndent(FileConfig{DBBackend: "sqlite", DBPath: "/tmp/g/graph.db", Port: 9090}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, want) {
		t.Errorf("saved file differs from json.MarshalIndent:\n got: %s\nwant: %s", after, want)
	}
}

func TestUpdateHome_UnknownKeysFollowKnownKeysInNameOrder(t *testing.T) {
	home := t.TempDir()
	writeHomeConfig(t, home, `{"zeta": 1, "alpha": 2, "dbBackend": "sqlite", "dbPath": "/tmp/g/graph.db"}`)

	after := update(t, home, func(cfg *FileConfig) { cfg.DBBackend = "mysql" })

	if keys := topLevelKeyOrder(t, after); !reflect.DeepEqual(keys, []string{"dbBackend", "dbPath", "alpha", "zeta"}) {
		t.Errorf("keys = %v, want [dbBackend dbPath alpha zeta]", keys)
	}
	doc := topLevel(t, after)
	for k, want := range map[string]string{"dbBackend": `"mysql"`, "dbPath": `"/tmp/g/graph.db"`, "alpha": `2`, "zeta": `1`} {
		if got := string(doc[k]); got != want {
			t.Errorf("%s = %s, want %s", k, got, want)
		}
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, after, "", "  "); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(indented.Bytes(), after) {
		t.Errorf("saved file is not 2-space indented:\n%s", after)
	}
}

func TestUpdateHome_MissingFileGetsOnlyKnownKeys(t *testing.T) {
	home := t.TempDir()

	after := update(t, home, func(cfg *FileConfig) { cfg.DBBackend = "sqlite" })

	want, _ := json.MarshalIndent(FileConfig{DBBackend: "sqlite"}, "", "  ")
	if !bytes.Equal(after, want) {
		t.Errorf("saved file = %s, want %s", after, want)
	}
}

func TestUpdateHome_NullFileIsAnEmptyObject(t *testing.T) {
	home := t.TempDir()
	writeHomeConfig(t, home, `null`)

	after := update(t, home, func(cfg *FileConfig) { cfg.DBBackend = "sqlite" })

	if keys := topLevelKeyOrder(t, after); !reflect.DeepEqual(keys, []string{"dbBackend"}) {
		t.Errorf("keys = %v, want [dbBackend]\n%s", keys, after)
	}
}

// --- 5. nothing is written when nothing should be --------------------------

func TestUpdateHome_FnErrorLeavesFileWithUnknownKeysAlone(t *testing.T) {
	home := t.TempDir()
	original := `{"dbBackend": "sqlite", ` + autopilotOnlyJSON + `}`
	path := writeHomeConfig(t, home, original)
	sentinel := errors.New("stop")

	_, _, err := UpdateHome(home, func(cfg *FileConfig) error {
		cfg.DBBackend = "mysql"
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the sentinel", err)
	}
	if got := readFile(t, path); string(got) != original {
		t.Errorf("file was rewritten:\n%s", got)
	}
}

func TestUpdateHome_UnreadableFileIsNotWritten(t *testing.T) {
	for name, original := range map[string]string{
		"broken syntax":   `{"dbBackend": "sqlite", ` + autopilotOnlyJSON,
		"top-level array": `[{"dbBackend": "sqlite"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			path := writeHomeConfig(t, home, original)
			_, _, err := UpdateHome(home, func(cfg *FileConfig) error {
				cfg.DBBackend = "sqlite"
				return nil
			})
			var readErr *HomeConfigReadError
			if !errors.As(err, &readErr) {
				t.Fatalf("err = %v, want a *HomeConfigReadError", err)
			}
			if got := readFile(t, path); string(got) != original {
				t.Errorf("file was rewritten:\n%s", got)
			}
		})
	}
}

// --- 6. the known-key list and the field-type guard ------------------------

// fillNonZero returns a value of struct type typ with every exported field
// set to something non-zero, so json.Marshal emits every key omitempty
// would otherwise hide.
func fillNonZero(t *testing.T, typ reflect.Type) reflect.Value {
	t.Helper()
	v := reflect.New(typ).Elem()
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).IsExported() {
			v.Field(i).Set(nonZeroValue(t, typ.Field(i).Type))
		}
	}
	return v
}

func nonZeroValue(t *testing.T, typ reflect.Type) reflect.Value {
	t.Helper()
	v := reflect.New(typ).Elem()
	switch typ.Kind() {
	case reflect.String:
		v.SetString("x")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Pointer:
		p := reflect.New(typ.Elem())
		p.Elem().Set(nonZeroValue(t, typ.Elem()))
		return p
	case reflect.Map:
		m := reflect.MakeMap(typ)
		m.SetMapIndex(nonZeroValue(t, typ.Key()), nonZeroValue(t, typ.Elem()))
		return m
	case reflect.Slice:
		s := reflect.MakeSlice(typ, 1, 1)
		s.Index(0).Set(nonZeroValue(t, typ.Elem()))
		return s
	case reflect.Interface:
		v.Set(reflect.ValueOf("x"))
	default:
		t.Fatalf("nonZeroValue: add a case for %v", typ)
	}
	return v
}

func marshaledKeySet(t *testing.T, v reflect.Value) map[string]bool {
	t.Helper()
	raw, err := json.Marshal(v.Interface())
	if err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	for k := range topLevel(t, raw) {
		set[k] = true
	}
	return set
}

func keySet(keys []string) map[string]bool {
	set := map[string]bool{}
	for _, k := range keys {
		set[k] = true
	}
	return set
}

func TestFileConfigKeys_MatchWhatMarshalWrites(t *testing.T) {
	got := keySet(fileConfigKeys)
	if len(got) != len(fileConfigKeys) {
		t.Errorf("fileConfigKeys has duplicates: %v", fileConfigKeys)
	}
	want := marshaledKeySet(t, fillNonZero(t, reflect.TypeOf(FileConfig{})))
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fileConfigKeys = %v, want the keys json.Marshal writes: %v", got, want)
	}
}

// The struct types below exercise jsonKeyNames' rules one at a time.
// encoding/json ignores an unexported field, and so must jsonKeyNames. The
// field has no json tag only because go vet rejects a tag on an unexported
// field; jsonKeyNames skips it before it ever looks at the tag.
type keysUnexported struct {
	Keep   string `json:"keep"`
	hidden string
}

type keysSkip struct {
	Keep string `json:"keep"`
	Skip string `json:"-"`
}

type keysDash struct {
	Keep string `json:"keep"`
	Dash string `json:"-,"`
}

type keysNoTag struct {
	Keep  string `json:"keep"`
	NoTag string
}

type keysOptionsOnly struct {
	Keep string `json:"keep"`
	Opt  string `json:",omitempty"`
}

func TestJSONKeyNames_FollowEncodingJSONRules(t *testing.T) {
	_ = keysUnexported{}.hidden
	cases := []struct {
		typ  reflect.Type
		want []string
	}{
		{reflect.TypeOf(keysUnexported{}), []string{"keep"}},
		{reflect.TypeOf(keysSkip{}), []string{"keep"}},
		{reflect.TypeOf(keysDash{}), []string{"keep", "-"}},
		{reflect.TypeOf(keysNoTag{}), []string{"keep", "NoTag"}},
		{reflect.TypeOf(keysOptionsOnly{}), []string{"keep", "Opt"}},
	}
	for _, tc := range cases {
		t.Run(tc.typ.Name(), func(t *testing.T) {
			got := jsonKeyNames(tc.typ)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("jsonKeyNames = %v, want %v", got, tc.want)
			}
			if marshaled := marshaledKeySet(t, fillNonZero(t, tc.typ)); !reflect.DeepEqual(keySet(got), marshaled) {
				t.Errorf("jsonKeyNames = %v, but json.Marshal writes %v", got, marshaled)
			}
		})
	}
}

// fileConfigRule is the rule the guard's failure message states -- the same
// rule written above FileConfig.
const fileConfigRule = "Rule (DFLT-00145): a new setting must be a new top-level key, or live inside a key whose type keeps unknown sub-keys (such as map[string]any). " +
	"Do not add a field whose value is a struct (directly, or via a pointer, map value, slice or array element), a type with its own JSON/text conversion, " +
	"an embedded field, or a field without an explicit json key name: another graph-engine version saving config.json would otherwise lose the sub-keys this version cannot hold."

var (
	jsonMarshalerType   = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	jsonUnmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
	textMarshalerType   = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)

// hasCustomConversion reports whether typ or *typ implements one of the
// interfaces encoding/json hands a value to instead of walking it, which
// could therefore drop sub-keys it does not know.
func hasCustomConversion(typ reflect.Type) bool {
	for _, c := range []reflect.Type{typ, reflect.PointerTo(typ)} {
		if c.Implements(jsonMarshalerType) || c.Implements(jsonUnmarshalerType) ||
			c.Implements(textMarshalerType) || c.Implements(textUnmarshalerType) {
			return true
		}
	}
	return false
}

// typeProblem walks typ through pointers, map values and slice/array
// elements, and names the first type on the way that could drop an unknown
// sub-key: a struct, or a type with its own conversion. "" means none.
func typeProblem(typ reflect.Type) string {
	for {
		if hasCustomConversion(typ) {
			return fmt.Sprintf("%v has its own JSON/text conversion", typ)
		}
		switch typ.Kind() {
		case reflect.Struct:
			return fmt.Sprintf("%v is a struct", typ)
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			typ = typ.Elem()
		default:
			return ""
		}
	}
}

// fieldProblems applies the guard to every field of struct type typ and
// returns one message per offending field, each ending with the rule.
func fieldProblems(typ reflect.Type) []string {
	var problems []string
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		var why string
		switch {
		case f.Anonymous:
			why = "is an embedded field"
		case !f.IsExported():
			continue // encoding/json never reads or writes it
		default:
			tag, hasTag := f.Tag.Lookup("json")
			name, _, _ := strings.Cut(tag, ",")
			switch {
			case tag == "-":
				continue // never read or written, so kept as an unknown key
			case !hasTag:
				why = "has no json tag"
			case name == "":
				why = "has a json tag with an empty key name"
			default:
				if p := typeProblem(f.Type); p != "" {
					why = "holds a type that can drop unknown sub-keys: " + p
				}
			}
		}
		if why != "" {
			problems = append(problems, fmt.Sprintf("field %s.%s %s. %s", typ.Name(), f.Name, why, fileConfigRule))
		}
	}
	return problems
}

func TestFileConfigFieldsKeepUnknownSubkeys(t *testing.T) {
	for _, p := range fieldProblems(reflect.TypeOf(FileConfig{})) {
		t.Error(p)
	}
}

type marshalerMap map[string]any

func (marshalerMap) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }

type unmarshalerMap map[string]any

func (*unmarshalerMap) UnmarshalJSON([]byte) error { return nil }

type textString string

func (textString) MarshalText() ([]byte, error) { return nil, nil }

// plainSettings stands for DFLT-00142's autopilot.LocalSettings: a named map
// with no conversion of its own, which keeps every sub-key.
type plainSettings map[string]any

type inner struct {
	A string `json:"a"`
}

type Embedded struct {
	A string `json:"a"`
}

func TestFieldProblems_FlagsFieldsThatCanDropSubkeys(t *testing.T) {
	cases := []struct {
		name string
		typ  reflect.Type
	}{
		{"struct value", reflect.TypeOf(struct {
			F inner `json:"f"`
		}{})},
		{"pointer to struct", reflect.TypeOf(struct {
			F *inner `json:"f"`
		}{})},
		{"map of structs", reflect.TypeOf(struct {
			F map[string]inner `json:"f"`
		}{})},
		{"slice of structs", reflect.TypeOf(struct {
			F []inner `json:"f"`
		}{})},
		{"array of structs", reflect.TypeOf(struct {
			F [2]inner `json:"f"`
		}{})},
		{"time.Time", reflect.TypeOf(struct {
			F time.Time `json:"f"`
		}{})},
		{"embedded field", reflect.TypeOf(struct {
			Embedded
		}{})},
		{"no json tag", reflect.TypeOf(struct {
			F string
		}{})},
		{"empty tag name", reflect.TypeOf(struct {
			F string `json:",omitempty"`
		}{})},
		{"value-receiver MarshalJSON map", reflect.TypeOf(struct {
			F marshalerMap `json:"f"`
		}{})},
		{"pointer-receiver UnmarshalJSON map", reflect.TypeOf(struct {
			F unmarshalerMap `json:"f"`
		}{})},
		{"MarshalText string", reflect.TypeOf(struct {
			F textString `json:"f"`
		}{})},
		{"map of MarshalJSON maps", reflect.TypeOf(struct {
			F map[string]marshalerMap `json:"f"`
		}{})},
		{"slice of UnmarshalJSON maps", reflect.TypeOf(struct {
			F []unmarshalerMap `json:"f"`
		}{})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems := fieldProblems(tc.typ)
			if len(problems) != 1 {
				t.Fatalf("problems = %q, want exactly one", problems)
			}
			if !strings.Contains(problems[0], fileConfigRule) {
				t.Errorf("message %q does not state the rule", problems[0])
			}
		})
	}
}

func TestHasCustomConversion_ChecksTypeAndPointer(t *testing.T) {
	for _, tc := range []struct {
		typ  reflect.Type
		want bool
	}{
		{reflect.TypeOf(marshalerMap{}), true},   // value receiver
		{reflect.TypeOf(unmarshalerMap{}), true}, // only *T implements it
		{reflect.TypeOf(textString("")), true},   // encoding.TextMarshaler
		{reflect.TypeOf(plainSettings{}), false}, // no conversion
		{reflect.TypeOf(map[string]any{}), false},
	} {
		if got := hasCustomConversion(tc.typ); got != tc.want {
			t.Errorf("hasCustomConversion(%v) = %v, want %v", tc.typ, got, tc.want)
		}
	}
}

func TestFieldProblems_AcceptsTypesThatKeepSubkeys(t *testing.T) {
	cases := []struct {
		name string
		typ  reflect.Type
	}{
		{"string", reflect.TypeOf(struct {
			F string `json:"f"`
		}{})},
		{"int", reflect.TypeOf(struct {
			F int `json:"f"`
		}{})},
		{"bool", reflect.TypeOf(struct {
			F bool `json:"f"`
		}{})},
		{"*string", reflect.TypeOf(struct {
			F *string `json:"f"`
		}{})},
		{"any", reflect.TypeOf(struct {
			F any `json:"f"`
		}{})},
		{"[]string", reflect.TypeOf(struct {
			F []string `json:"f"`
		}{})},
		{"map[string]string", reflect.TypeOf(struct {
			F map[string]string `json:"f"`
		}{})},
		{"map[string]any", reflect.TypeOf(struct {
			F map[string]any `json:"f"`
		}{})},
		{"map of a named map without conversion", reflect.TypeOf(struct {
			F map[string]plainSettings `json:"f"`
		}{})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if problems := fieldProblems(tc.typ); len(problems) != 0 {
				t.Errorf("problems = %q, want none", problems)
			}
		})
	}
}
