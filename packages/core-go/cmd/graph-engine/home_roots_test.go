package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/config"
)

// DFLT-00068: $HOME/.graph-ops must be read as the user tier only, never also
// as the team tier. DFLT-00124: the team tier comes from teamExtensionsDir
// alone. HOME is pinned to a temp dir so the developer's real ~/.graph-ops
// never affects these tests.

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
	out := captureStdout(t, func() {
		if err := cmdGetLanguageSettings(rc, args); err != nil {
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

// TestCmdGetExtensionRoots_RepoGraphOpsIsNotTeamRoot is completion criterion
// 6 seen through the command skills actually call: a .graph-ops in the
// process's own directory must not become the team root.
func TestCmdGetExtensionRoots_RepoGraphOpsIsNotTeamRoot(t *testing.T) {
	_, proj := pinHome(t)
	if err := os.MkdirAll(filepath.Join(proj, ".graph-ops"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(proj)

	out := captureStdout(t, func() {
		if err := cmdGetExtensionRoots(runtimeConfig{WorkDir: proj}); err != nil {
			t.Fatalf("cmdGetExtensionRoots: %v", err)
		}
	})
	var resp struct {
		TeamDir string `json:"teamDir"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, out)
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

// TestCmdGetLanguageSettings_RepoWorkflowYAMLIsNotRead: a workflow.yaml in
// the current directory's .graph-ops used to set the language for everything
// run there. It must not any more.
func TestCmdGetLanguageSettings_RepoWorkflowYAMLIsNotRead(t *testing.T) {
	_, proj := pinHome(t)
	writeWorkflowYAML(t, proj, "version: 1\nlanguage: en\n")
	t.Chdir(proj)

	resp := runGetLanguageSettings(t, runtimeConfig{WorkDir: proj}, nil)
	if resp.Resolved != "ja" || resp.Source != "user" {
		t.Errorf("got %+v, want resolved ja source user (the repository's workflow.yaml must be ignored)", resp)
	}
}

// TestCmdGetLanguageSettings_TeamLanguageIsIgnored pins DFLT-00153: the
// working language is a personal setting, so a team tier's workflow.yaml
// language is never resolved and "source" is never "team". It is reported on
// stderr instead, with the same message LoadWithRoots gives, and never in
// the JSON on stdout.
func TestCmdGetLanguageSettings_TeamLanguageIsIgnored(t *testing.T) {
	wantWarning := "graph-ops: warning: " + config.Warning{Code: config.WarnTeamLanguageIgnored, Language: "en"}.Message()

	t.Run("team tier only", func(t *testing.T) {
		_, proj := pinHome(t)
		// No user-tier language: replace pinHome's config.yaml.
		emptyUser := t.TempDir()
		shared := t.TempDir()
		if err := os.WriteFile(filepath.Join(shared, "workflow.yaml"), []byte("version: 1\nlanguage: en\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		collect := captureConfigWarnings(t)
		out := captureStdout(t, func() {
			if err := cmdGetLanguageSettings(runtimeConfig{WorkDir: proj, UserExtensionsDir: emptyUser, TeamExtensionsDir: shared}, nil); err != nil {
				t.Fatal(err)
			}
		})
		var resp languageSettingsResp
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("decode: %v (%s)", err, out)
		}
		if resp.Resolved != "" || resp.Source != "none" {
			t.Errorf("got %+v, want resolved \"\" source none (the team tier's language is ignored)", resp)
		}
		if strings.Contains(out, "warning") || strings.Contains(out, config.WarnTeamLanguageIgnored) {
			t.Errorf("stdout carries the warning: %s", out)
		}
		if got := collect(); len(got) != 1 || got[0] != wantWarning {
			t.Errorf("stderr warnings = %q, want exactly %q", got, wantWarning)
		}
	})

	t.Run("user tier decides", func(t *testing.T) {
		_, proj := pinHome(t)
		shared := t.TempDir()
		if err := os.WriteFile(filepath.Join(shared, "workflow.yaml"), []byte("version: 1\nlanguage: en\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		collect := captureConfigWarnings(t)
		resp := runGetLanguageSettings(t, runtimeConfig{WorkDir: proj, TeamExtensionsDir: shared}, nil)
		if resp.Resolved != "ja" || resp.Source != "user" {
			t.Errorf("got %+v, want resolved ja source user", resp)
		}
		if got := collect(); len(got) != 1 || got[0] != wantWarning {
			t.Errorf("stderr warnings = %q, want exactly %q", got, wantWarning)
		}
	})

	t.Run("no team language, no warning", func(t *testing.T) {
		_, proj := pinHome(t)
		shared := t.TempDir()
		if err := os.WriteFile(filepath.Join(shared, "workflow.yaml"), []byte("version: 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		collect := captureConfigWarnings(t)
		resp := runGetLanguageSettings(t, runtimeConfig{WorkDir: proj, TeamExtensionsDir: shared}, nil)
		if resp.Resolved != "ja" || resp.Source != "user" {
			t.Errorf("got %+v, want resolved ja source user", resp)
		}
		if got := collect(); len(got) != 0 {
			t.Errorf("stderr warnings = %q, want none", got)
		}
	})
}

// TestGetWorkflowCatalog_TeamLanguageIsIgnored: get-workflow-catalog neither
// localizes nor reports the team tier's language, and warns on stderr with
// the same message get-language-settings uses.
func TestGetWorkflowCatalog_TeamLanguageIsIgnored(t *testing.T) {
	env := newCatalogWarningEnv(t, "")
	writeFile(t, filepath.Join(env.rc.TeamExtensionsDir, "workflow.yaml"), "version: 1\nlanguage: ja\n")
	collect := captureConfigWarnings(t)
	out := captureStdout(t, func() {
		if err := cmdGetWorkflowCatalog(env.rc, nil); err != nil {
			t.Fatal(err)
		}
	})
	var resp struct {
		Language string `json:"language"`
		Nodes    []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, out)
	}
	if resp.Language != "" {
		t.Errorf("language = %q, want \"\" (the team tier's ja is ignored)", resp.Language)
	}
	for _, n := range resp.Nodes {
		if n.ID == "impl" && n.Name != "Implementation" {
			t.Errorf("impl node name = %q, want the English default", n.Name)
		}
	}
	if strings.Contains(out, config.WarnTeamLanguageIgnored) {
		t.Errorf("stdout carries the warning: %s", out)
	}
	want := "graph-ops: warning: " + config.Warning{Code: config.WarnTeamLanguageIgnored, Language: "ja"}.Message()
	if got := collect(); len(got) != 1 || got[0] != want {
		t.Errorf("stderr warnings = %q, want exactly %q", got, want)
	}
}

// TestCmdGetLanguageSettings_TeamRootEqualToUserRootIsDropped keeps
// DFLT-00068: one directory is never read as both tiers, so its
// workflow.yaml does not override its own config.yaml.
func TestCmdGetLanguageSettings_TeamRootEqualToUserRootIsDropped(t *testing.T) {
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

	rc := runtimeConfig{WorkDir: proj, UserExtensionsDir: shared, TeamExtensionsDir: shared}
	resp := runGetLanguageSettings(t, rc, nil)
	if resp.Resolved != "ja" || resp.Source != "user" {
		t.Errorf("got %+v, want resolved ja source user", resp)
	}
}

// TestCmdGetLanguageSettings_RejectsProjectFlag is completion criterion 7's
// "does not accept it" half: --project is refused rather than accepted and
// ignored, so nobody is left passing an id that silently does nothing.
func TestCmdGetLanguageSettings_RejectsProjectFlag(t *testing.T) {
	_, proj := pinHome(t)
	err := cmdGetLanguageSettings(runtimeConfig{WorkDir: proj}, []string{"--project", "proj-x"})
	if err == nil {
		t.Fatal("cmdGetLanguageSettings(--project) = nil error, want a usage error")
	}
	if !strings.Contains(err.Error(), "--project") {
		t.Errorf("error = %v, want it to name the unrecognized argument", err)
	}
}
