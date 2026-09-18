package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00068: $HOME/.graph-ops must be read as the user tier only, never also
// as the team tier. HOME is pinned to a temp dir so the developer's real
// ~/.graph-ops never affects these tests.

// pinHome makes <tmp>/home/.graph-ops with config.yaml "language: ja" and a
// project directory <home>/proj (no .graph-ops of its own), sets HOME, and
// returns (home, proj).
func pinHome(t *testing.T) (string, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	userRoot := filepath.Join(home, ".graph-ops")
	proj := filepath.Join(home, "proj")
	for _, dir := range []string{userRoot, proj} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(userRoot, "config.yaml"), []byte("language: ja\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	return home, proj
}

type languageSettingsResp struct {
	Resolved string `json:"resolved"`
	Source   string `json:"source"`
}

func runGetLanguageSettings(t *testing.T, rc runtimeConfig, args []string) languageSettingsResp {
	t.Helper()
	return runGetLanguageSettingsWithRepo(t, newTestRepo(t), rc, args)
}

func runGetLanguageSettingsWithRepo(t *testing.T, repo store.GraphRepository, rc runtimeConfig, args []string) languageSettingsResp {
	t.Helper()
	out := captureStdout(t, func() {
		if err := cmdGetLanguageSettings(repo, rc, args); err != nil {
			t.Fatalf("cmdGetLanguageSettings(%v): %v", args, err)
		}
	})
	var resp languageSettingsResp
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, out)
	}
	return resp
}

func TestCmdGetExtensionRoots_HomeGraphOpsIsNotTeamRoot(t *testing.T) {
	home, proj := pinHome(t)
	rc := runtimeConfig{WorkDir: proj}

	out := captureStdout(t, func() {
		if err := cmdGetExtensionRoots(rc); err != nil {
			t.Fatalf("cmdGetExtensionRoots: %v", err)
		}
	})
	var resp struct {
		UserDir string `json:"userDir"`
		TeamDir string `json:"teamDir"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, out)
	}
	if want := filepath.Join(home, ".graph-ops"); resp.UserDir != want {
		t.Errorf("userDir = %q, want %q", resp.UserDir, want)
	}
	if resp.TeamDir != "" {
		t.Errorf("teamDir = %q, want \"\"", resp.TeamDir)
	}
}

func TestCmdGetLanguageSettings_UserTierUnderHome(t *testing.T) {
	_, proj := pinHome(t)
	resp := runGetLanguageSettings(t, runtimeConfig{WorkDir: proj}, nil)
	if resp.Resolved != "ja" || resp.Source != "user" {
		t.Errorf("got %+v, want resolved ja source user", resp)
	}
}

func TestCmdGetLanguageSettings_ProjectLocalPathIsHome(t *testing.T) {
	home, proj := pinHome(t)
	repo := newTestRepo(t)
	p, _ := repo.CreateProject("Home", "HOME")
	rc := runtimeConfig{WorkDir: proj, ProjectPaths: map[string]string{p.ID: home}}

	resp := runGetLanguageSettingsWithRepo(t, repo, rc, []string{"--project", p.ID})
	if resp.Resolved != "ja" || resp.Source != "user" {
		t.Errorf("got %+v, want resolved ja source user (no team tier, no error)", resp)
	}
}

func TestCmdGetLanguageSettings_ProjectTeamRootEqualToUserOverrideIsDropped(t *testing.T) {
	// The user root is overridden to the project's own .graph-ops, whose
	// workflow.yaml says "en". That directory is the user tier only, so its
	// workflow.yaml must not be read as the team tier.
	_, proj := pinHome(t)
	shared := filepath.Join(proj, ".graph-ops")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "config.yaml"), []byte("language: ja\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "workflow.yaml"), []byte("version: 1\nlanguage: en\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := newTestRepo(t)
	p, _ := repo.CreateProject("P", "PROJ")
	rc := runtimeConfig{WorkDir: t.TempDir(), UserExtensionsDir: shared, ProjectPaths: map[string]string{p.ID: proj}}

	resp := runGetLanguageSettingsWithRepo(t, repo, rc, []string{"--project", p.ID})
	if resp.Resolved != "ja" || resp.Source != "user" {
		t.Errorf("got %+v, want resolved ja source user", resp)
	}
}
