package engine

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/displayname"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/identity"
	"github.com/graph-ops/core-go/internal/store"
)

// This file is who holds a node (DFLT-00327): the claim get-executable
// records on each node it hands out, the processing sessions whose
// heartbeats tell whether a claim is still being worked on (its lease), and
// the rules unstick-node applies before releasing a claim.
//
// The lease is judged per processing session, not per node: a subagent
// working on a node calls graph-engine rarely -- an implementation node can
// go half an hour without a call -- so a per-node heartbeat would expire on
// live work. The session's heartbeat moves with every graph-engine call made
// with --session, the driving session's and its subagents' alike; an
// autopilot session's lease is its run's heartbeat instead, which the runner
// moves every 30 seconds while it waits.
//
// Every time here is the calling machine's clock, as for every other
// timestamp in the store: members whose clocks are minutes apart see leases
// end minutes early or late. An expired claim is never released
// automatically -- releasing is always a caller's decision (unstick-node).

// ManualSessionLease is how long a processing session a person started (not
// an autopilot run's) stays live after its last heartbeat. It is the upper
// end of the 30-60 minutes DFLT-00325's review allowed, because the driving
// session may not call graph-engine at all while it waits for a long
// implementation subagent.
const ManualSessionLease = 60 * time.Minute

// sessionRetention is how old a processing session's heartbeat has to be for
// begin-session to delete it, and sessionRetentionDeleteLimit how many it
// deletes at most per call (a slow HTTP data source should not hold
// begin-session up; the rest go on later calls).
const (
	sessionRetention            = 7 * 24 * time.Hour
	sessionRetentionDeleteLimit = 5
)

// Claimer is who claims nodes in GetExecutableNodesAs: the display name
// (identity.DisplayName) and the processing session ("" when there is none).
type Claimer struct {
	Name           string
	NameIsFallback bool
	SessionID      string
}

// sessionTimestamp formats a heartbeat with a fixed number of fractional
// digits, so two of them compare as strings the way they do as times (the
// SQL backends' TouchProcessingSession relies on that). It is still an
// RFC3339 timestamp every reader parses.
func sessionTimestamp(t time.Time) string {
	return t.UTC().Format(domain.SessionTimestampLayout)
}

func (e *GraphEngine) now() time.Time {
	if e.clock != nil {
		return e.clock()
	}
	return time.Now()
}

// SetClock replaces the engine's clock (tests only).
func (e *GraphEngine) SetClock(clock func() time.Time) { e.clock = clock }

// SetLogf sets where the engine's warnings go; nil restores the default
// (stderr). The Web UI server points it at its structured logger.
func (e *GraphEngine) SetLogf(logf func(format string, args ...any)) { e.logf = logf }

func (e *GraphEngine) warnf(format string, args ...any) {
	if e.logf != nil {
		e.logf(format, args...)
		return
	}
	fmt.Fprintf(os.Stderr, "graph-engine: warning: "+format+"\n", args...)
}

// warnEvery is warnf for a warning that can recur on every call -- the Web
// UI server lists tickets every few seconds per open tab -- said at most
// once per every for the same key (every == 0: once per engine). The
// throttle belongs to the engine, so each engine (and its SetLogf sink)
// gets its own first warning.
func (e *GraphEngine) warnEvery(key string, every time.Duration, format string, args ...any) {
	now := e.now()
	e.warnMu.Lock()
	last, said := e.warned[key]
	if said && (every == 0 || now.Sub(last) < every) {
		e.warnMu.Unlock()
		return
	}
	if e.warned == nil {
		e.warned = map[string]time.Time{}
	}
	e.warned[key] = now
	e.warnMu.Unlock()
	e.warnf(format, args...)
}

// repeatedWarningInterval is how often a warning that repeats on every
// listing (a data source that keeps failing) is said again.
const repeatedWarningInterval = 5 * time.Minute

// sessionStore is the repository's ProcessingSessionStore, nil when it has
// none (a test fake).
func (e *GraphEngine) sessionStore() store.ProcessingSessionStore {
	s, _ := e.repo.(store.ProcessingSessionStore)
	return s
}

// sessionsUnsupported reports whether err means the data source keeps no
// processing sessions.
func sessionsUnsupported(err error) bool {
	return errors.Is(err, store.ErrProcessingSessionsUnsupported)
}

// warnSessionsUnsupported says, once per engine, that the data source keeps
// no processing sessions.
func (e *GraphEngine) warnSessionsUnsupported() {
	e.warnEvery("sessions-unsupported", 0, "this data source cannot keep processing sessions (an HTTP data source needs protocol 1.2 or newer), so whether another member is still working on a claimed node cannot be told; unstick-node releases such nodes without that check")
}

// holdsClaim reports whether a node at status is claimed -- somebody took it
// with get-executable and has not finished it -- so that it can carry claim
// information (IN PROGRESS / IN REVIEW). Compare isUnclaimable in engine.go,
// which also counts DONE.
func holdsClaim(status domain.NodeStatus) bool {
	return status == domain.NodeInProgress || status == domain.NodeInReview
}

// runRecords reads the shared autopilot run records of the given projects,
// by run ID. Runs that cannot be read (no AutopilotRunStore, an HTTP data
// source older than 1.2, an error) are simply absent: a session of such a
// run is then judged by its own heartbeat, like a manual one.
func (e *GraphEngine) runRecords(projectIDs []string) map[string]domain.AutopilotRunRecord {
	out := map[string]domain.AutopilotRunRecord{}
	runs, ok := e.repo.(store.AutopilotRunStore)
	if !ok {
		return out
	}
	seen := map[string]bool{}
	for _, pid := range projectIDs {
		if pid == "" || seen[pid] {
			continue
		}
		seen[pid] = true
		recs, err := runs.ListAutopilotRuns(pid)
		if err != nil {
			continue
		}
		for _, rec := range recs {
			out[rec.ID] = rec
		}
	}
	return out
}

// runProjects lists the projects of the sessions that belong to an
// autopilot run -- the only ones whose runs have to be read.
func runProjects(sessions []domain.ProcessingSession) []string {
	var out []string
	for _, s := range sessions {
		if s.RunID != "" {
			out = append(out, s.ProjectID)
		}
	}
	return out
}

// sessionLease judges a processing session at now: an autopilot session by
// its run's shared record when there is one (live while the run is active,
// autopilot.RecordActive), otherwise -- a manual session, or a run whose
// record cannot be read -- by its own heartbeat and ManualSessionLease. It
// returns the lease and the heartbeat it was judged by.
func sessionLease(s domain.ProcessingSession, runs map[string]domain.AutopilotRunRecord, now time.Time) (string, string) {
	if s.RunID != "" {
		if rec, ok := runs[s.RunID]; ok {
			if autopilot.RecordActive(rec, now) {
				return domain.ClaimLeaseLive, rec.Heartbeat
			}
			return domain.ClaimLeaseExpired, rec.Heartbeat
		}
	}
	hb, err := time.Parse(time.RFC3339Nano, s.Heartbeat)
	if err != nil {
		return domain.ClaimLeaseUnknown, s.Heartbeat
	}
	if now.Sub(hb) <= ManualSessionLease {
		return domain.ClaimLeaseLive, s.Heartbeat
	}
	return domain.ClaimLeaseExpired, s.Heartbeat
}

// evaluateLease is the lease of node's claim: legacy without a claim record,
// unknown without a session (or when the session cannot be found), and
// otherwise the session's (sessionLease). sessions maps session IDs to the
// sessions that could be read.
func evaluateLease(node domain.GraphNode, sessions map[string]domain.ProcessingSession, runs map[string]domain.AutopilotRunRecord, now time.Time) (string, *string) {
	if node.ClaimToken == nil && node.ClaimedByName == nil {
		return domain.ClaimLeaseLegacy, nil
	}
	if node.ClaimSessionID == nil {
		return domain.ClaimLeaseUnknown, nil
	}
	s, ok := sessions[*node.ClaimSessionID]
	if !ok {
		return domain.ClaimLeaseUnknown, nil
	}
	lease, hb := sessionLease(s, runs, now)
	return lease, &hb
}

// isOwnClaim reports whether node's claim is the caller's own: made in the
// caller's session, or in another session of the same autopilot run (a
// worker the run started earlier -- one that failed, or was restarted).
// A matching machine or name is deliberately not enough: the same person
// may be running another conversation on the same machine at the same time,
// and that one's claims are not this caller's to release.
func isOwnClaim(node domain.GraphNode, callerSessionID string, caller, claimant *domain.ProcessingSession) bool {
	if node.ClaimSessionID != nil && callerSessionID != "" && *node.ClaimSessionID == callerSessionID {
		return true
	}
	return caller != nil && claimant != nil && caller.RunID != "" && caller.RunID == claimant.RunID
}

// newClaim is the claim GetExecutableNodesAs records for one node: a fresh
// token per node.
func newClaim(c Claimer, now time.Time) *domain.NodeClaim {
	return &domain.NodeClaim{
		Name: c.Name, NameIsFallback: c.NameIsFallback, Token: identity.NewSessionID(),
		SessionID: c.SessionID, ClaimedAt: now.UTC().Format(time.RFC3339Nano),
	}
}

// --- sessions ---

// SessionPeer is another processing session of the ticket begin-session was
// called for, as begin-session reports it.
type SessionPeer struct {
	SessionID      string `json:"session_id"`
	Name           string `json:"name"`
	NameIsFallback bool   `json:"name_is_fallback"`
	// NodeIDs are the nodes the session holds claims on (IN PROGRESS / IN
	// REVIEW); empty for a session that is live but holds none right now.
	NodeIDs   []string `json:"node_ids"`
	Heartbeat string   `json:"heartbeat"`
	RunID     string   `json:"run_id,omitempty"`
	// SameMachine: the session was started on the caller's machine -- by
	// the same person in another conversation, most likely, which may or
	// may not still be running.
	SameMachine bool `json:"same_machine"`
	// SameRun: the session belongs to the caller's own autopilot run (an
	// earlier worker of it). Such sessions are listed apart, in
	// BeginSessionResult.SameRun, and are nothing to warn about.
	SameRun bool `json:"same_run"`
}

// BeginSessionResult is what begin-session prints.
type BeginSessionResult struct {
	SessionID string `json:"session_id"`
	// LeaseMinutes is how long the session stays live after a heartbeat
	// (ManualSessionLease; an autopilot session follows its run instead).
	LeaseMinutes int `json:"lease_minutes"`
	// SessionsSupported is false when the data source cannot keep sessions
	// (an HTTP data source older than 1.2): SessionID was minted but not
	// saved, and there is no point passing it with --session.
	SessionsSupported bool `json:"sessions_supported"`
	// Others are the other live sessions of the ticket -- somebody else is
	// processing it.
	Others []SessionPeer `json:"others"`
	// SameRun are the other live sessions of the caller's own autopilot run.
	SameRun []SessionPeer `json:"same_run"`
	// Warnings are for stderr, not part of the output.
	Warnings []string `json:"-"`
}

// BeginSession starts a processing session of ticketID for actor (runID is
// the autopilot run it belongs to, "" for one a person started) and reports
// the ticket's other live sessions. It never refuses because somebody else
// is processing the ticket -- that is for the caller to tell the user. A
// missing or CLOSED ticket is refused. It also deletes up to
// sessionRetentionDeleteLimit of the ticket's sessions that have been silent
// for sessionRetention.
func (e *GraphEngine) BeginSession(ticketID, runID string, actor identity.Actor) (BeginSessionResult, error) {
	res := BeginSessionResult{
		SessionID: identity.NewSessionID(), LeaseMinutes: int(ManualSessionLease / time.Minute),
		SessionsSupported: true, Others: []SessionPeer{}, SameRun: []SessionPeer{},
	}
	detail, err := e.repo.GetTicketDetail(ticketID)
	if err != nil {
		return BeginSessionResult{}, err
	}
	if detail == nil {
		return BeginSessionResult{}, domain.NewAPIError(domain.ErrCodeTicketNotFound, "ticket %s not found", ticketID)
	}
	if detail.Status == domain.TicketClosed {
		return BeginSessionResult{}, domain.NewAPIError(domain.ErrCodeValidation, "ticket %s is CLOSED; a closed ticket is not processed", ticketID)
	}
	st := e.sessionStore()
	now := e.now()
	self := domain.ProcessingSession{
		ID: res.SessionID, ProjectID: detail.ProjectID, TicketID: ticketID,
		ActorName: actor.Name, ActorNameIsFallback: actor.NameIsFallback, MachineID: actor.MachineID,
		RunID: runID, StartedAt: sessionTimestamp(now), Heartbeat: sessionTimestamp(now),
	}
	if st == nil {
		res.SessionsSupported = false
		return res, nil
	}
	existing, err := st.ListProcessingSessionsByTickets([]string{ticketID})
	if sessionsUnsupported(err) {
		res.SessionsSupported = false
		res.Warnings = append(res.Warnings, "this data source cannot keep processing sessions (an HTTP data source needs protocol 1.2 or newer): whether somebody else is processing this ticket cannot be told, and claims are not protected from unstick-node; the session ID printed is not saved, so do not pass it with --session")
		return res, nil
	}
	if err != nil {
		return BeginSessionResult{}, err
	}
	if err := st.SaveProcessingSession(self); err != nil {
		return BeginSessionResult{}, err
	}

	// Only the lease of an existing session of an autopilot run needs its
	// run's record, and all of them are of this ticket's project.
	var runs map[string]domain.AutopilotRunRecord
	for _, s := range existing {
		if s.RunID != "" {
			runs = e.runRecords([]string{detail.ProjectID})
			break
		}
	}
	claimsBySession := map[string][]string{}
	for _, n := range detail.Nodes {
		if holdsClaim(n.Status) && n.ClaimSessionID != nil {
			claimsBySession[*n.ClaimSessionID] = append(claimsBySession[*n.ClaimSessionID], n.ID)
		}
	}
	deleted := 0
	for _, s := range existing {
		if s.ID == self.ID {
			continue
		}
		lease, hb := sessionLease(s, runs, now)
		if lease != domain.ClaimLeaseLive {
			if t, err := time.Parse(time.RFC3339Nano, s.Heartbeat); err == nil && now.Sub(t) > sessionRetention &&
				len(claimsBySession[s.ID]) == 0 && deleted < sessionRetentionDeleteLimit {
				deleted++
				if err := st.DeleteProcessingSession(s.ID); err != nil {
					res.Warnings = append(res.Warnings, fmt.Sprintf("deleting the old processing session %s failed (it is retried by a later begin-session): %v", displayname.ID(s.ID), err))
				}
			}
			continue
		}
		nodes := claimsBySession[s.ID]
		if nodes == nil {
			nodes = []string{}
		}
		// Everything here was read from the shared data source and ends up
		// on the caller's terminal: the name is sanitized and the ID and time
		// fields are shown only when they have the shape they should.
		s.SanitizeActorName()
		peer := SessionPeer{
			SessionID: displayname.ID(s.ID), Name: s.ActorName, NameIsFallback: s.ActorNameIsFallback, NodeIDs: nodes,
			Heartbeat:   displayname.Timestamp(hb),
			SameMachine: s.MachineID != "" && s.MachineID == actor.MachineID,
			SameRun:     runID != "" && s.RunID == runID,
		}
		if s.RunID != "" {
			peer.RunID = displayname.ID(s.RunID)
		}
		if peer.SameRun {
			res.SameRun = append(res.SameRun, peer)
		} else {
			res.Others = append(res.Others, peer)
		}
	}
	sort.Slice(res.Others, func(i, j int) bool { return res.Others[i].SessionID < res.Others[j].SessionID })
	sort.Slice(res.SameRun, func(i, j int) bool { return res.SameRun[i].SessionID < res.SameRun[j].SessionID })
	return res, nil
}

// TouchSession records a heartbeat of sessionID, returning what the caller
// should warn about (nothing when it worked, or when the data source keeps
// no sessions -- begin-session has said so already). It never fails the
// caller's command: a missed heartbeat only shortens the lease.
func (e *GraphEngine) TouchSession(sessionID string) []string {
	st := e.sessionStore()
	if sessionID == "" || st == nil {
		return nil
	}
	found, err := st.TouchProcessingSession(sessionID, sessionTimestamp(e.now()))
	switch {
	case sessionsUnsupported(err):
		return nil
	case err != nil:
		return []string{fmt.Sprintf("recording the heartbeat of processing session %s failed: %v", sessionID, err)}
	case !found:
		return []string{fmt.Sprintf("processing session %s was not found, so its heartbeat was not recorded; run begin-session again to get a new one", sessionID)}
	}
	return nil
}

// AnnotateClaims fills ClaimLease and ClaimHeartbeat of every claimed node
// (IN PROGRESS / IN REVIEW) in the given node lists, in place, for display.
// Sessions are read only for the tickets that have a claimed node recorded
// with a session -- one read for all of them -- and autopilot runs only when
// one of those sessions belongs to a run; with no such node nothing is read
// at all. A failed read degrades to "unknown" rather than failing the
// caller's listing.
func (e *GraphEngine) AnnotateClaims(nodeLists ...[]domain.GraphNode) {
	var ticketIDs []string
	for _, nodes := range nodeLists {
		for _, n := range nodes {
			if holdsClaim(n.Status) && n.ClaimSessionID != nil {
				ticketIDs = append(ticketIDs, n.TicketID)
			}
		}
	}
	sessions := map[string]domain.ProcessingSession{}
	runs := map[string]domain.AutopilotRunRecord{}
	if st := e.sessionStore(); st != nil && len(ticketIDs) > 0 {
		list, err := st.ListProcessingSessionsByTickets(ticketIDs)
		switch {
		case sessionsUnsupported(err):
			e.warnSessionsUnsupported()
		case err != nil:
			e.warnEvery("sessions-read-failed", repeatedWarningInterval, "reading the processing sessions of claimed nodes failed; their leases are shown as unknown (repeated at most every %s while it keeps failing): %v", repeatedWarningInterval, err)
		default:
			for _, s := range list {
				sessions[s.ID] = s
			}
			runs = e.runRecords(runProjects(list))
		}
	}
	now := e.now()
	for _, nodes := range nodeLists {
		for i := range nodes {
			n := &nodes[i]
			sanitizeDecisionForDisplay(n)
			if !holdsClaim(n.Status) {
				n.ClaimLease, n.ClaimHeartbeat = "", nil
				continue
			}
			n.ClaimLease, n.ClaimHeartbeat = evaluateLease(*n, sessions, runs, now)
			sanitizeClaimForDisplay(n)
		}
	}
}

// sanitizeClaimForDisplay makes the claim fields of n that were read from
// the shared data source safe to print (get-ticket, the Web UI's API): the
// name is sanitized, a time that does not parse is dropped, and a session ID
// that does not look like one is replaced. The lease has been judged
// already, from the stored values.
func sanitizeClaimForDisplay(n *domain.GraphNode) {
	n.SanitizeClaimName()
	if n.ClaimHeartbeat != nil && displayname.Timestamp(*n.ClaimHeartbeat) == displayname.UnknownTime {
		n.ClaimHeartbeat = nil
	}
	if n.ClaimedAt != nil && displayname.Timestamp(*n.ClaimedAt) == displayname.UnknownTime {
		n.ClaimedAt = nil
	}
	if n.ClaimSessionID != nil {
		id := displayname.ID(*n.ClaimSessionID)
		n.ClaimSessionID = &id
	}
}

// sanitizeDecisionForDisplay is sanitizeClaimForDisplay for the decision
// fields (DFLT-00329): the decider's name is sanitized and a decision time
// that does not parse is dropped. It runs on every node AnnotateClaims sees,
// since a decision stays on a DONE / REJECTED node that holds no claim.
func sanitizeDecisionForDisplay(n *domain.GraphNode) {
	n.SanitizeDecisionName()
	if n.DecidedAt != nil && displayname.Timestamp(*n.DecidedAt) == displayname.UnknownTime {
		n.DecidedAt = nil
	}
}

// --- unstick ---

// UnstickOptions are UnstickNodeWith's settings.
type UnstickOptions struct {
	// SessionID is the caller's processing session ("" for none).
	SessionID string
	// MachineID is the caller's machine (identity), used only to tell the
	// caller that a claim was made on the same machine.
	MachineID string
	// Force releases a claim another session still holds.
	Force bool
}

// UnstickResult is the released node plus what the caller should be warned
// about.
type UnstickResult struct {
	Node     domain.GraphNode
	Warnings []string
}

// checkUnstick decides whether node's claim may be released by the caller,
// and what to warn about. See UnstickNodeWith for the rules.
func (e *GraphEngine) checkUnstick(node *domain.GraphNode, opts UnstickOptions) ([]string, error) {
	if node.ClaimToken == nil && node.ClaimedByName == nil {
		return []string{fmt.Sprintf("node %s was claimed without a record of who claimed it (by an older graph-engine, or through a data source older than protocol 1.2); it was released without checking whether somebody is still working on it", node.ID)}, nil
	}
	st := e.sessionStore()
	getSession := func(id string) (*domain.ProcessingSession, error) {
		if st == nil || id == "" {
			return nil, nil
		}
		s, err := st.GetProcessingSession(id)
		if sessionsUnsupported(err) {
			return nil, nil
		}
		return s, err
	}
	caller, err := getSession(opts.SessionID)
	if err != nil {
		return nil, err
	}
	var claimant *domain.ProcessingSession
	if node.ClaimSessionID != nil {
		if claimant, err = getSession(*node.ClaimSessionID); err != nil {
			return nil, err
		}
	}
	node.SanitizeClaimName()
	who := claimDescription(node)
	if isOwnClaim(*node, opts.SessionID, caller, claimant) {
		if claimant != nil && claimant.ID != opts.SessionID {
			return []string{fmt.Sprintf("node %s was claimed by session %s of the same autopilot run (%s); released it as this run's own claim", node.ID, displayname.ID(claimant.ID), displayname.ID(claimant.RunID))}, nil
		}
		return nil, nil
	}
	if claimant == nil {
		return []string{fmt.Sprintf("node %s was claimed by %s without a processing session that can be found, so whether somebody is still working on it cannot be told; it was released", node.ID, who)}, nil
	}
	sessions := map[string]domain.ProcessingSession{claimant.ID: *claimant}
	var runs map[string]domain.AutopilotRunRecord
	if claimant.RunID != "" {
		runs = e.runRecords([]string{claimant.ProjectID})
	}
	lease, hbRaw := evaluateLease(*node, sessions, runs, e.now())
	// The times, like the name and IDs, come from the shared data source and
	// are printed: only well-formed ones are shown.
	hb, claimedAt := displayname.Timestamp(deref(hbRaw)), displayname.Timestamp(deref(node.ClaimedAt))
	switch lease {
	case domain.ClaimLeaseLive:
		if opts.Force {
			return []string{fmt.Sprintf("node %s was released by --force although %s is still active (last heartbeat %s): make sure that session does not complete it", node.ID, who, hb)}, nil
		}
		sameMachine := claimant.MachineID != "" && claimant.MachineID == opts.MachineID
		details := map[string]any{
			"node_id":          node.ID,
			"claimed_by_name":  deref(node.ClaimedByName),
			"name_is_fallback": node.ClaimedByNameIsFallback != nil && *node.ClaimedByNameIsFallback,
			"claimed_at":       claimedAt,
			"heartbeat":        hb,
			"session_id":       displayname.ID(claimant.ID),
			"same_machine":     sameMachine,
		}
		// The message carries what the details do, since the CLI prints only
		// the message: whether the claim is this machine's (a person's own
		// earlier conversation, possibly) and the autopilot run it belongs to.
		var where string
		if sameMachine {
			where += " (another session on this machine)"
		}
		if claimant.RunID != "" {
			details["run_id"] = displayname.ID(claimant.RunID)
			where += " (autopilot run " + displayname.ID(claimant.RunID) + ")"
		}
		return nil, domain.NewAPIError(domain.ErrCodeNodeClaimedByOther,
			"NODE_CLAIMED_BY_OTHER: node %s is being worked on by %s%s (claimed at %s, last heartbeat %s), so it was not released. "+
				"Only if you have made sure nobody is working on it any more, run `graph-engine unstick-node %s --force`",
			node.ID, who, where, claimedAt, hb, node.ID).WithDetails(details)
	case domain.ClaimLeaseExpired:
		return []string{fmt.Sprintf("node %s was claimed by %s, whose last heartbeat (%s) is older than its lease; it was released", node.ID, who, hb)}, nil
	default:
		return []string{fmt.Sprintf("node %s was claimed by %s, whose liveness cannot be told; it was released", node.ID, who)}, nil
	}
}

// claimDescription names a claim's holder for messages: "<name> (session
// <id>)", the name as displayname.MemberLabel gives it and the ID only when
// it looks like one.
func claimDescription(n *domain.GraphNode) string {
	name := displayname.MemberLabel(deref(n.ClaimedByName), n.ClaimedByNameIsFallback != nil && *n.ClaimedByNameIsFallback)
	if n.ClaimSessionID != nil {
		return name + " (session " + displayname.ID(*n.ClaimSessionID) + ")"
	}
	return name
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
