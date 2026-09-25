package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/autopilot/runner"
	"github.com/graph-ops/core-go/internal/domain"
)

// autopilotService builds the runner the launch and runs APIs use: the same
// registry, settings and local paths as the CLI's `graph-engine autopilot`
// (so a run the Web UI reserves is the one the orchestrator's `start --run`
// adopts), with the terminal launcher Config.AutopilotLauncher supplies in
// tests.
func (s *Server) autopilotService() *runner.Service {
	svc := runner.New(runner.Options{
		Repo: s.repo, HomeDir: s.cfg.HomeDir,
		UserExtensionsDir: s.cfg.UserExtensionsDir, TeamExtensionsDir: s.cfg.TeamExtensionsDir,
		TerminalCommand: s.cfg.TerminalCommand, ClaudeBinary: s.cfg.ClaudeBinary,
	})
	if s.cfg.AutopilotLauncher != nil {
		svc.Launcher = s.cfg.AutopilotLauncher
	}
	return svc
}

// autopilotStartResponse is POST /api/tickets/{id}/autopilot's body.
type autopilotStartResponse struct {
	RunID   string `json:"run_id"`
	Mode    string `json:"mode"`
	Root    string `json:"root"`
	State   string `json:"state"`
	Created bool   `json:"created"`
	Resumed bool   `json:"resumed"`
}

// handleStartAutopilot answers POST /api/tickets/{id}/autopilot (DFLT-00142,
// plan phase 5): body {"mode": "ticket"|"tree"}. It
//
//  1. refuses, reserving nothing, an invalid mode (400 VALIDATION_ERROR), an
//     unknown ticket (404) and a project with no local path in this
//     environment (400 PROJECT_LOCAL_PATH_NOT_SET, spec S10);
//  2. reserves the run through runner.Service.Start with reserve -- the same
//     autopilot.Registry.Begin the CLI's `autopilot start` goes through, so
//     the decision is made in one place (the spec review's carry-over 1): an
//     interrupted or stopped run of the same root and mode is taken over
//     under its own run ID (S5) -- which is how a stopped tree is resumed
//     from the Web UI, and why a root that is DONE by then is not refused --
//     otherwise a DONE/CLOSED root is refused (409 AUTOPILOT_ROOT_FINISHED,
//     S4), and so is an overlap with an active run (409
//     AUTOPILOT_ALREADY_RUNNING);
//  3. opens the orchestrator's terminal in the project's local path with
//     `--permission-mode <the run's setting>` and the prompt
//     `/graph-ops:autopilot-<mode> <id> --run <runId>`, whose `start --run`
//     then adopts the reservation;
//  4. cancels the reservation when the terminal fails to open (500), so the
//     next launch is not refused as a duplicate.
//
// The CSRF header is required like on every state-changing route (withCORS),
// before any of this runs.
func (s *Server) handleStartAutopilot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Mode *string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			// writeError turns this into 413 REQUEST_BODY_TOO_LARGE.
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation, "invalid request body: %v", err))
		return
	}
	mode := ""
	if body.Mode != nil {
		mode = *body.Mode
	}
	if mode != autopilot.ModeTicket && mode != autopilot.ModeTree {
		writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation,
			"mode must be %q or %q, got %q", autopilot.ModeTicket, autopilot.ModeTree, mode))
		return
	}
	ticket, err := s.repo.GetTicket(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if ticket == nil {
		writeError(w, http.StatusNotFound, domain.NewAPIError(domain.ErrCodeTicketNotFound, "ticket not found: %s", id))
		return
	}
	localPath := s.projectLocalPath(ticket.ProjectID)
	if localPath == "" {
		writeError(w, http.StatusBadRequest, domain.NewAPIError(autopilot.ErrCodeLocalPathNotSet,
			"project %s has no local path in this environment; set it in the project settings first", ticket.ProjectID))
		return
	}

	svc := s.autopilotService()
	res, err := svc.Start(ticket.ID, mode, "", true)
	if err != nil {
		writeError(w, statusForError(err, http.StatusInternalServerError), err)
		return
	}
	// The run's own snapshot, which Start just took from the effective
	// settings: what the orchestrator -- and every child it launches -- runs
	// with.
	permissionMode := autopilot.Defaults().PermissionMode
	if run, err := svc.Registry.Load(res.ProjectID, res.RunID); err == nil && run != nil && run.Settings.PermissionMode != "" {
		permissionMode = run.Settings.PermissionMode
	}
	launchErr := svc.Launcher.Launch(localPath, []string{"--permission-mode", permissionMode},
		runner.OrchestratorPrompt(mode, ticket.ID, res.RunID))
	if launchErr != nil {
		if cancelErr := svc.CancelReservation(res.RunID); cancelErr != nil {
			s.logger.Warn("failed to cancel the autopilot reservation after the terminal failed to open",
				slog.String("event", "autopilot_reservation_cancel_failed"),
				slog.String("run_id", res.RunID),
				slog.String("error", cancelErr.Error()))
		}
		writeError(w, http.StatusInternalServerError, fmt.Errorf("opening the autopilot terminal: %w", launchErr))
		return
	}
	writeJSON(w, http.StatusOK, autopilotStartResponse{
		RunID: res.RunID, Mode: res.Mode, Root: res.Root, State: res.State,
		Created: res.Created, Resumed: res.Resumed,
	})
}

// handleListAutopilotRuns answers GET /api/autopilot/runs?project_id=<id>
// (DFLT-00142, plan phase 5): the project's active runs and its most recent
// other ones, newest first -- each with its state, root, the ticket whose
// session is running (and whether it waits for a person), every ticket's
// state within the run, and, for an active run, the tickets it owns
// (members), whose autopilot buttons the Web UI disables. Other projects'
// runs are never included: the registry keeps each project in its own
// directory.
func (s *Server) handleListAutopilotRuns(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation, "project_id is required"))
		return
	}
	if !s.requireProject(w, projectID) {
		return
	}
	runs, err := s.autopilotService().Runs(projectID)
	if err != nil {
		writeError(w, statusForError(err, http.StatusInternalServerError), err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}
