package runner

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/terminal"
)

// DFLT-00154: every child session of a run -- work, merge-up and finalize,
// at any depth -- is launched with the run's Terminal.app tty; the tty
// follows whichever orchestrator drives the run; a tab failure that will
// repeat disables the tab path for the rest of the run.

// ttyOf makes the harness's orchestrator detect tty, counting the asks.
func (h *harness) ttyOf(tty string) *int {
	asks := 0
	h.svc.TerminalTTY = func() string { asks++; return tty }
	return &asks
}

// captureLogs collects the service's warnings.
func (h *harness) captureLogs() *[]string {
	var logs []string
	h.svc.Logf = func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }
	return &logs
}

func TestLaunch_EveryRoleAndDepthGetsTheRunsTTY(t *testing.T) {
	h := newHarness(t)
	h.settings.MainReflection = autopilot.MainReflectionPullRequest
	h.ttyOf("/dev/ttys003")
	r := h.ticket("R", "")
	c := h.ticket("C", r)
	h.behave[c] = func(w *workerCall) {
		w.commit()
		w.release()
		// R's branch and C's diverge, so C's merge-up needs a session.
		commitIn(t, h.st(w.RunID, r).Worktree, "human.txt", "x", "human commit on R")
		commitIn(t, w.WorkDir, "more.txt", "y", "more on C")
		// A grandchild, created while C runs.
		w.child("G")
		w.report(autopilot.TicketDone, "c")
	}
	run := h.start(r, autopilot.ModeTree)
	if got := h.run(run.RunID).TerminalTTY; got != "/dev/ttys003" {
		t.Fatalf("run tty = %q", got)
	}
	if res := h.drive(run.RunID, nil); res.Action.Action != autopilot.ActionDone {
		t.Fatalf("res = %+v; actions = %v", res, h.actions)
	}
	if len(h.workLaunches()) != 3 || len(h.roleLaunches(autopilot.RoleMergeUp)) == 0 || len(h.roleLaunches(autopilot.RoleFinalize)) != 1 {
		t.Fatalf("launches: work %v, merge-up %v, finalize %v", h.workLaunches(), h.roleLaunches(autopilot.RoleMergeUp), h.roleLaunches(autopilot.RoleFinalize))
	}
	for _, c := range h.launches {
		if c.TerminalTTY != "/dev/ttys003" || c.SkipTab {
			t.Errorf("%s %s launched with tty %q, skip %v", c.Role, c.Ticket, c.TerminalTTY, c.SkipTab)
		}
	}
}

func TestLaunch_ReservationAsksNoTTYAndTheAdopterSetsIt(t *testing.T) {
	h := newHarness(t)
	asks := h.ttyOf("/dev/ttys005")
	r := h.ticket("R", "")
	reserved, err := h.svc.Start(r, autopilot.ModeTicket, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if *asks != 0 || h.run(reserved.RunID).TerminalTTY != "" {
		t.Fatalf("a reservation asked for the tty (%d) or kept one (%q)", *asks, h.run(reserved.RunID).TerminalTTY)
	}
	if _, err := h.svc.Start(r, autopilot.ModeTicket, reserved.RunID, false); err != nil {
		t.Fatal(err)
	}
	if *asks != 1 {
		t.Fatalf("adoption asked %d times", *asks)
	}
	h.drive(reserved.RunID, nil)
	if len(h.launches) != 1 || h.launches[0].TerminalTTY != "/dev/ttys005" {
		t.Fatalf("launches = %+v", h.launches)
	}
}

// stoppedTree runs R -> {A, B} until A fails and stops the run, launching A
// with the given outcome.
func stoppedTree(t *testing.T, h *harness, outcome terminal.LaunchOutcome) (runID, a, b string) {
	t.Helper()
	r := h.ticket("R", "")
	a = h.ticket("A", r)
	b = h.ticket("B", r)
	h.behave[a] = func(w *workerCall) { w.report(autopilot.TicketFailed, "broke") }
	h.launchOutcome = func(c launchCall) terminal.LaunchOutcome {
		if c.Ticket == a {
			return outcome
		}
		return terminal.LaunchOutcome{}
	}
	run := h.start(r, autopilot.ModeTree)
	if res := h.drive(run.RunID, nil); res.Action.Action != autopilot.ActionStopped {
		t.Fatalf("res = %+v", res)
	}
	return run.RunID, a, b
}

// launchesAfter returns the launches from index i on.
func (h *harness) launchesAfter(i int) []launchCall { return h.launches[i:] }

func TestLaunch_RepeatingTabFailureDisablesTheTabForTheRun(t *testing.T) {
	h := newHarness(t)
	h.ttyOf("/dev/ttys003")
	logs := h.captureLogs()
	runID, a, b := stoppedTree(t, h, terminal.LaunchOutcome{TabError: "osascript: not authorized (-1743)", DisableTab: true})
	run := h.run(runID)
	if run.TerminalTabDisabled != "osascript: not authorized (-1743)" {
		t.Fatalf("disabled = %q", run.TerminalTabDisabled)
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "-1743") || !strings.Contains((*logs)[0], "the rest of run") {
		t.Fatalf("logs = %q", *logs)
	}
	// A itself tried the tab (the record is made after its launch); that
	// later launches skip it is TestLaunch_LaterLaunchesSkipTheTabOnceDisabled.
	if last := h.launches[len(h.launches)-1]; last.Ticket != a || last.SkipTab {
		t.Fatalf("launches = %+v", h.launches)
	}

	// A takeover by an orchestrator in another tab: new tty, tab re-enabled.
	h.ttyOf("/dev/ttys009")
	delete(h.behave, a)
	h.launchOutcome = nil
	before := len(h.launches)
	h.makeStale()
	h.start(h.run(runID).RootTicketID, autopilot.ModeTree)
	if got := h.run(runID); got.TerminalTTY != "/dev/ttys009" || got.TerminalTabDisabled != "" {
		t.Fatalf("after takeover: tty %q, disabled %q", got.TerminalTTY, got.TerminalTabDisabled)
	}
	h.drive(runID, nil)
	after := h.launchesAfter(before)
	if len(after) == 0 || !contains(h.workLaunches(), b) {
		t.Fatalf("launches after takeover = %+v", after)
	}
	for _, c := range after {
		if c.TerminalTTY != "/dev/ttys009" || c.SkipTab {
			t.Errorf("%s %s after takeover: tty %q, skip %v", c.Role, c.Ticket, c.TerminalTTY, c.SkipTab)
		}
	}
}

func TestLaunch_LaterLaunchesSkipTheTabOnceDisabled(t *testing.T) {
	h := newHarness(t)
	h.ttyOf("/dev/ttys003")
	logs := h.captureLogs()
	r := h.ticket("R", "")
	a := h.ticket("A", r)
	b := h.ticket("B", r)
	// R's own launch fails in a way that repeats.
	h.launchOutcome = func(c launchCall) terminal.LaunchOutcome {
		if c.Ticket == r && c.Role == autopilot.RoleWork {
			return terminal.LaunchOutcome{TabError: "osascript did not finish within 10s", DisableTab: true}
		}
		return terminal.LaunchOutcome{}
	}
	run := h.start(r, autopilot.ModeTree)
	if res := h.drive(run.RunID, nil); res.Action.Action != autopilot.ActionDone {
		t.Fatalf("res = %+v", res)
	}
	for _, c := range h.launches {
		wantSkip := !(c.Ticket == r && c.Role == autopilot.RoleWork)
		if c.SkipTab != wantSkip || c.TerminalTTY != "/dev/ttys003" {
			t.Errorf("%s %s: skip %v (want %v), tty %q", c.Role, c.Ticket, c.SkipTab, wantSkip, c.TerminalTTY)
		}
	}
	if !contains(h.workLaunches(), a) || !contains(h.workLaunches(), b) {
		t.Fatalf("work launches = %v", h.workLaunches())
	}
	if len(*logs) != 1 {
		t.Fatalf("logs = %q", *logs)
	}
}

func TestLaunch_RetryableTabFailureIsNotRecorded(t *testing.T) {
	h := newHarness(t)
	h.ttyOf("/dev/ttys003")
	logs := h.captureLogs()
	r := h.ticket("R", "")
	h.ticket("A", r)
	h.launchOutcome = func(c launchCall) terminal.LaunchOutcome {
		return terminal.LaunchOutcome{TabError: "osascript: no Terminal window has a tab on /dev/ttys003 (9101)"}
	}
	run := h.start(r, autopilot.ModeTree)
	var results []LaunchResult
	for i := 0; i < 50; i++ {
		res, err := h.svc.Next(run.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if res.Action.Action != autopilot.ActionLaunch {
			break
		}
		lr, err := h.svc.Launch(run.RunID, res.Ticket, res.Role)
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, lr)
	}
	if got := h.run(run.RunID).TerminalTabDisabled; got != "" {
		t.Fatalf("disabled = %q", got)
	}
	if len(h.launches) != 2 {
		t.Fatalf("launches = %+v", h.launches)
	}
	for _, c := range h.launches {
		if c.SkipTab {
			t.Errorf("%s %s skipped the tab", c.Role, c.Ticket)
		}
	}
	if len(*logs) != 2 || strings.Contains((*logs)[0], "the rest of run") || !strings.Contains((*logs)[0], "9101") {
		t.Fatalf("logs = %q", *logs)
	}
	// The result the orchestrator reads is what it always was.
	if lr := results[0]; lr.Launched != r || lr.Role != autopilot.RoleWork || lr.Worktree == "" || !strings.HasPrefix(lr.Next, "graph-engine autopilot wait ") {
		t.Fatalf("launch result = %+v", lr)
	}
}

func TestLaunch_FailedLaunchDoesNotDisableTheTab(t *testing.T) {
	h := newHarness(t)
	h.ttyOf("/dev/ttys003")
	logs := h.captureLogs()
	r := h.ticket("R", "")
	run := h.start(r, autopilot.ModeTicket)
	if res, err := h.svc.Next(run.RunID); err != nil || res.Action.Action != autopilot.ActionLaunch {
		t.Fatalf("next = %+v, %v", res, err)
	}
	h.svc.Launcher = LauncherFunc(func(req LaunchRequest) (terminal.LaunchOutcome, error) {
		return terminal.LaunchOutcome{TabError: "osascript: not authorized (-1743)", DisableTab: true},
			errors.New("failed to open terminal: LSOpenURLsWithRole() failed")
	})
	if _, err := h.svc.Launch(run.RunID, r, autopilot.RoleWork); err == nil {
		t.Fatal("expected the launch to fail")
	}
	got := h.run(run.RunID)
	if got.TerminalTabDisabled != "" {
		t.Fatalf("disabled = %q", got.TerminalTabDisabled)
	}
	if s := got.Ticket(r); s.LaunchFailures != 1 || s.Status != autopilot.TicketQueued {
		t.Fatalf("R = %+v", s)
	}
	if len(*logs) != 0 {
		t.Fatalf("logs = %q", *logs)
	}
}
