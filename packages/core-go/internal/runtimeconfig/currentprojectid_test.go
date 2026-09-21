package runtimeconfig

// DFLT-00106: currentProjectId has to survive a round trip through
// the home config file with all THREE of its states distinguishable -- absent,
// an explicit empty string, and an id. That is the whole reason the field is
// a *string: with a plain string + omitempty, "never set here" and
// "deliberately deselected" would both be an absent key, and every project
// deletion would let internal/currentproject re-inherit the stale value from
// the shared data source.

import (
	"encoding/json"
	"testing"
)

func strPtr(s string) *string { return &s }

func TestCurrentProjectID_ThreeStatesRoundTripThroughJSON(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   *string
		wantKey bool
		wantRaw any
	}{
		{"absent", nil, false, nil},
		{"explicitly empty", strPtr(""), true, ""},
		{"an id", strPtr("proj-alpha"), true, "proj-alpha"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if _, _, err := UpdateHome(home, func(cfg *FileConfig) error {
				cfg.CurrentProjectID = tc.value
				cfg.MyName = "山田"
				return nil
			}); err != nil {
				t.Fatalf("UpdateHome: %v", err)
			}

			raw := readRawConfig(t, HomeConfigPath(home))
			got, has := raw["currentProjectId"]
			if has != tc.wantKey {
				t.Fatalf("currentProjectId present = %v, want %v (file: %v)", has, tc.wantKey, raw)
			}
			if tc.wantKey && got != tc.wantRaw {
				t.Errorf("currentProjectId = %v, want %v", got, tc.wantRaw)
			}

			cfg, err := LoadHomeConfig(home)
			if err != nil {
				t.Fatalf("LoadHomeConfig: %v", err)
			}
			switch {
			case tc.value == nil && cfg.CurrentProjectID != nil:
				t.Errorf("expected nil after reload, got %q", *cfg.CurrentProjectID)
			case tc.value != nil && cfg.CurrentProjectID == nil:
				t.Errorf("expected %q after reload, got nil", *tc.value)
			case tc.value != nil && *cfg.CurrentProjectID != *tc.value:
				t.Errorf("expected %q after reload, got %q", *tc.value, *cfg.CurrentProjectID)
			}
			if cfg.MyName != "山田" {
				t.Errorf("other settings must survive, myName = %q", cfg.MyName)
			}
		})
	}
}

// The distinction has to hold at the encoding/json level too, not only
// through UpdateHome/LoadHomeConfig -- GET /api/settings/app marshals the same struct.
func TestCurrentProjectID_EmptyStringIsNotOmitted(t *testing.T) {
	withEmpty, err := json.Marshal(FileConfig{CurrentProjectID: strPtr("")})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(withEmpty) != `{"currentProjectId":""}` {
		t.Errorf("marshaled = %s, want the key kept with an empty value", withEmpty)
	}
	absent, err := json.Marshal(FileConfig{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(absent) != `{}` {
		t.Errorf("marshaled = %s, want the key omitted when unset", absent)
	}
}
