package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/autopilot/runner"
	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/sessionstatus"
	"github.com/graph-ops/core-go/internal/store"
)

// This file is graph-engine's side of the status band the plugin's mod draws
// above Claude Code's prompt (DFLT-00383): the `session-status` command the
// skills call at the turns the engine cannot see (refining, asking a person,
// the end of a skill), and the updates the orchestrator's own commands make
// as a side effect (begin-session, get-executable, wait-node, refine-ticket
// and the autopilot commands), so the band follows the work even where a
// skill forgets a call.
//
// Everything here is best effort: with no Claude Code session in the
// environment it does nothing, and a failure is one warning line on stderr
// -- never a change to the command's stdout or exit code. The commands a
// node's subagent runs (get-ticket, add-artifact, complete-node) write
// nothing: the subagents run in the same Claude Code session, and the band
// shows what the orchestrator is doing.

const sessionStatusUsageLine = "usage: graph-engine session-status <set|clear|show> ..."

const sessionStatusSetUsage = "usage: graph-engine session-status set [--ticket <ticketId>] --phase <" +
	"refining|planning|running|awaiting_approval|awaiting_human|releasing|finishing|idle> [--detail <text>] [--role worker|controller]"

// sessionStatusWarnWriter is where the status warnings go (stderr; a
// variable so tests can capture it).
var sessionStatusWarnWriter io.Writer = os.Stderr

// sessionStatusNow is the status writes' clock; a variable for tests.
var sessionStatusNow = time.Now

// sessionStatusTarget returns the graph-ops user directory and this Claude
// Code session's ID, or ok=false when there is no session to write for (no
// CLAUDE_CODE_SESSION_ID, no home directory) or the ID cannot name a file.
func sessionStatusTarget(rc runtimeConfig) (graphOpsDir, sessionID string, ok bool) {
	sessionID = os.Getenv(sessionstatus.EnvSessionID)
	if sessionID == "" || rc.HomeDir == "" {
		return "", "", false
	}
	if !sessionstatus.ValidSessionID(sessionID) {
		warnSessionStatus(fmt.Errorf("%s is not a usable session ID", sessionstatus.EnvSessionID))
		return "", "", false
	}
	return filepath.Join(rc.HomeDir, ".graph-ops"), sessionID, true
}

func warnSessionStatus(err error) {
	msg := strings.Join(strings.Fields(err.Error()), " ")
	fmt.Fprintln(sessionStatusWarnWriter, "graph-engine: warning: session status not updated: "+msg)
}

// updateSessionStatus applies fn to this session's status and writes it,
// with the configured language. It does nothing without a session, and only
// warns when it fails (a panic included): the caller's own result stands.
func updateSessionStatus(rc runtimeConfig, fn func(*sessionstatus.Status)) *sessionstatus.Status {
	dir, sid, ok := sessionStatusTarget(rc)
	if !ok {
		return nil
	}
	var out *sessionstatus.Status
	func() {
		defer func() {
			if r := recover(); r != nil {
				warnSessionStatus(fmt.Errorf("%v", r))
			}
		}()
		lang := sessionStatusLanguage(rc)
		st, err := sessionstatus.Update(dir, sid, sessionStatusNow(), func(st *sessionstatus.Status) {
			st.Language = lang
			fn(st)
		})
		if err != nil {
			warnSessionStatus(err)
			return
		}
		out = st
	}()
	return out
}

// readSessionStatus is this session's current status, nil when there is
// none (or no session).
func readSessionStatus(rc runtimeConfig) *sessionstatus.Status {
	dir, sid, ok := sessionStatusTarget(rc)
	if !ok {
		return nil
	}
	st, err := sessionstatus.Read(dir, sid)
	if err != nil {
		return nil
	}
	return st
}

// sessionStatusLanguage is the user's configured language ("" when none or
// unreadable), which the mod draws its labels in.
func sessionStatusLanguage(rc runtimeConfig) string {
	roots := extensionRoots(rc)
	if roots.UserDir == "" {
		return ""
	}
	doc, err := config.LoadDocumentAt(config.UserDocumentPath(roots.UserDir))
	if err != nil {
		return ""
	}
	return config.ResolveLanguage("", doc)
}

// setStatusTicket points st at ticket id. Switching to another ticket drops
// what the status said about the previous one (its nodes, the detail).
func setStatusTicket(st *sessionstatus.Status, id, title string) {
	if st.Ticket == nil || st.Ticket.ID != id {
		st.Nodes, st.Detail = nil, ""
	}
	st.Ticket = &sessionstatus.Ticket{ID: id, Title: title}
}

// ticketTitle is ticket id's title, "" when it cannot be read.
func ticketTitle(repo store.GraphRepository, id string) string {
	if repo == nil || id == "" {
		return ""
	}
	t, err := repo.GetTicket(id)
	if err != nil || t == nil {
		return ""
	}
	return t.Title
}

// ---------------------------------------------------------------------------
// The session-status command

// cmdSessionStatus implements `graph-engine session-status <set|clear|show>`.
// It is dispatched before the store is opened: only `set --ticket` reads the
// data source (for the ticket's title), and a data source that cannot be
// opened leaves the title out rather than failing the call.
func cmdSessionStatus(rc runtimeConfig, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", sessionStatusUsageLine)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "set":
		return cmdSessionStatusSet(rc, rest)
	case "clear":
		if len(rest) != 0 {
			return fmt.Errorf("usage: graph-engine session-status clear")
		}
		dir, sid, ok := sessionStatusTarget(rc)
		cleared := false
		if ok {
			var err error
			if cleared, err = sessionstatus.Clear(dir, sid, sessionStatusNow()); err != nil {
				warnSessionStatus(err)
			}
		}
		return printCompactJSON(map[string]any{"cleared": cleared})
	case "show":
		if len(rest) != 0 {
			return fmt.Errorf("usage: graph-engine session-status show")
		}
		return printJSON(readSessionStatus(rc))
	default:
		return fmt.Errorf("unknown session-status subcommand %q; %s", sub, sessionStatusUsageLine)
	}
}

func cmdSessionStatusSet(rc runtimeConfig, args []string) error {
	pos, vals, err := autopilotFlags(args, sessionStatusSetUsage, "--ticket", "--phase", "--detail", "--role")
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return fmt.Errorf("%s: unexpected argument %q", sessionStatusSetUsage, pos[0])
	}
	phase, ok := vals["--phase"]
	if !ok {
		return fmt.Errorf("%s: --phase is required", sessionStatusSetUsage)
	}
	if !sessionstatus.ValidPhase(phase) {
		return fmt.Errorf("%s: unknown --phase %q", sessionStatusSetUsage, truncateForError(phase))
	}
	role, hasRole := vals["--role"]
	if hasRole && !sessionstatus.ValidRole(role) {
		return fmt.Errorf("%s: --role must be worker or controller, got %q", sessionStatusSetUsage, truncateForError(role))
	}
	ticketID, hasTicket := vals["--ticket"]
	if hasTicket && (ticketID == "" || sessionstatus.CleanID(ticketID) == "") {
		return fmt.Errorf("%s: --ticket takes a ticket ID, got %q", sessionStatusSetUsage, truncateForError(ticketID))
	}
	if _, _, ok := sessionStatusTarget(rc); !ok {
		// Not in a Claude Code session: nothing to show, nothing to do.
		return printJSON(nil)
	}
	title := ""
	if hasTicket {
		if repo, err := openStore(rc); err == nil {
			title = ticketTitle(repo, ticketID)
		}
	}
	st := updateSessionStatus(rc, func(st *sessionstatus.Status) {
		if hasRole {
			if role != st.Role && role == sessionstatus.RoleWorker {
				st.Autopilot = nil
			}
			st.Role = role
		}
		if st.Role == "" {
			st.Role = sessionstatus.RoleWorker
		}
		if hasTicket {
			setStatusTicket(st, ticketID, title)
		}
		st.Phase, st.Detail = phase, vals["--detail"]
	})
	return printJSON(st)
}

// ---------------------------------------------------------------------------
// Updates made by the orchestrator's commands

// noteWorkerTicket records that this session works on ticket, in phase:
// refine-ticket (planning: refined, waiting to be processed) and
// begin-session (running). A session that begins processing a ticket is a
// worker, whatever it was before.
func noteWorkerTicket(rc runtimeConfig, ticket *domain.Ticket, id, phase string) {
	if _, _, ok := sessionStatusTarget(rc); !ok {
		return
	}
	title := ""
	if ticket != nil {
		title = ticket.Title
	}
	updateSessionStatus(rc, func(st *sessionstatus.Status) {
		st.Role, st.Autopilot = sessionstatus.RoleWorker, nil
		setStatusTicket(st, id, title)
		st.Phase, st.Detail = phase, ""
	})
}

// noteExecutable records the ticket's state after get-executable:
//
//   - nodes being worked (IN PROGRESS / IN REVIEW, by this call or earlier
//     ones): those nodes, phase running -- releasing when one is a release;
//   - none, but a manual node waiting for a person (a TODO or REJECTED
//     approval gate or release whose prerequisites are done): no nodes,
//     phase awaiting_approval (releasing for a release) with the nodes'
//     names as the detail;
//   - every node DONE: phase finishing;
//   - otherwise (a blocked ticket, say): no nodes, the phase as it was.
func noteExecutable(rc runtimeConfig, repo store.GraphRepository, ticketID string) {
	if _, _, ok := sessionStatusTarget(rc); !ok {
		return
	}
	// The ticket, its nodes and its edges -- not GetTicketDetail, which also
	// reads every artifact's content, and the band needs none of it.
	ticket, err := repo.GetTicket(ticketID)
	if err != nil || ticket == nil {
		return
	}
	graphNodes, err := repo.ListNodesByTicket(ticketID)
	if err != nil {
		return
	}
	edges, err := repo.ListEdgesByTicket(ticketID)
	if err != nil {
		return
	}
	phase, nodes, note := executablePhase(graphNodes, edges)
	updateSessionStatus(rc, func(st *sessionstatus.Status) {
		if st.Role == "" || st.Role == sessionstatus.RoleController {
			st.Role, st.Autopilot = sessionstatus.RoleWorker, nil
		}
		setStatusTicket(st, ticket.ID, ticket.Title)
		st.Nodes = nodes
		if phase != "" {
			st.Phase, st.Detail = phase, note
		} else if st.Phase == "" {
			st.Phase = sessionstatus.PhaseRunning
		}
	})
}

// executablePhase is noteExecutable's decision, from the ticket's nodes:
// the phase ("" to keep the current one), the nodes being worked, and the
// detail.
func executablePhase(graphNodes []domain.GraphNode, edges []domain.GraphEdge) (phase string, nodes []sessionstatus.Node, note string) {
	byID := make(map[string]domain.GraphNode, len(graphNodes))
	for _, n := range graphNodes {
		byID[n.ID] = n
	}
	prereqsDone := func(id string) bool {
		for _, e := range edges {
			if e.ToNodeID != id || e.Condition == domain.EdgeLoop {
				continue
			}
			if from, ok := byID[e.FromNodeID]; !ok || from.Status != domain.NodeDone {
				return false
			}
		}
		return true
	}
	allDone := len(graphNodes) > 0
	releasing := false
	var waiting []string
	waitingRelease := true
	for _, n := range graphNodes {
		if n.Status != domain.NodeDone {
			allDone = false
		}
		switch {
		case n.Status == domain.NodeInProgress || n.Status == domain.NodeInReview:
			nodes = append(nodes, sessionstatus.Node{ID: n.ID, Name: n.Name, Type: string(n.Type)})
			if n.Type == domain.NodeTypeRelease {
				releasing = true
			}
		case n.IsManual && (n.Status == domain.NodeTODO || n.Status == domain.NodeRejected) && prereqsDone(n.ID):
			waiting = append(waiting, n.Name)
			if n.Type != domain.NodeTypeRelease {
				waitingRelease = false
			}
		}
	}
	switch {
	case len(nodes) > 0 && releasing:
		return sessionstatus.PhaseReleasing, nodes, ""
	case len(nodes) > 0:
		return sessionstatus.PhaseRunning, nodes, ""
	case len(waiting) > 0 && waitingRelease:
		return sessionstatus.PhaseReleasing, nil, strings.Join(waiting, ", ")
	case len(waiting) > 0:
		return sessionstatus.PhaseAwaitingApproval, nil, strings.Join(waiting, ", ")
	case allDone:
		return sessionstatus.PhaseFinishing, nil, ""
	}
	return "", nil, ""
}

// waitNodeStatus is what wait-node wrote when it started waiting, so it can
// put the status back when it ends -- but only while the status still says
// what it wrote: another command of the session (the person approved in the
// terminal, get-executable handed out the next node) may have moved it on.
type waitNodeStatus struct {
	mu         sync.Mutex // end may run from the signal handler too
	rc         runtimeConfig
	active     bool
	prevPhase  string
	prevDetail string
	detail     string
}

// beginWaitNodeStatus marks the session as awaiting approval of nodes.
func beginWaitNodeStatus(rc runtimeConfig, repo store.GraphRepository, nodes []domain.GraphNode) *waitNodeStatus {
	w := &waitNodeStatus{rc: rc}
	if _, _, ok := sessionStatusTarget(rc); !ok || len(nodes) == 0 {
		return w
	}
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		names = append(names, n.Name)
	}
	ticketID := nodes[0].TicketID
	title := ticketTitle(repo, ticketID)
	st := updateSessionStatus(rc, func(st *sessionstatus.Status) {
		w.prevPhase, w.prevDetail = st.Phase, st.Detail
		if st.Role == "" {
			st.Role = sessionstatus.RoleWorker
		}
		if ticketID != "" && (st.Ticket == nil || st.Ticket.ID != ticketID) {
			setStatusTicket(st, ticketID, title)
		}
		st.Nodes = nil
		st.Phase, st.Detail = sessionstatus.PhaseAwaitingApproval, strings.Join(names, ", ")
	})
	if st != nil {
		w.active, w.detail = true, st.Detail
	}
	return w
}

// end puts the status back once wait-node is done: running when a node
// changed (the decision came), unchanged on a timeout (the skill waits
// again), and what it was before the wait on an error or interruption.
// After a timeout, the next wait-node starts from awaiting_approval, so an
// error in that one leaves awaiting_approval in place: still true, since the
// gate is still waiting, and the next get-executable sets the band anew.
func (w *waitNodeStatus) end(result string, failed bool) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.active {
		return
	}
	w.active = false
	if !failed && result == "timeout" {
		return
	}
	updateSessionStatus(w.rc, func(st *sessionstatus.Status) {
		if st.Phase != sessionstatus.PhaseAwaitingApproval || st.Detail != w.detail {
			return
		}
		if failed {
			st.Phase, st.Detail = w.prevPhase, w.prevDetail
			return
		}
		st.Phase, st.Detail = sessionstatus.PhaseRunning, ""
	})
}

// noteController records an autopilot run as this session's: role
// controller, the run's root ticket, and the run's progress. The run is read
// again after the command, so the counts include what it just did.
func noteController(rc runtimeConfig, repo store.GraphRepository, svc *runner.Service, runID string) {
	if _, _, ok := sessionStatusTarget(rc); !ok || runID == "" {
		return
	}
	run, err := svc.Snapshot(runID)
	if err != nil {
		// An unknown run ID already failed the command itself, with its own
		// message; a second line about the band would only repeat it.
		var apiErr *domain.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != autopilot.ErrCodeRunNotFound {
			warnSessionStatus(err)
		}
		return
	}
	p := autopilot.RunProgress(run)
	ap := &sessionstatus.Autopilot{
		RunID: run.ID, Mode: run.Mode, RootTicketID: run.RootTicketID, State: run.State,
		Counts: sessionstatus.Counts{Done: p.Done, InProgress: p.InProgress, Remaining: p.Remaining,
			Blocked: p.Blocked, Failed: p.Failed, Skipped: p.Skipped},
		AwaitingHuman: p.CurrentAwaitingHuman,
	}
	if p.Current != "" {
		ap.Current = &sessionstatus.RunTicket{TicketID: p.Current, Title: ticketTitle(repo, p.Current)}
	}
	if p.Last != "" {
		ap.Last = &sessionstatus.RunTicket{TicketID: p.Last, Title: ticketTitle(repo, p.Last), Outcome: p.LastOutcome}
	}
	phase, detail := sessionstatus.PhaseRunning, ""
	switch run.State {
	case autopilot.RunFinalizing:
		phase = sessionstatus.PhaseFinishing
	case autopilot.RunFinished:
		phase = sessionstatus.PhaseIdle
	case autopilot.RunStopped:
		phase, detail = sessionstatus.PhaseIdle, run.StopReason
	}
	rootTitle := ticketTitle(repo, run.RootTicketID)
	updateSessionStatus(rc, func(st *sessionstatus.Status) {
		st.Role, st.Autopilot = sessionstatus.RoleController, ap
		setStatusTicket(st, run.RootTicketID, rootTitle)
		st.Nodes = nil
		st.Phase, st.Detail = phase, detail
	})
}

// noteWorkerContext records an autopilot worker session's start (autopilot
// worker-context, its first command): the ticket and its role's phase --
// refining for work (running once the ticket is refined), finishing for
// merge-up and finalize.
func noteWorkerContext(rc runtimeConfig, repo store.GraphRepository, ticketID, role string) {
	if _, _, ok := sessionStatusTarget(rc); !ok {
		return
	}
	var title string
	refined := false
	if t, err := repo.GetTicket(ticketID); err == nil && t != nil {
		title, refined = t.Title, t.RefinedAt != nil && *t.RefinedAt != ""
	}
	phase, detail := sessionstatus.PhaseRefining, ""
	switch {
	case role == autopilot.RoleMergeUp || role == autopilot.RoleFinalize:
		phase, detail = sessionstatus.PhaseFinishing, role
	case refined:
		phase = sessionstatus.PhaseRunning
	}
	updateSessionStatus(rc, func(st *sessionstatus.Status) {
		st.Role, st.Autopilot = sessionstatus.RoleWorker, nil
		setStatusTicket(st, ticketID, title)
		st.Phase, st.Detail = phase, detail
	})
}

// noteWorkerTouch follows an autopilot worker's touch: --awaiting-human
// means a person is asked (awaiting_human, with what is awaited); a plain
// touch after that means the person answered.
func noteWorkerTouch(rc runtimeConfig, awaiting string, hasAwaiting bool) {
	if _, _, ok := sessionStatusTarget(rc); !ok {
		return
	}
	updateSessionStatus(rc, func(st *sessionstatus.Status) {
		switch {
		case hasAwaiting:
			st.Phase, st.Detail = sessionstatus.PhaseAwaitingHuman, awaiting
		case st.Phase == sessionstatus.PhaseAwaitingHuman:
			st.Phase, st.Detail = sessionstatus.PhaseRunning, ""
		}
	})
}

// clearSessionStatus removes this session's status: an autopilot worker's
// report is its session's last command.
func clearSessionStatus(rc runtimeConfig) {
	dir, sid, ok := sessionStatusTarget(rc)
	if !ok {
		return
	}
	if _, err := sessionstatus.Clear(dir, sid, sessionStatusNow()); err != nil {
		warnSessionStatus(err)
	}
}

// restoreStatusOnSignal makes an interrupted wait-node (the session stopped
// its background watcher, or the terminal was closed) put the band back
// before it exits, as an error would. It installs nothing when wait-node
// wrote no status, so the process keeps Go's default signal handling then.
// The returned func uninstalls it. SIGKILL cannot be caught: a band left
// "awaiting approval" by one is corrected by the session's next
// get-executable or begin-session.
func restoreStatusOnSignal(w *waitNodeStatus) func() {
	if w == nil || !w.active {
		return func() {}
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-ch:
			w.end("", true)
			code := 130
			if sig == syscall.SIGTERM {
				code = 143
			}
			os.Exit(code)
		case <-done:
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}
