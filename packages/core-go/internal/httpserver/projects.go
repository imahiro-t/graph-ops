package httpserver

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
)

// projectResponse is every project API's response shape: the shared DB's
// domain.Project plus this environment's own local path for it (DFLT-00080).
// LocalPath comes from the home config file's projectPaths, never from the
// DB, so two members sharing one DB each see their own path here; "" means
// the project has no local path in this environment ("未設定").
type projectResponse struct {
	domain.Project
	LocalPath string `json:"local_path"`
}

// loadProjectPaths reads this server's home config file fresh on every call
// (not cached in s.cfg at startup), so a local path set through the settings
// UI or the project-setup dialog takes effect immediately without a restart.
// It is the same file SetProjectPath writes to, which is what makes a saved
// path readable back without a restart (DFLT-00124, completion criterion 5).
//
// It never fails. A missing file simply means no project has a local path
// yet, and a file that cannot be parsed is treated the same way, with a
// warning logged: reading is non-fatal while writing refuses to touch a file
// it could not parse (runtimeconfig.UpdateHome, plan decision D-6). Making a
// broken home config break the project list instead would take the whole Web
// UI down over a file the user can only fix from outside it.
func (s *Server) loadProjectPaths() runtimeconfig.FileConfig {
	fileCfg, err := runtimeconfig.LoadHomeConfig(s.cfg.HomeDir)
	if err != nil {
		s.logger.Warn("failed to read the home config file for projectPaths; treating every local path as not set",
			slog.String("event", "project_paths_load_failed"),
			slog.String("error", err.Error()))
		return runtimeconfig.FileConfig{}
	}
	return fileCfg
}

// projectLocalPath returns this environment's local path for projectID, or
// "" when none is set (see loadProjectPaths, which already absorbs an
// unreadable config file). It is for the callers that must never fail over a
// missing path (Claude launch, catalog loading) and fall back to their own
// defaults instead.
func (s *Server) projectLocalPath(projectID string) string {
	return s.loadProjectPaths().ProjectPath(projectID)
}

func withLocalPath(p domain.Project, fileCfg runtimeconfig.FileConfig) projectResponse {
	return projectResponse{Project: p, LocalPath: fileCfg.ProjectPath(p.ID)}
}

// handleCreateProject creates a new Project. prefix may be omitted (an
// unambiguous prefix is derived from name and de-duplicated -- see
// internal/project.ResolvePrefix). local_path is optional: when given it
// must be absolute, and it is saved to this environment's home config file
// (projectPaths) after the DB row is created -- it becomes the directory
// `POST /api/claude/launch` opens a terminal in for this project, the root
// the project's team extensions are read from, and what `graph-engine ui`
// matches the current directory against.
//
// If the DB insert succeeds but saving the local path fails, the response is
// a 500 with code PROJECT_CREATED_LOCAL_PATH_NOT_SAVED and a message saying
// the project itself was created, and the failure is logged. The Web UI keys
// off that code to re-fetch the project list and steer the user to the
// existing project instead of creating a duplicate one.
func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string `json:"name"`
		Prefix    string `json:"prefix"`
		LocalPath string `json:"local_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation, "name is required"))
		return
	}
	localPath, err := runtimeconfig.NormalizeProjectPath(body.LocalPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	project, err := s.repo.CreateProject(body.Name, body.Prefix)
	if err != nil {
		writeError(w, statusForError(err, http.StatusInternalServerError), err)
		return
	}
	if localPath != "" {
		if _, err := runtimeconfig.SetProjectPath(s.cfg.HomeDir, project.ID, localPath); err != nil {
			// writeError does not log 5xx responses, and this one leaves a
			// half-done state (a project in the shared DB with no local path
			// here), so record it for whoever investigates later.
			s.logger.Error("project was created but saving its local path to the home config file failed",
				slog.String("event", "project_local_path_save_failed_after_create"),
				slog.String("project_id", project.ID),
				slog.String("project_name", project.Name),
				slog.String("error", err.Error()))
			writeError(w, http.StatusInternalServerError, domain.NewAPIError(domain.ErrCodeProjectCreatedLocalPathNotSaved,
				"project %s (%s) was created, but saving its local path to the home config file failed: %v", project.Name, project.ID, err))
			return
		}
	}
	writeJSON(w, http.StatusCreated, projectResponse{Project: project, LocalPath: localPath})
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.repo.ListProjects()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	fileCfg := s.loadProjectPaths()
	out := make([]projectResponse, 0, len(projects))
	for _, p := range projects {
		out = append(out, withLocalPath(p, fileCfg))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	project, err := s.repo.GetProject(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if project == nil {
		writeError(w, http.StatusNotFound, domain.NewAPIError(domain.ErrCodeProjectNotFound, "project not found: %s", id))
		return
	}
	s.writeProject(w, http.StatusOK, *project)
}

// writeProject writes p with this environment's local path attached.
func (s *Server) writeProject(w http.ResponseWriter, status int, p domain.Project) {
	writeJSON(w, status, withLocalPath(p, s.loadProjectPaths()))
}

// handleUpdateProject allows editing name (in the shared DB) and local_path
// (in this environment's home config file only) -- prefix is immutable once
// a project is created (see domain.Project's doc comment), so there is no
// field for it at all. Both body fields are optional; an omitted field is
// left unchanged. local_path "" clears the entry (back to "未設定"), and a
// non-empty relative path is a 400 before anything is written. An unknown
// project is a 404 and nothing is written to the home config file. Sending the
// same local_path again is harmless (the Web UI's "choose an existing
// project" flow relies on that when retrying after a failed switch).
func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Name      *string `json:"name"`
		LocalPath *string `json:"local_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var localPath string
	if body.LocalPath != nil {
		normalized, err := runtimeconfig.NormalizeProjectPath(*body.LocalPath)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		localPath = normalized
	}

	var project domain.Project
	if body.Name != nil {
		updated, err := s.repo.UpdateProject(id, store.ProjectPatch{Name: body.Name})
		if err != nil {
			writeError(w, statusForError(err, http.StatusInternalServerError), err)
			return
		}
		project = updated
	} else {
		cur, err := s.repo.GetProject(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if cur == nil {
			writeError(w, http.StatusNotFound, domain.NewAPIError(domain.ErrCodeProjectNotFound, "project not found: %s", id))
			return
		}
		project = *cur
	}

	if body.LocalPath != nil {
		if _, err := runtimeconfig.SetProjectPath(s.cfg.HomeDir, project.ID, localPath); err != nil {
			writeError(w, statusForError(err, http.StatusInternalServerError), err)
			return
		}
	}
	s.writeProject(w, http.StatusOK, project)
}

// handleDeleteProject deletes the project and every ticket under it
// (cascading to their nodes/edges/artifacts -- see
// store.SQLiteRepository.DeleteProject), clearing the "current project"
// pointer first if it referenced this project. Always 200 (with
// {"success":true}), even if the project didn't exist -- deletion is
// idempotent, same as handleDeleteTicket.
//
// After the DB delete, this environment's projectPaths entry for the
// project is removed on a best-effort basis: failing to do so is only
// logged, never turned into an error response, because the project is
// already gone and a leftover entry for an unknown ID is ignored everywhere
// (see runtimeconfig.FindProjectIDForDir).
func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.repo.DeleteProject(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if _, _, err := runtimeconfig.UpdateHome(s.cfg.HomeDir, func(cfg *runtimeconfig.FileConfig) error {
		if _, ok := cfg.ProjectPaths[id]; !ok {
			return errNothingToClean
		}
		delete(cfg.ProjectPaths, id)
		if len(cfg.ProjectPaths) == 0 {
			cfg.ProjectPaths = nil
		}
		return nil
	}); err != nil && !errors.Is(err, errNothingToClean) {
		s.logger.Warn("failed to remove deleted project's local path from the home config file",
			slog.String("event", "project_local_path_cleanup_failed"),
			slog.String("project_id", id),
			slog.String("error", err.Error()))
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// errNothingToClean aborts handleDeleteProject's UpdateHome without
// rewriting the home config file when there is no entry to remove.
var errNothingToClean = errors.New("no projectPaths entry to remove")

// handleGetCurrentProject returns the currently-selected project, or JSON
// null if none has ever been selected (a brand-new install before its first
// `create-project`/POST /api/projects, see the ticket's completion
// criteria).
func (s *Server) handleGetCurrentProject(w http.ResponseWriter, r *http.Request) {
	id, err := s.repo.GetCurrentProjectID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if id == "" {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	project, err := s.repo.GetProject(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if project == nil {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	s.writeProject(w, http.StatusOK, *project)
}

func (s *Server) handleSetCurrentProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.ProjectID == "" {
		writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation, "project_id is required"))
		return
	}
	project, err := s.repo.GetProject(body.ProjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if project == nil {
		writeError(w, http.StatusNotFound, domain.NewAPIError(domain.ErrCodeProjectNotFound, "project not found: %s", body.ProjectID))
		return
	}
	if err := s.repo.SetCurrentProjectID(project.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.writeProject(w, http.StatusOK, *project)
}
