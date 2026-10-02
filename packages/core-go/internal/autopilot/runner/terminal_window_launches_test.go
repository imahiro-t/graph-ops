package runner

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/terminal"
)

// DFLT-00362: a failed tab's diagnostics land in its record, the screen
// being locked is recorded like any passing failure, a launch that tried
// no tab is counted by reason (and does not push tab failures out), a tab
// that opened late is counted, and the orchestrator's tty reason is kept.

var screenLocked = terminal.LaunchOutcome{
	TabError:   "the screen is locked, so no tab was tried (Terminal cannot come to the front while it is)",
	TabFailure: &terminal.TabFailure{Kind: terminal.TabFailureScreenLocked, Diagnostics: terminal.TabDiagnostics{ScreenLock: terminal.ScreenLocked}},
}

func TestRecordTabOutcome_Diagnostics(t *testing.T) {
	run := &autopilot.Run{}
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	notFront := terminal.LaunchOutcome{
		TabError: "osascript: 0:1: execution error: graph-ops: Terminal did not come to the front with the window of /dev/ttys003, so no key was sent (9103)",
		TabFailure: &terminal.TabFailure{Kind: terminal.TabFailureScriptError, Phase: terminal.TabPhaseSystemEventsOK, ErrorNumber: 9103,
			Diagnostics: terminal.TabDiagnostics{ScreenLock: terminal.ScreenUnlocked, ScreenLockAfter: terminal.ScreenUnlocked, FrontmostApp: "Google Chrome",
				FrontWindowID: "55704", FrontWindowBounds: "{1, 2, 3, 4}", TargetWindowID: "55701", TargetWindowBounds: "{1, 2, 3, 4}", TabsAfter: "12"}},
	}
	if recordTabOutcome(run, notFront, "A", autopilot.RoleWork, now) || recordTabOutcome(run, screenLocked, "B", autopilot.RoleWork, now) {
		t.Fatal("a passing failure disabled the tab")
	}
	recordTabOutcome(run, lockBusy, "C", autopilot.RoleWork, now)
	recs := run.TerminalTabFailures
	if len(recs) != 3 || run.TerminalTabDisabled != "" || run.TerminalTabSlowTimeouts != 0 {
		t.Fatalf("run = %+v", run)
	}
	want := autopilot.TabDiagnosticsRecord{ScreenLock: "unlocked", ScreenLockAfter: "unlocked", FrontmostApp: "Google Chrome",
		FrontWindowID: "55704", FrontWindowBounds: "{1, 2, 3, 4}", TargetWindowID: "55701", TargetWindowBounds: "{1, 2, 3, 4}", TabsAfter: "12"}
	if recs[0].Diagnostics == nil || *recs[0].Diagnostics != want {
		t.Fatalf("diagnostics = %+v", recs[0].Diagnostics)
	}
	if recs[1].Kind != "screen-locked" || recs[1].Diagnostics == nil || recs[1].Diagnostics.ScreenLock != "locked" || recs[1].KeystrokeSent {
		t.Fatalf("screen-locked record = %+v", recs[1])
	}
	if recs[2].Diagnostics != nil {
		t.Fatalf("a lock-busy launch read nothing: %+v", recs[2].Diagnostics)
	}
}

func TestRecordNoTabLaunch(t *testing.T) {
	run := &autopilot.Run{}
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	for i := 0; i < autopilot.MaxTabFailureRecords; i++ {
		recordTabOutcome(run, boundsMismatch, fmt.Sprintf("F%d", i), autopilot.RoleWork, now)
	}
	noTTY := terminal.LaunchOutcome{NoTabReason: terminal.NoTabNoTTY}
	for i, o := range []terminal.LaunchOutcome{noTTY, noTTY, {NoTabReason: terminal.NoTabDisabled}} {
		recordTabOutcome(run, o, fmt.Sprintf("W%d", i), autopilot.RoleWork, now)
		recordNoTabLaunch(run, o, fmt.Sprintf("W%d", i), now)
	}
	// Launches that tried a tab are not window launches, and a launcher
	// reporting no reason is not counted.
	for _, o := range []terminal.LaunchOutcome{tabOpened, boundsMismatch, {}} {
		if n := recordNoTabLaunch(run, o, "X", now); n != 0 {
			t.Fatalf("%+v counted as a window launch (%d)", o, n)
		}
	}
	if got := run.TerminalWindowLaunches; len(got) != 2 || got["no-tty"].Count != 2 || got["no-tty"].LastTicketID != "W1" || got["tab-disabled"].Count != 1 {
		t.Fatalf("window launches = %+v", got)
	}
	if len(run.TerminalTabFailures) != autopilot.MaxTabFailureRecords || run.TerminalTabFailures[0].TicketID != "F0" {
		t.Fatalf("window launches pushed tab failures out: first %s, %d kept", run.TerminalTabFailures[0].TicketID, len(run.TerminalTabFailures))
	}
}

func TestRecordTabOutcome_LateTab(t *testing.T) {
	run := &autopilot.Run{TerminalTabSlowTimeouts: 1}
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	recordTabOutcome(run, terminal.LaunchOutcome{UsedTab: true, LateTab: true}, "A", autopilot.RoleWork, now)
	recordTabOutcome(run, tabOpened, "B", autopilot.RoleWork, now)
	if run.TerminalLateTabs == nil || run.TerminalLateTabs.Count != 1 || run.TerminalLateTabs.LastTicketID != "A" || run.TerminalTabSlowTimeouts != 0 || run.TerminalTabFailures != nil {
		t.Fatalf("run = %+v", run)
	}
}

// Through the service: the orchestrator's tty reason is kept, the
// launches of a run whose tab is disabled are counted and warned about
// once, and a late tab is counted and logged.
func TestLaunch_WindowLaunchesAndTTYReason(t *testing.T) {
	h := newHarness(t)
	h.ttyOf("/dev/ttys003")
	logs := h.captureLogs()
	r := h.ticket("R", "")
	for _, name := range []string{"A", "B", "C", "D"} {
		h.ticket(name, r)
	}
	tried := 0
	h.launchOutcome = func(c launchCall) terminal.LaunchOutcome {
		if c.SkipTab {
			return terminal.LaunchOutcome{NoTabReason: terminal.NoTabDisabled}
		}
		tried++
		switch tried {
		case 1:
			return terminal.LaunchOutcome{UsedTab: true, LateTab: true}
		case 2:
			return screenLocked
		}
		return terminal.LaunchOutcome{TabError: "osascript: not authorized (-1743)", DisableTab: true}
	}
	run := h.start(r, autopilot.ModeTree)
	if res := h.drive(run.RunID, nil); res.Action.Action != autopilot.ActionDone {
		t.Fatalf("res = %+v", res)
	}
	got := h.run(run.RunID)
	if got.TerminalTTYReason != "" || got.TerminalLateTabs == nil || got.TerminalLateTabs.Count != 1 {
		t.Fatalf("run: reason %q, late %+v", got.TerminalTTYReason, got.TerminalLateTabs)
	}
	if len(got.TerminalTabFailures) != 2 || got.TerminalTabFailures[0].Kind != "screen-locked" || got.TerminalTabFailures[0].DisabledRun || !got.TerminalTabFailures[1].DisabledRun {
		t.Fatalf("failures = %+v", got.TerminalTabFailures)
	}
	skipped := len(h.launches) - 3
	if skipped < 2 || got.TerminalWindowLaunches["tab-disabled"].Count != skipped || len(got.TerminalWindowLaunches) != 1 {
		t.Fatalf("window launches = %+v (%d launches skipped the tab)", got.TerminalWindowLaunches, skipped)
	}
	var disabledWarnings, lateWarnings, lockWarnings int
	for _, l := range *logs {
		switch {
		case strings.Contains(l, "without trying a tab") && strings.Contains(l, "-1743"):
			disabledWarnings++
		case strings.Contains(l, "appeared only after the usual wait"):
			lateWarnings++
		case strings.Contains(l, "screen is locked"):
			lockWarnings++
		}
	}
	if disabledWarnings != 1 || lateWarnings != 1 || lockWarnings != 1 {
		t.Fatalf("logs = %q", *logs)
	}

	// An orchestrator outside Terminal.app: the reason is recorded.
	h2 := newHarness(t)
	h2.ttyOf("")
	r2 := h2.ticket("R", "")
	run2 := h2.start(r2, autopilot.ModeTicket)
	if got := h2.run(run2.RunID); got.TerminalTTY != "" || got.TerminalTTYReason != terminal.TTYReasonTermProgram {
		t.Fatalf("tty %q, reason %q", got.TerminalTTY, got.TerminalTTYReason)
	}
}

func TestDescribeTabFailure_Diagnostics(t *testing.T) {
	for _, tc := range []struct {
		f    *terminal.TabFailure
		want string
	}{
		{screenLocked.TabFailure, "; screen-locked"},
		{&terminal.TabFailure{Kind: terminal.TabFailureScriptError, Phase: terminal.TabPhaseSystemEventsOK, ErrorNumber: 9103,
			Diagnostics: terminal.TabDiagnostics{FrontmostApp: "Google Chrome", ScreenLock: "unlocked", ScreenLockAfter: "locked"}},
			`; script-error, after system-events-ok, frontmost app "Google Chrome", the screen was locked by then`},
		{&terminal.TabFailure{Kind: terminal.TabFailureScriptError, Phase: terminal.TabPhaseKeystrokeSent, KeystrokeSent: true, ErrorNumber: 9102,
			Diagnostics: terminal.TabDiagnostics{FrontmostApp: "ターミナル", ScreenLockAfter: "unlocked"}},
			`; script-error, after keystroke-sent, an empty tab may be left, frontmost app "ターミナル"`},
	} {
		if got := describeTabFailure(tc.f); got != tc.want {
			t.Errorf("describeTabFailure(%+v) = %q, want %q", tc.f, got, tc.want)
		}
	}
}
