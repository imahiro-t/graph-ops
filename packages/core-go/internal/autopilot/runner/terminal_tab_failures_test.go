package runner

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/terminal"
)

// DFLT-00361: every launch whose tab could not be opened is recorded in
// the run with its details, and a timeout after Terminal and System Events
// both answered (Terminal answering slowly) disables the tab only on the
// MaxSlowTabTimeouts-th in a row.

var (
	tabOpened          = terminal.LaunchOutcome{UsedTab: true}
	slowAfterKeystroke = terminal.LaunchOutcome{
		TabError:   "osascript: osascript did not finish within 15s after keystroke-sent (Terminal answering slowly); a new tab may have been left empty",
		TabFailure: &terminal.TabFailure{Kind: terminal.TabFailureTimeout, Phase: terminal.TabPhaseKeystrokeSent, KeystrokeSent: true},
	}
	slowBeforeKeystroke = terminal.LaunchOutcome{
		TabError:   "osascript: osascript did not finish within 15s after system-events-ok (Terminal answering slowly)",
		TabFailure: &terminal.TabFailure{Kind: terminal.TabFailureTimeout, Phase: terminal.TabPhaseSystemEventsOK},
	}
	promptTimeout = terminal.LaunchOutcome{
		TabError:   "osascript: osascript did not finish within 15s after Terminal answered but before System Events did (waiting for an Automation permission prompt for System Events?)",
		DisableTab: true,
		TabFailure: &terminal.TabFailure{Kind: terminal.TabFailureTimeout, Phase: terminal.TabPhaseWindowFound},
	}
	boundsMismatch = terminal.LaunchOutcome{
		TabError:   "osascript: 54:97: execution error: graph-ops: the new tab did not open in the Terminal window of /dev/ttys003 (bounds target {0, 25, 800, 600}, new tab {400, 25, 1200, 600}) (9102)",
		TabFailure: &terminal.TabFailure{Kind: terminal.TabFailureScriptError, Phase: terminal.TabPhaseKeystrokeSent, KeystrokeSent: true, ErrorNumber: 9102},
	}
	lockBusy = terminal.LaunchOutcome{
		TabError:   "other graph-ops processes kept the Terminal tab lock for more than 20s",
		TabFailure: &terminal.TabFailure{Kind: terminal.TabFailureLockBusy},
	}
)

func TestRecordTabOutcome_SlowTimeoutsInARow(t *testing.T) {
	cases := []struct {
		name string
		seq  []terminal.LaunchOutcome
		// wantDisabledAt is the index of the launch that disables the tab
		// (-1: none).
		wantDisabledAt int
		wantCount      int
	}{
		{"one slow timeout", []terminal.LaunchOutcome{slowAfterKeystroke}, -1, 1},
		{"two in a row", []terminal.LaunchOutcome{slowBeforeKeystroke, slowAfterKeystroke}, 1, 2},
		{"a tab in between resets", []terminal.LaunchOutcome{slowAfterKeystroke, tabOpened, slowAfterKeystroke}, -1, 1},
		{"other failures in between do not reset", []terminal.LaunchOutcome{slowAfterKeystroke, boundsMismatch, lockBusy, slowBeforeKeystroke}, 3, 2},
		{"a permission prompt's timeout disables at once", []terminal.LaunchOutcome{promptTimeout}, 0, 0},
		{"a permission prompt's timeout does not count as slow", []terminal.LaunchOutcome{slowAfterKeystroke, promptTimeout}, 1, 1},
		{"quick failures never disable", []terminal.LaunchOutcome{boundsMismatch, lockBusy, boundsMismatch}, -1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := &autopilot.Run{TerminalTTY: "/dev/ttys003"}
			now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
			disabledAt := -1
			failures := 0
			for i, o := range tc.seq {
				if recordTabOutcome(run, o, fmt.Sprintf("T%d", i), autopilot.RoleWork, now) {
					if disabledAt >= 0 {
						t.Fatalf("launch %d disabled the tab again", i)
					}
					disabledAt = i
				}
				if o.TabError != "" {
					failures++
				}
			}
			if disabledAt != tc.wantDisabledAt {
				t.Fatalf("disabled at launch %d, want %d", disabledAt, tc.wantDisabledAt)
			}
			if (run.TerminalTabDisabled != "") != (tc.wantDisabledAt >= 0) {
				t.Fatalf("TerminalTabDisabled = %q", run.TerminalTabDisabled)
			}
			if tc.wantDisabledAt >= 0 && run.TerminalTabDisabled != tc.seq[tc.wantDisabledAt].TabError {
				t.Fatalf("TerminalTabDisabled = %q, want the disabling launch's error", run.TerminalTabDisabled)
			}
			if run.TerminalTabSlowTimeouts != tc.wantCount {
				t.Fatalf("slow timeouts = %d, want %d", run.TerminalTabSlowTimeouts, tc.wantCount)
			}
			if len(run.TerminalTabFailures) != failures {
				t.Fatalf("recorded %d failures, want %d", len(run.TerminalTabFailures), failures)
			}
			for _, rec := range run.TerminalTabFailures {
				idx := 0
				fmt.Sscanf(rec.TicketID, "T%d", &idx)
				if rec.DisabledRun != (idx == tc.wantDisabledAt) {
					t.Errorf("record %+v: disabled_run wrong", rec)
				}
			}
		})
	}
}

func TestRecordTabOutcome_RecordsTheDetails(t *testing.T) {
	run := &autopilot.Run{}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	recordTabOutcome(run, boundsMismatch, "DFLT-1", autopilot.RoleMergeUp, now)
	recordTabOutcome(run, lockBusy, "DFLT-2", autopilot.RoleWork, now)
	// A launcher that reports no details (an older fake, say) is recorded
	// with what it has.
	recordTabOutcome(run, terminal.LaunchOutcome{TabError: "osascript: not authorized (-1743)", DisableTab: true}, "DFLT-3", autopilot.RoleFinalize, now)
	// Neither a tab that opened nor a launch that tried none is recorded.
	recordTabOutcome(run, tabOpened, "DFLT-4", autopilot.RoleWork, now)
	recordTabOutcome(run, terminal.LaunchOutcome{}, "DFLT-5", autopilot.RoleWork, now)
	want := []autopilot.TabFailureRecord{
		{At: now, TicketID: "DFLT-1", Role: autopilot.RoleMergeUp, Kind: "script-error", ErrorNumber: 9102, Message: boundsMismatch.TabError, Phase: "keystroke-sent", KeystrokeSent: true},
		{At: now, TicketID: "DFLT-2", Role: autopilot.RoleWork, Kind: "lock-busy", Message: lockBusy.TabError},
		{At: now, TicketID: "DFLT-3", Role: autopilot.RoleFinalize, Message: "osascript: not authorized (-1743)", DisabledRun: true},
	}
	if fmt.Sprintf("%+v", run.TerminalTabFailures) != fmt.Sprintf("%+v", want) {
		t.Fatalf("records = %+v\nwant      %+v", run.TerminalTabFailures, want)
	}
}

func TestRecordTabOutcome_KeepsTheLatestRecords(t *testing.T) {
	run := &autopilot.Run{}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	for i := 0; i < autopilot.MaxTabFailureRecords+5; i++ {
		recordTabOutcome(run, boundsMismatch, fmt.Sprintf("T%d", i), autopilot.RoleWork, now)
	}
	if n := len(run.TerminalTabFailures); n != autopilot.MaxTabFailureRecords {
		t.Fatalf("kept %d records", n)
	}
	if first, last := run.TerminalTabFailures[0].TicketID, run.TerminalTabFailures[autopilot.MaxTabFailureRecords-1].TicketID; first != "T5" || last != fmt.Sprintf("T%d", autopilot.MaxTabFailureRecords+4) {
		t.Fatalf("kept %s..%s, want the latest", first, last)
	}
}

// Through the service: the records land in the run file, the warning names
// the details, and the tab stays enabled after one slow timeout but not
// after the second in a row.
func TestLaunch_SlowTimeoutsAreRecordedAndDisableOnTheSecond(t *testing.T) {
	h := newHarness(t)
	h.ttyOf("/dev/ttys003")
	logs := h.captureLogs()
	r := h.ticket("R", "")
	for _, name := range []string{"A", "B", "C"} {
		h.ticket(name, r)
	}
	seq := []terminal.LaunchOutcome{slowAfterKeystroke, boundsMismatch, slowBeforeKeystroke}
	tried := 0
	h.launchOutcome = func(c launchCall) terminal.LaunchOutcome {
		if c.SkipTab {
			return terminal.LaunchOutcome{}
		}
		tried++
		if tried <= len(seq) {
			return seq[tried-1]
		}
		return tabOpened
	}
	run := h.start(r, autopilot.ModeTree)
	if res := h.drive(run.RunID, nil); res.Action.Action != autopilot.ActionDone {
		t.Fatalf("res = %+v", res)
	}
	got := h.run(run.RunID)
	if got.TerminalTabDisabled != slowBeforeKeystroke.TabError || got.TerminalTabSlowTimeouts != 2 {
		t.Fatalf("disabled %q, slow timeouts %d", got.TerminalTabDisabled, got.TerminalTabSlowTimeouts)
	}
	if tried != 3 {
		t.Fatalf("%d launches tried a tab, want 3 (the rest skip it)", tried)
	}
	recs := got.TerminalTabFailures
	if len(recs) != 3 || recs[0].DisabledRun || recs[1].DisabledRun || !recs[2].DisabledRun ||
		recs[0].Kind != "timeout" || !recs[0].KeystrokeSent || recs[1].ErrorNumber != 9102 || recs[2].Phase != "system-events-ok" {
		t.Fatalf("records = %+v", recs)
	}
	for i, c := range h.launches {
		if wantSkip := i >= 3; c.SkipTab != wantSkip {
			t.Errorf("launch %d (%s %s): skip %v, want %v", i, c.Role, c.Ticket, c.SkipTab, wantSkip)
		}
	}
	if len(*logs) != 3 || !strings.Contains((*logs)[0], "timeout, after keystroke-sent, an empty tab may be left") ||
		strings.Contains((*logs)[0], "the rest of run") || !strings.Contains((*logs)[2], "the rest of run") {
		t.Fatalf("logs = %q", *logs)
	}

}

// A timeout while a permission prompt may be waiting disables the tab on
// the first one, as before, and says so in its record.
func TestLaunch_PromptTimeoutDisablesAtOnce(t *testing.T) {
	h := newHarness(t)
	h.ttyOf("/dev/ttys003")
	r := h.ticket("R", "")
	h.ticket("A", r)
	h.launchOutcome = func(c launchCall) terminal.LaunchOutcome {
		if c.SkipTab {
			return terminal.LaunchOutcome{}
		}
		return promptTimeout
	}
	run := h.start(r, autopilot.ModeTree)
	h.drive(run.RunID, nil)
	got := h.run(run.RunID)
	if got.TerminalTabDisabled == "" || len(got.TerminalTabFailures) != 1 || !got.TerminalTabFailures[0].DisabledRun ||
		got.TerminalTabFailures[0].Phase != "window-found" || got.TerminalTabSlowTimeouts != 0 {
		t.Fatalf("run: disabled %q, records %+v, slow %d", got.TerminalTabDisabled, got.TerminalTabFailures, got.TerminalTabSlowTimeouts)
	}
}
