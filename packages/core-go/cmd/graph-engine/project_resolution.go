package main

import (
	"fmt"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
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
//  1. If cwd (the CLI's current directory, runtimeConfig.WorkDir) is non-empty and
//     equals or lies under some project's local path (projectPaths, i.e.
//     graph-config.json's per-environment project ID -> path map, see
//     DFLT-00080), that project wins -- the deepest such path when several
//     nest (see findProjectForDir).
//  2. Otherwise, the globally-selected current project
//     (app_state.current_project_id, set by use-project and by the Web UI's
//     project switcher).
//  3. Otherwise, the long-standing "no current project selected" error.
//
// An empty cwd skips step 1 entirely rather than falling back to
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
func resolveCreateTicketProject(repo store.GraphRepository, cwd string, projectPaths map[string]string) (*domain.Project, projectResolutionSource, error) {
	if cwd != "" {
		projects, err := repo.ListProjects()
		if err != nil {
			return nil, "", fmt.Errorf("listing projects to match the current directory against their local paths: %w", err)
		}
		if matched := findProjectForDir(projects, projectPaths, cwd); matched != nil {
			return matched, resolvedFromCurrentDirectory, nil
		}
	}

	pid, err := repo.GetCurrentProjectID()
	if err != nil {
		return nil, "", err
	}
	if pid == "" {
		return nil, "", fmt.Errorf("no current project selected, and the current directory matches no project's local path (projectPaths in graph-config.json); set the project's local path in the Web UI settings (or via `graph-engine create-project --workdir`), run `graph-engine use-project <id>`, or pass --project <id>")
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

// findProjectForDir returns the project whose local path (projectPaths) is
// dir itself or an ancestor of it, preferring the deepest path when several
// nested ones match, or nil when nothing matches. The matching rules live in
// runtimeconfig.FindProjectIDForDir (shared with `graph-engine ui`): paths
// are compared after filepath.Clean on a path-separator boundary, empty or
// relative paths and a relative dir never match, symlinks are not resolved
// and comparison is case-sensitive, and a tie between projects sharing the
// same deepest path goes to the one listed first by ListProjects. An entry
// for a project ID not in projects (deleted from the DB) is ignored.
func findProjectForDir(projects []domain.Project, projectPaths map[string]string, dir string) *domain.Project {
	ids := make([]string, len(projects))
	for i := range projects {
		ids[i] = projects[i].ID
	}
	id := runtimeconfig.FindProjectIDForDir(projectPaths, ids, dir)
	if id == "" {
		return nil
	}
	for i := range projects {
		if projects[i].ID == id {
			return &projects[i]
		}
	}
	return nil
}
