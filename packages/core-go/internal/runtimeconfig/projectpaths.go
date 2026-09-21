package runtimeconfig

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/graph-ops/core-go/internal/domain"
)

// fileMu serializes every in-process read-modify-write of the home config
// file done through UpdateHome (DFLT-00080). The HTTP server has more than
// one writer of that file -- the project API (PATCH/POST/DELETE
// /api/projects, which edit projectPaths) and PUT /api/settings/app (which
// edits the app-settings fields) -- and each one is a load, an in-memory edit
// and a save. Two such requests arriving together would otherwise both load
// the same old contents and the second save would silently discard the first
// one's change. One process-wide mutex is enough: the file is small, writes
// are rare and user-driven, and every writer in this process goes through
// UpdateHome.
//
// It does not (and cannot) protect against a different process -- e.g. the
// CLI's `create-project` -- writing the same file at the same moment; that
// is accepted for this local, single-user tool.
var fileMu sync.Mutex

// ProjectPath returns this environment's local path for projectID from
// ProjectPaths, or "" when none is set (the "未設定" state). A stored value
// that is not absolute is treated as unset too, since it has no fixed
// meaning (see FindProjectIDForDir).
func (c FileConfig) ProjectPath(projectID string) string {
	p := c.ProjectPaths[projectID]
	if p == "" || !filepath.IsAbs(p) {
		return ""
	}
	return filepath.Clean(p)
}

// NormalizeProjectPath validates and normalizes a local path submitted for
// a project: "" is accepted as-is (meaning "unset"), anything else must be
// absolute and is returned filepath.Clean-ed. A relative path is a
// VALIDATION_ERROR.
func NormalizeProjectPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if !filepath.IsAbs(path) {
		return "", domain.NewAPIError(domain.ErrCodeValidation, "local_path must be an absolute path, got %q", path)
	}
	return filepath.Clean(path), nil
}

// SetProjectPath stores path as this environment's local path for projectID
// in the home config file's projectPaths (through UpdateHome, so every other
// field in the file is preserved and concurrent in-process writers are
// serialized). An empty path removes the entry, returning the project to
// "unset". A non-empty relative path is rejected with a VALIDATION_ERROR
// before the file is touched. Returns the path of the file written.
//
// Writing through UpdateHome is what keeps this in step with the read side:
// projectPaths is read back out of the same home config file (see
// LoadEffective), so a save cannot land in a file the next read will not
// look at -- the "I set the local path and it did not stick" bug that a
// write aimed at a working-directory file used to produce whenever the two
// resolved differently (DFLT-00124, completion criterion 5).
func SetProjectPath(home, projectID, path string) (string, error) {
	normalized, err := NormalizeProjectPath(path)
	if err != nil {
		return HomeConfigPath(home), err
	}
	_, written, err := UpdateHome(home, func(cfg *FileConfig) error {
		setProjectPathIn(cfg, projectID, normalized)
		return nil
	})
	return written, err
}

// setProjectPathIn applies an already-normalized path to cfg.ProjectPaths:
// "" deletes the entry (and drops an emptied map so the key is omitted from
// the file), anything else sets it.
func setProjectPathIn(cfg *FileConfig, projectID, normalized string) {
	if normalized == "" {
		if cfg.ProjectPaths != nil {
			delete(cfg.ProjectPaths, projectID)
			if len(cfg.ProjectPaths) == 0 {
				cfg.ProjectPaths = nil
			}
		}
		return
	}
	if cfg.ProjectPaths == nil {
		cfg.ProjectPaths = map[string]string{}
	}
	cfg.ProjectPaths[projectID] = normalized
}

// FindProjectIDForDir returns the ID of the project whose local path (from
// paths, i.e. the home config file's projectPaths) is dir itself or an ancestor
// of it, preferring the deepest (longest cleaned) path when several nested
// ones match. It returns "" when nothing matches. knownIDs is the list of
// projects that actually exist (in store.ListProjects order); an entry for
// an ID not in it -- a project deleted from the shared DB, possibly by
// another member -- is ignored.
//
// Matching rules (carried over unchanged from the pre-DFLT-00080
// cmd/graph-engine findProjectForDir, which matched the per-project
// directory the DB used to hold):
//   - dir and each path are compared after filepath.Clean, so trailing
//     separators, "//", "." and ".." segments don't matter.
//   - "Under" is checked on a path-separator boundary: /a/foo matches
//     /a/foo/bar but not /a/foobar.
//   - An empty or non-absolute path is never a candidate: an empty one
//     would prefix-match every directory, and a relative one has no fixed
//     meaning.
//   - A non-absolute dir matches nothing, for the same reason.
//   - Symlinks are not resolved and comparison is case-sensitive.
//   - When several projects share the same deepest path, the one that comes
//     first in knownIDs wins.
func FindProjectIDForDir(paths map[string]string, knownIDs []string, dir string) string {
	if dir == "" || !filepath.IsAbs(dir) || len(paths) == 0 {
		return ""
	}
	dir = filepath.Clean(dir)

	best := ""
	bestLen := -1
	for _, id := range knownIDs {
		p := paths[id]
		if p == "" || !filepath.IsAbs(p) {
			continue
		}
		p = filepath.Clean(p)
		if !IsSameOrUnderDir(dir, p) {
			continue
		}
		if len(p) > bestLen {
			best = id
			bestLen = len(p)
		}
	}
	return best
}

// IsSameOrUnderDir reports whether dir equals base or lies beneath it, on a
// path-separator boundary. Both must already be cleaned. A base that
// already ends in a separator (only a filesystem root such as "/" or `C:\`
// survives Clean that way) is used as the prefix as-is, so the root still
// contains its subdirectories.
func IsSameOrUnderDir(dir, base string) bool {
	if dir == base {
		return true
	}
	prefix := base
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(dir, prefix)
}
