package autopilot

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

// This file is the autopilot's side of sharing runs between members through
// the data source (DFLT-00326). The local registry stays each machine's
// source of truth for its own runs; every time a run is saved there, its
// shared view -- the run without what only means something on this machine
// -- is written to the data source too, and a start decides on the union of
// the local runs and the shared ones, so it also sees other members' runs.

// SharedRuns is where runs are shared (the data source, adapted by the
// runner). Registry.Shared nil means runs are not shared.
type SharedRuns interface {
	// List returns every shared run of projectID (other members' included),
	// rebuilt from their shared views.
	List(projectID string) ([]*Run, error)
	// Begin reads projectID's shared runs, calls decide with them and saves
	// the run decide returns (nothing for nil), serialized against every
	// other Begin of the project where the data source can (the SQL
	// backends). decide must not call the data source.
	Begin(projectID string, decide func(shared []*Run) (*Run, error)) error
	// Save writes run's shared view, unless a copy with the same or a
	// higher Revision is already there.
	Save(run *Run) error
	// Delete removes a run's shared record; a missing one is not an error.
	Delete(projectID, runID string) error
}

// ErrSharedRunsUnsupported is what SharedRuns answers when the data source
// cannot share runs (an HTTP data source older than protocol 1.2): the
// caller decides from the local registry alone, as before DFLT-00326.
var ErrSharedRunsUnsupported = errors.New("the data source does not share autopilot runs")

// sharedUnsupportedWarned makes WarnSharedUnsupported say its piece once per
// process: the Web UI's server lists runs on every poll.
var sharedUnsupportedWarned atomic.Bool

// WarnSharedUnsupported tells the operator, once per process, that runs are
// not being shared and duplicate starts by other members cannot be caught.
func WarnSharedUnsupported(logf func(format string, args ...any)) {
	if logf == nil || !sharedUnsupportedWarned.CompareAndSwap(false, true) {
		return
	}
	logf("the HTTP data source speaks a protocol older than 1.2, so autopilot runs are not shared with other members and a start that duplicates another member's run cannot be detected (update the data source plugin to protocol 1.2)")
}

// ResetSharedUnsupportedWarning re-arms WarnSharedUnsupported (tests only).
func ResetSharedUnsupportedWarning() { sharedUnsupportedWarned.Store(false) }

// sharedErrLog thins out a repeated shared-run error: the same message is
// logged once until a different one (or success) comes along.
var sharedErrLog struct {
	mu   sync.Mutex
	last string
}

// LogSharedError reports err through logf unless it is the same as the
// previous one reported; ErrSharedRunsUnsupported goes to
// WarnSharedUnsupported instead. what says what was being done.
func LogSharedError(logf func(format string, args ...any), what string, err error) {
	if err == nil || logf == nil {
		return
	}
	if errors.Is(err, ErrSharedRunsUnsupported) {
		WarnSharedUnsupported(logf)
		return
	}
	msg := what + ": " + err.Error()
	sharedErrLog.mu.Lock()
	repeat := sharedErrLog.last == msg
	sharedErrLog.last = msg
	sharedErrLog.mu.Unlock()
	if !repeat {
		logf("autopilot: %s (the run goes on; other members may see it as interrupted until the shared copy is written again)", msg)
	}
}

// SharedView returns a deep copy of r without what only means something on
// this machine: the terminal tab it drives (TerminalTTY,
// TerminalTabDisabled), the JSON a reservation keeps to undo itself
// (Reservation.Previous), and each ticket's worktree, branch names and
// fingerprints.
func (r *Run) SharedView() (*Run, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	var v Run
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	v.TerminalTTY, v.TerminalTabDisabled = "", ""
	if v.Reservation != nil {
		v.Reservation.Previous = nil
	}
	for _, st := range v.Tickets {
		st.Worktree, st.Branch, st.BaseBranch = "", "", ""
		st.DBFingerprint, st.WorktreeFingerprint = "", ""
	}
	return &v, nil
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ToRecord is r's shared record: the columns filled from r, the snapshot
// its SharedView.
func (r *Run) ToRecord() (domain.AutopilotRunRecord, error) {
	view, err := r.SharedView()
	if err != nil {
		return domain.AutopilotRunRecord{}, err
	}
	snap, err := json.Marshal(view)
	if err != nil {
		return domain.AutopilotRunRecord{}, err
	}
	rec := domain.AutopilotRunRecord{
		ID: r.ID, ProjectID: r.ProjectID, RootTicketID: r.RootTicketID, Mode: r.Mode, State: r.State,
		Heartbeat: formatTime(r.Heartbeat), CreatedAt: formatTime(r.CreatedAt), UpdatedAt: formatTime(r.UpdatedAt),
		Revision: r.Revision, Snapshot: snap,
	}
	if r.StartedBy != nil {
		rec.StartedByName, rec.MachineID = r.StartedBy.Name, r.StartedBy.MachineID
	}
	return rec, nil
}

// RunFromRecord rebuilds a run from its shared record: the snapshot, with
// the record's columns taking precedence.
func RunFromRecord(rec domain.AutopilotRunRecord) (*Run, error) {
	var r Run
	if len(rec.Snapshot) > 0 {
		if err := json.Unmarshal(rec.Snapshot, &r); err != nil {
			return nil, fmt.Errorf("reading the shared autopilot run %s: %w", rec.ID, err)
		}
	}
	r.ID, r.ProjectID, r.RootTicketID, r.Mode, r.State = rec.ID, rec.ProjectID, rec.RootTicketID, rec.Mode, rec.State
	r.Heartbeat, r.CreatedAt, r.UpdatedAt = parseTime(rec.Heartbeat), parseTime(rec.CreatedAt), parseTime(rec.UpdatedAt)
	r.Revision = rec.Revision
	if rec.MachineID != "" || rec.StartedByName != "" {
		if r.StartedBy == nil {
			r.StartedBy = &StartedBy{}
		}
		r.StartedBy.Name, r.StartedBy.MachineID = rec.StartedByName, rec.MachineID
	}
	if r.Tickets == nil {
		r.Tickets = map[string]*TicketState{}
	}
	return &r, nil
}

// StartedByName is the name of whoever started r, "" when unknown.
func (r *Run) StartedByName() string {
	if r.StartedBy == nil {
		return ""
	}
	return r.StartedBy.Name
}

// BelongsTo reports whether r was started on the machine machineID: a run
// with no recorded machine (a run file from before DFLT-00326), or when
// machineID is unknown, counts as belonging.
func (r *Run) BelongsTo(machineID string) bool {
	return machineID == "" || r.StartedBy == nil || r.StartedBy.MachineID == "" || r.StartedBy.MachineID == machineID
}

// startedByDetails adds r's starter to an error's details.
func startedByDetails(r *Run, details map[string]any) map[string]any {
	if r.StartedBy != nil && r.StartedBy.Name != "" {
		details["started_by"] = r.StartedBy.Name
		details["name_is_fallback"] = r.StartedBy.NameIsFallback
	}
	return details
}

// startedBySuffix is " (started by <name>)" for an error message, "" when
// the starter is unknown.
func startedBySuffix(r *Run) string {
	if r.StartedBy == nil || r.StartedBy.Name == "" {
		return ""
	}
	if r.StartedBy.NameIsFallback {
		return fmt.Sprintf(" (started by %s, name not set)", r.StartedBy.Name)
	}
	return fmt.Sprintf(" (started by %s)", r.StartedBy.Name)
}
