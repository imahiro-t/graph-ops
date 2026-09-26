// Package claudetrust tells, on a best-effort basis, whether Claude Code has
// not yet trusted a folder (DFLT-00182), so the autopilot can say so before
// the first session it opens there stops at the workspace trust dialog.
//
// Claude Code keeps the trust you accept in ~/.claude.json, as
// projects["<path>"].hasTrustDialogAccepted (the permissions page of the
// Claude Code documentation names this key, but the file as a whole is
// Claude Code's own and has no published schema). Inside a git repository
// the key is the repository root -- for a worktree, the main checkout's
// root -- and outside one, a trusted folder covers its subfolders. There is
// no command or flag that answers the question, so this package reads the
// file.
//
// Because the file can change shape with any Claude Code update, Check is
// deliberately one-sided: it answers Untrusted only when the file looks
// exactly as expected and no entry covering the folder is trusted. Whenever
// it is unsure -- the file is missing, unreadable, too large or not the
// expected shape, the folder is the home directory (whose trust is never
// saved), CLAUDE_CONFIG_DIR moves the file, the platform is Windows, the path
// has non-ASCII characters whose Unicode normalization could differ from the
// key's, or a git worktree's main checkout cannot be told -- it answers "not
// untrusted". The worst a format change can do is make the notice
// disappear, never claim a trusted folder is untrusted; and the answer is
// only ever a notice, never a reason to refuse or stop a launch.
package claudetrust

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// MaxConfigBytes is the largest ~/.claude.json Check reads; a larger file
// is not judged.
const MaxConfigBytes = 16 << 20

// maxGitFileBytes bounds the read of a worktree's .git file.
const maxGitFileBytes = 4 << 10

// Result is Check's answer.
type Result struct {
	// Untrusted is true only when the folder was judged not trusted.
	Untrusted bool
	// Path is the folder's resolved path when Untrusted is true.
	Path string
}

// Check reports whether Claude Code has not trusted dir, reading
// <homeDir>/.claude.json. It never fails and never panics: anything it
// cannot judge with confidence comes back as Result{} (not untrusted).
// dir must exist.
func Check(homeDir, dir string) (res Result) {
	defer func() {
		if recover() != nil {
			res = Result{}
		}
	}()
	if homeDir == "" || dir == "" || runtime.GOOS == "windows" {
		return Result{}
	}
	if os.Getenv("CLAUDE_CONFIG_DIR") != "" {
		return Result{}
	}
	real, ok := resolve(dir)
	if !ok || !isASCII(real) {
		return Result{}
	}
	home, ok := resolve(homeDir)
	if !ok {
		home = filepath.Clean(homeDir)
	}
	if strings.EqualFold(real, home) {
		return Result{}
	}

	trusted, ok := trustedKeys(filepath.Join(homeDir, ".claude.json"))
	if !ok {
		return Result{}
	}
	candidates := ancestors(real)
	mainRoot, ok := worktreeMainRoot(real)
	if !ok {
		return Result{}
	}
	if mainRoot != "" {
		candidates = append(candidates, ancestors(mainRoot)...)
	}
	for _, c := range candidates {
		for _, k := range trusted {
			if strings.EqualFold(c, k) {
				return Result{}
			}
		}
	}
	return Result{Untrusted: true, Path: real}
}

// resolve returns p with symlinks resolved and cleaned; false when that
// fails (including when p does not exist).
func resolve(p string) (string, bool) {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", false
	}
	abs, err := filepath.Abs(r)
	if err != nil {
		return "", false
	}
	return filepath.Clean(abs), true
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// ancestors returns p and every parent of p up to the root.
func ancestors(p string) []string {
	var out []string
	for {
		out = append(out, p)
		parent := filepath.Dir(p)
		if parent == p {
			return out
		}
		p = parent
	}
}

// trustedKeys reads the config file and returns the keys of the projects
// entries whose hasTrustDialogAccepted is true (cleaned, and also with
// symlinks resolved when that works). ok is false unless the file parses,
// its top level and "projects" are objects, and at least one entry has a
// boolean hasTrustDialogAccepted -- the evidence that the format is still
// the one this package understands.
func trustedKeys(path string) (keys []string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if err != nil || len(data) > MaxConfigBytes {
		return nil, false
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil || top == nil {
		return nil, false
	}
	var projects map[string]json.RawMessage
	if raw, found := top["projects"]; !found || json.Unmarshal(raw, &projects) != nil || projects == nil {
		return nil, false
	}
	sawBool := false
	for key, raw := range projects {
		var entry map[string]json.RawMessage
		if json.Unmarshal(raw, &entry) != nil || entry == nil {
			continue
		}
		v, found := entry["hasTrustDialogAccepted"]
		if !found {
			continue
		}
		var accepted bool
		if json.Unmarshal(v, &accepted) != nil {
			continue
		}
		sawBool = true
		if !accepted || key == "" {
			continue
		}
		clean := filepath.Clean(key)
		keys = append(keys, clean)
		if r, rok := resolve(clean); rok && r != clean {
			keys = append(keys, r)
		}
	}
	if !sawBool {
		return nil, false
	}
	return keys, true
}

// worktreeMainRoot finds the git repository dir is in and, when it is a
// linked worktree (its .git is a file pointing into
// <main>/.git/worktrees/<name>), returns <main>: Claude Code keys a
// worktree's trust on the main checkout's root, which need not be an
// ancestor of the worktree. It returns "" when dir is in an ordinary
// checkout, a submodule or no repository at all, and ok=false when a .git
// file is there but cannot be read or points into a worktree layout whose
// main checkout cannot be told (a bare repository's worktree, say).
func worktreeMainRoot(dir string) (mainRoot string, ok bool) {
	for _, p := range ancestors(dir) {
		gitPath := filepath.Join(p, ".git")
		fi, err := os.Lstat(gitPath)
		if err != nil {
			continue
		}
		if fi.IsDir() {
			return "", true
		}
		if !fi.Mode().IsRegular() {
			return "", false
		}
		gitdir, gok := readGitdir(gitPath)
		if !gok {
			return "", false
		}
		if !filepath.IsAbs(gitdir) {
			gitdir = filepath.Join(p, gitdir)
		}
		gitdir = filepath.Clean(gitdir)
		sep := string(filepath.Separator)
		marker := sep + ".git" + sep + "worktrees" + sep
		if i := strings.LastIndex(gitdir, marker); i > 0 {
			root := gitdir[:i]
			if r, rok := resolve(root); rok {
				root = r
			}
			return root, true
		}
		if filepath.Base(filepath.Dir(gitdir)) == "worktrees" {
			return "", false
		}
		// A submodule or a separate git dir: the folder's own repository
		// root (p) is already among the candidates.
		return "", true
	}
	return "", true
}

func readGitdir(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxGitFileBytes+1))
	if err != nil || len(data) > maxGitFileBytes {
		return "", false
	}
	line := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if gitdir == "" {
		return "", false
	}
	return gitdir, true
}
