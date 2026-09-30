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
	// transaction). decide must not call the data source.
	//
	// err is the start's own failure (nothing saved). dropErr is non-nil
	// only when the run was saved but deleting some of drop failed, which
	// only happens where the data source cannot delete atomically (HTTP):
	// the start has succeeded, and the records left are dropped by a later
	// start. There, too, only the first few records of drop are deleted per
	// start (store.AutopilotRunStore), so drop lists the records to delete
	// first -- the oldest -- at its head.
	Begin(projectID string, decide func(shared []*Run) (run *Run, drop []string, err error)) (dropErr error, err error)
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

// SharedOp is the kind of data source call LogSharedError reports on: each
// kind is thinned out on its own, so one kind failing while another
// succeeds neither re-arms the failing one nor says it is reachable again.
type SharedOp string

const (
	// SharedOpList is listing a project's shared runs (the runs listing,
	// and the overtaking check of next and launch).
	SharedOpList SharedOp = "list"
	// SharedOpShare is writing a run's shared copy after a local save.
	SharedOpShare SharedOp = "share"
	// SharedOpUndoReservation is putting back the shared record of a
	// cancelled reservation.
	SharedOpUndoReservation SharedOp = "undo-reservation"
	// SharedOpRetention is deleting settled shared records when a run
	// starts (SharedRetention).
	SharedOpRetention SharedOp = "retention"
)

// sharedErrLog thins out a repeated shared-run error: per SharedOp, the
// same message is logged once until a different one, or a success of the
// same kind, comes along.
var sharedErrLog struct {
	mu   sync.Mutex
	last map[SharedOp]string
}

// LogSharedError reports the outcome of a data source call of kind op for
// shared runs.
//
// A failure is reported through logf as "autopilot: <what>: <err>
// (<consequence>)" unless it is the same as the previous failure reported
// for op, so a data source that stays down is not logged on every poll of
// the Web UI. what says what was being done, consequence what the failure
// means for the caller -- they differ between writing a run and listing
// runs, so each caller says its own. ErrSharedRunsUnsupported goes to
// WarnSharedUnsupported instead.
//
// A success (err nil) re-arms op only: the next failure of op is reported
// even if it is the same one as before, and if a failure of op had been
// reported, one line says the shared runs are reachable again. A success
// of another kind changes nothing for op -- listing may keep failing while
// writing works, and saying "reachable again" then would be misleading.
// The state is per kind, not per project: what names the project, so the
// log tells projects apart.
func LogSharedError(logf func(format string, args ...any), op SharedOp, what, consequence string, err error) {
	if err == nil {
		sharedErrLog.mu.Lock()
		recovered := sharedErrLog.last[op] != ""
		delete(sharedErrLog.last, op)
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
	repeat := sharedErrLog.last[op] == msg
	if sharedErrLog.last == nil {
		sharedErrLog.last = map[SharedOp]string{}
	}
	sharedErrLog.last[op] = msg
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

// ResetSharedErrorLog forgets the last shared-run error reported of every
// kind (tests only).
func ResetSharedErrorLog() {
	sharedErrLog.mu.Lock()
	sharedErrLog.last = nil
	sharedErrLog.mu.Unlock()
}

// SharedView returns a deep copy of r without what only means something on
// this machine: the terminal tab it drives (TerminalTTY,
// TerminalTabDisabled, TerminalTabSlowTimeouts, TerminalTabFailures), the
// JSON a reservation keeps to undo itself (Reservation.Previous), and each
// ticket's worktree, branch names and fingerprints.
func (r *Run) SharedView() (*Run, error) {
	v, err := cloneRun(r)
	if err != nil {
		return nil, err
	}
	v.dropLocalOnly()
	return v, nil
}

// dropLocalOnly clears, in place, what SharedView leaves out of a run's
// shared view (see there).
//
// RunFromRecord calls it too (DFLT-00339): a record saved the regular way
// has none of it anyway, but a member who writes to the data source
// directly could put a branch name or a tty there, and none of it is ever
// used from another member's run -- a shared run is only listed, checked
// for overlaps (decideBegin, Overtaker), counted for retention
// (SharedRetention, which looks at IDs, states and times) and asked for its
// revision (stamp), and it never becomes a local run file. If a shared
// record is ever used to rebuild a local run, this needs another look.
func (r *Run) dropLocalOnly() {
	r.TerminalTTY, r.TerminalTabDisabled = "", ""
	r.TerminalTabSlowTimeouts, r.TerminalTabFailures = 0, nil
	if r.Reservation != nil {
		r.Reservation.Previous = nil
	}
	for _, st := range r.Tickets {
		if st == nil {
			continue
		}
		st.Worktree, st.Branch, st.BaseBranch = "", "", ""
		st.DBFingerprint, st.WorktreeFingerprint = "", ""
	}
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
// the record's columns taking precedence, and without what only means
// something on the machine that wrote it (dropLocalOnly).
//
// The record may be another member's, or written to the data source
// directly, so its strings are not trusted for display; they are not
// rewritten here, though (DFLT-00339): the IDs, root, project, mode, state
// and machine ID are what runs are matched on -- by ID, for overlaps, for
// retention (the IDs deleted), by BelongsTo -- so they are kept as they are,
// and every display sanitizes them instead (runner's runStatus, the start
// errors, StopOvertakenBy). Only the starter's name is sanitized here
// (sanitizeStartedBy), as it is used for display alone.
func RunFromRecord(rec domain.AutopilotRunRecord) (*Run, error) {
	var r Run
	if len(rec.Snapshot) > 0 {
		if err := json.Unmarshal(rec.Snapshot, &r); err != nil {
			return nil, fmt.Errorf("reading the shared autopilot run %s: %w", displayname.ID(rec.ID), err)
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
	r.dropLocalOnly()
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

// runDetails is an error's details naming r (DFLT-00339): its run ID and
// root, through displayname.ID since r may be another member's run, and its
// starter (startedByDetails).
func runDetails(r *Run) map[string]any {
	return startedByDetails(r, map[string]any{"run_id": displayname.ID(r.ID), "root_ticket_id": displayname.ID(r.RootTicketID)})
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
// them, the least recently updated first -- a data source that deletes only
// a few per start (HTTP) then drops the oldest first. Active records, and
// keep (the run being begun), are never among them; this is where the run
// being begun is left out (the data source skipping it too is only a
// safeguard).
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
	for i := len(settled) - 1; i >= KeepSharedSettledRuns; i-- {
		drop = append(drop, settled[i].ID)
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
// started it in the stop's detail. o is another member's run, so its ID and
// root are sanitized (displayname.ID) before they go into the detail
// (DFLT-00339): the detail is saved in r's own run file and shared record,
// and shown by next, launch, summary and the Web UI from there on -- the
// one way a value of another member's run gets into this machine's runs.
func (r *Run) StopOvertakenBy(o *Run, now time.Time) {
	detail := fmt.Sprintf("the overlapping run %s rooted at %s%s was started at %s, while this run's heartbeat had expired; this run stopped so as not to work on the same tickets alongside it -- start it again once that run has ended",
		displayname.ID(o.ID), displayname.ID(o.RootTicketID), startedBySuffix(o), o.begunAt().UTC().Format(time.RFC3339))
	r.stop(now, StopOvertaken, "", detail)
}

// RecordActive reports whether the run behind a shared record is still
// active at now -- not finished or stopped, and with a heartbeat within
// ActiveThreshold (Run.IsActive) -- without decoding its snapshot. The
// engine uses it to decide the lease of a node claimed by an autopilot
// worker (DFLT-00327).
func RecordActive(rec domain.AutopilotRunRecord, now time.Time) bool {
	r := Run{State: rec.State, Heartbeat: parseTime(rec.Heartbeat)}
	return r.IsActive(now)
}
