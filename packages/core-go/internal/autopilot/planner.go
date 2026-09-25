package autopilot

import (
	"fmt"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

// This file is `autopilot next`'s decision (plan phase 3-2): given the run,
// the ticket tree as it is in the DB right now, and the clock, what is the
// one thing the orchestrator should do next. It does no I/O -- the caller
// loads the inputs and saves the run -- so every rule below is testable
// without a DB, git or a terminal.
//
// The rules, in the plan's and the spec's words:
//
//   - Tree mode walks the tree depth first, children in creation order, and
//     launches tickets in pre-order (D6). Ticket mode has only the root.
//   - A ticket's subtree is complete when the ticket and every descendant the
//     run reached are in an end state. On the way back up (post-order), a done
//     ticket with a merge target whose subtree is complete is merged up into
//     that target before anything after it starts (D6's merge-up).
//   - Skips (S1-S3): DONE -> already_done, CLOSED -> closed (their unfinished
//     descendants are still processed), IN PROGRESS that no run of this
//     registry launched -> in_progress_elsewhere (and its descendants
//     ancestor_in_progress), deeper than maxDepth -> limit_depth, beyond
//     maxTickets work launches -> limit_tickets. The root is never skipped
//     (S4).
//   - onFailure (D6): stop stops the run at the first failed/blocked ticket
//     (no merge-up, no finalize); continue leaves the ticket's branch out
//     (not_merged), skips its unlaunched descendants (parent_failed), and
//     treats its subtree as complete.
//   - The stall check (D4-2): a session with no sign of activity for
//     stallTimeoutMinutes fails as unresponsive, unless it said it is waiting
//     for a person.
//   - At the end (S7, S8): a failed root never finalizes; tree mode with a
//     mainReflection other than branch launches one finalize session, whose
//     failure stops the run as finalize_failed.

// Next actions.
const (
	ActionLaunch  = "launch"
	ActionWait    = "wait"
	ActionMergeUp = "merge-up"
	ActionDone    = "done"
	ActionStopped = "stopped"
)

// TreeTicket is one ticket of the DB tree, as the planner sees it.
type TreeTicket struct {
	ID       string
	ParentID string
	Status   domain.TicketStatus
	// Children in creation order.
	Children []string
}

// Tree is the root's subtree in the DB, by ticket ID.
type Tree map[string]*TreeTicket

// PlanInput is everything Next decides from.
type PlanInput struct {
	Run  *Run
	Tree Tree
	// ForeignLaunched are tickets that an inactive run of this project's
	// registry launched (S6): their IN PROGRESS is a leftover of that run,
	// not somebody else working on them.
	ForeignLaunched map[string]bool
	Now             time.Time
}

// Action is Next's answer.
type Action struct {
	Action string `json:"action"`
	Ticket string `json:"ticket,omitempty"`
	Role   string `json:"role,omitempty"`
	Reason string `json:"reason,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// IsInProgress reports whether a ticket status means somebody is working on
// it: IN PROGRESS, and the statuses a ticket moves through while its graph
// runs (IN REVIEW, IN RELEASE).
func IsInProgress(s domain.TicketStatus) bool {
	return s == domain.TicketInProgress || s == domain.TicketInReview || s == domain.TicketInRelease
}

// Next decides the run's next action, updating in.Run (classifications,
// skips, stall failures, the run's state) along the way. The caller saves
// the run afterwards.
func Next(in PlanInput) Action {
	run := in.Run
	switch run.State {
	case RunFinished:
		return Action{Action: ActionDone}
	case RunStopped:
		return Action{Action: ActionStopped, Reason: run.StopReason, Ticket: run.StopTicket, Detail: run.StopDetail}
	}
	CheckStall(run, in.Now)
	p := &planner{in: in, run: run}
	if a := p.visit(run.RootTicketID, 0, "", "", ""); a != nil {
		return *a
	}
	return p.finish()
}

// CheckStall fails every session that has shown no activity for the run's
// stallTimeoutMinutes (D4-2) and returns the affected ticket IDs. A session
// waiting for a person (AwaitingHuman) is exempt.
func CheckStall(run *Run, now time.Time) []string {
	limit := time.Duration(run.Settings.StallTimeoutMinutes) * time.Minute
	if limit <= 0 {
		return nil
	}
	var failed []string
	for _, id := range run.Order {
		st := run.Tickets[id]
		if st == nil || st.Role == "" || st.AwaitingHuman != "" {
			continue
		}
		if now.Sub(st.LastActivityAt) < limit {
			continue
		}
		detail := fmt.Sprintf("no sign of activity for %d minutes; last activity: %s (%s)",
			int(now.Sub(st.LastActivityAt).Minutes()), st.LastActivityAt.UTC().Format(time.RFC3339), orNone(st.LastActivityKind))
		ApplyOutcome(run, st, TicketFailed, ReasonUnresponsive, detail, "")
		failed = append(failed, id)
	}
	return failed
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// ApplyOutcome records the end of st's running session (or, with no session
// running, overwrites its latest result: the last report wins) and returns
// false when the report is a late one -- one for a session already failed
// as unresponsive -- which is kept as LateReport without changing anything
// else (D4-2).
func ApplyOutcome(run *Run, st *TicketState, result, reason, detail, summary string) bool {
	role := st.Role
	if role == "" {
		if isUnresponsive(run, st) {
			st.LateReport = summary
			return false
		}
		role = st.LastRole
	}
	if role == "" {
		role = RoleWork
	}
	switch role {
	case RoleFinalize:
		if run.Finalize == nil {
			run.Finalize = &Outcome{}
		}
		run.Finalize.Status, run.Finalize.Reason, run.Finalize.Detail, run.Finalize.Summary = result, reason, detail, summary
		if result != TicketDone && run.State == RunFinalizing {
			run.State = RunRunning
		}
	case RoleMergeUp:
		if summary != "" {
			st.Summary = summary
		}
		if result == TicketDone {
			markMerged(run, st, MergedSubtree)
			st.Status, st.Reason, st.Detail, st.FailedRole = TicketDone, "", "", ""
		} else {
			if reason == "" {
				reason = ReasonMergeConflict
			}
			st.Status, st.Reason, st.Detail, st.FailedRole = result, reason, detail, RoleMergeUp
			// A ticket whose own merge-into-parent went through stays
			// merged_self: its own commits are in the target; only what
			// its subtree merged into it since is not (the summary says
			// so).
			if st.Merge != MergedSelf {
				st.Merge = NotMerged
			}
		}
		st.MergeNeedsSession = false
	default:
		st.Status, st.Reason, st.Detail = result, reason, detail
		if result == TicketDone {
			st.Reason, st.FailedRole = "", ""
		} else {
			st.FailedRole = RoleWork
		}
		if summary != "" || role == RoleWork {
			st.Summary = summary
		}
	}
	st.Role = ""
	st.LastRole = role
	st.AwaitingHuman = ""
	return true
}

func isUnresponsive(run *Run, st *TicketState) bool {
	if st.LastRole == RoleFinalize {
		return run.Finalize != nil && run.Finalize.Reason == ReasonUnresponsive
	}
	return st.Reason == ReasonUnresponsive && (st.Status == TicketFailed || st.Status == TicketBlocked)
}

// markMerged sets st's merge state, and -- since st's branch moved its
// target's branch -- makes a target that had already been merged up need
// merging up again.
func markMerged(run *Run, st *TicketState, state string) {
	st.Merge = state
	if t := run.Ticket(st.Target); t != nil && t.Merge == MergedSubtree {
		t.Merge = MergedSelf
	}
}

// MarkMergedSelf records a successful merge-into-parent of st.
func MarkMergedSelf(run *Run, st *TicketState) {
	if st.Merge != MergedSubtree {
		markMerged(run, st, MergedSelf)
	} else {
		markMerged(run, st, MergedSubtree)
	}
}

// MarkMergedUp records a successful merge-up of st.
func MarkMergedUp(run *Run, st *TicketState) {
	markMerged(run, st, MergedSubtree)
	st.MergeNeedsSession = false
}

type planner struct {
	in  PlanInput
	run *Run
}

func (p *planner) settings() Settings { return p.run.Settings }

// visit walks id's subtree. target is the ticket id's work would be based
// on (its nearest ancestor launched as work); inheritedSkip/skipCause carry a
// skip that applies to the whole subtree. It returns the first action found,
// or nil when the subtree is complete.
func (p *planner) visit(id string, depth int, target, inheritedSkip, skipCause string) *Action {
	tt := p.in.Tree[id]
	st := p.run.Ticket(id)
	if st != nil && st.Status == TicketSkipped && st.Reason == ReasonParentFailed && inheritedSkip != ReasonParentFailed {
		// The failure that skipped it has since been retried successfully.
		if c := p.run.Ticket(st.SkipCause); c == nil || (c.Status != TicketFailed && c.Status != TicketBlocked) {
			st = p.reclassify(id, depth, target, tt)
		}
	}
	if st == nil {
		st = p.classify(id, depth, target, inheritedSkip, skipCause, tt)
	}

	switch st.Status {
	case TicketQueued:
		return &Action{Action: ActionLaunch, Ticket: id, Role: RoleWork}
	case TicketLaunched:
		return &Action{Action: ActionWait, Ticket: id, Role: RoleWork}
	case TicketSkipped:
		switch st.Reason {
		case ReasonAlreadyDone, ReasonClosed:
			return p.visitChildren(tt, depth, target, "", "")
		case ReasonInProgressElsewhere, ReasonAncestorInProgress:
			return p.visitChildren(tt, depth, target, ReasonAncestorInProgress, "")
		case ReasonParentFailed:
			return p.visitChildren(tt, depth, target, ReasonParentFailed, st.SkipCause)
		}
		return nil
	case TicketDone:
		if st.Role != "" {
			return &Action{Action: ActionWait, Ticket: id, Role: st.Role}
		}
		if a := p.visitChildren(tt, depth, id, "", ""); a != nil {
			return a
		}
		if st.Target != "" && st.Merge != MergedSubtree {
			if st.MergeNeedsSession {
				return &Action{Action: ActionLaunch, Ticket: id, Role: RoleMergeUp}
			}
			return &Action{Action: ActionMergeUp, Ticket: id}
		}
		return nil
	case TicketFailed, TicketBlocked:
		if st.Role != "" {
			return &Action{Action: ActionWait, Ticket: id, Role: st.Role}
		}
		if st.RetryPending {
			return &Action{Action: ActionLaunch, Ticket: id, Role: RoleWork}
		}
		if p.settings().OnFailure != OnFailureContinue {
			p.run.stop(p.in.Now, StopTicketFailed, id, fmt.Sprintf("%s %s: %s", id, st.Status, reasonText(st)))
			return &Action{Action: ActionStopped, Ticket: id, Reason: StopTicketFailed, Detail: p.run.StopDetail}
		}
		if st.Merge == "" {
			st.Merge = NotMerged
		}
		return p.visitChildren(tt, depth, id, ReasonParentFailed, id)
	}
	return nil
}

func reasonText(st *TicketState) string {
	if st.Detail != "" {
		return st.Reason + " (" + st.Detail + ")"
	}
	if st.Reason != "" {
		return st.Reason
	}
	return "reported " + st.Status
}

func (p *planner) visitChildren(tt *TreeTicket, depth int, target, inheritedSkip, skipCause string) *Action {
	if tt == nil || p.run.Mode != ModeTree {
		return nil
	}
	for _, c := range tt.Children {
		if a := p.visit(c, depth+1, target, inheritedSkip, skipCause); a != nil {
			return a
		}
	}
	return nil
}

func (p *planner) reclassify(id string, depth int, target string, tt *TreeTicket) *TicketState {
	p.run.dropTicket(id)
	return p.classify(id, depth, target, "", "", tt)
}

// classify records a ticket the run reaches for the first time: queued for
// launch, or skipped with its reason.
func (p *planner) classify(id string, depth int, target, inheritedSkip, skipCause string, tt *TreeTicket) *TicketState {
	st := &TicketState{ID: id, Depth: depth, Target: target}
	if tt != nil {
		st.ParentID = tt.ParentID
	}
	skip := func(reason string) *TicketState {
		st.Status, st.Reason = TicketSkipped, reason
		if reason == ReasonParentFailed {
			st.SkipCause = skipCause
		}
		p.run.addTicket(st)
		return st
	}
	if id != p.run.RootTicketID {
		if inheritedSkip != "" {
			return skip(inheritedSkip)
		}
		if depth > p.settings().MaxDepth {
			return skip(ReasonLimitDepth)
		}
		status := domain.TicketStatus("")
		if tt != nil {
			status = tt.Status
		}
		switch {
		case status == domain.TicketDone:
			return skip(ReasonAlreadyDone)
		case status == domain.TicketClosed:
			return skip(ReasonClosed)
		case IsInProgress(status) && !p.in.ForeignLaunched[id]:
			return skip(ReasonInProgressElsewhere)
		}
		if p.run.WorkLaunches >= p.settings().MaxTickets {
			return skip(ReasonLimitTickets)
		}
	}
	st.Status = TicketQueued
	p.run.addTicket(st)
	return st
}

// finish is reached when the root's whole subtree is complete.
func (p *planner) finish() Action {
	run := p.run
	root := run.Ticket(run.RootTicketID)
	if root == nil || root.Status != TicketDone {
		// S8: a failed/blocked root (onFailure continue; stop has already
		// stopped the run) never finalizes and never reaches main.
		run.State = RunFinished
		return Action{Action: ActionDone}
	}
	if run.Mode != ModeTree || run.Settings.MainReflection == MainReflectionBranch {
		run.State = RunFinished
		return Action{Action: ActionDone}
	}
	switch {
	case run.Finalize == nil:
		return Action{Action: ActionLaunch, Ticket: root.ID, Role: RoleFinalize}
	case run.Finalize.Status == TicketLaunched:
		return Action{Action: ActionWait, Ticket: root.ID, Role: RoleFinalize}
	case run.Finalize.Status == TicketDone:
		run.State = RunFinished
		return Action{Action: ActionDone}
	default:
		detail := "finalize " + run.Finalize.Status
		if run.Finalize.Reason != "" {
			detail += ": " + run.Finalize.Reason
		}
		if run.Finalize.Detail != "" {
			detail += " (" + run.Finalize.Detail + ")"
		}
		run.stop(p.in.Now, StopFinalizeFailed, root.ID, detail)
		return Action{Action: ActionStopped, Ticket: root.ID, Reason: StopFinalizeFailed, Detail: detail}
	}
}

// Pending returns the tickets of run's tree that the run may still launch as
// work, in the order it would reach them -- the Web UI's "waiting" badge. It
// mirrors Next's walk without changing the run: a ticket already queued, or
// failed and due for its retry, is pending; a ticket the run has not reached
// yet is pending unless the planner would skip it (DONE/CLOSED, IN PROGRESS
// elsewhere, deeper than maxDepth, beyond maxTickets). Subtrees the planner
// would not enter -- under a limit_depth/limit_tickets or in-progress skip,
// or under a failed ticket (onFailure stop stops the run; continue skips
// them as parent_failed) -- contribute nothing. A finished or stopped run
// has nothing pending.
func Pending(run *Run, tree Tree, foreignLaunched map[string]bool) []string {
	if run.IsFinal() {
		return nil
	}
	budget := run.Settings.MaxTickets - run.WorkLaunches
	var out []string
	var walk func(id string, depth int)
	walk = func(id string, depth int) {
		tt := tree[id]
		descend := true
		if st := run.Ticket(id); st != nil {
			switch st.Status {
			case TicketQueued:
				out = append(out, id)
				if !st.Counted {
					budget--
				}
			case TicketFailed, TicketBlocked:
				if st.RetryPending && st.Role == "" {
					out = append(out, id)
				} else if !st.RetryPending {
					descend = false
				}
			case TicketSkipped:
				descend = st.Reason == ReasonAlreadyDone || st.Reason == ReasonClosed
			}
		} else if id == run.RootTicketID {
			out = append(out, id)
			budget--
		} else {
			status := domain.TicketStatus("")
			if tt != nil {
				status = tt.Status
			}
			switch {
			case depth > run.Settings.MaxDepth:
				return
			case status == domain.TicketDone || status == domain.TicketClosed:
				// Skipped, but its children are still processed.
			case IsInProgress(status) && !foreignLaunched[id]:
				return
			case budget <= 0:
				return
			default:
				out = append(out, id)
				budget--
			}
		}
		if !descend || tt == nil || run.Mode != ModeTree {
			return
		}
		for _, c := range tt.Children {
			walk(c, depth+1)
		}
	}
	walk(run.RootTicketID, 0)
	return out
}
