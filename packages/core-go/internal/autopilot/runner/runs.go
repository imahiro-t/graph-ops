package runner

import (
	"fmt"
	"sort"

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
	// Pending is the members the run may still launch as work
	// (autopilot.Pending): the Web UI's "waiting" badge. Unlike Members it
	// leaves out what the planner would skip -- DONE/CLOSED tickets, and
	// subtrees beyond maxDepth or maxTickets, under a ticket in progress
	// elsewhere, or under a failed one. Empty for a run that is not active.
	Pending []string `json:"pending"`
	// StartedBy is who started the run (DFLT-00326), its name sanitized
	// (DFLT-00336); left out when unknown (a run file from before, or a name
	// that is empty once sanitized). The machine ID is never sent.
	StartedBy *RunStarter `json:"started_by,omitempty"`
	// Mine: the run is this machine's -- in its local registry, or started
	// with its machine ID -- so it is this machine that can resume it.
	// False for another member's run.
	Mine bool `json:"mine"`
}

// RunStarter is RunView.StartedBy.
type RunStarter struct {
	Name           string `json:"name"`
	NameIsFallback bool   `json:"name_is_fallback"`
}

// Runs lists projectID's active runs and its RecentInactiveRuns most recent
// other ones, newest first (the Web UI's runs API). The tree is read from
// the DB once, whatever the number of runs.
//
// Since DFLT-00326 the list holds other members' runs too, from the data
// source (Registry.Shared), merged with the local registry's by run ID (the
// local copy wins). The list is for display -- the duplicate-start check
// itself is Begin's -- so when the data source cannot be read it still
// answers, with the local runs alone: silently apart from a one-time
// warning for a data source that cannot share runs (every run is then
// "mine", exactly as before), with a (thinned-out) warning for any other
// error.
func (s *Service) Runs(projectID string) ([]RunView, error) {
	local, err := s.registry().List(projectID)
	if err != nil {
		return nil, err
	}
	isLocal := map[string]bool{}
	for _, r := range local {
		isLocal[r.ID] = true
	}
	runs := append([]*autopilot.Run(nil), local...)
	if shared := s.registry().Shared; shared != nil {
		others, err := shared.List(projectID)
		autopilot.LogSharedError(s.logf, autopilot.SharedOpList, "listing the shared autopilot runs of project "+projectID,
			"showing this machine's runs only", err)
		for _, r := range others {
			if !isLocal[r.ID] {
				runs = append(runs, r)
			}
		}
		sort.SliceStable(runs, func(i, j int) bool {
			if !runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
				return runs[i].CreatedAt.Before(runs[j].CreatedAt)
			}
			return runs[i].ID < runs[j].ID
		})
	}
	// The machine a run belongs to, for Mine and for Pending's foreign
	// launches: a local run without a recorded machine is this one's.
	machine := ""
	if a, err := s.actor(); err == nil && a != nil {
		machine = a.MachineID
	}
	owner := func(r *autopilot.Run) string {
		if r.StartedBy != nil && r.StartedBy.MachineID != "" {
			return r.StartedBy.MachineID
		}
		if isLocal[r.ID] {
			return machine
		}
		return ""
	}
	mine := func(r *autopilot.Run) bool {
		return isLocal[r.ID] || (machine != "" && r.StartedBy != nil && r.StartedBy.MachineID == machine)
	}

	now := s.now()
	out := make([]RunView, 0, len(runs))
	var idx *projectIndex
	inactive := 0
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		view := RunView{RunStatus: runStatus(r, now), Members: []string{}, Pending: []string{}, Mine: mine(r)}
		// Another member's name: sanitized again for display (DFLT-00336);
		// one that becomes "" is left out, as unknown.
		if name := r.StartedByName(); name != "" {
			view.StartedBy = &RunStarter{Name: name, NameIsFallback: r.StartedBy.NameIsFallback}
		}
		if !view.Active {
			if inactive >= RecentInactiveRuns {
				continue
			}
			inactive++
			out = append(out, view)
			continue
		}
		if idx == nil {
			if idx, err = s.index(projectID); err != nil {
				return nil, err
			}
		}
		view.Members = append(view.Members, r.RootTicketID)
		if r.Mode == autopilot.ModeTree {
			view.Members = append(view.Members, idx.descendants(r.RootTicketID)...)
		}
		view.Pending = append(view.Pending, autopilot.Pending(r, idx.tree(r.RootTicketID, r.Mode), foreignLaunched(sameMachine(runs, owner, owner(r)), r.ID, now))...)
		out = append(out, view)
	}
	return out, nil
}

// sameMachine is the runs of runs owned by machine: only a run of the same
// machine could be taken over by it, so only those runs' launches count as
// foreign launches for its Pending (S6).
func sameMachine(runs []*autopilot.Run, owner func(*autopilot.Run) string, machine string) []*autopilot.Run {
	var out []*autopilot.Run
	for _, r := range runs {
		if owner(r) == machine {
			out = append(out, r)
		}
	}
	return out
}

// OrchestratorPrompt is the prompt the Web UI's launch opens the
// orchestrator's terminal with: the mode's skill, the root, and the run the
// launch reserved for it to adopt.
func OrchestratorPrompt(mode, ticketID, runID string) string {
	return fmt.Sprintf("/graph-ops:autopilot-%s %s --run %s", mode, ticketID, runID)
}
