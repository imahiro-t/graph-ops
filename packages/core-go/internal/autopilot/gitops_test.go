package autopilot

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00142 phase 3: the git side of the autopilot, always against a
// throwaway repository in a temp dir.

// newGitRepo creates a temp repository on branch main with one commit.
func newGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	// Resolve symlinks (macOS /var -> /private/var) so paths compare equal
	// to what git reports.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	gitT(t, dir, "init", "-q", "-b", "main")
	commitFile(t, dir, "README.md", "hello\n", "initial")
	return dir
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitFile(t *testing.T, dir, name, content, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", name)
	gitT(t, dir, "commit", "-q", "-m", msg)
	return gitT(t, dir, "rev-parse", "HEAD")
}

func assertCode(t *testing.T, err error, code domain.ErrorCode) {
	t.Helper()
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
}

func TestGit_DefaultBranch(t *testing.T) {
	repo := newGitRepo(t)
	g := Git{}
	if got := g.DefaultBranch(repo); got != "main" {
		t.Fatalf("no origin: DefaultBranch = %q, want main", got)
	}
	// origin/HEAD naming a local branch wins.
	gitT(t, repo, "branch", "trunk")
	gitT(t, repo, "update-ref", "refs/remotes/origin/trunk", "HEAD")
	gitT(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
	if got := g.DefaultBranch(repo); got != "trunk" {
		t.Fatalf("DefaultBranch = %q, want trunk", got)
	}
}

func TestGit_EnsureWorktreeCreatesAndReuses(t *testing.T) {
	repo := newGitRepo(t)
	g := Git{}
	path, branch, created, err := g.EnsureWorktree(repo, "T-1", "main")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(repo, ".claude", "worktrees", "T-1") || branch != "worktree-T-1" || !created {
		t.Fatalf("path=%s branch=%s created=%v", path, branch, created)
	}
	if head := gitT(t, path, "rev-parse", "HEAD"); head != gitT(t, repo, "rev-parse", "main") {
		t.Fatalf("worktree HEAD %s is not main's tip", head)
	}
	path2, _, created2, err := g.EnsureWorktree(repo, "T-1", "main")
	if err != nil || path2 != path || created2 {
		t.Fatalf("reuse: path=%s created=%v err=%v", path2, created2, err)
	}
	// A branch without a worktree is checked out, not re-created from base.
	gitT(t, repo, "branch", "worktree-T-2", "main")
	sha := commitFile(t, path, "a.txt", "a", "a on T-1")
	gitT(t, repo, "branch", "-f", "worktree-T-2", sha)
	path3, _, created3, err := g.EnsureWorktree(repo, "T-2", "main")
	if err != nil || !created3 {
		t.Fatalf("existing branch: created=%v err=%v", created3, err)
	}
	if head := gitT(t, path3, "rev-parse", "HEAD"); head != sha {
		t.Fatalf("existing branch not reused: HEAD %s, want %s", head, sha)
	}
}

func TestGit_FastForwardMovesUncheckedOutBranch(t *testing.T) {
	repo := newGitRepo(t)
	g := Git{}
	gitT(t, repo, "branch", "parent", "main")
	child, _, _, err := g.EnsureWorktree(repo, "C", "parent")
	if err != nil {
		t.Fatal(err)
	}
	sha := commitFile(t, child, "c.txt", "c", "c")
	moved, err := g.FastForward(repo, "worktree-C", "parent")
	if err != nil || !moved {
		t.Fatalf("moved=%v err=%v", moved, err)
	}
	if got := gitT(t, repo, "rev-parse", "parent"); got != sha {
		t.Fatalf("parent = %s, want %s", got, sha)
	}
	// No merge commit: parent's tip is the child's commit itself.
	if n := gitT(t, repo, "rev-list", "--count", "--merges", "parent"); n != "0" {
		t.Fatalf("merge commits = %s", n)
	}
	// Doing it again is a no-op.
	moved, err = g.FastForward(repo, "worktree-C", "parent")
	if err != nil || moved {
		t.Fatalf("second: moved=%v err=%v", moved, err)
	}
}

func TestGit_FastForwardInCheckedOutWorktreeUpdatesFiles(t *testing.T) {
	repo := newGitRepo(t)
	g := Git{}
	parent, _, _, err := g.EnsureWorktree(repo, "P", "main")
	if err != nil {
		t.Fatal(err)
	}
	child, _, _, err := g.EnsureWorktree(repo, "C", "worktree-P")
	if err != nil {
		t.Fatal(err)
	}
	commitFile(t, child, "c.txt", "from child", "c")
	if _, err := g.FastForward(repo, "worktree-C", "worktree-P"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(parent, "c.txt"))
	if err != nil || string(data) != "from child" {
		t.Fatalf("parent worktree file = %q, %v", data, err)
	}
	if status := gitT(t, parent, "status", "--porcelain"); status != "" {
		t.Fatalf("parent worktree not clean: %q", status)
	}
}

func TestGit_FastForwardRefusesNonFastForward(t *testing.T) {
	repo := newGitRepo(t)
	g := Git{}
	parent, _, _, _ := g.EnsureWorktree(repo, "P", "main")
	child, _, _, _ := g.EnsureWorktree(repo, "C", "worktree-P")
	commitFile(t, child, "c.txt", "c", "c")
	before := commitFile(t, parent, "p.txt", "p", "p (not in C)")
	_, err := g.FastForward(repo, "worktree-C", "worktree-P")
	assertCode(t, err, ErrCodeNotFastForward)
	if got := gitT(t, repo, "rev-parse", "worktree-P"); got != before {
		t.Fatalf("parent moved to %s", got)
	}
}

func TestGit_FastForwardRefusesDirtyTargetWorktree(t *testing.T) {
	repo := newGitRepo(t)
	g := Git{}
	parent, _, _, _ := g.EnsureWorktree(repo, "P", "main")
	child, _, _, _ := g.EnsureWorktree(repo, "C", "worktree-P")
	commitFile(t, child, "c.txt", "c", "c")
	before := gitT(t, repo, "rev-parse", "worktree-P")
	if err := os.WriteFile(filepath.Join(parent, "README.md"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := g.FastForward(repo, "worktree-C", "worktree-P")
	assertCode(t, err, ErrCodeParentDirty)
	if got := gitT(t, repo, "rev-parse", "worktree-P"); got != before {
		t.Fatalf("parent moved")
	}
	if data, _ := os.ReadFile(filepath.Join(parent, "README.md")); string(data) != "uncommitted\n" {
		t.Fatalf("uncommitted change lost: %q", data)
	}
}

func TestGit_WorktreeFingerprintChangesWithWork(t *testing.T) {
	repo := newGitRepo(t)
	g := Git{}
	wt, _, _, _ := g.EnsureWorktree(repo, "T", "main")
	fp0 := g.WorktreeFingerprint(wt)
	if fp0 == "" || fp0 != g.WorktreeFingerprint(wt) {
		t.Fatalf("fingerprint not stable: %q", fp0)
	}
	os.WriteFile(filepath.Join(wt, "README.md"), []byte("edit 1\n"), 0o644)
	fp1 := g.WorktreeFingerprint(wt)
	if fp1 == fp0 {
		t.Fatal("an uncommitted change did not change the fingerprint")
	}
	// Editing the already-modified file again still changes it.
	os.WriteFile(filepath.Join(wt, "README.md"), []byte("edit 2, longer\n"), 0o644)
	fp2 := g.WorktreeFingerprint(wt)
	if fp2 == fp1 {
		t.Fatal("a second edit of a modified file did not change the fingerprint")
	}
	gitT(t, wt, "commit", "-q", "-am", "x")
	if g.WorktreeFingerprint(wt) == fp2 {
		t.Fatal("a commit did not change the fingerprint")
	}
	if g.WorktreeFingerprint(filepath.Join(repo, "missing")) != "" {
		t.Fatal("a missing worktree should give an empty fingerprint")
	}
}
