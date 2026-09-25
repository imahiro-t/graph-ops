// Package autopilot holds the pieces of the autopilot commands
// (/graph-ops:autopilot-ticket, /graph-ops:autopilot-tree -- DFLT-00142) that
// live on the Go side, so the skills driving them stay thin.
//
// This file is the per-project settings: the keys and their built-in
// defaults, the one validation every source goes through (the HTTP PUT that
// saves local values, the team autopilot.yaml, and values already stored in
// the home config), and the resolution of the four tiers into the settings
// that are in effect for one project.
//
// Resolution is per key, highest first (plan decision D2):
//
//  1. the team file's projects.<projectId>
//  2. the team file's defaults
//  3. the local value (the home config's autopilotSettings.<projectId>)
//  4. the built-in default
//
// The team tier wins, as it does everywhere else (internal/config.Merge's
// "team beats user"), and a key the team sets is "locked": the Web UI shows
// it read-only and a PUT naming it is refused as a whole. The one exception is
// permissionMode: bypassPermissions, which is never accepted from the team
// tier (a shared directory must not be able to turn every member's child
// sessions into unconfirmed execution); such a value is ignored with a
// warning and does not lock the key.
//
// A team value that fails validation is likewise ignored with a warning and
// falls through to the next tier, so a typo in a shared file degrades to
// "not set" instead of breaking everybody's autopilot.
package autopilot

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/graph-ops/core-go/internal/domain"
)

// The setting keys, spelled exactly as they appear in JSON and YAML.
const (
	KeyMainReflection      = "mainReflection"
	KeyPermissionMode      = "permissionMode"
	KeyAutoApproveGates    = "autoApproveGates"
	KeyAutoCreateTickets   = "autoCreateTickets"
	KeyMaxTickets          = "maxTickets"
	KeyMaxDepth            = "maxDepth"
	KeyOnFailure           = "onFailure"
	KeyStallTimeoutMinutes = "stallTimeoutMinutes"
)

// Values of the enumerated keys.
const (
	MainReflectionBranch      = "branch"
	MainReflectionPullRequest = "pull_request"
	MainReflectionMerge       = "merge"

	PermissionModeAcceptEdits       = "acceptEdits"
	PermissionModeAuto              = "auto"
	PermissionModeDontAsk           = "dontAsk"
	PermissionModeBypassPermissions = "bypassPermissions"

	OnFailureStop     = "stop"
	OnFailureContinue = "continue"
)

// TeamFileName is the team settings file's name inside the team extensions
// directory (next to workflow.yaml).
const TeamFileName = "autopilot.yaml"

// Source says which tier an effective value came from.
type Source string

const (
	SourceDefault      Source = "default"
	SourceLocal        Source = "local"
	SourceTeamDefaults Source = "team_defaults"
	SourceTeamProject  Source = "team_project"
)

// Warning codes. The Web UI translates Code; Message is English and for the
// CLI and logs only.
const (
	// WarnTeamFileInvalid: autopilot.yaml exists but could not be read or
	// parsed, or declares an unsupported version. The whole file is ignored.
	WarnTeamFileInvalid = "AUTOPILOT_TEAM_FILE_INVALID"
	// WarnTeamBypassIgnored: the team file sets permissionMode to
	// bypassPermissions, which is never accepted from the team tier.
	WarnTeamBypassIgnored = "AUTOPILOT_TEAM_BYPASS_PERMISSIONS_IGNORED"
	// WarnInvalidValue: a stored value (team or local) fails validation and
	// is ignored.
	WarnInvalidValue = "AUTOPILOT_INVALID_VALUE"
	// WarnUnknownKey: a stored entry (team or local) names a key that is not
	// a setting, and is ignored.
	WarnUnknownKey = "AUTOPILOT_UNKNOWN_KEY"
)

// Settings is a complete, validated set of values -- what the autopilot
// commands act on.
type Settings struct {
	MainReflection      string `json:"mainReflection"`
	PermissionMode      string `json:"permissionMode"`
	AutoApproveGates    bool   `json:"autoApproveGates"`
	AutoCreateTickets   bool   `json:"autoCreateTickets"`
	MaxTickets          int    `json:"maxTickets"`
	MaxDepth            int    `json:"maxDepth"`
	OnFailure           string `json:"onFailure"`
	StallTimeoutMinutes int    `json:"stallTimeoutMinutes"`
}

// Defaults returns the built-in defaults (plan decision D3).
func Defaults() Settings {
	return Settings{
		MainReflection:      MainReflectionBranch,
		PermissionMode:      PermissionModeAuto,
		AutoApproveGates:    true,
		AutoCreateTickets:   true,
		MaxTickets:          20,
		MaxDepth:            3,
		OnFailure:           OnFailureStop,
		StallTimeoutMinutes: 60,
	}
}

// LocalSettings is one project's entry in the home config's
// autopilotSettings: key -> value, with unset keys absent. It is a plain map
// rather than a struct on purpose -- the home config is hand-editable, and a
// struct would make one mistyped value (say "maxTickets": "10") fail the
// decode of the whole file, taking every other setting down with it. Values
// are validated when resolved instead, and a bad one is ignored with a
// warning.
type LocalSettings map[string]any

// keySpec describes one setting: its key, its default, and how a raw value
// is validated and normalized.
type keySpec struct {
	key       string
	normalize func(raw any) (any, error)
}

// specs lists every setting in display order (plan decision D3's table).
var specs = []keySpec{
	{KeyMainReflection, enumValue(MainReflectionBranch, MainReflectionPullRequest, MainReflectionMerge)},
	// "default" and "plan" are deliberately missing: a child session in
	// either mode stops to ask, which an unattended run cannot answer.
	{KeyPermissionMode, enumValue(PermissionModeAcceptEdits, PermissionModeAuto, PermissionModeDontAsk, PermissionModeBypassPermissions)},
	{KeyAutoApproveGates, boolValue},
	{KeyAutoCreateTickets, boolValue},
	{KeyMaxTickets, intValue(1, 100)},
	{KeyMaxDepth, intValue(0, 10)},
	{KeyOnFailure, enumValue(OnFailureStop, OnFailureContinue)},
	{KeyStallTimeoutMinutes, intValue(15, 1440)},
}

// Keys returns every setting key, in display order.
func Keys() []string {
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = s.key
	}
	return out
}

// IsKey reports whether key names a setting.
func IsKey(key string) bool {
	_, ok := specFor(key)
	return ok
}

func specFor(key string) (keySpec, bool) {
	for _, s := range specs {
		if s.key == key {
			return s, true
		}
	}
	return keySpec{}, false
}

// NormalizeValue validates raw as a value for key and returns it in its
// canonical Go type (string, bool or int). It is the single validation every
// source goes through -- a PUT body (decoded with json.Decoder.UseNumber, so
// numbers arrive as json.Number), the team YAML, and values already stored
// in the home config (decoded as float64). The type must match exactly: the
// string "10" is not a number and the string "false" is not a boolean.
func NormalizeValue(key string, raw any) (any, error) {
	spec, ok := specFor(key)
	if !ok {
		return nil, fmt.Errorf("unknown autopilot setting %q", key)
	}
	v, err := spec.normalize(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return v, nil
}

func enumValue(allowed ...string) func(any) (any, error) {
	return func(raw any) (any, error) {
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("must be one of %s, got %s", strings.Join(allowed, " / "), describe(raw))
		}
		for _, a := range allowed {
			if s == a {
				return s, nil
			}
		}
		return nil, fmt.Errorf("must be one of %s, got %q", strings.Join(allowed, " / "), s)
	}
}

func boolValue(raw any) (any, error) {
	b, ok := raw.(bool)
	if !ok {
		return nil, fmt.Errorf("must be true or false, got %s", describe(raw))
	}
	return b, nil
}

func intValue(min, max int) func(any) (any, error) {
	return func(raw any) (any, error) {
		n, ok := asInt(raw)
		if !ok {
			return nil, fmt.Errorf("must be an integer from %d to %d, got %s", min, max, describe(raw))
		}
		if n < int64(min) || n > int64(max) {
			return nil, fmt.Errorf("must be an integer from %d to %d, got %d", min, max, n)
		}
		return int(n), nil
	}
}

// asInt accepts the integer representations the three decoders produce: a
// json.Number written as an integer literal (a PUT body), Go integers (YAML),
// and an integral float64 (json.Unmarshal into any, i.e. a value already in
// the home config). A json.Number such as "2.5" or "1e1" is not an integer
// literal and is refused, as is any non-integral float.
func asInt(raw any) (int64, bool) {
	switch v := raw.(type) {
	case json.Number:
		n, err := strconv.ParseInt(string(v), 10, 64)
		return n, err == nil
	case int:
		return int64(v), true
	case int64:
		return v, true
	case uint64:
		if v > math.MaxInt64 {
			return 0, false
		}
		return int64(v), true
	case float64:
		if v != math.Trunc(v) || math.IsInf(v, 0) || v > math.MaxInt64 || v < math.MinInt64 {
			return 0, false
		}
		return int64(v), true
	}
	return 0, false
}

// describe renders a raw value for an error or warning message, with its
// type visible (a string is quoted).
func describe(raw any) string {
	if raw == nil {
		return "null"
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return fmt.Sprintf("%v", raw)
	}
	return string(b)
}

// TeamFile is the parsed team settings file.
type TeamFile struct {
	// Path is the file it was read from.
	Path     string
	Defaults map[string]any
	Projects map[string]map[string]any
}

// teamFileDoc is autopilot.yaml's on-disk shape:
//
//	version: 1
//	defaults:
//	  mainReflection: pull_request
//	projects:
//	  proj-64195d2b:
//	    maxTickets: 10
type teamFileDoc struct {
	Version  *int                      `yaml:"version"`
	Defaults map[string]any            `yaml:"defaults"`
	Projects map[string]map[string]any `yaml:"projects"`
}

// TeamFilePath is where the team settings file lives for a team extensions
// directory, or "" when there is no team tier.
func TeamFilePath(teamDir string) string {
	if teamDir == "" {
		return ""
	}
	return filepath.Join(teamDir, TeamFileName)
}

// LoadTeamFile reads <teamDir>/autopilot.yaml. It returns nil, and no
// warning, when there is no team tier or no such file. A file that cannot be
// read or parsed, or that declares a version other than 1, is ignored as a
// whole with a WarnTeamFileInvalid warning -- it never fails the caller,
// since a broken shared file must not stop anyone's autopilot or settings
// screen.
func LoadTeamFile(teamDir string) (*TeamFile, []Warning) {
	path := TeamFilePath(teamDir)
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, []Warning{teamFileWarning(path, err.Error())}
	}
	var doc teamFileDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, []Warning{teamFileWarning(path, err.Error())}
	}
	if doc.Version != nil && *doc.Version != 1 {
		return nil, []Warning{teamFileWarning(path, fmt.Sprintf("unsupported version %d (only 1 is supported)", *doc.Version))}
	}
	return &TeamFile{Path: path, Defaults: doc.Defaults, Projects: doc.Projects}, nil
}

func teamFileWarning(path, reason string) Warning {
	return Warning{
		Code:    WarnTeamFileInvalid,
		Value:   path,
		Message: fmt.Sprintf("the team autopilot settings file %s is ignored: %s", path, reason),
	}
}

// Warning is one problem found while resolving settings. Source and Key say
// where (absent for a whole-file problem); Value is the offending value
// rendered as JSON (or the file path for WarnTeamFileInvalid).
type Warning struct {
	Code    string `json:"code"`
	Source  Source `json:"source,omitempty"`
	Key     string `json:"key,omitempty"`
	Value   string `json:"value,omitempty"`
	Message string `json:"message"`
}

// Item is one key's resolution, as shown by the settings screen and the CLI.
type Item struct {
	Key    string `json:"key"`
	Value  any    `json:"value"`
	Source Source `json:"source"`
	// Locked is true when a team tier supplies the value, so the local
	// value cannot be changed through the API.
	Locked bool `json:"locked"`
	// Local is the valid local value, or nil when there is none. It is
	// reported even while Locked (a value saved before the team fixed the
	// key stays on disk and takes effect again once the team stops setting
	// it).
	Local any `json:"local"`
	// Team is the team value in effect (projects.<id> before defaults), or
	// nil when the team tier does not set this key.
	Team    any `json:"team"`
	Default any `json:"default"`
}

// Effective is the outcome of resolving one project's settings.
type Effective struct {
	ProjectID string    `json:"project_id"`
	Settings  Settings  `json:"settings"`
	Items     []Item    `json:"items"`
	Warnings  []Warning `json:"warnings"`
	// TeamFile is the team settings file's path when a team tier is
	// configured (whether or not the file exists), else "".
	TeamFile string `json:"team_file"`
}

// LockedKeys returns the keys a team tier fixes.
func (e Effective) LockedKeys() map[string]bool {
	out := map[string]bool{}
	for _, it := range e.Items {
		if it.Locked {
			out[it.Key] = true
		}
	}
	return out
}

// ResolveProject loads the team file from teamDir and resolves projectID's
// settings against local. It is what the HTTP API and the CLI call.
func ResolveProject(projectID string, local LocalSettings, teamDir string) Effective {
	team, warnings := LoadTeamFile(teamDir)
	eff := Resolve(projectID, local, team)
	eff.TeamFile = TeamFilePath(teamDir)
	eff.Warnings = append(append([]Warning{}, warnings...), eff.Warnings...)
	return eff
}

// Resolve resolves projectID's settings from local and team (either may be
// nil), per key: team projects.<projectID>, team defaults, local, built-in
// default. Invalid or unknown stored values, and bypassPermissions from the
// team tier, are skipped with a warning.
func Resolve(projectID string, local LocalSettings, team *TeamFile) Effective {
	eff := Effective{ProjectID: projectID, Warnings: []Warning{}}

	var teamProject, teamDefaults map[string]any
	if team != nil {
		teamDefaults = team.Defaults
		teamProject = team.Projects[projectID]
	}

	defaults := Defaults()
	defaultValues := defaults.values()
	values := map[string]any{}

	for _, spec := range specs {
		key := spec.key
		item := Item{Key: key, Default: defaultValues[key]}

		teamProjectValue, teamProjectOK := eff.teamValue(SourceTeamProject, teamProject, key)
		teamDefaultsValue, teamDefaultsOK := eff.teamValue(SourceTeamDefaults, teamDefaults, key)
		localValue, localOK := eff.storedValue(SourceLocal, local, key)
		if localOK {
			item.Local = localValue
		}

		switch {
		case teamProjectOK:
			item.Value, item.Source, item.Locked, item.Team = teamProjectValue, SourceTeamProject, true, teamProjectValue
		case teamDefaultsOK:
			item.Value, item.Source, item.Locked, item.Team = teamDefaultsValue, SourceTeamDefaults, true, teamDefaultsValue
		case localOK:
			item.Value, item.Source = localValue, SourceLocal
		default:
			item.Value, item.Source = defaultValues[key], SourceDefault
		}
		values[key] = item.Value
		eff.Items = append(eff.Items, item)
	}

	eff.warnUnknownKeys(SourceTeamProject, teamProject)
	eff.warnUnknownKeys(SourceTeamDefaults, teamDefaults)
	eff.warnUnknownKeys(SourceLocal, local)

	eff.Settings = settingsFromValues(values)
	return eff
}

// teamValue reads key from a team tier: like storedValue, plus the
// bypassPermissions exclusion.
func (e *Effective) teamValue(src Source, m map[string]any, key string) (any, bool) {
	v, ok := e.storedValue(src, m, key)
	if !ok {
		return nil, false
	}
	if key == KeyPermissionMode && v == PermissionModeBypassPermissions {
		e.Warnings = append(e.Warnings, Warning{
			Code:    WarnTeamBypassIgnored,
			Source:  src,
			Key:     key,
			Value:   describe(v),
			Message: fmt.Sprintf("permissionMode %q is not accepted from the team settings (%s) and is ignored; set it locally if you want it", v, src),
		})
		return nil, false
	}
	return v, true
}

// storedValue reads and validates key from one stored tier. An absent key is
// (nil, false) with no warning; an explicit null is treated as absent too;
// an invalid value is (nil, false) with a WarnInvalidValue warning.
func (e *Effective) storedValue(src Source, m map[string]any, key string) (any, bool) {
	raw, present := m[key]
	if !present || raw == nil {
		return nil, false
	}
	v, err := NormalizeValue(key, raw)
	if err != nil {
		e.Warnings = append(e.Warnings, Warning{
			Code:    WarnInvalidValue,
			Source:  src,
			Key:     key,
			Value:   describe(raw),
			Message: fmt.Sprintf("autopilot setting ignored (%s): %v", src, err),
		})
		return nil, false
	}
	return v, true
}

func (e *Effective) warnUnknownKeys(src Source, m map[string]any) {
	var unknown []string
	for k := range m {
		if !IsKey(k) {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		e.Warnings = append(e.Warnings, Warning{
			Code:    WarnUnknownKey,
			Source:  src,
			Key:     k,
			Message: fmt.Sprintf("unknown autopilot setting %q (%s) is ignored", k, src),
		})
	}
}

// values maps each key to s's value for it.
func (s Settings) values() map[string]any {
	return map[string]any{
		KeyMainReflection:      s.MainReflection,
		KeyPermissionMode:      s.PermissionMode,
		KeyAutoApproveGates:    s.AutoApproveGates,
		KeyAutoCreateTickets:   s.AutoCreateTickets,
		KeyMaxTickets:          s.MaxTickets,
		KeyMaxDepth:            s.MaxDepth,
		KeyOnFailure:           s.OnFailure,
		KeyStallTimeoutMinutes: s.StallTimeoutMinutes,
	}
}

// settingsFromValues builds Settings from already-normalized values.
func settingsFromValues(v map[string]any) Settings {
	return Settings{
		MainReflection:      v[KeyMainReflection].(string),
		PermissionMode:      v[KeyPermissionMode].(string),
		AutoApproveGates:    v[KeyAutoApproveGates].(bool),
		AutoCreateTickets:   v[KeyAutoCreateTickets].(bool),
		MaxTickets:          v[KeyMaxTickets].(int),
		MaxDepth:            v[KeyMaxDepth].(int),
		OnFailure:           v[KeyOnFailure].(string),
		StallTimeoutMinutes: v[KeyStallTimeoutMinutes].(int),
	}
}

// Patch is a validated partial update of one project's local settings.
type Patch struct {
	// Set holds the keys to overwrite, with normalized values.
	Set map[string]any
	// Unset lists the keys whose local value is to be removed (sent as
	// null), sorted.
	Unset []string
}

// ParsePatch parses and validates a PUT body -- a JSON object whose keys are
// settings, each either a value to store locally or null to remove the local
// value; keys not present are left alone. locked is the set of keys the team
// tier fixes (Effective.LockedKeys).
//
// The whole body is refused, so nothing is saved, when:
//
//   - it is not a JSON object (VALIDATION_ERROR);
//   - it names any locked key, whatever the value -- even null, even the
//     team's own value (AUTOPILOT_SETTING_LOCKED, details.keys);
//   - it names an unknown key or carries an invalid value
//     (VALIDATION_ERROR, details.keys and details.errors).
//
// Locked keys are checked first, so a caller hitting both learns about the
// lock, which no value change can fix.
func ParsePatch(body []byte, locked map[string]bool) (Patch, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil || raw == nil {
		return Patch{}, domain.NewAPIError(domain.ErrCodeValidation, "the request body must be a JSON object of autopilot settings")
	}

	var lockedKeys []string
	for k := range raw {
		if locked[k] {
			lockedKeys = append(lockedKeys, k)
		}
	}
	if len(lockedKeys) > 0 {
		sort.Strings(lockedKeys)
		return Patch{}, domain.NewAPIError(domain.ErrCodeAutopilotSettingLocked,
			"these autopilot settings are fixed by the team settings and cannot be saved locally: %s", strings.Join(lockedKeys, ", ")).
			WithDetails(map[string]any{"keys": lockedKeys})
	}

	p := Patch{Set: map[string]any{}}
	problems := map[string]string{}
	for k, msg := range raw {
		if !IsKey(k) {
			problems[k] = fmt.Sprintf("unknown autopilot setting %q", k)
			continue
		}
		dec := json.NewDecoder(strings.NewReader(string(msg)))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			problems[k] = err.Error()
			continue
		}
		if v == nil {
			p.Unset = append(p.Unset, k)
			continue
		}
		norm, err := NormalizeValue(k, v)
		if err != nil {
			problems[k] = err.Error()
			continue
		}
		p.Set[k] = norm
	}
	if len(problems) > 0 {
		keys := make([]string, 0, len(problems))
		for k := range problems {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		msgs := make([]string, len(keys))
		errs := map[string]any{}
		for i, k := range keys {
			msgs[i] = problems[k]
			errs[k] = problems[k]
		}
		return Patch{}, domain.NewAPIError(domain.ErrCodeValidation, "invalid autopilot settings: %s", strings.Join(msgs, "; ")).
			WithDetails(map[string]any{"keys": keys, "errors": errs})
	}
	sort.Strings(p.Unset)
	return p, nil
}

// Empty reports whether p changes nothing.
func (p Patch) Empty() bool {
	return len(p.Set) == 0 && len(p.Unset) == 0
}

// Apply returns local with p applied, as a new map; local is not modified.
// Keys p does not mention -- including ones the team currently locks, and
// ones this version does not know -- are carried over untouched. The result
// is nil when no key is left, so the caller can drop the project's entry.
func (p Patch) Apply(local LocalSettings) LocalSettings {
	out := LocalSettings{}
	for k, v := range local {
		out[k] = v
	}
	for k, v := range p.Set {
		out[k] = v
	}
	for _, k := range p.Unset {
		delete(out, k)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
