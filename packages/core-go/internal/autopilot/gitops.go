package autopilot

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

// This file is the autopilot's git side (plan phase 3-3, decision D6): the
// default branch, a ticket's worktree and branch, and the fast-forward that
// both merge-into-parent and merge-up use. git is run as a subprocess; the
// repository is the project's local path.

// WorktreeDirName is where ticket worktrees live inside the project, the same
// place Claude Code's EnterWorktree puts them, so a person can re-enter one.
const WorktreeDirName = ".claude/worktrees"

// BranchFor returns the branch of ticketID's worktree (EnterWorktree's
// naming).
func BranchFor(ticketID string) string { return "worktree-" + ticketID }

// WorktreePath returns ticketID's worktree path inside repo.
func WorktreePath(repo, ticketID string) string {
	return filepath.Join(repo, filepath.FromSlash(WorktreeDirName), ticketID)
}

// Git runs git in a repository.
type Git struct {
	// Bin is the git executable; "" means "git".
	Bin string
	// Timeout bounds one git invocation; 0 means 5 minutes.
	Timeout time.Duration
}

func (g Git) run(dir string, args ...string) (string, error) {
	bin := g.Bin
	if bin == "" {
		bin = "git"
	}
	timeout := g.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{"-C", dir}, args...)...)
	// Never prompt (credentials, editors): nobody is there to answer.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true", "GIT_MERGE_AUTOEDIT=no")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), &GitError{Args: args, Msg: msg, Err: err}
	}
	return stdout.String(), nil
}

// GitError is a failed git invocation.
type GitError struct {
	Args []string
	Msg  string
	Err  error
}

func (e *GitError) Error() string { return "git " + strings.Join(e.Args, " ") + ": " + e.Msg }
func (e *GitError) Unwrap() error { return e.Err }

// DefaultBranch returns the repository's default branch: the local branch
// origin/HEAD names, or "main" when that cannot be determined (D6).
func (g Git) DefaultBranch(repo string) string {
	out, err := g.run(repo, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err == nil {
		name := strings.TrimSpace(out)
		if i := strings.Index(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		if name != "" && g.BranchExists(repo, name) {
			return name
		}
	}
	return "main"
}

// BranchExists reports whether a local branch exists.
func (g Git) BranchExists(repo, branch string) bool {
	_, err := g.run(repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// RevParse returns the commit a ref points at.
func (g Git) RevParse(repo, ref string) (string, error) {
	out, err := g.run(repo, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// IsAncestor reports whether a is an ancestor of (or equal to) b.
func (g Git) IsAncestor(repo, a, b string) (bool, error) {
	_, err := g.run(repo, "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// Worktree is one entry of `git worktree list`.
type Worktree struct {
	Path   string
	Branch string // short name; "" when detached
}

// Worktrees lists the repository's worktrees.
func (g Git) Worktrees(repo string) ([]Worktree, error) {
	out, err := g.run(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var list []Worktree
	var cur *Worktree
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			list = append(list, Worktree{Path: strings.TrimPrefix(line, "worktree ")})
			cur = &list[len(list)-1]
		case strings.HasPrefix(line, "branch ") && cur != nil:
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	return list, nil
}

// WorktreeFor returns the path of the worktree that has branch checked out,
// "" if none has.
func (g Git) WorktreeFor(repo, branch string) (string, error) {
	list, err := g.Worktrees(repo)
	if err != nil {
		return "", err
	}
	for _, w := range list {
		if w.Branch == branch {
			return w.Path, nil
		}
	}
	return "", nil
}

// EnsureWorktree makes sure ticketID has its worktree and branch
// (worktree-<ticketID>), creating the branch from base when it does not exist
// yet. An existing worktree or branch is reused as it is (resumption).
// It returns the worktree path, the branch, and whether anything was created.
func (g Git) EnsureWorktree(repo, ticketID, base string) (path, branch string, created bool, err error) {
	branch = BranchFor(ticketID)
	path = WorktreePath(repo, ticketID)
	existing, err := g.WorktreeFor(repo, branch)
	if err != nil {
		return "", "", false, err
	}
	if existing != "" {
		return existing, branch, false, nil
	}
	if _, statErr := os.Stat(path); statErr == nil {
		return "", "", false, fmt.Errorf("%s already exists but is not the worktree of branch %s; move it away and retry", path, branch)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", false, err
	}
	if g.BranchExists(repo, branch) {
		_, err = g.run(repo, "worktree", "add", path, branch)
	} else {
		if base == "" {
			return "", "", false, errors.New("no base branch to create " + branch + " from")
		}
		_, err = g.run(repo, "worktree", "add", "-b", branch, path, base)
	}
	if err != nil {
		return "", "", false, err
	}
	return path, branch, true, nil
}

// FastForward moves branch target to source's tip, fast-forward only
// (merge-into-parent and merge-up, plan phase 3-3). When target is checked
// out in a worktree, the fast-forward happens there with `merge --ff-only`
// (so that worktree's files follow); otherwise the ref is moved with
// update-ref after merge-base has confirmed it is a fast-forward. A target
// that already contains source is left as it is (nothing to do).
//
// It changes nothing and returns an APIError with a distinguishable code
// when it cannot: NOT_FAST_FORWARD when target has commits source lacks, and
// PARENT_WORKTREE_DIRTY when the worktree holding target has uncommitted
// changes to tracked files (S9).
func (g Git) FastForward(repo, source, target string) (moved bool, err error) {
	srcSHA, err := g.RevParse(repo, "refs/heads/"+source)
	if err != nil {
		return false, fmt.Errorf("branch %s not found: %w", source, err)
	}
	dstSHA, err := g.RevParse(repo, "refs/heads/"+target)
	if err != nil {
		return false, fmt.Errorf("branch %s not found: %w", target, err)
	}
	if srcSHA == dstSHA {
		return false, nil
	}
	if contained, err := g.IsAncestor(repo, srcSHA, dstSHA); err != nil {
		return false, err
	} else if contained {
		return false, nil
	}
	ff, err := g.IsAncestor(repo, dstSHA, srcSHA)
	if err != nil {
		return false, err
	}
	if !ff {
		return false, domain.NewAPIError(ErrCodeNotFastForward,
			"NOT_FAST_FORWARD: %s has commits that %s does not contain, so it cannot be fast-forwarded; merge %s into %s first", target, source, target, source)
	}
	wt, err := g.WorktreeFor(repo, target)
	if err != nil {
		return false, err
	}
	if wt != "" {
		status, err := g.run(wt, "status", "--porcelain", "--untracked-files=no")
		if err != nil {
			return false, err
		}
		if strings.TrimSpace(status) != "" {
			return false, domain.NewAPIError(ErrCodeParentDirty,
				"PARENT_WORKTREE_DIRTY: the worktree %s that has %s checked out has uncommitted changes; nothing was changed", wt, target)
		}
		if _, err := g.run(wt, "merge", "--ff-only", "--quiet", srcSHA); err != nil {
			return false, err
		}
		return true, nil
	}
	if _, err := g.run(repo, "update-ref", "refs/heads/"+target, srcSHA, dstSHA); err != nil {
		return false, err
	}
	return true, nil
}

// WorktreeFingerprint hashes what a worktree looks like right now -- HEAD,
// `git status --porcelain`, and the size and modification time of every
// file that status lists -- so a change between two polls is a sign that a
// session is at work in it (D4-2). The file stats matter: a file that is
// already modified shows the same porcelain line however often it is edited
// again. "" when the worktree cannot be read.
func (g Git) WorktreeFingerprint(worktree string) string {
	if worktree == "" {
		return ""
	}
	head, err := g.run(worktree, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	status, err := g.run(worktree, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(head))
	h.Write([]byte{0})
	h.Write([]byte(status))
	var paths []string
	for _, rec := range strings.Split(status, "\x00") {
		if len(rec) > 3 {
			paths = append(paths, rec[3:])
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		if info, err := os.Stat(filepath.Join(worktree, p)); err == nil {
			fmt.Fprintf(h, "\x00%s %d %d", p, info.Size(), info.ModTime().UnixNano())
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}
