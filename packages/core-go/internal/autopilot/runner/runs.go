package runner

import (
	"fmt"

	"github.com/graph-ops/core-go/internal/autopilot"
)

// This file is what the Web UI reads and starts runs through (plan phase 5):
// the runs API's view of a project's runs, and the orchestrator prompt the
// launch API opens a terminal with.

// RecentInactiveRuns is how many finished/stopped/interrupted runs Runs
// returns besides the active ones -- enough for "what happened lately"
// without the answer growing with the project's whole history.
const RecentInactiveRuns = 5

// RunView is one run in GET /api/autopilot/runs.
type RunView struct {
	RunStatus
	// Members is every ticket the run owns for the duplicate-start check
	// (autopilot.Registry.Begin): the root and, for a tree run, all of its
	// descendants as the DB has them now -- including the ones the run has
	// not reached yet, which Tickets does not list. Empty for a run that is
	// not active, since only an active run blocks a start.
	Members []string `json:"members"`
}

// Runs lists projectID's active runs and its RecentInactiveRuns most recent
// other ones, newest first (the Web UI's runs API). The tree is read from
// the DB once, whatever the number of runs.
func (s *Service) Runs(projectID string) ([]RunView, error) {
	statuses, err := s.Status(projectID)
	if err != nil {
		return nil, err
	}
	out := make([]RunView, 0, len(statuses))
	var idx *projectIndex
	inactive := 0
	for _, st := range statuses {
		view := RunView{RunStatus: st, Members: []string{}}
		if !st.Active {
			if inactive >= RecentInactiveRuns {
				continue
			}
			inactive++
			out = append(out, view)
			continue
		}
		view.Members = append(view.Members, st.Root)
		if st.Mode == autopilot.ModeTree {
			if idx == nil {
				if idx, err = s.index(projectID); err != nil {
					return nil, err
				}
			}
			view.Members = append(view.Members, idx.descendants(st.Root)...)
		}
		out = append(out, view)
	}
	return out, nil
}

// OrchestratorPrompt is the prompt the Web UI's launch opens the
// orchestrator's terminal with: the mode's skill, the root, and the run the
// launch reserved for it to adopt.
func OrchestratorPrompt(mode, ticketID, runID string) string {
	return fmt.Sprintf("/graph-ops:autopilot-%s %s --run %s", mode, ticketID, runID)
}
