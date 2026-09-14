package httpserver

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// handleCreateProject creates a new Project. prefix may be omitted (an
// unambiguous prefix is derived from name and de-duplicated -- see
// internal/project.ResolvePrefix); work_dir must be a non-empty absolute
// path, since it becomes the directory `POST /api/claude/launch` opens a
// terminal in for this project (see handleClaudeLaunch).
func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		Prefix  string `json:"prefix"`
		WorkDir string `json:"work_dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation, "name is required"))
		return
	}
	if body.WorkDir == "" || !filepath.IsAbs(body.WorkDir) {
		writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation, "work_dir must be an absolute path"))
		return
	}

	project, err := s.repo.CreateProject(body.Name, body.Prefix, body.WorkDir)
	if err != nil {
		writeError(w, statusForError(err, http.StatusInternalServerError), err)
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.repo.ListProjects()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, projects)
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
	writeJSON(w, http.StatusOK, project)
}

// handleUpdateProject only allows editing name/work_dir -- prefix is
// immutable once a project is created (see domain.Project's doc comment),
// so ProjectPatch has no field for it at all.
func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Name    *string `json:"name"`
		WorkDir *string `json:"work_dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	updated, err := s.repo.UpdateProject(id, store.ProjectPatch{Name: body.Name, WorkDir: body.WorkDir})
	if err != nil {
		writeError(w, statusForError(err, http.StatusInternalServerError), err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleDeleteProject deletes the project and every ticket under it
// (cascading to their nodes/edges/artifacts -- see
// store.SQLiteRepository.DeleteProject), clearing the "current project"
// pointer first if it referenced this project. Always 200 (with
// {"success":true}), even if the project didn't exist -- deletion is
// idempotent, same as handleDeleteTicket.
func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.repo.DeleteProject(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

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
	writeJSON(w, http.StatusOK, project)
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
	writeJSON(w, http.StatusOK, project)
}
