package autopilot

import (
	"encoding/json"
	"errors"
	"fmt"
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
	// Actor is who is starting (DFLT-00326): stamped on the run started,
	// taken over or adopted, and its MachineID limits what is taken over to
	// this machine's runs. nil stamps nothing and takes over any local run
	// (as before DFLT-00326).
	Actor *StartedBy
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
//     if it was interrupted or stopped -- only a run of this machine
//     (DFLT-00326): another member's run is refused, with
//     AUTOPILOT_ALREADY_RUNNING while it is active and
//     AUTOPILOT_INVALID_STATE otherwise.
//  2. Otherwise, the latest of this machine's runs with the same root and
//     mode, if it is interrupted (non-final, heartbeat stale) or stopped, is
//     taken over -- same run ID, back to running (S5). A finished one is
//     not. Another member's stopped or interrupted run is never taken over:
//     it does not block the start either, which creates a new run.
//  3. Only when a new run is to be created: a DONE/CLOSED root is refused
//     with AUTOPILOT_ROOT_FINISHED (S4). A taken-over run is not refused on
//     that account -- in tree mode the root is DONE long before the tree is.
//  4. The run (new or taken over) is refused with AUTOPILOT_ALREADY_RUNNING
//     if an active run (other than itself) owns the root, or -- in tree mode
//     -- if the root of an active run is among this root's descendants. With
//     Shared set, "an active run" includes other members' runs from the data
//     source; the error's details then name who started it (started_by).
//
// With Shared set, steps 1-4 run inside SharedRuns.Begin, which saves the
// run's shared record in the same data source transaction (so on the SQL
// backends two members' overlapping starts cannot both pass), and the run
// is saved locally afterwards with the same revision. The same transaction
// deletes the settled records beyond the project's retention
// (SharedRetention); where that is not atomic (HTTP), a delete that fails
// does not fail the start but is logged (LogSharedError, SharedOpRetention),
// also when the local save then fails. If the local save fails, the shared
// record is put back (after the lock is released). A data
// source that cannot share runs (ErrSharedRunsUnsupported) falls back to
// the local runs alone, with a one-time warning; any other data source
// error fails the start, since it could not be told whether it duplicates a
// run.
func (g *Registry) Begin(req BeginRequest) (BeginResult, error) {
	if req.Mode != ModeTicket && req.Mode != ModeTree {
		return BeginResult{}, domain.NewAPIError(domain.ErrCodeValidation, "VALIDATION_ERROR: mode must be %q or %q, got %q", ModeTicket, ModeTree, req.Mode)
	}
	var res BeginResult
	var savedID string
	// What has to be done in the data source once the lock is released.
	var (
		sharedUsed bool
		compensate func()
		pruned     []string
		// How many deletes the retention asked of the data source (not
		// how many succeeded), and what of them failed (see
		// SharedRuns.Begin).
		dropAsked int
		dropErr   error
	)
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
		// The run as it was on disk before this start touched it (nil for
		// a new run): what the compensation puts back.
		var original *Run
		decide := func(shared []*Run) (*Run, error) {
			// On copies, so a decision that is thrown away (the data
			// source failing after it) leaves nothing half-applied for the
			// next one, or for Prune.
			local := make([]*Run, 0, len(runs))
			for _, r := range runs {
				c, err := cloneRun(r)
				if err != nil {
					return nil, err
				}
				local = append(local, c)
			}
			res = BeginResult{}
			run, orig, err := decideBegin(req, local, shared, now, &res)
			original = orig
			return run, err
		}
		var run *Run
		if g.Shared != nil {
			// The only data source call made under the lock (see Registry).
			// decide itself touches neither the data source nor any file.
			derr, err := g.Shared.Begin(req.ProjectID, func(shared []*Run) (*Run, []string, error) {
				r, err := decide(shared)
				if err != nil {
					return nil, nil, err
				}
				run = r
				drop := SharedRetention(shared, r.ID, now)
				dropAsked = len(drop)
				return r, drop, nil
			})
			switch {
			case err == nil:
				sharedUsed = true
				dropErr = derr
			case errors.Is(err, ErrSharedRunsUnsupported):
				WarnSharedUnsupported(g.Logf)
				run = nil
			default:
				return err
			}
		}
		if !sharedUsed {
			if run, err = decide(nil); err != nil {
				return err
			}
		}
		if err := tx.saveExact(run); err != nil {
			if sharedUsed {
				compensate = g.compensation(run, original)
			}
			return err
		}
		savedID = run.ID
		if res.Created {
			// Pruned here, after the save and outside the data source
			// transaction; their shared records go after the lock.
			deleted, err := tx.Prune(runs, now)
			if err != nil {
				g.logf("pruning old autopilot runs of project %s: %v", req.ProjectID, err)
			} else if len(deleted) > 0 {
				g.logf("pruned %d settled autopilot run(s) of project %s beyond the newest %d", len(deleted), req.ProjectID, KeepSettledRuns)
			}
			pruned = deleted
		}
		return nil
	})
	if compensate != nil {
		compensate()
	}
	// The retention's outcome was settled in the data source when its Begin
	// succeeded, so it is reported even if the local save then failed: the
	// compensation puts back the run's own record, not the ones deleted.
	// Nothing is reported when nothing was to be deleted, so a start that
	// sent no DELETE does not say the data source is reachable again.
	if sharedUsed && dropAsked > 0 {
		LogSharedError(g.Logf, SharedOpRetention, "deleting settled shared autopilot runs of project "+req.ProjectID,
			"they are deleted by a later start", dropErr)
	}
	if err != nil {
		return BeginResult{}, err
	}
	if sharedUsed {
		for _, id := range pruned {
			if derr := g.Shared.Delete(id); derr != nil {
				g.logf("deleting the shared record of pruned autopilot run %s: %v (it is inactive, so it blocks nobody)", id, derr)
			}
		}
	}
	// Re-read so the caller sees exactly what was saved.
	run, err := g.Load(req.ProjectID, savedID)
	if err != nil {
		return BeginResult{}, err
	}
	res.Run = run
	return res, nil
}

// compensation returns what puts the shared record of run back after its
// local save failed: a new run's record is deleted, a taken-over or adopted
// one's is restored from original with a revision above the one just
// written (so it is not ignored as stale). Best effort: a failure is
// logged, and the record left behind stops blocking anyone once its
// heartbeat is ActiveThreshold old.
//
// The restored record's revision (run.Revision + 1) is ahead of the local
// file, which still carries original's: typically by two. Until the next
// start stamps the run above both copies (decideBegin takes the shared
// revision into account), a local save of that run -- a wait's heartbeat,
// a cancelled reservation -- produces a revision the data source ignores
// as stale, so the record keeps showing the restored state. That is what
// the run was in anyway: a run whose start failed is not being driven, and
// its record stops counting as active once its heartbeat is
// ActiveThreshold old.
func (g *Registry) compensation(run, original *Run) func() {
	return func() {
		var err error
		if original == nil {
			err = g.Shared.Delete(run.ID)
		} else {
			original.Revision = run.Revision + 1
			err = g.Shared.Save(original)
		}
		if err != nil {
			g.logf("undoing the shared record of autopilot run %s after its local save failed: %v (it stops blocking other members once its heartbeat is %s old)", run.ID, err, ActiveThreshold)
		}
	}
}

// decideBegin is Begin's decision (see Begin's steps 1-4) on the local runs
// and the shared ones. It returns the run to save -- with its state, starter,
// UpdatedAt, BegunAt and Revision set -- and, for a run that existed, a copy
// of it as it was. It reads and writes nothing.
func decideBegin(req BeginRequest, local, shared []*Run, now time.Time, res *BeginResult) (*Run, *Run, error) {
	machine := ""
	if req.Actor != nil {
		machine = req.Actor.MachineID
	}
	localByID := map[string]*Run{}
	for _, r := range local {
		localByID[r.ID] = r
	}
	// all is every run the overlap check looks at: the local ones, and the
	// shared ones this machine does not hold (a run held in both is judged
	// by the local copy, which is at least as recent).
	all := append([]*Run(nil), local...)
	sharedByID := map[string]*Run{}
	for _, r := range shared {
		sharedByID[r.ID] = r
		if localByID[r.ID] == nil {
			all = append(all, r)
		}
	}

	var cand *Run
	if req.RunID != "" {
		cand = localByID[req.RunID]
		foreign := cand != nil && !cand.BelongsTo(machine)
		if cand == nil {
			// Not in this machine's registry: another member's run is
			// refused below; this machine's own (its file lost) or an
			// unknown one is not found.
			if r := sharedByID[req.RunID]; r != nil && machine != "" && !r.BelongsTo(machine) {
				cand, foreign = r, true
			}
		}
		if cand == nil {
			return nil, nil, domain.NewAPIError(ErrCodeRunNotFound, "AUTOPILOT_RUN_NOT_FOUND: run %s not found in project %s", req.RunID, req.ProjectID)
		}
		if cand.RootTicketID != req.RootID || cand.Mode != req.Mode {
			return nil, nil, domain.NewAPIError(domain.ErrCodeValidation,
				"VALIDATION_ERROR: run %s is a %s run of %s, not a %s run of %s", cand.ID, cand.Mode, cand.RootTicketID, req.Mode, req.RootID)
		}
		if foreign {
			if cand.IsActive(now) {
				return nil, nil, alreadyRunning(cand)
			}
			if cand.State == RunFinished {
				return nil, nil, domain.NewAPIError(ErrCodeInvalidRunState, "AUTOPILOT_INVALID_STATE: run %s has finished; start without --run to begin a new run", cand.ID)
			}
			return nil, nil, domain.NewAPIError(ErrCodeInvalidRunState,
				"AUTOPILOT_INVALID_STATE: run %s was started on another machine%s, so it cannot be taken over here; start without --run to begin a new run",
				cand.ID, startedBySuffix(cand)).
				WithDetails(startedByDetails(cand, map[string]any{"run_id": cand.ID}))
		}
		switch {
		case cand.State == RunStarting && cand.Reservation != nil && !cand.Interrupted(now):
			original, err := cloneRun(cand)
			if err != nil {
				return nil, nil, err
			}
			cand.State = RunRunning
			cand.Reservation = nil
			cand.Heartbeat = now
			// The adopting orchestrator is the run's first window now,
			// whatever the reservation (or a run it took over) held.
			cand.TerminalTTY, cand.TerminalTabDisabled = req.TerminalTTY, ""
			res.Adopted = true
			stamp(cand, req.Actor, now, sharedByID)
			return cand, original, nil
		case cand.State == RunStopped || cand.Interrupted(now):
			// Taken over below, like an ordinary start would.
		case cand.State == RunFinished:
			return nil, nil, domain.NewAPIError(ErrCodeInvalidRunState, "AUTOPILOT_INVALID_STATE: run %s has finished; start without --run to begin a new run", cand.ID)
		default:
			return nil, nil, alreadyRunning(cand)
		}
	} else {
		for i := len(local) - 1; i >= 0; i-- {
			r := local[i]
			if r.RootTicketID == req.RootID && r.Mode == req.Mode && r.BelongsTo(machine) {
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
			return nil, nil, domain.NewAPIError(ErrCodeRootFinished,
				"AUTOPILOT_ROOT_FINISHED: ticket %s is %s, so there is nothing to start from it; to process an unfinished or failed descendant, start the autopilot from that ticket instead",
				req.RootID, req.RootStatus)
		}
	}

	if err := checkOverlap(all, cand, req, now); err != nil {
		return nil, nil, err
	}

	var original *Run
	if cand != nil {
		var err error
		if original, err = cloneRun(cand); err != nil {
			return nil, nil, err
		}
		var previous []byte
		if req.Reserve {
			var err error
			if previous, err = json.Marshal(cand); err != nil {
				return nil, nil, err
			}
		}
		cand.TakeOver(now, req.Settings)
		res.TookOver = true
		if req.Reserve {
			cand.Reservation = &Reservation{Previous: previous}
		}
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
		if req.Reserve {
			cand.Reservation = &Reservation{Created: true}
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
	}
	stamp(cand, req.Actor, now, sharedByID)
	return cand, original, nil
}

// stamp marks run as begun by actor at now and gives it its next revision
// -- once, here, above both its local and its shared copy's -- which the
// local file and the shared record then both carry.
func stamp(run *Run, actor *StartedBy, now time.Time, sharedByID map[string]*Run) {
	if actor != nil {
		a := *actor
		run.StartedBy = &a
	}
	rev := run.Revision
	if s := sharedByID[run.ID]; s != nil && s.Revision > rev {
		rev = s.Revision
	}
	run.Revision = rev + 1
	run.UpdatedAt = now
	run.BegunAt = now
}

func alreadyRunning(r *Run) error {
	return domain.NewAPIError(ErrCodeAlreadyRunning, "AUTOPILOT_ALREADY_RUNNING: run %s is already running%s (heartbeat %s)",
		r.ID, startedBySuffix(r), r.Heartbeat.UTC().Format(time.RFC3339)).
		WithDetails(startedByDetails(r, map[string]any{"run_id": r.ID, "root_ticket_id": r.RootTicketID}))
}

// checkOverlap refuses req if an active run other than self owns its root,
// or (tree mode) roots somewhere in its descendants. runs may include other
// members' runs; req.Descendants must not call the data source (Begin runs
// this inside the data source's transaction) -- the runner's is a closure
// over the ticket index it read before.
func checkOverlap(runs []*Run, self *Run, req BeginRequest, now time.Time) error {
	for _, r := range runs {
		if self != nil && r.ID == self.ID {
			continue
		}
		if !r.IsActive(now) {
			continue
		}
		conflict, err := overlaps(req.RootID, req.Mode, r.RootTicketID, r.Mode, req.Descendants)
		if err != nil {
			return err
		}
		if conflict {
			return domain.NewAPIError(ErrCodeAlreadyRunning,
				"AUTOPILOT_ALREADY_RUNNING: ticket %s overlaps the active %s run %s rooted at %s%s",
				req.RootID, r.Mode, r.ID, r.RootTicketID, startedBySuffix(r)).
				WithDetails(startedByDetails(r, map[string]any{"run_id": r.ID, "root_ticket_id": r.RootTicketID}))
		}
	}
	return nil
}

// CancelReservation undoes a reservation made by Begin with Reserve (the Web
// UI's launch whose terminal failed to open): a run the reservation created
// is deleted, a run it took over is put back as it was. With Shared set the
// shared record follows, after the lock is released: deleted, or put back
// (the restored run's revision is above the reservation's, see Tx.Save). A
// failure there is only logged -- the local registry is already right, and
// a reservation record left behind stops blocking other members once its
// heartbeat is ActiveThreshold old.
func (g *Registry) CancelReservation(projectID, runID string) error {
	var restored *Run
	deleted := false
	err := g.WithLock(projectID, func(tx *Tx) error {
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
			if err := tx.Delete(runID); err != nil {
				return err
			}
			deleted = true
			return nil
		}
		var prev Run
		if err := json.Unmarshal(run.Reservation.Previous, &prev); err != nil {
			return err
		}
		// Previous is not read through Load: sanitize the name before it is
		// written back here and to the shared record.
		prev.sanitizeStartedBy()
		if err := tx.Save(&prev); err != nil {
			return err
		}
		restored = &prev
		return nil
	})
	if err != nil || g.Shared == nil {
		return err
	}
	var serr error
	switch {
	case deleted:
		serr = g.Shared.Delete(runID)
	case restored != nil:
		serr = g.Shared.Save(restored)
	}
	LogSharedError(g.Logf, SharedOpUndoReservation, fmt.Sprintf("putting back the shared record of cancelled reservation %s", runID),
		"the reservation is undone on this machine; other members may see it as starting until its heartbeat expires", serr)
	return nil
}
