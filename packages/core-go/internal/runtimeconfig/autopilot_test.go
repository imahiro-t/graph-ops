package runtimeconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/graph-ops/core-go/internal/autopilot"
)

// DFLT-00142 phase 2: local autopilot settings live in the home config and
// are written through UpdateHome without disturbing anything else.

func TestUpdateAutopilotSettings_PreservesOtherFields(t *testing.T) {
	home := t.TempDir()
	cur := "proj-A"
	if _, _, err := UpdateHome(home, func(cfg *FileConfig) error {
		cfg.DBBackend = "mysql"
		cfg.MySQLPassword = "secret"
		cfg.ProjectPaths = map[string]string{"proj-A": "/tmp/a"}
		cfg.CurrentProjectID = &cur
		cfg.AutopilotSettings = map[string]autopilot.LocalSettings{"proj-B": {"maxDepth": 1}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	patch := autopilot.Patch{Set: map[string]any{"maxTickets": 30, "onFailure": "continue"}}
	if _, err := UpdateAutopilotSettings(home, "proj-A", patch); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadHomeConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBBackend != "mysql" || cfg.MySQLPassword != "secret" || cfg.ProjectPaths["proj-A"] != "/tmp/a" || cfg.CurrentProjectID == nil || *cfg.CurrentProjectID != "proj-A" {
		t.Fatalf("other fields changed: %+v", cfg)
	}
	want := map[string]autopilot.LocalSettings{
		"proj-A": {"maxTickets": float64(30), "onFailure": "continue"},
		"proj-B": {"maxDepth": float64(1)},
	}
	if !reflect.DeepEqual(cfg.AutopilotSettings, want) {
		t.Fatalf("autopilotSettings = %+v, want %+v", cfg.AutopilotSettings, want)
	}
	if got := cfg.AutopilotLocal("proj-A"); got["maxTickets"] != float64(30) {
		t.Errorf("AutopilotLocal = %+v", got)
	}

	// Removing every key drops the project's entry, and then the map.
	for _, pid := range []string{"proj-A", "proj-B"} {
		keys := []string{}
		for k := range cfg.AutopilotSettings[pid] {
			keys = append(keys, k)
		}
		if _, err := UpdateAutopilotSettings(home, pid, autopilot.Patch{Unset: keys}); err != nil {
			t.Fatal(err)
		}
	}
	cfg, _ = LoadHomeConfig(home)
	if cfg.AutopilotSettings != nil {
		t.Fatalf("autopilotSettings = %+v, want nil", cfg.AutopilotSettings)
	}
}

func TestUpdateAutopilotSettings_EmptyPatchWritesNothing(t *testing.T) {
	home := t.TempDir()
	if _, err := UpdateAutopilotSettings(home, "proj-A", autopilot.Patch{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(HomeConfigPath(home)); !os.IsNotExist(err) {
		t.Fatalf("an empty patch created the config file: %v", err)
	}
}

// A hand-edited value of the wrong type must not make the rest of the file
// unreadable.
func TestLoadHomeConfig_ToleratesMistypedAutopilotValue(t *testing.T) {
	home := t.TempDir()
	path := HomeConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := `{"dbBackend":"sqlite","autopilotSettings":{"proj-A":{"maxTickets":"10","autoApproveGates":"yes"}}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadHomeConfig(home)
	if err != nil || cfg.DBBackend != "sqlite" || cfg.AutopilotLocal("proj-A")["maxTickets"] != "10" {
		t.Fatalf("cfg = %+v, err = %v", cfg, err)
	}
}
