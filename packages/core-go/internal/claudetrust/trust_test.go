package claudetrust

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Every test uses a temp directory as the home directory: the real user's
// ~/.claude.json is never read.

func realTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mkdir(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func writeConfig(t *testing.T, home string, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeGitFile writes a linked worktree's or submodule's .git file.
func writeGitFile(t *testing.T, dir, gitdir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// projectsJSON builds a ~/.claude.json with the given hasTrustDialogAccepted
// values (a nil value leaves the key out of the entry).
// makeSubmodule lays out <root>/outer as a repository with a submodule at
// outer/lib whose .git file points to ../.git/modules/lib, the way
// `git submodule add` leaves it, and returns both paths.
func makeSubmodule(t *testing.T, root string) (outer, sub string) {
	t.Helper()
	outer = mkdir(t, filepath.Join(root, "outer"))
	mkdir(t, filepath.Join(outer, ".git", "modules", "lib"))
	sub = mkdir(t, filepath.Join(outer, "lib"))
	writeGitFile(t, sub, "../.git/modules/lib")
	return outer, sub
}

func projectsJSON(t *testing.T, entries map[string]any) string {
	t.Helper()
	projects := map[string]any{}
	for k, v := range entries {
		e := map[string]any{"allowedTools": []string{}}
		if v != nil {
			e["hasTrustDialogAccepted"] = v
		}
		projects[k] = e
	}
	b, err := json.Marshal(map[string]any{"numStartups": 3, "projects": projects})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCheck(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if runtime.GOOS == "windows" {
		t.Skip("Check never judges on Windows")
	}

	type setup struct {
		home, root string // root is a work area outside home
	}
	cases := []struct {
		name string
		// prepare returns the dir to check.
		prepare   func(t *testing.T, s setup) string
		untrusted bool
	}{
		{
			name: "no entry for the folder or any parent",
			prepare: func(t *testing.T, s setup) string {
				other := mkdir(t, filepath.Join(s.root, "other"))
				writeConfig(t, s.home, projectsJSON(t, map[string]any{other: true}))
				return mkdir(t, filepath.Join(s.root, "proj"))
			},
			untrusted: true,
		},
		{
			name: "only false entries for the folder and its parent",
			prepare: func(t *testing.T, s setup) string {
				dir := mkdir(t, filepath.Join(s.root, "proj", "sub"))
				writeConfig(t, s.home, projectsJSON(t, map[string]any{dir: false, filepath.Dir(dir): false}))
				return dir
			},
			untrusted: true,
		},
		{
			name: "the folder itself is trusted",
			prepare: func(t *testing.T, s setup) string {
				dir := mkdir(t, filepath.Join(s.root, "proj"))
				writeConfig(t, s.home, projectsJSON(t, map[string]any{dir: true}))
				return dir
			},
		},
		{
			name: "a parent is trusted outside any repository, the folder has a false entry",
			prepare: func(t *testing.T, s setup) string {
				parent := mkdir(t, filepath.Join(s.root, "dev"))
				dir := mkdir(t, filepath.Join(parent, "notes", "sub"))
				writeConfig(t, s.home, projectsJSON(t, map[string]any{parent: true, dir: false}))
				return dir
			},
		},
		{
			name: "a worktree under the trusted main checkout (the autopilot's layout), the worktree has a false entry",
			prepare: func(t *testing.T, s setup) string {
				repo := mkdir(t, filepath.Join(s.root, "repo"))
				mkdir(t, filepath.Join(repo, ".git", "worktrees", "T-1"))
				wt := mkdir(t, filepath.Join(repo, ".claude", "worktrees", "T-1"))
				writeGitFile(t, wt, filepath.Join(repo, ".git", "worktrees", "T-1"))
				writeConfig(t, s.home, projectsJSON(t, map[string]any{repo: true, wt: false}))
				return wt
			},
		},
		{
			name: "a repository under a trusted folder that is not a repository (a clone under ~/dev)",
			prepare: func(t *testing.T, s setup) string {
				parent := mkdir(t, filepath.Join(s.root, "dev"))
				repo := mkdir(t, filepath.Join(parent, "newrepo"))
				mkdir(t, filepath.Join(repo, ".git"))
				writeConfig(t, s.home, projectsJSON(t, map[string]any{parent: true}))
				return repo
			},
			untrusted: true,
		},
		{
			name: "a subfolder of a repository under a trusted folder that is not a repository",
			prepare: func(t *testing.T, s setup) string {
				parent := mkdir(t, filepath.Join(s.root, "dev"))
				repo := mkdir(t, filepath.Join(parent, "newrepo"))
				mkdir(t, filepath.Join(repo, ".git"))
				writeConfig(t, s.home, projectsJSON(t, map[string]any{parent: true}))
				return mkdir(t, filepath.Join(repo, "pkg", "sub"))
			},
			untrusted: true,
		},
		{
			name: "a subfolder of a trusted repository root",
			prepare: func(t *testing.T, s setup) string {
				repo := mkdir(t, filepath.Join(s.root, "dev", "repo"))
				mkdir(t, filepath.Join(repo, ".git"))
				writeConfig(t, s.home, projectsJSON(t, map[string]any{repo: true}))
				return mkdir(t, filepath.Join(repo, "pkg", "sub"))
			},
		},
		{
			name: "a key between the folder and its repository root is trusted (kept on the silent side)",
			prepare: func(t *testing.T, s setup) string {
				repo := mkdir(t, filepath.Join(s.root, "repo"))
				mkdir(t, filepath.Join(repo, ".git"))
				pkg := mkdir(t, filepath.Join(repo, "pkg"))
				writeConfig(t, s.home, projectsJSON(t, map[string]any{pkg: true}))
				return mkdir(t, filepath.Join(pkg, "sub"))
			},
		},
		{
			name: "a repository nested inside a trusted repository",
			prepare: func(t *testing.T, s setup) string {
				repo := mkdir(t, filepath.Join(s.root, "repo"))
				mkdir(t, filepath.Join(repo, ".git"))
				nested := mkdir(t, filepath.Join(repo, "vendor", "lib"))
				mkdir(t, filepath.Join(nested, ".git"))
				writeConfig(t, s.home, projectsJSON(t, map[string]any{repo: true}))
				return nested
			},
			untrusted: true,
		},
		// Submodules (a .git file into <outer>/.git/modules/...): the outer
		// repository's trust does not cover them, and their own trust is
		// keyed on the submodule's root -- both confirmed with Claude Code
		// 2.1.283 (DFLT-00185).
		{
			name: "a submodule's root with only the outer repository trusted (confirmed: the dialog appears)",
			prepare: func(t *testing.T, s setup) string {
				repo, sub := makeSubmodule(t, s.root)
				writeConfig(t, s.home, projectsJSON(t, map[string]any{repo: true}))
				return sub
			},
			untrusted: true,
		},
		{
			name: "a submodule's subfolder with only the outer repository trusted (confirmed: the dialog appears)",
			prepare: func(t *testing.T, s setup) string {
				repo, sub := makeSubmodule(t, s.root)
				writeConfig(t, s.home, projectsJSON(t, map[string]any{repo: true}))
				return mkdir(t, filepath.Join(sub, "sub"))
			},
			untrusted: true,
		},
		{
			name: "a submodule's root keyed as trusted (confirmed: the key Claude Code saves)",
			prepare: func(t *testing.T, s setup) string {
				_, sub := makeSubmodule(t, s.root)
				writeConfig(t, s.home, projectsJSON(t, map[string]any{sub: true}))
				return sub
			},
		},
		{
			name: "a submodule's subfolder with the submodule's root trusted (confirmed: no dialog)",
			prepare: func(t *testing.T, s setup) string {
				_, sub := makeSubmodule(t, s.root)
				writeConfig(t, s.home, projectsJSON(t, map[string]any{sub: true}))
				return mkdir(t, filepath.Join(sub, "sub"))
			},
		},
		{
			name: "the outer repository with only its submodule trusted (confirmed: the dialog appears)",
			prepare: func(t *testing.T, s setup) string {
				repo, sub := makeSubmodule(t, s.root)
				writeConfig(t, s.home, projectsJSON(t, map[string]any{sub: true}))
				return repo
			},
			untrusted: true,
		},
		{
			name: "a submodule with no trusted entry anywhere (still judged)",
			prepare: func(t *testing.T, s setup) string {
				_, sub := makeSubmodule(t, s.root)
				other := mkdir(t, filepath.Join(s.root, "other"))
				writeConfig(t, s.home, projectsJSON(t, map[string]any{other: true}))
				return sub
			},
			untrusted: true,
		},
		{
			name: "a worktree outside the checkout under a trusted non-repository folder, main checkout not trusted",
			prepare: func(t *testing.T, s setup) string {
				repo := mkdir(t, filepath.Join(s.root, "repo"))
				mkdir(t, filepath.Join(repo, ".git", "worktrees", "T-4"))
				parent := mkdir(t, filepath.Join(s.root, "dev"))
				wt := mkdir(t, filepath.Join(parent, "T-4"))
				writeGitFile(t, wt, filepath.Join(repo, ".git", "worktrees", "T-4"))
				writeConfig(t, s.home, projectsJSON(t, map[string]any{parent: true}))
				return wt
			},
			untrusted: true,
		},
		{
			name: "a worktree whose main checkout is only covered by a trusted non-repository parent",
			prepare: func(t *testing.T, s setup) string {
				parent := mkdir(t, filepath.Join(s.root, "dev"))
				repo := mkdir(t, filepath.Join(parent, "repo"))
				mkdir(t, filepath.Join(repo, ".git", "worktrees", "T-5"))
				wt := mkdir(t, filepath.Join(s.root, "elsewhere", "T-5"))
				writeGitFile(t, wt, filepath.Join(repo, ".git", "worktrees", "T-5"))
				writeConfig(t, s.home, projectsJSON(t, map[string]any{parent: true}))
				return wt
			},
			untrusted: true,
		},
		{
			name: "a worktree outside the checkout whose main checkout is trusted",
			prepare: func(t *testing.T, s setup) string {
				repo := mkdir(t, filepath.Join(s.root, "repo"))
				mkdir(t, filepath.Join(repo, ".git", "worktrees", "T-2"))
				wt := mkdir(t, filepath.Join(s.root, "elsewhere", "T-2"))
				if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+filepath.Join(repo, ".git", "worktrees", "T-2")+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				writeConfig(t, s.home, projectsJSON(t, map[string]any{repo: true}))
				return mkdir(t, filepath.Join(wt, "pkg"))
			},
		},
		{
			name: "a worktree outside the checkout whose main checkout is not trusted",
			prepare: func(t *testing.T, s setup) string {
				repo := mkdir(t, filepath.Join(s.root, "repo"))
				mkdir(t, filepath.Join(repo, ".git", "worktrees", "T-3"))
				wt := mkdir(t, filepath.Join(s.root, "elsewhere", "T-3"))
				if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: ../../repo/.git/worktrees/T-3\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				writeConfig(t, s.home, projectsJSON(t, map[string]any{filepath.Join(s.root, "other"): true}))
				return wt
			},
			untrusted: true,
		},
		{
			name: "an ordinary checkout (.git directory) with no trusted entry",
			prepare: func(t *testing.T, s setup) string {
				repo := mkdir(t, filepath.Join(s.root, "repo"))
				mkdir(t, filepath.Join(repo, ".git"))
				writeConfig(t, s.home, projectsJSON(t, map[string]any{filepath.Join(s.root, "other"): true}))
				return repo
			},
			untrusted: true,
		},
		{
			name: "a .git file that is not a gitdir pointer",
			prepare: func(t *testing.T, s setup) string {
				wt := mkdir(t, filepath.Join(s.root, "odd"))
				if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("garbage\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				writeConfig(t, s.home, projectsJSON(t, map[string]any{filepath.Join(s.root, "other"): true}))
				return wt
			},
		},
		{
			name: "a bare repository's worktree (main checkout unknown)",
			prepare: func(t *testing.T, s setup) string {
				wt := mkdir(t, filepath.Join(s.root, "bare-wt"))
				if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+filepath.Join(s.root, "repo.git", "worktrees", "x")+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				writeConfig(t, s.home, projectsJSON(t, map[string]any{filepath.Join(s.root, "other"): true}))
				return wt
			},
		},
		{
			name: "reached through a symlink, the real path is trusted",
			prepare: func(t *testing.T, s setup) string {
				dir := mkdir(t, filepath.Join(s.root, "real"))
				link := filepath.Join(s.root, "link")
				if err := os.Symlink(dir, link); err != nil {
					t.Fatal(err)
				}
				writeConfig(t, s.home, projectsJSON(t, map[string]any{dir: true}))
				return link
			},
		},
		{
			name: "the key is a symlink to the folder",
			prepare: func(t *testing.T, s setup) string {
				dir := mkdir(t, filepath.Join(s.root, "real2"))
				link := filepath.Join(s.root, "link2")
				if err := os.Symlink(dir, link); err != nil {
					t.Fatal(err)
				}
				writeConfig(t, s.home, projectsJSON(t, map[string]any{link: true}))
				return dir
			},
		},
		{
			name: "a key differing only in case is trusted (the folder)",
			prepare: func(t *testing.T, s setup) string {
				dir := mkdir(t, filepath.Join(s.root, "Proj"))
				writeConfig(t, s.home, projectsJSON(t, map[string]any{strings.ToUpper(dir): true}))
				return dir
			},
		},
		{
			name: "a key differing only in case is trusted (a parent)",
			prepare: func(t *testing.T, s setup) string {
				dir := mkdir(t, filepath.Join(s.root, "Parent", "child"))
				writeConfig(t, s.home, projectsJSON(t, map[string]any{strings.ToLower(filepath.Dir(dir)): true}))
				return dir
			},
		},
		{
			name: "a key with a trailing slash is trusted",
			prepare: func(t *testing.T, s setup) string {
				dir := mkdir(t, filepath.Join(s.root, "slash"))
				writeConfig(t, s.home, projectsJSON(t, map[string]any{dir + "/": true}))
				return dir
			},
		},
		{
			name: "no config file",
			prepare: func(t *testing.T, s setup) string {
				return mkdir(t, filepath.Join(s.root, "proj"))
			},
		},
		{
			name: "broken JSON",
			prepare: func(t *testing.T, s setup) string {
				writeConfig(t, s.home, `{"projects": {`)
				return mkdir(t, filepath.Join(s.root, "proj"))
			},
		},
		{
			name: "top level is an array",
			prepare: func(t *testing.T, s setup) string {
				writeConfig(t, s.home, `[{"projects": {}}]`)
				return mkdir(t, filepath.Join(s.root, "proj"))
			},
		},
		{
			name: "top level is null",
			prepare: func(t *testing.T, s setup) string {
				writeConfig(t, s.home, `null`)
				return mkdir(t, filepath.Join(s.root, "proj"))
			},
		},
		{
			name: "no projects",
			prepare: func(t *testing.T, s setup) string {
				writeConfig(t, s.home, `{"numStartups": 1}`)
				return mkdir(t, filepath.Join(s.root, "proj"))
			},
		},
		{
			name: "projects is not an object",
			prepare: func(t *testing.T, s setup) string {
				writeConfig(t, s.home, `{"projects": ["a"]}`)
				return mkdir(t, filepath.Join(s.root, "proj"))
			},
		},
		{
			name: "projects is empty",
			prepare: func(t *testing.T, s setup) string {
				writeConfig(t, s.home, `{"projects": {}}`)
				return mkdir(t, filepath.Join(s.root, "proj"))
			},
		},
		{
			name: "no entry has hasTrustDialogAccepted (a renamed key)",
			prepare: func(t *testing.T, s setup) string {
				writeConfig(t, s.home, projectsJSON(t, map[string]any{filepath.Join(s.root, "other"): nil}))
				return mkdir(t, filepath.Join(s.root, "proj"))
			},
		},
		{
			name: "hasTrustDialogAccepted is a string",
			prepare: func(t *testing.T, s setup) string {
				writeConfig(t, s.home, projectsJSON(t, map[string]any{filepath.Join(s.root, "other"): "true"}))
				return mkdir(t, filepath.Join(s.root, "proj"))
			},
		},
		{
			name: "the file is over the size limit",
			prepare: func(t *testing.T, s setup) string {
				other := filepath.Join(s.root, "other")
				body := projectsJSON(t, map[string]any{other: true})
				pad := strings.Repeat(" ", MaxConfigBytes)
				writeConfig(t, s.home, body[:len(body)-1]+pad+"}")
				return mkdir(t, filepath.Join(s.root, "proj"))
			},
		},
		{
			name: "the folder is the home directory",
			prepare: func(t *testing.T, s setup) string {
				writeConfig(t, s.home, projectsJSON(t, map[string]any{filepath.Join(s.root, "other"): true}))
				return s.home
			},
		},
		{
			name: "the folder is the home directory spelled in another case",
			prepare: func(t *testing.T, s setup) string {
				writeConfig(t, s.home, projectsJSON(t, map[string]any{filepath.Join(s.root, "other"): true}))
				// On a case-sensitive file system the other spelling is a
				// different, missing folder, which is not judged either.
				return filepath.Join(filepath.Dir(s.home), strings.ToUpper(filepath.Base(s.home)))
			},
		},
		{
			name: "the path has non-ASCII characters",
			prepare: func(t *testing.T, s setup) string {
				writeConfig(t, s.home, projectsJSON(t, map[string]any{filepath.Join(s.root, "other"): true}))
				return mkdir(t, filepath.Join(s.root, "プロジェクト"))
			},
		},
		{
			name: "the folder does not exist",
			prepare: func(t *testing.T, s setup) string {
				writeConfig(t, s.home, projectsJSON(t, map[string]any{filepath.Join(s.root, "other"): true}))
				return filepath.Join(s.root, "missing")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := realTempDir(t)
			s := setup{home: mkdir(t, filepath.Join(base, "home")), root: mkdir(t, filepath.Join(base, "work"))}
			dir := tc.prepare(t, s)
			got := Check(s.home, dir)
			if got.Untrusted != tc.untrusted {
				t.Fatalf("Check(%q).Untrusted = %v, want %v", dir, got.Untrusted, tc.untrusted)
			}
			if tc.untrusted {
				want, _ := filepath.EvalSymlinks(dir)
				if got.Path != want {
					t.Errorf("Path = %q, want %q", got.Path, want)
				}
			} else if got.Path != "" {
				t.Errorf("Path = %q, want empty", got.Path)
			}
		})
	}
}

func TestCheckUnreadableConfig(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if runtime.GOOS == "windows" {
		t.Skip("permissions and Windows are not judged")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	base := realTempDir(t)
	home := mkdir(t, filepath.Join(base, "home"))
	dir := mkdir(t, filepath.Join(base, "proj"))
	writeConfig(t, home, projectsJSON(t, map[string]any{filepath.Join(base, "other"): true}))
	if err := os.Chmod(filepath.Join(home, ".claude.json"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(home, ".claude.json"), 0o600) })
	if got := Check(home, dir); got.Untrusted {
		t.Fatalf("an unreadable config was judged: %+v", got)
	}
}

func TestCheckClaudeConfigDir(t *testing.T) {
	base := realTempDir(t)
	home := mkdir(t, filepath.Join(base, "home"))
	dir := mkdir(t, filepath.Join(base, "proj"))
	writeConfig(t, home, projectsJSON(t, map[string]any{filepath.Join(base, "other"): true}))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if runtime.GOOS != "windows" {
		if got := Check(home, dir); !got.Untrusted {
			t.Fatalf("precondition: the folder should be judged untrusted without CLAUDE_CONFIG_DIR")
		}
	}
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(base, "cfg"))
	if got := Check(home, dir); got.Untrusted {
		t.Fatalf("judged with CLAUDE_CONFIG_DIR set: %+v", got)
	}
}

func TestCheckEmptyArguments(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	base := realTempDir(t)
	if got := Check("", base); got.Untrusted {
		t.Fatalf("judged with no home: %+v", got)
	}
	if got := Check(base, ""); got.Untrusted {
		t.Fatalf("judged with no dir: %+v", got)
	}
}
