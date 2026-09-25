package httpserver

import (
	"io"
	"net/http"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
)

// resolveAutopilotSettings resolves projectID's autopilot settings from this
// environment's home config and the team tier. fileCfg is the home config as
// just read (or written). It is the body of both GET and PUT
// /api/projects/{id}/autopilot-settings: the effective values, each key's
// source/locked state and local/team/default values, and any warnings.
func (s *Server) resolveAutopilotSettings(projectID string, fileCfg runtimeconfig.FileConfig) autopilot.Effective {
	return autopilot.ResolveProject(projectID, fileCfg.AutopilotLocal(projectID), s.resolveRoots().TeamDir)
}

// requireProject writes a 404 (or 500) and returns false when projectID does
// not name a project.
func (s *Server) requireProject(w http.ResponseWriter, projectID string) bool {
	p, err := s.repo.GetProject(projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return false
	}
	if p == nil {
		writeError(w, http.StatusNotFound, domain.NewAPIError(domain.ErrCodeProjectNotFound, "project not found: %s", projectID))
		return false
	}
	return true
}

// handleGetAutopilotSettings answers GET /api/projects/{id}/autopilot-settings
// (DFLT-00142). Reading never fails over a broken home config or team file:
// the former reads as "no local values" (with the same logged warning as the
// project list), the latter as a warning in the response.
func (s *Server) handleGetAutopilotSettings(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.requireProject(w, id) {
		return
	}
	writeJSON(w, http.StatusOK, s.resolveAutopilotSettings(id, s.loadProjectPaths()))
}

// handlePutAutopilotSettings answers PUT /api/projects/{id}/autopilot-settings
// (DFLT-00142, plan decision D2). The body is a partial object of local
// values: a key given is stored, a key given as null has its local value
// removed, a key left out is untouched. The request is refused as a whole,
// with nothing written, when it names a key the team settings fix
// (400 AUTOPILOT_SETTING_LOCKED, even with the team's own value), an unknown
// key or an invalid value (400 VALIDATION_ERROR). A local value saved before
// the team fixed its key is never removed by a PUT that leaves the key out,
// so it takes effect again once the team stops setting it. This is the only
// write path for these settings -- the CLI only reads them.
func (s *Server) handlePutAutopilotSettings(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !s.requireProject(w, id) {
		return
	}
	// The home config's read here only feeds the locked check; the write
	// itself re-reads the file under UpdateHome's lock.
	current := s.resolveAutopilotSettings(id, s.loadProjectPaths())
	patch, err := autopilot.ParsePatch(body, current.LockedKeys())
	if err != nil {
		writeError(w, statusForError(err, http.StatusBadRequest), err)
		return
	}
	saved, err := runtimeconfig.UpdateAutopilotSettings(s.cfg.HomeDir, id, patch)
	if err != nil {
		writeError(w, statusForError(err, http.StatusInternalServerError), err)
		return
	}
	writeJSON(w, http.StatusOK, s.resolveAutopilotSettings(id, saved))
}
