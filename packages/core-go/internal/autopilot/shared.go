package autopilot

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/graph-ops/core-go/internal/displayname"
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
	// Begin reads projectID's shared runs and calls decide with them; then
	// it saves the run decide returns (nothing for nil) and deletes the
	// records decide lists in drop (the retention of settled records, see
	// SharedRetention), serialized against every other Begin and Save of
	// the project where the data source can (the SQL backends: all in one
	// transaction). decide must not call the data source. Where the data
	// source cannot delete atomically (HTTP), a drop that fails is left for
	// a later start rather than failing this one.
	Begin(projectID string, decide func(shared []*Run) (run *Run, drop []string, err error)) error
	// Save writes run's shared view, unless a copy with the same or a
	// higher Revision is already there.
	Save(run *Run) error
	// Delete removes a run's shared record; a missing one is not an error.
	// Run IDs are unique across projects, so the ID alone names it.
	Delete(runID string) error
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
// logged once until a different one, or a success, comes along.
var sharedErrLog struct {
	mu   sync.Mutex
	last string
}

// LogSharedError reports the outcome of a data source call for shared runs.
//
// A failure is reported through logf as "autopilot: <what>: <err>
// (<consequence>)" unless it is the same as the previous failure reported,
// so a data source that stays down is not logged on every poll of the Web
// UI. what says what was being done, consequence what the failure means for
// the caller -- they differ between writing a run and listing runs, so each
// caller says its own. ErrSharedRunsUnsupported goes to
// WarnSharedUnsupported instead.
//
// A success (err nil) re-arms it: the next failure is reported even if it
// is the same one as before, and if a failure had been reported, one line
// says the shared runs are reachable again.
func LogSharedError(logf func(format string, args ...any), what, consequence string, err error) {
	if err == nil {
		sharedErrLog.mu.Lock()
		recovered := sharedErrLog.last != ""
		sharedErrLog.last = ""
		sharedErrLog.mu.Unlock()
		if recovered && logf != nil {
			logf("autopilot: the shared autopilot runs are reachable again (%s)", what)
		}
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
	if repeat || logf == nil {
		return
	}
	if consequence == "" {
		logf("autopilot: %s", msg)
		return
	}
	logf("autopilot: %s (%s)", msg, consequence)
}

// ResetSharedErrorLog forgets the last shared-run error reported (tests
// only).
func ResetSharedErrorLog() {
	sharedErrLog.mu.Lock()
	sharedErrLog.last = ""
	sharedErrLog.mu.Unlock()
}

// SharedView returns a deep copy of r without what only means something on
// this machine: the terminal tab it drives (TerminalTTY,
// TerminalTabDisabled), the JSON a reservation keeps to undo itself
// (Reservation.Previous), and each ticket's worktree, branch names and
// fingerprints.
func (r *Run) SharedView() (*Run, error) {
	v, err := cloneRun(r)
	if err != nil {
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
	return v, nil
}

// cloneRun returns a deep copy of r (a JSON round trip: a Run holds only
// JSON-shaped data).
func cloneRun(r *Run) (*Run, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("copying autopilot run %s: %w", r.ID, err)
	}
	var c Run
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("copying autopilot run %s: %w", r.ID, err)
	}
	return &c, nil
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
		rec.StartedByName, rec.MachineID = displayname.Sanitize(r.StartedBy.Name), r.StartedBy.MachineID
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
	r.sanitizeStartedBy()
	return &r, nil
}

// sanitizeStartedBy passes the starter's name, which may come from another
// member's machine (or a direct write to the data source), through
// displayname.Sanitize (DFLT-00336). A name that becomes "" is an unknown
// name; the machine ID is kept either way, since that -- not the name -- is
// what BelongsTo matches.
func (r *Run) sanitizeStartedBy() {
	if r.StartedBy == nil {
		return
	}
	r.StartedBy.Name = displayname.Sanitize(r.StartedBy.Name)
	if r.StartedBy.Name == "" {
		r.StartedBy.NameIsFallback = false
	}
}

// startedByName is r's starter's name, sanitized, and whether it is the
// "<OS user>@<host>" fallback; "" when unknown. Every display of the name
// goes through it, even of a run that did not come through one of the
// sanitizing reads (Sanitize is idempotent, so a second pass changes
// nothing).
func (r *Run) startedByName() (name string, fallback bool) {
	if r.StartedBy == nil {
		return "", false
	}
	name = displayname.Sanitize(r.StartedBy.Name)
	return name, name != "" && r.StartedBy.NameIsFallback
}

// StartedByName is the name of whoever started r, sanitized for display;
// "" when unknown.
func (r *Run) StartedByName() string {
	name, _ := r.startedByName()
	return name
}

// BelongsTo reports whether r was started on the machine machineID: a run
// with no recorded machine (a run file from before DFLT-00326), or when
// machineID is unknown, counts as belonging.
func (r *Run) BelongsTo(machineID string) bool {
	return machineID == "" || r.StartedBy == nil || r.StartedBy.MachineID == "" || r.StartedBy.MachineID == machineID
}

// startedByDetails adds r's starter to an error's details.
func startedByDetails(r *Run, details map[string]any) map[string]any {
	if name, fallback := r.startedByName(); name != "" {
		details["started_by"] = name
		details["name_is_fallback"] = fallback
	}
	return details
}

// startedBySuffix is " (started by <name>)" for an error message, "" when
// the starter is unknown.
func startedBySuffix(r *Run) string {
	name, fallback := r.startedByName()
	if name == "" {
		return ""
	}
	if fallback {
		return fmt.Sprintf(" (started by %s, name not set)", name)
	}
	return fmt.Sprintf(" (started by %s)", name)
}

// KeepSharedSettledRuns is how many settled records -- runs that are not
// active: finished, stopped, or with an expired heartbeat -- the data source
// keeps per project, whoever started them (see SharedRetention).
const KeepSharedSettledRuns = KeepSettledRuns

// SharedRetention returns the IDs of the shared records to delete so that
// at most KeepSharedSettledRuns settled ones remain: the settled records
// beyond the KeepSharedSettledRuns most recently updated, whoever started
// them. Active records, and keep (the run being begun), are never among
// them.
//
// Without it a record whose machine never starts a run again -- a member
// who left, a replaced machine, a removed machine-id -- would stay until
// the project is deleted, and every listing would read it. Deleting another
// member's settled record takes nothing from that member: its local run
// file stays the source of truth, a takeover saves the record again, and
// so does the next save of a run that comes back after its heartbeat had
// expired.
func SharedRetention(shared []*Run, keep string, now time.Time) []string {
	var settled []*Run
	for _, r := range shared {
		if r.ID != keep && !r.IsActive(now) {
			settled = append(settled, r)
		}
	}
	if len(settled) <= KeepSharedSettledRuns {
		return nil
	}
	sort.Slice(settled, func(i, j int) bool {
		if !settled[i].UpdatedAt.Equal(settled[j].UpdatedAt) {
			return settled[i].UpdatedAt.After(settled[j].UpdatedAt)
		}
		return settled[i].ID > settled[j].ID
	})
	drop := make([]string, 0, len(settled)-KeepSharedSettledRuns)
	for _, r := range settled[KeepSharedSettledRuns:] {
		drop = append(drop, r.ID)
	}
	return drop
}

// begunAt is when r was last begun: BegunAt, or CreatedAt for a run file
// written before DFLT-00326.
func (r *Run) begunAt() time.Time {
	if !r.BegunAt.IsZero() {
		return r.BegunAt
	}
	return r.CreatedAt
}

// overlaps reports whether a run rooted at rootA in modeA and one rooted at
// rootB in modeB own a ticket in common: one owns the other's root (a
// ticket-mode run owns its root, a tree-mode run its root and every
// descendant). It is the overlap Begin refuses (checkOverlap).
func overlaps(rootA, modeA, rootB, modeB string, descendants func(string) ([]string, error)) (bool, error) {
	if rootA == rootB {
		return true, nil
	}
	owns := func(root, mode, id string) (bool, error) {
		if mode != ModeTree || descendants == nil {
			return false, nil
		}
		desc, err := descendants(root)
		if err != nil {
			return false, err
		}
		for _, d := range desc {
			if d == id {
				return true, nil
			}
		}
		return false, nil
	}
	if ok, err := owns(rootA, modeA, rootB); ok || err != nil {
		return ok, err
	}
	return owns(rootB, modeB, rootA)
}

// Overtaker returns the run that has overtaken r, or nil: an active run in
// others, other than r, that overlaps r and was begun after r (ties go to
// the higher run ID).
//
// Begin never lets two overlapping runs both be active, but a run whose
// heartbeat expired stops counting -- so another member can begin the same
// tree -- and it counts again once its machine wakes up and saves a
// heartbeat. From then on both would go on launching sessions for the same
// tickets. The run begun earlier is the one that yields: the other one was
// begun, and judged, while this one did not count. (On an HTTP data
// source, where two starts at the same moment may both pass, this also
// settles which of them goes on.) The runner asks before every next and
// launch, and stops r with StopOvertaken when it has been overtaken.
func Overtaker(r *Run, others []*Run, descendants func(string) ([]string, error), now time.Time) (*Run, error) {
	mine := r.begunAt()
	for _, o := range others {
		if o.ID == r.ID || !o.IsActive(now) {
			continue
		}
		theirs := o.begunAt()
		if theirs.Before(mine) || (theirs.Equal(mine) && o.ID < r.ID) {
			continue
		}
		ok, err := overlaps(r.RootTicketID, r.Mode, o.RootTicketID, o.Mode, descendants)
		if err != nil {
			return nil, err
		}
		if ok {
			return o, nil
		}
	}
	return nil, nil
}

// StopOvertakenBy stops r, overtaken by o (see Overtaker), naming o and who
// started it in the stop's detail.
func (r *Run) StopOvertakenBy(o *Run, now time.Time) {
	detail := fmt.Sprintf("the overlapping run %s rooted at %s%s was started at %s, while this run's heartbeat had expired; this run stopped so as not to work on the same tickets alongside it -- start it again once that run has ended",
		o.ID, o.RootTicketID, startedBySuffix(o), o.begunAt().UTC().Format(time.RFC3339))
	r.stop(now, StopOvertaken, "", detail)
}
