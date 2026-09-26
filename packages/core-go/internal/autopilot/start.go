package autopilot

import (
	"encoding/json"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

// This file is the one place a run is started (plan D4, spec S4/S5, and the
// spec review's carry-over 1): the CLI's `autopilot start` and the Web UI's
// launch API both call Registry.Begin, so "take over an interrupted/stopped
// run, else refuse a finished root, then refuse an overlapping active run" is
// decided identically for both.

// BeginRequest is a start (or, with Reserve, a reservation) of a run.
type BeginRequest struct {
	// RootID/ProjectID/RootStatus describe the ticket the run starts from.
	RootID     string
	ProjectID  string
	RootStatus domain.TicketStatus
	Mode       string
	// RunID adopts that run (the orchestrator's `start --run <id>` after the
	// Web UI reserved it). Empty for an ordinary start.
	RunID string
	// Reserve leaves the run in state starting for an orchestrator to adopt
	// (the Web UI's launch). CancelReservation undoes it.
	Reserve bool
	// Settings is the project's effective settings, snapshotted into the run.
	Settings Settings
	// Descendants returns every descendant of a ticket (the ticket itself
	// excluded), for the overlap check.
	Descendants func(ticketID string) ([]string, error)
	// TerminalTTY is the starting orchestrator's Terminal.app tty (see
	// Run.TerminalTTY), "" when it has none. Ignored with Reserve: the
	// process reserving the run is not the orchestrator.
	TerminalTTY string
}

// BeginResult says what Begin did.
type BeginResult struct {
	Run *Run
	// Created: a new run. TookOver: an interrupted or stopped run with the
	// same root and mode was taken over (S5). Adopted: a reserved run was
	// adopted (RunID).
	Created  bool
	TookOver bool
	Adopted  bool
}

// Begin starts a run under the project's lock. The decision order is:
//
//  1. RunID given: adopt that reserved run (state starting), or take it over
//     if it was interrupted or stopped.
//  2. Otherwise, the latest run with the same root and mode, if it is
//     interrupted (non-final, heartbeat stale) or stopped, is taken over --
//     same run ID, back to running (S5). A finished one is not.
//  3. Only when a new run is to be created: a DONE/CLOSED root is refused
//     with AUTOPILOT_ROOT_FINISHED (S4). A taken-over run is not refused on
//     that account -- in tree mode the root is DONE long before the tree is.
//  4. The run (new or taken over) is refused with AUTOPILOT_ALREADY_RUNNING
//     if an active run (other than itself) owns the root, or -- in tree mode
//     -- if the root of an active run is among this root's descendants.
func (g *Registry) Begin(req BeginRequest) (BeginResult, error) {
	if req.Mode != ModeTicket && req.Mode != ModeTree {
		return BeginResult{}, domain.NewAPIError(domain.ErrCodeValidation, "VALIDATION_ERROR: mode must be %q or %q, got %q", ModeTicket, ModeTree, req.Mode)
	}
	var res BeginResult
	var savedID string
	err := g.WithLock(req.ProjectID, func(tx *Tx) error {
		now := g.now()
		runs, unreadable, err := g.ListChecked(req.ProjectID)
		if err != nil {
			return err
		}
		if len(unreadable) > 0 {
			// An unreadable file may be an active run: starting now could
			// duplicate it, so refuse until somebody looks.
			return domain.NewAPIError(ErrCodeRegistryCorrupt,
				"AUTOPILOT_REGISTRY_CORRUPT: the autopilot run file %s cannot be read (%s), so whether this start would duplicate an active run cannot be told; fix or remove the file and start again",
				unreadable[0].Path, unreadable[0].Err).
				WithDetails(map[string]any{"path": unreadable[0].Path})
		}
		var cand *Run
		var previous []byte
		if req.RunID != "" {
			cand, err = tx.Load(req.RunID)
			if err != nil {
				return err
			}
			if cand == nil {
				return domain.NewAPIError(ErrCodeRunNotFound, "AUTOPILOT_RUN_NOT_FOUND: run %s not found in project %s", req.RunID, req.ProjectID)
			}
			if cand.RootTicketID != req.RootID || cand.Mode != req.Mode {
				return domain.NewAPIError(domain.ErrCodeValidation,
					"VALIDATION_ERROR: run %s is a %s run of %s, not a %s run of %s", cand.ID, cand.Mode, cand.RootTicketID, req.Mode, req.RootID)
			}
			switch {
			case cand.State == RunStarting && cand.Reservation != nil && !cand.Interrupted(now):
				cand.State = RunRunning
				cand.Reservation = nil
				cand.Heartbeat = now
				// The adopting orchestrator is the run's first window now,
				// whatever the reservation (or a run it took over) held.
				cand.TerminalTTY, cand.TerminalTabDisabled = req.TerminalTTY, ""
				res.Adopted = true
				savedID = cand.ID
				return tx.Save(cand)
			case cand.State == RunStopped || cand.Interrupted(now):
				// Taken over below, like an ordinary start would.
			case cand.State == RunFinished:
				return domain.NewAPIError(ErrCodeInvalidRunState, "AUTOPILOT_INVALID_STATE: run %s has finished; start without --run to begin a new run", cand.ID)
			default:
				return domain.NewAPIError(ErrCodeAlreadyRunning, "AUTOPILOT_ALREADY_RUNNING: run %s is already running (heartbeat %s)", cand.ID, cand.Heartbeat.UTC().Format(time.RFC3339)).
					WithDetails(map[string]any{"run_id": cand.ID, "root_ticket_id": cand.RootTicketID})
			}
		} else {
			for i := len(runs) - 1; i >= 0; i-- {
				r := runs[i]
				if r.RootTicketID == req.RootID && r.Mode == req.Mode {
					if r.State == RunStopped || r.Interrupted(now) {
						cand = r
					}
					break
				}
			}
		}

		if cand == nil {
			// S4: only a new run is refused for a finished root.
			if req.RootStatus == domain.TicketDone || req.RootStatus == domain.TicketClosed {
				return domain.NewAPIError(ErrCodeRootFinished,
					"AUTOPILOT_ROOT_FINISHED: ticket %s is %s, so there is nothing to start from it; to process an unfinished or failed descendant, start the autopilot from that ticket instead",
					req.RootID, req.RootStatus)
			}
		}

		if err := checkOverlap(runs, cand, req, now); err != nil {
			return err
		}

		if cand != nil {
			if req.Reserve {
				previous, err = json.Marshal(cand)
				if err != nil {
					return err
				}
			}
			cand.TakeOver(now, req.Settings)
			res.TookOver = true
		} else {
			cand = &Run{
				ID:           NewRunID(now),
				ProjectID:    req.ProjectID,
				Mode:         req.Mode,
				RootTicketID: req.RootID,
				State:        RunRunning,
				CreatedAt:    now,
				Heartbeat:    now,
				Settings:     req.Settings,
				Generation:   1,
				Tickets:      map[string]*TicketState{},
			}
			res.Created = true
			if deleted, err := tx.Prune(runs, now); err != nil {
				g.logf("pruning old autopilot runs of project %s: %v", req.ProjectID, err)
			} else if len(deleted) > 0 {
				g.logf("pruned %d settled autopilot run(s) of project %s beyond the newest %d", len(deleted), req.ProjectID, KeepSettledRuns)
			}
		}
		// Whoever drives the run from now on decides its window: a new or
		// taken-over run takes this orchestrator's tty even when it is empty,
		// so a tty left by an earlier orchestrator (whose number the system
		// may since have given to an unrelated tab) is never used, and a
		// disabled tab path gets another chance (a permission may have been
		// granted since). A reservation holds none until it is adopted.
		cand.TerminalTTY, cand.TerminalTabDisabled = req.TerminalTTY, ""
		if req.Reserve {
			cand.TerminalTTY = ""
			cand.State = RunStarting
			cand.Reservation = &Reservation{Created: res.Created, Previous: previous}
		}
		savedID = cand.ID
		return tx.Save(cand)
	})
	if err != nil {
		return BeginResult{}, err
	}
	// Re-read so the caller sees exactly what was saved.
	run, err := g.Load(req.ProjectID, savedID)
	if err != nil {
		return BeginResult{}, err
	}
	res.Run = run
	return res, nil
}

// checkOverlap refuses req if an active run other than self owns its root,
// or (tree mode) roots somewhere in its descendants.
func checkOverlap(runs []*Run, self *Run, req BeginRequest, now time.Time) error {
	var mine map[string]bool
	for _, r := range runs {
		if self != nil && r.ID == self.ID {
			continue
		}
		if !r.IsActive(now) {
			continue
		}
		owned := map[string]bool{r.RootTicketID: true}
		if r.Mode == ModeTree && req.Descendants != nil {
			desc, err := req.Descendants(r.RootTicketID)
			if err != nil {
				return err
			}
			for _, id := range desc {
				owned[id] = true
			}
		}
		conflict := owned[req.RootID]
		if !conflict && req.Mode == ModeTree && req.Descendants != nil {
			if mine == nil {
				desc, err := req.Descendants(req.RootID)
				if err != nil {
					return err
				}
				mine = map[string]bool{}
				for _, id := range desc {
					mine[id] = true
				}
			}
			conflict = mine[r.RootTicketID]
		}
		if conflict {
			return domain.NewAPIError(ErrCodeAlreadyRunning,
				"AUTOPILOT_ALREADY_RUNNING: ticket %s overlaps the active %s run %s rooted at %s",
				req.RootID, r.Mode, r.ID, r.RootTicketID).
				WithDetails(map[string]any{"run_id": r.ID, "root_ticket_id": r.RootTicketID})
		}
	}
	return nil
}

// CancelReservation undoes a reservation made by Begin with Reserve (the Web
// UI's launch whose terminal failed to open): a run the reservation created
// is deleted, a run it took over is put back as it was.
func (g *Registry) CancelReservation(projectID, runID string) error {
	return g.WithLock(projectID, func(tx *Tx) error {
		run, err := tx.Load(runID)
		if err != nil {
			return err
		}
		if run == nil {
			return nil
		}
		if run.State != RunStarting || run.Reservation == nil {
			return domain.NewAPIError(ErrCodeInvalidRunState, "AUTOPILOT_INVALID_STATE: run %s is not a pending reservation", runID)
		}
		if run.Reservation.Created {
			return tx.Delete(runID)
		}
		var prev Run
		if err := json.Unmarshal(run.Reservation.Previous, &prev); err != nil {
			return err
		}
		return tx.Save(&prev)
	})
}
