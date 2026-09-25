package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// DFLT-00142: the three autopilot skills are built-in skills, so the settings
// UI offers them and get-skill-context merges extensions for them.
func TestListKnownSkills_IncludesAutopilotSkills(t *testing.T) {
	got := ListKnownSkills()
	want := []string{
		"create-ticket", "refine-ticket", "process-ticket", "onboarding",
		"autopilot-ticket", "autopilot-tree", "autopilot-worker",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ListKnownSkills() = %v, want %v", got, want)
	}
}

// Every built-in skill must be shipped by the plugin (and nothing the plugin
// ships may be missing here), or the settings UI and the plugin drift apart.
func TestBuiltinSkills_MatchPluginSkillDirectories(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "plugin", "skills")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("plugin skills directory not available: %v", err)
	}
	shipped := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "SKILL.md")); err == nil {
			shipped[e.Name()] = true
		}
	}
	for _, name := range ListKnownSkills() {
		if !shipped[name] {
			t.Errorf("built-in skill %q has no packages/plugin/skills/%s/SKILL.md", name, name)
		}
		delete(shipped, name)
	}
	for name := range shipped {
		t.Errorf("plugin skill %q is missing from builtinSkills", name)
	}
}

func TestResolveSkillContext_AutopilotWorkerMergesUserAndTeam(t *testing.T) {
	userDir, teamDir := t.TempDir(), t.TempDir()
	writeExtensionFile(t, userDir, SkillsSubdir, "autopilot-worker.md", "user: run the full test suite before merge-into-parent.")
	writeExtensionFile(t, teamDir, SkillsSubdir, "autopilot-worker.md", "team: never approve a gate with failing tests.")

	got := ResolveSkillContext(Roots{UserDir: userDir, TeamDir: teamDir}, "autopilot-worker")
	u := strings.Index(got, "user: run the full test suite")
	tm := strings.Index(got, "team: never approve a gate")
	if u < 0 || tm < 0 || u > tm {
		t.Fatalf("expected user then team text, got %q", got)
	}
}
