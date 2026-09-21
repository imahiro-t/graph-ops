// Package currentproject is the single read/write path for "which project
// is this environment currently looking at" (DFLT-00106).
//
// The answer lives in currentProjectId in the home config file
// ($HOME/.graph-ops/config.json, see runtimeconfig.HomeConfigPath) -- a
// per-user, per-machine file -- not in the data source. Before DFLT-00106 it was
// app_state.current_project_id, one row in a DB that README recommends
// sharing with a whole team over MySQL or an HTTP data source, which made a
// per-person piece of state into a global variable: a teammate (or another
// tab) switching projects changed what everyone else's ticket list showed
// and what their `create-ticket` wrote to.
//
// Both callers -- internal/httpserver (GET/PUT /api/current-project, POST
// /api/tickets) and cmd/graph-engine (use-project, create-ticket) -- go
// through here, so the three-state rule on
// runtimeconfig.FileConfig.CurrentProjectID and the one-time inheritance
// from the DB below are implemented once.
//
// This package deliberately sits between runtimeconfig and store rather than
// inside runtimeconfig: the inheritance has to read the DB, and
// runtimeconfig (a plain settings-file reader) must not depend on the store
// layer.
package currentproject

import (
	"errors"
	"log/slog"

	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
)

// errAlreadyInherited aborts Get's inheritance write without rewriting the
// home config file when another writer set currentProjectId between Get's
// read and its update (runtimeconfig.UpdateHome re-loads the file under its
// lock, so this is the point where that is noticed).
var errAlreadyInherited = errors.New("currentProjectId was set by another writer")

// Get returns this environment's current project ID, or "" when none is
// selected. "" is not an error: a fresh install simply has no project yet.
//
// When the home config file has no currentProjectId at all (the key absent,
// i.e. an environment that predates DFLT-00106), the DB's
// app_state.current_project_id is read once and written here, so upgrading
// does not silently drop the project the user had selected. After that the
// DB value is never consulted again -- which is the whole point: it may be
// changed at any moment by somebody else sharing the same data source.
//
// An inherited value of "" is deliberately NOT written back. Writing it
// would create the home config file on a brand-new install just because
// something read the current project (GET /api/current-project runs on every
// page load), and there is nothing to preserve: the DB says "no project
// selected" and so does an absent key. The cost is that such an environment
// re-reads one DB row per call until a project is actually selected.
//
// A failure to write the inherited value is logged and otherwise ignored:
// the caller asked what the current project is, and the answer is known.
// The inheritance is simply retried on the next call. A successful
// inheritance is logged at Info (event=current_project_inherited): it is a
// one-way, once-per-environment migration, so the log volume is negligible
// and it is the only way to tell whether an environment has already been
// migrated without opening its home config file by hand. logger may be
// nil, in which case slog.Default() is used.
//
// BEWARE: THIS READ CAN WRITE (SEC-1). The inheritance above makes Get --
// and therefore the CSRF-header-free GET /api/current-project that the Web
// UI issues on every page load -- a writer of the home config file. v0.7.0
// deliberately removed the state-changing GETs, so this is a documented
// exception, not an oversight: the value written comes from the data source
// and can never be influenced by the request, the write is idempotent and
// happens at most once per environment, and its only effect is to preserve
// the project the user had already selected, which the next page load would
// do anyway. Do not extend this function with any state change that lacks
// all three of those properties -- that belongs behind Set, which is only
// reachable through the CSRF-protected PUT /api/current-project.
//
// Only the home config file is consulted (DFLT-00124): there is no cwd
// parameter because nothing about the process's working directory can change
// the answer, so the Web UI server and the CLI of the same user always agree
// on it no matter where each was started. A home config that exists but
// cannot be parsed is an error here, unlike runtimeconfig.LoadEffective's
// "warn and fall back": falling back would mean answering "no project" (or
// inheriting one from the DB) for an environment that may well have a
// project selected, and create-ticket would then write to the wrong place.
func Get(home string, repo store.GraphRepository, logger *slog.Logger) (string, error) {
	cfg, err := runtimeconfig.LoadHomeConfig(home)
	if err != nil {
		return "", err
	}
	if cfg.CurrentProjectID != nil {
		return *cfg.CurrentProjectID, nil
	}

	inherited, err := repo.GetCurrentProjectID()
	if err != nil {
		return "", err
	}
	if inherited == "" {
		return "", nil
	}

	saved, path, err := runtimeconfig.UpdateHome(home, func(c *runtimeconfig.FileConfig) error {
		if c.CurrentProjectID != nil {
			return errAlreadyInherited
		}
		SetIn(c, inherited)
		return nil
	})
	switch {
	case errors.Is(err, errAlreadyInherited):
		return *saved.CurrentProjectID, nil
	case err != nil:
		log(logger).Warn("failed to save the current project inherited from the data source to the home config file; it will be inherited again on the next read",
			slog.String("event", "current_project_inherit_save_failed"),
			slog.String("project_id", inherited),
			slog.String("config_path", path),
			slog.String("error", err.Error()))
	default:
		log(logger).Info("inherited the current project from the data source into the home config file; the data source's value is not consulted again",
			slog.String("event", "current_project_inherited"),
			slog.String("project_id", inherited),
			slog.String("config_path", path))
	}
	return inherited, nil
}

// Set makes projectID this environment's current project. It writes only
// the home config file -- the DB's app_state is never written any more, so
// another environment sharing the same data source is unaffected.
//
// Validation belongs to the caller: both callers (PUT /api/current-project
// and use-project) look the project up first so an unknown ID fails before
// anything is stored.
func Set(home, projectID string) error {
	_, _, err := runtimeconfig.UpdateHome(home, func(cfg *runtimeconfig.FileConfig) error {
		SetIn(cfg, projectID)
		return nil
	})
	return err
}

// Clear deselects this environment's current project. It stores an explicit
// empty string rather than removing the key, because an absent key means
// "never set, inherit from the DB" (see Get) -- dropping it would make the
// next read resurrect the very project that was just deleted.
func Clear(home string) error {
	return Set(home, "")
}

// SetIn applies projectID to an already-loaded FileConfig, for a caller that
// is inside a runtimeconfig.UpdateHome of its own and must change
// currentProjectId together with other fields in one write (see
// httpserver.handleDeleteProject, which clears it in the same update that
// drops the deleted project's projectPaths entry).
//
// It always stores a non-nil pointer, including for "": that is what marks
// the value as this environment's own answer rather than "not set yet".
func SetIn(cfg *runtimeconfig.FileConfig, projectID string) {
	id := projectID
	cfg.CurrentProjectID = &id
}

// IsCurrent reports whether cfg's currentProjectId is set and names
// projectID. An absent key is never a match: it means the environment has
// not selected anything itself yet.
func IsCurrent(cfg runtimeconfig.FileConfig, projectID string) bool {
	return cfg.CurrentProjectID != nil && *cfg.CurrentProjectID == projectID
}

func log(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.Default()
	}
	return logger
}
