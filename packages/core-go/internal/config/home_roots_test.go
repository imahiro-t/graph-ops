package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// DFLT-00068: $HOME/.graph-ops is the user tier's root and must never also
// be picked as the team root. These tests pin HOME to a temp dir with
// t.Setenv so the developer's real ~/.graph-ops never affects them (and so
// they can't use t.Parallel).

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

func TestResolveRoots_SkipsHomeGraphOpsAsTeamRoot(t *testing.T) {
	home, _, nested := setupHome(t)

	roots, err := ResolveRoots(nested, "", "")
	if err != nil {
		t.Fatalf("ResolveRoots: %v", err)
	}
	if want := filepath.Join(home, configDirName); roots.UserDir != want {
		t.Errorf("UserDir = %q, want %q", roots.UserDir, want)
	}
	if roots.TeamDir != "" {
		t.Errorf("TeamDir = %q, want \"\" ($HOME/.graph-ops must not be the team root)", roots.TeamDir)
	}
}

func TestResolveRoots_ProjectGraphOpsUnderHomeStillFound(t *testing.T) {
	_, proj, nested := setupHome(t)
	teamDir := filepath.Join(proj, configDirName)
	mkdirAll(t, teamDir)

	roots, err := ResolveRoots(nested, "", "")
	if err != nil {
		t.Fatalf("ResolveRoots: %v", err)
	}
	if roots.TeamDir != teamDir {
		t.Errorf("TeamDir = %q, want %q", roots.TeamDir, teamDir)
	}
}

func TestResolveRoots_WalkContinuesAboveHome(t *testing.T) {
	// A .graph-ops above $HOME (e.g. a shared parent directory) is still a
	// valid team root: $HOME/.graph-ops is skipped, not a stop point.
	home, _, nested := setupHome(t)
	above := filepath.Join(filepath.Dir(home), configDirName)
	mkdirAll(t, above)

	roots, err := ResolveRoots(nested, "", "")
	if err != nil {
		t.Fatalf("ResolveRoots: %v", err)
	}
	if roots.TeamDir != above {
		t.Errorf("TeamDir = %q, want %q", roots.TeamDir, above)
	}
}

func TestResolveContexts_HomeGraphOpsContentAppearsOnce(t *testing.T) {
	home, _, nested := setupHome(t)
	userRoot := filepath.Join(home, configDirName)
	const marker = "USER-TIER-MARKER-DFLT-00068"
	writeExtensionFile(t, userRoot, NodeTypesSubdir, "plan.md", marker)
	writeExtensionFile(t, userRoot, SkillsSubdir, "process-ticket.md", marker)

	roots, err := ResolveRoots(nested, "", "")
	if err != nil {
		t.Fatalf("ResolveRoots: %v", err)
	}
	if n := strings.Count(ResolveNodeTypeContext(roots, "plan"), marker); n != 1 {
		t.Errorf("node type context contains the user-tier text %d times, want 1", n)
	}
	if n := strings.Count(ResolveSkillContext(roots, "process-ticket"), marker); n != 1 {
		t.Errorf("skill context contains the user-tier text %d times, want 1", n)
	}
}

func TestResolveRoots_SameOverridesKeepUserOnly(t *testing.T) {
	dir := t.TempDir()
	for _, team := range []string{dir, dir + string(filepath.Separator), filepath.Join(dir, "x", "..")} {
		roots, err := ResolveRoots(t.TempDir(), dir, team)
		if err != nil {
			t.Fatalf("ResolveRoots: %v", err)
		}
		if roots.UserDir != dir || roots.TeamDir != "" {
			t.Errorf("team override %q: got %+v, want UserDir=%q and TeamDir=\"\"", team, roots, dir)
		}
	}
}

func TestResolveRoots_UserOverrideEqualToDiscoveredTeamRoot(t *testing.T) {
	proj := t.TempDir()
	teamDir := filepath.Join(proj, configDirName)
	mkdirAll(t, teamDir)

	roots, err := ResolveRoots(proj, teamDir, "")
	if err != nil {
		t.Fatalf("ResolveRoots: %v", err)
	}
	if roots.UserDir != teamDir || roots.TeamDir != "" {
		t.Errorf("got %+v, want the shared directory as the user root only", roots)
	}
}

// symlinkedHome makes a real home directory and a symlink pointing at it,
// each containing (through the link) .graph-ops and proj/sub, and returns
// (realHome, linkHome).
func symlinkedHome(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	real := filepath.Join(base, "real-home")
	mkdirAll(t, filepath.Join(real, configDirName))
	mkdirAll(t, filepath.Join(real, "proj", "sub"))
	link := filepath.Join(base, "link-home")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return real, link
}

func TestResolveRoots_HomeViaSymlink_StartDirViaRealPath(t *testing.T) {
	real, link := symlinkedHome(t)
	t.Setenv("HOME", link)

	roots, err := ResolveRoots(filepath.Join(real, "proj", "sub"), "", "")
	if err != nil {
		t.Fatalf("ResolveRoots: %v", err)
	}
	if roots.TeamDir != "" {
		t.Errorf("TeamDir = %q, want \"\" (same directory as HOME/.graph-ops through a symlink)", roots.TeamDir)
	}
}

func TestResolveRoots_HomeViaRealPath_StartDirViaSymlink(t *testing.T) {
	real, link := symlinkedHome(t)
	t.Setenv("HOME", real)

	roots, err := ResolveRoots(filepath.Join(link, "proj", "sub"), "", "")
	if err != nil {
		t.Fatalf("ResolveRoots: %v", err)
	}
	if roots.TeamDir != "" {
		t.Errorf("TeamDir = %q, want \"\" (same directory as HOME/.graph-ops through a symlink)", roots.TeamDir)
	}
}

func TestResolveRoots_TempDirHomeWithOSSymlinks(t *testing.T) {
	// On macOS t.TempDir() is under /var, a symlink to /private/var. Check
	// both spellings of the same home in both directions.
	home, _, nested := setupHome(t)
	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	resolvedNested := filepath.Join(resolvedHome, "proj", "sub")
	cases := []struct{ homeEnv, start string }{
		{home, resolvedNested},
		{resolvedHome, nested},
	}
	for _, c := range cases {
		t.Setenv("HOME", c.homeEnv)
		roots, err := ResolveRoots(c.start, "", "")
		if err != nil {
			t.Fatalf("ResolveRoots: %v", err)
		}
		if roots.TeamDir != "" {
			t.Errorf("HOME=%q start=%q: TeamDir = %q, want \"\"", c.homeEnv, c.start, roots.TeamDir)
		}
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

func TestProjectTeamRoot_UnderHomeWithoutOwnGraphOps(t *testing.T) {
	home, proj, _ := setupHome(t)

	got, err := ProjectTeamRoot(proj)
	if err != nil {
		t.Fatalf("ProjectTeamRoot: %v", err)
	}
	if want := filepath.Join(proj, configDirName); got != want {
		t.Errorf("ProjectTeamRoot = %q, want %q (not %q)", got, want, filepath.Join(home, configDirName))
	}
}

func TestProjectTeamRoot_HomeItselfIsRejected(t *testing.T) {
	home, _, _ := setupHome(t)

	if _, err := ProjectTeamRoot(home); !errors.Is(err, ErrTeamRootIsUserRoot) {
		t.Errorf("ProjectTeamRoot(home) error = %v, want ErrTeamRootIsUserRoot", err)
	}
	if _, err := ProjectTeamConfigPath(home); !errors.Is(err, ErrTeamRootIsUserRoot) {
		t.Errorf("ProjectTeamConfigPath(home) error = %v, want ErrTeamRootIsUserRoot", err)
	}
}

func TestLoadWithRoots_UserLanguageStillReadUnderHome(t *testing.T) {
	home, _, nested := setupHome(t)
	if err := os.WriteFile(filepath.Join(home, configDirName, userConfigFile), []byte("language: ja\n"), 0o644); err != nil {
		t.Fatalf("write config.yaml: %v", err)
	}

	cat, err := LoadWithRoots(nested, "", "", "")
	if err != nil {
		t.Fatalf("LoadWithRoots: %v", err)
	}
	if cat.Language != "ja" {
		t.Errorf("Language = %q, want \"ja\" from the user tier", cat.Language)
	}
}
