package autopilot

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00142 phase 2: per-project autopilot settings.

func writeTeamFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, TeamFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func itemFor(t *testing.T, eff Effective, key string) Item {
	t.Helper()
	for _, it := range eff.Items {
		if it.Key == key {
			return it
		}
	}
	t.Fatalf("no item for %q in %+v", key, eff.Items)
	return Item{}
}

func warningsFor(eff Effective, code, key string) []Warning {
	var out []Warning
	for _, w := range eff.Warnings {
		if w.Code == code && (key == "" || w.Key == key) {
			out = append(out, w)
		}
	}
	return out
}

func TestResolve_DefaultsWhenNothingIsSet(t *testing.T) {
	eff := ResolveProject("proj-A", nil, "")
	if eff.Settings != Defaults() {
		t.Fatalf("settings = %+v, want defaults %+v", eff.Settings, Defaults())
	}
	want := map[string]any{
		KeyMainReflection: "branch", KeyPermissionMode: "auto", KeyAutoApproveGates: true,
		KeyAutoCreateTickets: true, KeyMaxTickets: 20, KeyMaxDepth: 3, KeyOnFailure: "stop",
		KeyStallTimeoutMinutes: 60,
	}
	if len(eff.Items) != len(want) {
		t.Fatalf("items = %d, want %d", len(eff.Items), len(want))
	}
	for _, it := range eff.Items {
		if it.Value != want[it.Key] || it.Source != SourceDefault || it.Locked || it.Local != nil || it.Team != nil || it.Default != want[it.Key] {
			t.Errorf("item %+v, want default %v", it, want[it.Key])
		}
	}
	if len(eff.Warnings) != 0 || eff.TeamFile != "" {
		t.Errorf("warnings = %+v, team file = %q", eff.Warnings, eff.TeamFile)
	}
	if !reflect.DeepEqual(Keys(), []string{"mainReflection", "permissionMode", "autoApproveGates", "autoCreateTickets", "maxTickets", "maxDepth", "onFailure", "stallTimeoutMinutes"}) {
		t.Errorf("Keys() = %v", Keys())
	}
}

// Every combination of the three stored tiers being set or not, for one key.
func TestResolve_PrecedenceAllCombinations(t *testing.T) {
	for mask := 0; mask < 8; mask++ {
		teamProject, teamDefaults, local := mask&4 != 0, mask&2 != 0, mask&1 != 0
		var team *TeamFile
		if teamProject || teamDefaults {
			team = &TeamFile{Projects: map[string]map[string]any{}}
			if teamProject {
				team.Projects["proj-A"] = map[string]any{KeyMaxTickets: 10}
			}
			if teamDefaults {
				team.Defaults = map[string]any{KeyMaxTickets: 15}
			}
		}
		var loc LocalSettings
		if local {
			loc = LocalSettings{KeyMaxTickets: float64(5)} // as decoded from config.json
		}
		var wantValue int
		var wantSource Source
		switch {
		case teamProject:
			wantValue, wantSource = 10, SourceTeamProject
		case teamDefaults:
			wantValue, wantSource = 15, SourceTeamDefaults
		case local:
			wantValue, wantSource = 5, SourceLocal
		default:
			wantValue, wantSource = 20, SourceDefault
		}
		eff := Resolve("proj-A", loc, team)
		it := itemFor(t, eff, KeyMaxTickets)
		wantLocked := teamProject || teamDefaults
		if it.Value != wantValue || it.Source != wantSource || it.Locked != wantLocked || eff.Settings.MaxTickets != wantValue {
			t.Errorf("mask %03b: item %+v, settings.MaxTickets %d; want %d from %s locked=%v", mask, it, eff.Settings.MaxTickets, wantValue, wantSource, wantLocked)
		}
		if local && it.Local != 5 {
			t.Errorf("mask %03b: local = %v, want 5 even when overridden", mask, it.Local)
		}
		if wantLocked && it.Team != wantValue {
			t.Errorf("mask %03b: team = %v, want %d", mask, it.Team, wantValue)
		}
	}
}

func TestResolve_OtherProjectsTeamEntryDoesNotApply(t *testing.T) {
	team := &TeamFile{Projects: map[string]map[string]any{"proj-B": {KeyMaxTickets: 10}}}
	it := itemFor(t, Resolve("proj-A", nil, team), KeyMaxTickets)
	if it.Value != 20 || it.Locked {
		t.Fatalf("item = %+v, want the default, unlocked", it)
	}
}

func TestResolve_TeamBypassPermissionsIsIgnored(t *testing.T) {
	dir := writeTeamFile(t, "version: 1\ndefaults:\n  permissionMode: bypassPermissions\n")
	eff := ResolveProject("proj-A", LocalSettings{KeyPermissionMode: "acceptEdits"}, dir)
	it := itemFor(t, eff, KeyPermissionMode)
	if it.Value != "acceptEdits" || it.Locked || it.Source != SourceLocal || it.Team != nil {
		t.Fatalf("item = %+v, want the local acceptEdits, unlocked", it)
	}
	ws := warningsFor(eff, WarnTeamBypassIgnored, KeyPermissionMode)
	if len(ws) != 1 || ws[0].Source != SourceTeamDefaults || !strings.Contains(ws[0].Message, "bypassPermissions") {
		t.Fatalf("warnings = %+v", eff.Warnings)
	}
	if eff.TeamFile != filepath.Join(dir, TeamFileName) {
		t.Errorf("team file = %q", eff.TeamFile)
	}
}

// A bypassPermissions in projects.<id> falls to the next tier down, which
// here is the team defaults' valid value.
func TestResolve_TeamProjectBypassFallsToTeamDefaults(t *testing.T) {
	team := &TeamFile{
		Defaults: map[string]any{KeyPermissionMode: "dontAsk"},
		Projects: map[string]map[string]any{"proj-A": {KeyPermissionMode: "bypassPermissions"}},
	}
	eff := Resolve("proj-A", nil, team)
	it := itemFor(t, eff, KeyPermissionMode)
	if it.Value != "dontAsk" || it.Source != SourceTeamDefaults || !it.Locked {
		t.Fatalf("item = %+v", it)
	}
	if len(warningsFor(eff, WarnTeamBypassIgnored, KeyPermissionMode)) != 1 {
		t.Fatalf("warnings = %+v", eff.Warnings)
	}
}

func TestResolve_LocalBypassPermissionsIsAccepted(t *testing.T) {
	eff := Resolve("proj-A", LocalSettings{KeyPermissionMode: "bypassPermissions"}, nil)
	if eff.Settings.PermissionMode != PermissionModeBypassPermissions || len(eff.Warnings) != 0 {
		t.Fatalf("settings = %+v, warnings = %+v", eff.Settings, eff.Warnings)
	}
}

func TestResolve_InvalidTeamValuesAreIgnoredWithWarnings(t *testing.T) {
	for name, tc := range map[string]struct {
		yaml string
		keys []string
	}{
		"out of range and unknown enum": {"defaults:\n  maxTickets: 1000\n  onFailure: sometimes\n", []string{KeyMaxTickets, KeyOnFailure}},
		"wrong types":                   {"defaults:\n  autoApproveGates: \"yes\"\n  maxDepth: deep\n", []string{KeyAutoApproveGates, KeyMaxDepth}},
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeTeamFile(t, tc.yaml)
			local := LocalSettings{KeyMaxTickets: float64(7)}
			eff := ResolveProject("proj-A", local, dir)
			for _, k := range tc.keys {
				it := itemFor(t, eff, k)
				if it.Locked || it.Team != nil {
					t.Errorf("%s: item %+v must fall through, unlocked", k, it)
				}
				ws := warningsFor(eff, WarnInvalidValue, k)
				if len(ws) != 1 || ws[0].Source != SourceTeamDefaults || ws[0].Value == "" {
					t.Errorf("%s: warnings = %+v", k, eff.Warnings)
				}
			}
			if name == "out of range and unknown enum" {
				if eff.Settings.MaxTickets != 7 || eff.Settings.OnFailure != "stop" {
					t.Errorf("settings = %+v, want local maxTickets 7 and default onFailure", eff.Settings)
				}
			} else if eff.Settings.AutoApproveGates != true || eff.Settings.MaxDepth != 3 {
				t.Errorf("settings = %+v", eff.Settings)
			}
		})
	}
}

func TestResolve_UnknownKeysAndBadLocalValuesWarn(t *testing.T) {
	team := &TeamFile{Defaults: map[string]any{"maxTicket": 3}}
	local := LocalSettings{KeyMaxDepth: "2", "foo": true}
	eff := Resolve("proj-A", local, team)
	if len(warningsFor(eff, WarnUnknownKey, "maxTicket")) != 1 || len(warningsFor(eff, WarnUnknownKey, "foo")) != 1 {
		t.Errorf("warnings = %+v", eff.Warnings)
	}
	ws := warningsFor(eff, WarnInvalidValue, KeyMaxDepth)
	if len(ws) != 1 || ws[0].Source != SourceLocal || ws[0].Value != `"2"` {
		t.Errorf("warnings = %+v", eff.Warnings)
	}
	if it := itemFor(t, eff, KeyMaxDepth); it.Local != nil || it.Value != 3 {
		t.Errorf("item = %+v", it)
	}
}

func TestLoadTeamFile(t *testing.T) {
	if tf, ws := LoadTeamFile(""); tf != nil || ws != nil {
		t.Errorf("no team dir: %+v %+v", tf, ws)
	}
	if tf, ws := LoadTeamFile(t.TempDir()); tf != nil || ws != nil {
		t.Errorf("no file: %+v %+v", tf, ws)
	}
	dir := writeTeamFile(t, "version: 1\ndefaults:\n  mainReflection: pull_request\nprojects:\n  proj-64195d2b:\n    maxTickets: 10\n")
	tf, ws := LoadTeamFile(dir)
	if len(ws) != 0 || tf == nil || tf.Defaults[KeyMainReflection] != "pull_request" || tf.Projects["proj-64195d2b"][KeyMaxTickets] != 10 {
		t.Fatalf("tf = %+v, ws = %+v", tf, ws)
	}
	for name, content := range map[string]string{
		"syntax":  "defaults: [\n",
		"version": "version: 2\ndefaults:\n  maxTickets: 10\n",
		"shape":   "defaults: 3\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeTeamFile(t, content)
			tf, ws := LoadTeamFile(dir)
			if tf != nil || len(ws) != 1 || ws[0].Code != WarnTeamFileInvalid {
				t.Fatalf("tf = %+v, ws = %+v", tf, ws)
			}
			eff := ResolveProject("proj-A", nil, dir)
			if eff.Settings != Defaults() || len(warningsFor(eff, WarnTeamFileInvalid, "")) != 1 {
				t.Fatalf("eff = %+v", eff)
			}
		})
	}
}

func TestNormalizeValue(t *testing.T) {
	ok := []struct {
		key  string
		raw  any
		want any
	}{
		{KeyMainReflection, "merge", "merge"},
		{KeyPermissionMode, "dontAsk", "dontAsk"},
		{KeyAutoApproveGates, false, false},
		{KeyMaxTickets, json.Number("100"), 100},
		{KeyMaxTickets, 1, 1},
		{KeyMaxTickets, float64(30), 30},
		{KeyMaxDepth, json.Number("0"), 0},
		{KeyStallTimeoutMinutes, 1440, 1440},
		{KeyOnFailure, "continue", "continue"},
	}
	for _, c := range ok {
		got, err := NormalizeValue(c.key, c.raw)
		if err != nil || got != c.want {
			t.Errorf("NormalizeValue(%s, %#v) = %#v, %v; want %#v", c.key, c.raw, got, err, c.want)
		}
	}
	bad := []struct {
		key string
		raw any
	}{
		{KeyMainReflection, "squash"},
		{KeyPermissionMode, "default"},
		{KeyPermissionMode, "plan"},
		{KeyMaxTickets, json.Number("0")},
		{KeyMaxTickets, json.Number("101")},
		{KeyMaxTickets, "10"},
		{KeyMaxTickets, json.Number("2.5")},
		{KeyMaxTickets, float64(2.5)},
		{KeyMaxTickets, json.Number("1e1")},
		{KeyMaxDepth, json.Number("-1")},
		{KeyMaxDepth, json.Number("11")},
		{KeyOnFailure, "retry"},
		{KeyStallTimeoutMinutes, json.Number("14")},
		{KeyStallTimeoutMinutes, json.Number("1441")},
		{KeyAutoApproveGates, "yes"},
		{KeyAutoApproveGates, json.Number("1")},
		{KeyAutoCreateTickets, "false"},
		{"nope", "x"},
	}
	for _, c := range bad {
		if got, err := NormalizeValue(c.key, c.raw); err == nil {
			t.Errorf("NormalizeValue(%s, %#v) = %#v, want an error", c.key, c.raw, got)
		} else if c.key != "nope" && !strings.Contains(err.Error(), c.key) {
			t.Errorf("error %q does not name the key %s", err, c.key)
		}
	}
}

func apiErr(t *testing.T, err error, code domain.ErrorCode) *domain.APIError {
	t.Helper()
	var ae *domain.APIError
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
	return ae
}

func TestParsePatch(t *testing.T) {
	p, err := ParsePatch([]byte(`{"maxTickets": 30, "maxDepth": null, "autoApproveGates": false}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Set, map[string]any{KeyMaxTickets: 30, KeyAutoApproveGates: false}) || !reflect.DeepEqual(p.Unset, []string{KeyMaxDepth}) {
		t.Fatalf("patch = %+v", p)
	}

	for _, v := range []string{"10", "12", "null"} {
		_, err := ParsePatch([]byte(`{"maxTickets": `+v+`, "maxDepth": 5}`), map[string]bool{KeyMaxTickets: true})
		ae := apiErr(t, err, domain.ErrCodeAutopilotSettingLocked)
		if !reflect.DeepEqual(ae.Details["keys"], []string{KeyMaxTickets}) {
			t.Errorf("value %s: details = %+v", v, ae.Details)
		}
	}
	// Locked beats invalid.
	_, err = ParsePatch([]byte(`{"maxTickets": "x"}`), map[string]bool{KeyMaxTickets: true})
	apiErr(t, err, domain.ErrCodeAutopilotSettingLocked)

	_, err = ParsePatch([]byte(`{"maxTickets": "10", "bogus": 1, "onFailure": "stop"}`), nil)
	ae := apiErr(t, err, domain.ErrCodeValidation)
	if !reflect.DeepEqual(ae.Details["keys"], []string{"bogus", KeyMaxTickets}) {
		t.Errorf("details = %+v", ae.Details)
	}
	for _, body := range []string{`[]`, `"x"`, `null`, `{`, ``} {
		_, err := ParsePatch([]byte(body), nil)
		apiErr(t, err, domain.ErrCodeValidation)
	}
	if p, err := ParsePatch([]byte(`{}`), nil); err != nil || !p.Empty() {
		t.Errorf("empty object: %+v %v", p, err)
	}
}

func TestPatchApply(t *testing.T) {
	local := LocalSettings{KeyMaxDepth: float64(2), KeyMaxTickets: float64(7), "future": "x"}
	p := Patch{Set: map[string]any{KeyOnFailure: "continue"}, Unset: []string{KeyMaxDepth}}
	got := p.Apply(local)
	want := LocalSettings{KeyMaxTickets: float64(7), "future": "x", KeyOnFailure: "continue"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Apply = %+v, want %+v", got, want)
	}
	if _, still := local[KeyOnFailure]; still {
		t.Fatal("Apply modified its input")
	}
	if got := (Patch{Unset: []string{KeyMaxDepth}}).Apply(LocalSettings{KeyMaxDepth: 2}); got != nil {
		t.Fatalf("emptied settings = %+v, want nil", got)
	}
}
