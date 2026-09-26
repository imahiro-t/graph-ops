package autopilot

import (
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// This file is the autopilot run record (plan decision D4): what one
// `autopilot start` keeps about its tree between the orchestrator's CLI
// calls. The orchestrator is an LLM session with no resident process, so the
// record lives in a file (see Registry) and every `autopilot` subcommand
// loads it, acts, and saves it back.

// Modes.
const (
	ModeTicket = "ticket"
	ModeTree   = "tree"
)

// Run states.
const (
	RunStarting   = "starting"   // reserved (the Web UI's launch), not yet adopted by an orchestrator
	RunRunning    = "running"    // being driven
	RunFinalizing = "finalizing" // the finalize session is running
	RunFinished   = "finished"   // done: every ticket reached an end state
	RunStopped    = "stopped"    // stopped by onFailure: stop, or a failed finalize
)

// Ticket states within a run.
const (
	TicketQueued   = "queued"   // next chose it; launch has not run yet
	TicketLaunched = "launched" // its work session is running
	TicketDone     = "done"
	TicketFailed   = "failed"
	TicketBlocked  = "blocked"
	TicketSkipped  = "skipped"
)

// Session roles (the worker's --role).
const (
	RoleWork     = "work"
	RoleMergeUp  = "merge-up"
	RoleFinalize = "finalize"
)

// Reason codes for skipped / failed / blocked tickets and stopped runs.
const (
	ReasonAlreadyDone         = "already_done"
	ReasonClosed              = "closed"
	ReasonInProgressElsewhere = "in_progress_elsewhere"
	ReasonAncestorInProgress  = "ancestor_in_progress"
	ReasonLimitTickets        = "limit_tickets"
	ReasonLimitDepth          = "limit_depth"
	ReasonParentFailed        = "parent_failed"
	ReasonUnresponsive        = "unresponsive"
	ReasonMergeConflict       = "merge_conflict"
	// ReasonLaunchFailed: the session could not be started (git could not
	// prepare the worktree, or the terminal did not open) MaxLaunchAttempts
	// times in a row.
	ReasonLaunchFailed = "launch_failed"

	StopTicketFailed   = "ticket_failed"
	StopFinalizeFailed = "finalize_failed"
)

// Merge states: how far a ticket's branch has been carried towards its
// merge target (plan decision D6).
const (
	MergedSelf    = "merged_self"    // the worker's merge-into-parent fast-forwarded it
	MergedSubtree = "merged_subtree" // merge-up carried it (with its whole subtree) up
	NotMerged     = "not_merged"     // failed/blocked: deliberately left out of the target
)

// ActiveThreshold is how recent a run's heartbeat has to be for the run to
// count as active (D4). A run in a non-final state whose heartbeat is older is
// "interrupted": a start for the same root takes it over.
const ActiveThreshold = 10 * time.Minute

// Summary limits for report (D5): 3 lines, 500 characters.
const (
	SummaryMaxLines = 3
	SummaryMaxChars = 500
)

// Run is one autopilot run.
type Run struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"project_id"`
	Mode         string    `json:"mode"`
	RootTicketID string    `json:"root_ticket_id"`
	State        string    `json:"state"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Heartbeat    time.Time `json:"heartbeat"`
	// Settings is the snapshot the run acts on, refreshed each time a start
	// takes the run (over), so a setting changed to get past a stop applies.
	Settings Settings `json:"settings"`
	// Generation counts the starts that have taken this run: 1 on creation,
	// +1 for every takeover. A failed/blocked ticket is re-launched once per
	// generation (S6).
	Generation int `json:"generation"`
	// WorkLaunches is how many distinct tickets have been launched as work,
	// the count maxTickets limits. A re-launch is not counted again (S6).
	WorkLaunches int `json:"work_launches"`
	// Tickets holds every ticket this run has classified, by ID; Order is
	// the order in which they were first classified (DFS pre-order).
	Tickets map[string]*TicketState `json:"tickets"`
	Order   []string                `json:"order"`
	// Finalize is the finalize session's outcome (tree mode, mainReflection
	// other than branch); nil until it is launched.
	Finalize *Outcome `json:"finalize,omitempty"`
	// StopReason / StopTicket / StopDetail describe the latest stop; Stops
	// keeps every stop for the summary, since a takeover clears the former.
	StopReason string       `json:"stop_reason,omitempty"`
	StopTicket string       `json:"stop_ticket,omitempty"`
	StopDetail string       `json:"stop_detail,omitempty"`
	Stops      []StopRecord `json:"stops,omitempty"`
	// Reservation is set while a reserved run (state starting) waits for
	// its orchestrator: it holds what the run looked like before the
	// reservation, so CancelReservation can put it back.
	Reservation *Reservation `json:"reservation,omitempty"`
	// TerminalTTY is the tty of the Terminal.app tab the run's current
	// orchestrator runs in (DFLT-00154): its child sessions open as new tabs
	// of that tab's window. "" (another terminal, tmux, not detectable)
	// opens them in new windows. Every start that decides who drives the run
	// -- creation, takeover, adoption of a reservation -- overwrites it with
	// that orchestrator's value, even an empty one; a reservation clears it.
	TerminalTTY string `json:"terminal_tty,omitempty"`
	// TerminalTabDisabled, when set, is why the tab path failed in a way that
	// would repeat (a missing permission, a timeout): the rest of the run
	// opens its sessions in new windows without trying a tab. Cleared by the
	// same starts that set TerminalTTY.
	TerminalTabDisabled string `json:"terminal_tab_disabled,omitempty"`
}

// Reservation remembers what a reservation replaced.
type Reservation struct {
	// Created: the reservation created the run (cancelling deletes it).
	Created bool `json:"created"`
	// Previous is the run's JSON before a reservation that took it over.
	Previous []byte `json:"previous,omitempty"`
}

// StopRecord is one stop in a run's history.
type StopRecord struct {
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
	Ticket string    `json:"ticket,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

// Outcome is a finished (or running) session's result.
type Outcome struct {
	Status  string `json:"status"` // launched / done / failed / blocked
	Reason  string `json:"reason,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// TicketState is one ticket's record within a run.
type TicketState struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id,omitempty"`
	Depth    int    `json:"depth"`
	Status   string `json:"status"`
	Reason   string `json:"reason,omitempty"`
	Detail   string `json:"detail,omitempty"`
	// SkipCause is the failed ticket a parent_failed skip is due to; the
	// skip is lifted if that ticket is later retried and succeeds.
	SkipCause string `json:"skip_cause,omitempty"`

	// Target is the ticket whose branch this one is based on and merges
	// into: the nearest ancestor this run launched as work (S3). Empty for
	// the root.
	Target     string `json:"target,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Worktree   string `json:"worktree,omitempty"`
	BaseBranch string `json:"base_branch,omitempty"`
	Merge      string `json:"merge,omitempty"`
	// MergeNeedsSession: merge-up could not fast-forward, so a merge-up
	// session has to merge by hand.
	MergeNeedsSession bool `json:"merge_needs_session,omitempty"`

	// Counted: counted in Run.WorkLaunches.
	Counted bool `json:"counted,omitempty"`
	// LaunchedGeneration is the run generation of the latest work launch.
	LaunchedGeneration int `json:"launched_generation,omitempty"`
	// RetryPending: failed/blocked in an earlier generation, to be launched
	// once more in this one (S6).
	RetryPending bool `json:"retry_pending,omitempty"`
	// FailedRole is the role of the session whose report failed/blocked
	// this ticket (work or merge-up).
	FailedRole string `json:"failed_role,omitempty"`
	// LaunchFailures counts the launches in a row that failed to start this
	// ticket's session; reset by a launch that succeeds.
	LaunchFailures int `json:"launch_failures,omitempty"`

	// Role is the role of the session running for this ticket right now,
	// "" when none is. LastRole is the role of the latest one.
	Role       string    `json:"role,omitempty"`
	LastRole   string    `json:"last_role,omitempty"`
	LaunchedAt time.Time `json:"launched_at,omitempty"`
	// Activity (D4-2): the last time any sign of life was seen, and what it
	// was; the fingerprints it is compared against.
	LastActivityAt      time.Time `json:"last_activity_at,omitempty"`
	LastActivityKind    string    `json:"last_activity_kind,omitempty"`
	DBFingerprint       string    `json:"db_fingerprint,omitempty"`
	WorktreeFingerprint string    `json:"worktree_fingerprint,omitempty"`
	// AwaitingHuman is what the worker said it is waiting for a person on
	// (touch --awaiting-human); such a session is exempt from the stall
	// check until the next activity.
	AwaitingHuman string `json:"awaiting_human,omitempty"`

	Summary    string `json:"summary,omitempty"`
	LateReport string `json:"late_report,omitempty"`
	// PendingDecisions are automatic decisions recorded before the ticket
	// had a node to attach them to (D7), by kind.
	PendingDecisions map[string]string `json:"pending_decisions,omitempty"`
}

// IsFinal reports whether the run has ended (finished or stopped).
func (r *Run) IsFinal() bool { return r.State == RunFinished || r.State == RunStopped }

// IsActive reports whether the run is active at now: not final, and its
// heartbeat within ActiveThreshold.
func (r *Run) IsActive(now time.Time) bool {
	return !r.IsFinal() && now.Sub(r.Heartbeat) <= ActiveThreshold
}

// Interrupted reports whether the run was left in a non-final state and its
// heartbeat has gone stale.
func (r *Run) Interrupted(now time.Time) bool {
	return !r.IsFinal() && now.Sub(r.Heartbeat) > ActiveThreshold
}

// Ticket returns the state of ticket id, or nil.
func (r *Run) Ticket(id string) *TicketState {
	if r.Tickets == nil {
		return nil
	}
	return r.Tickets[id]
}

// addTicket records st, keeping Order. A ticket classified again (after
// dropTicket) keeps its original place in Order.
func (r *Run) addTicket(st *TicketState) {
	if r.Tickets == nil {
		r.Tickets = map[string]*TicketState{}
	}
	if _, ok := r.Tickets[st.ID]; !ok && !r.inOrder(st.ID) {
		r.Order = append(r.Order, st.ID)
	}
	r.Tickets[st.ID] = st
}

func (r *Run) inOrder(id string) bool {
	for _, o := range r.Order {
		if o == id {
			return true
		}
	}
	return false
}

// dropTicket forgets a ticket's classification so the planner classifies it
// afresh the next time it reaches it. Its place in Order is kept.
func (r *Run) dropTicket(id string) { delete(r.Tickets, id) }

// reclassifiedOnTakeOver are the skips a takeover forgets, because what they
// were decided from may have changed since: the settings (maxTickets,
// maxDepth) that a person raises to get past a stop, and the DB statuses
// (DONE, CLOSED, IN PROGRESS elsewhere) that may have moved on. A
// parent_failed skip is re-decided by the planner itself once its cause is
// retried.
var reclassifiedOnTakeOver = map[string]bool{
	ReasonLimitTickets:        true,
	ReasonLimitDepth:          true,
	ReasonAlreadyDone:         true,
	ReasonClosed:              true,
	ReasonInProgressElsewhere: true,
	ReasonAncestorInProgress:  true,
}

// ActiveSession returns the ticket that has a session running, or nil. A
// run processes serially, so there is at most one.
func (r *Run) ActiveSession() *TicketState {
	for _, id := range r.Order {
		if st := r.Tickets[id]; st != nil && st.Role != "" {
			return st
		}
	}
	return nil
}

// LaunchedTicketIDs returns the IDs of the tickets this run has launched as
// work, sorted.
func (r *Run) LaunchedTicketIDs() []string {
	var ids []string
	for id, st := range r.Tickets {
		if st.LaunchedGeneration > 0 {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (r *Run) stop(now time.Time, reason, ticket, detail string) {
	r.State = RunStopped
	r.StopReason, r.StopTicket, r.StopDetail = reason, ticket, detail
	r.Stops = append(r.Stops, StopRecord{At: now, Reason: reason, Ticket: ticket, Detail: detail})
}

// TakeOver prepares an interrupted or stopped run for another start (S5/S6):
// back to running, a new generation, settings refreshed, and the failed
// pieces queued for one more attempt -- failed/blocked work is re-launched
// once, a merge-up that could not be done is tried again, and a failed
// finalize is launched again (S7). Records of done tickets are kept; skips
// that depended on the settings or on DB statuses are forgotten, so the
// planner decides them again under the refreshed settings -- raising
// maxTickets or maxDepth and running the same command again processes the
// tickets the limits had left out.
func (r *Run) TakeOver(now time.Time, settings Settings) {
	r.State = RunRunning
	r.Generation++
	r.Settings = settings
	r.Heartbeat = now
	r.StopReason, r.StopTicket, r.StopDetail = "", "", ""
	for id, st := range r.Tickets {
		if st.Status == TicketSkipped && reclassifiedOnTakeOver[st.Reason] {
			r.dropTicket(id)
		}
	}
	for _, st := range r.Tickets {
		if st.Status != TicketFailed && st.Status != TicketBlocked {
			continue
		}
		if st.FailedRole == RoleMergeUp {
			// The ticket's own work is done; only carrying it up failed.
			st.Status, st.Reason, st.Detail = TicketDone, "", ""
			st.FailedRole = ""
			st.MergeNeedsSession = false
			if st.Merge == NotMerged {
				st.Merge = ""
			}
			continue
		}
		st.RetryPending = true
	}
	if r.Finalize != nil && r.Finalize.Status != TicketDone && r.Finalize.Status != TicketLaunched {
		r.Finalize = nil
	}
}

// TruncateSummary cuts s to SummaryMaxLines lines and SummaryMaxChars
// characters (runes), trimming surrounding blank space.
func TruncateSummary(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	lines := strings.Split(s, "\n")
	if len(lines) > SummaryMaxLines {
		lines = lines[:SummaryMaxLines]
	}
	s = strings.Join(lines, "\n")
	if utf8.RuneCountInString(s) > SummaryMaxChars {
		r := []rune(s)
		s = string(r[:SummaryMaxChars-1]) + "…"
	}
	return s
}
