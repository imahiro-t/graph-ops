package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/graph-ops/core-go/internal/terminal"
)

// handleClaudeLaunch opens an external, interactive terminal running
// `claude "<prompt>"` in the project directory and returns immediately --
// it does not wait for or capture that session. The human sees and drives
// Claude Code themselves (normal permission prompts included), which is why
// this replaced the previous headless `claude -p ... --dangerously-skip-permissions`
// SSE-streamed spawn entirely.
//
// The working directory is resolved with the following priority (completion
// criterion: "selecting a project auto-launches a Claude Code terminal
// session targeting that project's linked working folder"):
//  1. the local path (the home config's projectPaths, DFLT-00080) of an
//     explicit project_id in the body;
//  2. the local path of the project the given ticketId belongs to;
//  3. the server's global TerminalWorkDir fallback (unchanged behavior for
//     callers that don't know about projects at all).
//
// Any lookup failure along the way (unknown id, ticket without a project, a
// project with no local path in this environment, etc.) is treated as "fall
// through to the next priority", not an error --
// launching in a reasonable default directory is better than failing the
// whole request over a project lookup that isn't this call's main point.
func (s *Server) handleClaudeLaunch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Prompt    string `json:"prompt"`
		TicketID  string `json:"ticketId"`
		ProjectID string `json:"project_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	workDir := s.resolveLaunchWorkDir(body.ProjectID, body.TicketID)

	err := terminal.Launch(
		terminal.Config{TerminalCommand: s.cfg.TerminalCommand},
		workDir,
		s.cfg.ClaudeBinary,
		withTicketContext(body.Prompt, body.TicketID),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"started": true})
}

// resolveLaunchWorkDir returns the directory a Claude launch opens in --
// see handleClaudeLaunch's doc comment for the priority order.
func (s *Server) resolveLaunchWorkDir(projectID, ticketID string) string {
	if projectID != "" {
		if p, err := s.repo.GetProject(projectID); err == nil && p != nil {
			if localPath := s.projectLocalPath(p.ID); localPath != "" {
				return localPath
			}
		}
	}
	if ticketID != "" {
		if t, err := s.repo.GetTicket(ticketID); err == nil && t != nil && t.ProjectID != "" {
			if localPath := s.projectLocalPath(t.ProjectID); localPath != "" {
				return localPath
			}
		}
	}
	return s.cfg.TerminalWorkDir
}

// withTicketContext prepends a reference to ticketID onto prompt so the
// spawned Claude Code session always knows which ticket it's working on --
// even when the caller overwrote the UI's default prompt text (which
// otherwise carries the only mention of the ticket ID) with a fully custom
// instruction. If prompt already names the ticket (e.g. the untouched
// default prompt), it's left as-is to avoid a redundant mention.
//
// This text is kept in English rather than the project's Japanese ticket
// language: it's Go source, not UI copy, and the i18n plan (art-65c889b9)
// established that non-test strings in packages/core-go stay English.
func withTicketContext(prompt, ticketID string) string {
	if ticketID == "" || strings.Contains(prompt, ticketID) {
		return prompt
	}
	return fmt.Sprintf("This instruction concerns ticket %s.\n\n%s", ticketID, prompt)
}
