package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// writeTierFile writes content to <dir>/<file>, creating dir.
func writeTierFile(t *testing.T, dir, file, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, file)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// tierDoc renders a minimal tier Document, with a top-level max_iterations
// line when v is non-empty.
func tierDoc(v string) string {
	s := "version: 1\n"
	if v != "" {
		s += "max_iterations: " + v + "\n"
	}
	return s
}

func TestValidateMaxIterations(t *testing.T) {
	for _, v := range []int{3, 4, 5} {
		if err := ValidateMaxIterations(v); err != nil {
			t.Errorf("ValidateMaxIterations(%d) = %v, want nil", v, err)
		}
	}
	for _, v := range []int{-1, 0, 1, 2, 6, 10} {
		if err := ValidateMaxIterations(v); err == nil {
			t.Errorf("ValidateMaxIterations(%d) = nil, want an error", v)
		}
	}
}

// Completion criterion 1: one workflow-wide value, team tier over user tier
// over the plugin default, 3 when nobody sets it.
func TestLoadWithRoots_MaxIterationsPrecedence(t *testing.T) {
	cases := []struct {
		user, team string
		want       int
	}{
		{"", "", 3},
		{"4", "", 4},
		{"", "5", 5},
		{"4", "5", 5},
		{"5", "3", 3},
	}
	for _, tc := range cases {
		t.Run("user="+tc.user+"/team="+tc.team, func(t *testing.T) {
			userDir, teamDir := t.TempDir(), t.TempDir()
			writeTierFile(t, userDir, userConfigFile, tierDoc(tc.user))
			writeTierFile(t, teamDir, teamConfigFile, tierDoc(tc.team))
			cat, err := LoadWithRoots(userDir, teamDir, "")
			if err != nil {
				t.Fatalf("LoadWithRoots: %v", err)
			}
			if cat.MaxIterations != tc.want {
				t.Errorf("MaxIterations = %d, want %d", cat.MaxIterations, tc.want)
			}
		})
	}
}

func TestMerge_MaxIterationsDefaultsTo3WithNoDocuments(t *testing.T) {
	if got := Merge().MaxIterations; got != DefaultMaxIterations {
		t.Errorf("Merge().MaxIterations = %d, want %d", got, DefaultMaxIterations)
	}
	if got := Merge(Document{}, Document{}).MaxIterations; got != 3 {
		t.Errorf("MaxIterations with no layer setting it = %d, want 3", got)
	}
}

// A value outside 3/4/5 is a load error naming the file and the value -- a
// typo must not silently fall back to the default.
func TestLoadWithRoots_InvalidMaxIterationsIsAnError(t *testing.T) {
	cases := []struct {
		tier  string
		value int
	}{
		{"user", 0}, {"user", 2}, {"user", 6}, {"team", 1}, {"team", 10},
	}
	for _, tc := range cases {
		t.Run(tc.tier+"="+strconv.Itoa(tc.value), func(t *testing.T) {
			userDir, teamDir := t.TempDir(), t.TempDir()
			var badPath string
			if tc.tier == "user" {
				badPath = writeTierFile(t, userDir, userConfigFile, tierDoc(strconv.Itoa(tc.value)))
			} else {
				badPath = writeTierFile(t, teamDir, teamConfigFile, tierDoc(strconv.Itoa(tc.value)))
			}
			cat, err := LoadWithRoots(userDir, teamDir, "")
			if err == nil {
				t.Fatalf("expected an error, got catalog with MaxIterations=%d", cat.MaxIterations)
			}
			if !strings.Contains(err.Error(), badPath) {
				t.Errorf("error %q does not name the file %s", err, badPath)
			}
			if !strings.Contains(err.Error(), strconv.Itoa(tc.value)) {
				t.Errorf("error %q does not name the value %d", err, tc.value)
			}
		})
	}
}

// Completion criterion 4: a per-gate max_iterations is ignored, with a
// warning naming the gate -- in whichever tier it sits.
func TestLoadWithRoots_LegacyPerGateMaxIterationsIsIgnoredWithAWarning(t *testing.T) {
	for _, tier := range []string{"user", "team"} {
		t.Run(tier, func(t *testing.T) {
			userDir, teamDir := t.TempDir(), t.TempDir()
			content := "version: 1\nreview_gates:\n  code_review:\n    max_iterations: 7\n"
			if tier == "user" {
				writeTierFile(t, userDir, userConfigFile, content)
			} else {
				writeTierFile(t, teamDir, teamConfigFile, content)
			}
			cat, err := LoadWithRoots(userDir, teamDir, "")
			if err != nil {
				t.Fatalf("LoadWithRoots: %v", err)
			}
			if cat.MaxIterations != 3 {
				t.Errorf("MaxIterations = %d, want 3 (the per-gate value must not leak in)", cat.MaxIterations)
			}
			if len(cat.Warnings) != 1 {
				t.Fatalf("Warnings = %+v, want exactly one", cat.Warnings)
			}
			if got := cat.Warnings[0]; got.Code != WarnLegacyGateMaxIterations || got.GateID != "code_review" {
				t.Errorf("warning = %+v, want code %s for code_review", got, WarnLegacyGateMaxIterations)
			}
			w := cat.Warnings[0].Message()
			for _, want := range []string{`"code_review"`, "max_iterations", "ignored", "top-level max_iterations (3/4/5)"} {
				if !strings.Contains(w, want) {
					t.Errorf("warning %q does not contain %q", w, want)
				}
			}
			if cat.ReviewGates["code_review"].LegacyMaxIterations != nil {
				t.Errorf("merged code_review still carries the legacy value")
			}
		})
	}
}

func TestLoadWithRoots_NoLegacyValueMeansNoWarnings(t *testing.T) {
	userDir, teamDir := t.TempDir(), t.TempDir()
	writeTierFile(t, userDir, userConfigFile, "version: 1\nmax_iterations: 4\nreview_gates:\n  code_review:\n    criteria: x\n")
	cat, err := LoadWithRoots(userDir, teamDir, "")
	if err != nil {
		t.Fatalf("LoadWithRoots: %v", err)
	}
	if len(cat.Warnings) != 0 {
		t.Errorf("Warnings = %+v, want none", cat.Warnings)
	}
}

// The legacy field is JSON-invisible, so the settings API neither returns
// nor accepts it.
func TestReviewGateDef_LegacyMaxIterationsIsNotJSON(t *testing.T) {
	seven := 7
	rawBytes, err := json.Marshal(ReviewGateDef{Name: "x", LegacyMaxIterations: &seven})
	raw := string(rawBytes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "max_iterations") {
		t.Errorf("JSON %s must not contain max_iterations", raw)
	}
}

// Completion criterion 4: saving never writes the per-gate value back.
func TestSaveDocumentAt_DropsLegacyPerGateMaxIterations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	four, nine := 4, 9
	doc := Document{
		Version:       1,
		MaxIterations: &four,
		ReviewGates: map[string]ReviewGateDef{
			"code_review": {Criteria: "c", LegacyMaxIterations: &nine},
		},
	}
	if err := SaveDocumentAt(path, doc); err != nil {
		t.Fatalf("SaveDocumentAt: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := yaml.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	if generic["max_iterations"] != 4 {
		t.Errorf("top-level max_iterations = %v, want 4 in:\n%s", generic["max_iterations"], raw)
	}
	gate := generic["review_gates"].(map[string]any)["code_review"].(map[string]any)
	if _, ok := gate["max_iterations"]; ok {
		t.Errorf("code_review.max_iterations was written:\n%s", raw)
	}
	// The caller's document is not mutated.
	if doc.ReviewGates["code_review"].LegacyMaxIterations == nil {
		t.Errorf("SaveDocumentAt mutated the caller's document")
	}
}

// The plugin defaults carry the limit at the top level only, and the Go
// mirror matches the plugin's canonical file.
func TestDefaults_MaxIterationsIsTopLevelOnly(t *testing.T) {
	def, err := DefaultDocument()
	if err != nil {
		t.Fatal(err)
	}
	if def.MaxIterations == nil || *def.MaxIterations != 3 {
		t.Errorf("default top-level max_iterations = %v, want 3", def.MaxIterations)
	}
	for id, g := range def.ReviewGates {
		if g.LegacyMaxIterations != nil {
			t.Errorf("default review gate %q still sets max_iterations", id)
		}
	}
	if w := Merge(def).Warnings; len(w) != 0 {
		t.Errorf("defaults produce warnings: %+v", w)
	}
	plugin, err := os.ReadFile(filepath.Join("..", "..", "..", "plugin", "defaults", "workflow.yaml"))
	if err != nil {
		t.Skipf("plugin defaults not reachable from here: %v", err)
	}
	if !bytes.Equal(plugin, defaultYAML) {
		t.Errorf("internal/config/defaults/workflow.yaml is out of sync with packages/plugin/defaults/workflow.yaml; run npm run sync:defaults")
	}
}

// MaxIterationsWarnings flags only a set, out-of-range top-level value, and
// Message gives the CLI's English text for each warning code.
func TestMaxIterationsWarningsAndMessages(t *testing.T) {
	for _, v := range []int{3, 4, 5} {
		v := v
		if w := MaxIterationsWarnings(Document{MaxIterations: &v}); len(w) != 0 {
			t.Errorf("value %d: warnings = %+v, want none", v, w)
		}
	}
	if w := MaxIterationsWarnings(Document{}); len(w) != 0 {
		t.Errorf("unset: warnings = %+v, want none", w)
	}
	seven := 7
	w := MaxIterationsWarnings(Document{MaxIterations: &seven})
	if len(w) != 1 || w[0].Code != WarnMaxIterationsOutOfRange || w[0].Value == nil || *w[0].Value != 7 {
		t.Fatalf("value 7: warnings = %+v", w)
	}
	msgs := Messages(append(w, Warning{Code: WarnLegacyGateMaxIterations, GateID: "qa_review"}))
	if msgs[0] != "max_iterations must be 3, 4 or 5, got 7" {
		t.Errorf("out-of-range message = %q", msgs[0])
	}
	if !strings.Contains(msgs[1], `review gate "qa_review"`) || !strings.Contains(msgs[1], "ignored") {
		t.Errorf("legacy message = %q", msgs[1])
	}
}
