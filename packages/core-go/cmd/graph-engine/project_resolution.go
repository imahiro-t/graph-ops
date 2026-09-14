package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// projectResolutionSource says how create-ticket picked a project when
// --project was omitted. It is only used to word the one-line stderr notice
// so the caller can tell a cwd match from a fall back to use-project's
// selection.
type projectResolutionSource string

const (
	resolvedFromCurrentDirectory projectResolutionSource = "current directory"
	resolvedFromCurrentProject   projectResolutionSource = "current project (use-project)"
)

// resolveCreateTicketProject decides which project `create-ticket` targets
// when --project is omitted (DFLT-00025):
//
//  1. If workDir (the CLI's cwd, runtimeConfig.WorkDir) is non-empty and
//     equals or lies under some project's work_dir, that project wins -- the
//     deepest such work_dir when several nest (see findProjectForDir).
//  2. Otherwise, the globally-selected current project
//     (app_state.current_project_id, set by use-project and by the Web UI's
//     project switcher).
//  3. Otherwise, the long-standing "no current project selected" error.
//
// An empty workDir skips step 1 entirely rather than falling back to
// os.Getwd()/filepath.Abs("") -- the caller didn't supply a cwd, and
// silently substituting the process's own one would make the result depend
// on wherever the process (e.g. a test binary) happens to run.
//
// A ListProjects failure is returned as-is instead of falling through to
// step 2: quietly ignoring it and creating the ticket in whatever project
// the UI last selected is exactly the misrouting this resolution exists to
// prevent. Likewise a current_project_id that no longer names a project is
// an error (with the dangling id in the message) rather than something to
// guess around.
func resolveCreateTicketProject(repo store.GraphRepository, workDir string) (*domain.Project, projectResolutionSource, error) {
	if workDir != "" {
		projects, err := repo.ListProjects()
		if err != nil {
			return nil, "", fmt.Errorf("listing projects to match the current directory against their work_dir: %w", err)
		}
		if matched := findProjectForDir(projects, workDir); matched != nil {
			return matched, resolvedFromCurrentDirectory, nil
		}
	}

	pid, err := repo.GetCurrentProjectID()
	if err != nil {
		return nil, "", err
	}
	if pid == "" {
		return nil, "", fmt.Errorf("no current project selected; run `graph-engine create-project` then `use-project`, or pass --project <id>")
	}
	project, err := repo.GetProject(pid)
	if err != nil {
		return nil, "", err
	}
	if project == nil {
		return nil, "", fmt.Errorf("current project %s not found; run `graph-engine use-project <id>` to select an existing project, or pass --project <id>", pid)
	}
	return project, resolvedFromCurrentProject, nil
}

// findProjectForDir returns the project whose work_dir is dir itself or an
// ancestor of it, preferring the deepest (longest cleaned) work_dir when
// several nested ones match. It returns nil when nothing matches.
//
// Matching rules:
//   - dir and each work_dir are compared after filepath.Clean, so trailing
//     separators, "//", "." and ".." segments don't matter.
//   - "Under" is checked on a path-separator boundary: /a/foo matches
//     /a/foo/bar but not /a/foobar.
//   - A project whose work_dir is empty or not absolute is never a
//     candidate. An empty one would prefix-match every directory, and a
//     relative one (the store rejects both, but a row could predate that
//     validation or be edited by hand) has no fixed meaning -- resolving
//     "." against the CLI's cwd would make it match wherever the CLI runs,
//     the very misrouting this is meant to fix.
//   - A non-absolute dir matches nothing, for the same reason.
//   - Symlinks are not resolved and comparison is case-sensitive: a plain
//     path comparison, like findProjectByWorkDir in ui.go.
//   - If several projects share the same deepest work_dir (not prevented by
//     the schema), which one wins follows ListProjects order and is not
//     guaranteed.
//
// This intentionally differs from ui.go's findProjectByWorkDir, which is
// exact-match only and serves the `ui` command's own flow (projects fetched
// from the running UI server's API). Changing `ui` to also accept
// subdirectories was out of scope for DFLT-00025, so the two stay separate.
func findProjectForDir(projects []domain.Project, dir string) *domain.Project {
	if dir == "" || !filepath.IsAbs(dir) {
		return nil
	}
	dir = filepath.Clean(dir)

	var best *domain.Project
	bestLen := -1
	for i := range projects {
		wd := projects[i].WorkDir
		if wd == "" || !filepath.IsAbs(wd) {
			continue
		}
		wd = filepath.Clean(wd)
		if !isSameOrUnderDir(dir, wd) {
			continue
		}
		if len(wd) > bestLen {
			best = &projects[i]
			bestLen = len(wd)
		}
	}
	return best
}

// isSameOrUnderDir reports whether dir equals base or lies beneath it, on a
// path-separator boundary. Both must already be cleaned. A base that
// already ends in a separator (only a filesystem root such as "/" or `C:\`
// survives Clean that way) is used as the prefix as-is, so the root still
// contains its subdirectories.
func isSameOrUnderDir(dir, base string) bool {
	if dir == base {
		return true
	}
	prefix := base
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(dir, prefix)
}
