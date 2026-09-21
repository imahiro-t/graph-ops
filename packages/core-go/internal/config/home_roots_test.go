package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// DFLT-00124: the team tier is resolved from teamExtensionsDir /
// GRAPH_TEAM_EXTENSIONS_DIR alone. Nothing in the current directory or its
// ancestors is ever read, so a repository that merely contains a .graph-ops
// directory supplies nothing. DFLT-00068's rule -- one directory is never
// read as both tiers -- still holds on top of that.
//
// These tests pin HOME to a temp dir with t.Setenv so the developer's real
// ~/.graph-ops never affects them (and so they can't use t.Parallel).

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

// setupHome makes <tmp>/home with a .graph-ops directory and a nested
// project directory <home>/proj/sub (no .graph-ops of its own), sets HOME to
// it, and returns (home, projectDir, nestedDir).
func setupHome(t *testing.T) (string, string, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	mkdirAll(t, filepath.Join(home, configDirName))
	proj := filepath.Join(home, "proj")
	nested := filepath.Join(proj, "sub")
	mkdirAll(t, nested)
	t.Setenv("HOME", home)
	return home, proj, nested
}

func TestResolveRoots_NoTeamOverrideMeansNoTeamTier(t *testing.T) {
	home, _, _ := setupHome(t)

	roots := ResolveRoots("", "")
	if want := filepath.Join(home, configDirName); roots.UserDir != want {
		t.Errorf("UserDir = %q, want %q", roots.UserDir, want)
	}
	if roots.TeamDir != "" {
		t.Errorf("TeamDir = %q, want \"\" (no teamExtensionsDir configured)", roots.TeamDir)
	}
}

// TestResolveRoots_RepoGraphOpsIsNotDiscovered is completion criterion 6's
// core: a .graph-ops directory sitting in a checked-out repository -- the
// process's own directory or any ancestor of it -- must not become the team
// tier. Before DFLT-00124 this was exactly how the team tier was found, so
// cloning a repository was enough to have its workflow.yaml and
// extensions/**/*.md feed agent instructions.
func TestResolveRoots_RepoGraphOpsIsNotDiscovered(t *testing.T) {
	_, proj, nested := setupHome(t)
	repoTeamDir := filepath.Join(proj, configDirName)
	writeExtensionFile(t, repoTeamDir, NodeTypesSubdir, "plan.md", "REPO-SUPPLIED-INSTRUCTIONS")
	if err := os.WriteFile(filepath.Join(repoTeamDir, teamConfigFile), []byte("language: ja\n"), 0o644); err != nil {
		t.Fatalf("write workflow.yaml: %v", err)
	}
	// Run from inside the repository, the way a user would.
	t.Chdir(nested)

	roots := ResolveRoots("", "")
	if roots.TeamDir != "" {
		t.Errorf("TeamDir = %q, want \"\" (a repository's own .graph-ops is never the team tier)", roots.TeamDir)
	}
	if got := ResolveNodeTypeContext(roots, "plan"); strings.Contains(got, "REPO-SUPPLIED-INSTRUCTIONS") {
		t.Error("plan instructions still contain the repository's .graph-ops text")
	}
	cat, err := LoadWithRoots("", "", "")
	if err != nil {
		t.Fatalf("LoadWithRoots: %v", err)
	}
	if cat.Language == "ja" {
		t.Error("the repository's workflow.yaml still set the language")
	}
}

// TestResolveRoots_AncestorGraphOpsIsNotDiscovered is the same for a
// .graph-ops several levels above the working directory.
func TestResolveRoots_AncestorGraphOpsIsNotDiscovered(t *testing.T) {
	home, _, _ := setupHome(t)
	above := filepath.Join(filepath.Dir(home), configDirName)
	mkdirAll(t, above)
	deep := filepath.Join(home, "proj", "a", "b", "c")
	mkdirAll(t, deep)
	t.Chdir(deep)

	roots := ResolveRoots("", "")
	if roots.TeamDir != "" {
		t.Errorf("TeamDir = %q, want \"\" (an ancestor .graph-ops is never the team tier)", roots.TeamDir)
	}
}

func TestResolveRoots_ExplicitTeamOverrideIsUsed(t *testing.T) {
	home, proj, nested := setupHome(t)
	shared := filepath.Join(home, "team-shared")
	mkdirAll(t, shared)
	writeExtensionFile(t, shared, NodeTypesSubdir, "plan.md", "SHARED-TEAM-INSTRUCTIONS")
	// A .graph-ops in the repository as well, to show which one is read.
	repoTeamDir := filepath.Join(proj, configDirName)
	mkdirAll(t, repoTeamDir)
	writeExtensionFile(t, repoTeamDir, NodeTypesSubdir, "plan.md", "REPO-SUPPLIED-INSTRUCTIONS")
	t.Chdir(nested)

	roots := ResolveRoots("", shared)
	if roots.TeamDir != shared {
		t.Errorf("TeamDir = %q, want %q", roots.TeamDir, shared)
	}
	got := ResolveNodeTypeContext(roots, "plan")
	if !strings.Contains(got, "SHARED-TEAM-INSTRUCTIONS") {
		t.Error("plan instructions do not contain the explicitly configured team directory's text")
	}
	if strings.Contains(got, "REPO-SUPPLIED-INSTRUCTIONS") {
		t.Error("plan instructions contain the repository's .graph-ops text")
	}
}

func TestResolveContexts_HomeGraphOpsContentAppearsOnce(t *testing.T) {
	home, _, _ := setupHome(t)
	userRoot := filepath.Join(home, configDirName)
	const marker = "USER-TIER-MARKER-DFLT-00068"
	writeExtensionFile(t, userRoot, NodeTypesSubdir, "plan.md", marker)
	writeExtensionFile(t, userRoot, SkillsSubdir, "process-ticket.md", marker)

	roots := ResolveRoots("", "")
	if n := strings.Count(ResolveNodeTypeContext(roots, "plan"), marker); n != 1 {
		t.Errorf("node type context contains the user-tier text %d times, want 1", n)
	}
	if n := strings.Count(ResolveSkillContext(roots, "process-ticket"), marker); n != 1 {
		t.Errorf("skill context contains the user-tier text %d times, want 1", n)
	}
}

// TestResolveRoots_SameOverridesKeepUserOnly pins DFLT-00068: pointing
// teamExtensionsDir at the user root drops the team tier rather than reading
// one directory twice.
func TestResolveRoots_SameOverridesKeepUserOnly(t *testing.T) {
	dir := t.TempDir()
	for _, team := range []string{dir, dir + string(filepath.Separator), filepath.Join(dir, "x", "..")} {
		roots := ResolveRoots(dir, team)
		if roots.UserDir != dir || roots.TeamDir != "" {
			t.Errorf("team override %q: got %+v, want UserDir=%q and TeamDir=\"\"", team, roots, dir)
		}
	}
}

// TestResolveRoots_DefaultUserRootEqualToTeamOverride is the same rule with
// the user root coming from $HOME rather than an override.
func TestResolveRoots_DefaultUserRootEqualToTeamOverride(t *testing.T) {
	home, _, _ := setupHome(t)
	userRoot := filepath.Join(home, configDirName)

	roots := ResolveRoots("", userRoot)
	if roots.UserDir != userRoot || roots.TeamDir != "" {
		t.Errorf("got %+v, want the shared directory as the user root only", roots)
	}
}

// TestResolveRoots_SameDirThroughSymlink pins that the DFLT-00068 drop uses
// SameDir, so two spellings of one directory (e.g. macOS's /var vs
// /private/var) still count as the same tier.
func TestResolveRoots_SameDirThroughSymlink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real-root")
	mkdirAll(t, real)
	link := filepath.Join(base, "link-root")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	roots := ResolveRoots(real, link)
	if roots.TeamDir != "" {
		t.Errorf("TeamDir = %q, want \"\" (same directory as the user root through a symlink)", roots.TeamDir)
	}
}

func TestSameDir(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist")
	cases := []struct {
		a, b string
		want bool
	}{
		{dir, dir, true},
		{dir, dir + string(filepath.Separator), true},
		{missing, filepath.Join(dir, "x", "..", "does-not-exist"), true},
		{dir, missing, false},
		{"", "", false},
		{dir, "", false},
	}
	for _, c := range cases {
		if got := SameDir(c.a, c.b); got != c.want {
			t.Errorf("SameDir(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestLoadWithRoots_UserLanguageStillReadUnderHome(t *testing.T) {
	home, _, nested := setupHome(t)
	if err := os.WriteFile(filepath.Join(home, configDirName, userConfigFile), []byte("language: ja\n"), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}
	t.Chdir(nested)

	cat, err := LoadWithRoots("", "", "")
	if err != nil {
		t.Fatalf("LoadWithRoots: %v", err)
	}
	if cat.Language != "ja" {
		t.Errorf("Language = %q, want \"ja\" from the user tier", cat.Language)
	}
}
