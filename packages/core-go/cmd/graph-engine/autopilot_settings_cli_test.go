package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
)

// DFLT-00142 phase 2: `graph-engine autopilot settings` is read-only.

func TestAutopilotSettingsCLI_PrintsEffectiveSettings(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	rc := sandboxRC(t)
	rc.TeamExtensionsDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(rc.TeamExtensionsDir, autopilot.TeamFileName),
		[]byte("defaults:\n  mainReflection: pull_request\n  permissionMode: bypassPermissions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtimeconfig.UpdateHome(rc.HomeDir, func(cfg *runtimeconfig.FileConfig) error {
		cfg.AutopilotSettings = map[string]autopilot.LocalSettings{projectID: {"maxTickets": 5}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var runErr error
	out := captureStdout(t, func() {
		runErr = cmdAutopilot(repo, rc, []string{"settings", "--project", projectID})
	})
	if runErr != nil {
		t.Fatal(runErr)
	}
	var eff autopilot.Effective
	if err := json.Unmarshal([]byte(out), &eff); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if eff.ProjectID != projectID || eff.Settings.MainReflection != "pull_request" || eff.Settings.MaxTickets != 5 || eff.Settings.PermissionMode != "auto" {
		t.Fatalf("settings = %+v", eff.Settings)
	}
	sources := map[string]autopilot.Source{}
	locked := map[string]bool{}
	for _, it := range eff.Items {
		sources[it.Key], locked[it.Key] = it.Source, it.Locked
	}
	if sources["mainReflection"] != autopilot.SourceTeamDefaults || !locked["mainReflection"] ||
		sources["maxTickets"] != autopilot.SourceLocal || locked["maxTickets"] ||
		sources["maxDepth"] != autopilot.SourceDefault {
		t.Fatalf("sources = %v, locked = %v", sources, locked)
	}
	if len(eff.Warnings) != 1 || eff.Warnings[0].Code != autopilot.WarnTeamBypassIgnored {
		t.Fatalf("warnings = %+v", eff.Warnings)
	}
}

func TestAutopilotSettingsCLI_ResolvesCurrentProjectWhenOmitted(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	rc := sandboxRC(t)
	var runErr error
	out := captureStdout(t, func() { runErr = cmdAutopilot(repo, rc, []string{"settings"}) })
	if runErr != nil {
		t.Fatal(runErr)
	}
	var eff autopilot.Effective
	if err := json.Unmarshal([]byte(out), &eff); err != nil || eff.ProjectID != projectID || eff.Settings != autopilot.Defaults() {
		t.Fatalf("eff = %+v, err = %v", eff, err)
	}
}

func TestAutopilotSettingsCLI_Errors(t *testing.T) {
	repo, _ := newTestRepoWithProject(t)
	rc := sandboxRC(t)
	for name, args := range map[string][]string{
		"no subcommand":       {},
		"write subcommand":    {"set-settings", "maxTickets", "3"},
		"set via settings":    {"settings", "--set", "maxTickets=3"},
		"missing project arg": {"settings", "--project"},
		"twice":               {"settings", "--project", "a", "--project", "b"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := cmdAutopilot(repo, rc, args); err == nil || !strings.Contains(err.Error(), "usage") && !strings.Contains(err.Error(), "unknown autopilot subcommand") {
				t.Fatalf("err = %v", err)
			}
		})
	}
	err := cmdAutopilot(repo, rc, []string{"settings", "--project", "proj-missing"})
	assertAPIErrorCode(t, err, domain.ErrCodeProjectNotFound)
}
