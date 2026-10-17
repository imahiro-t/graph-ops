package runner

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/terminal"
)

// DFLT-00379: a launch whose tab opened at the retry with no stray tab seen
// in the tab script's 1 second is looked at again later, with ps, by the
// next wait poll or launch after its span (StrayTabGrace) and
// autopilot.StraySettle have passed. What the look finds is counted in
// terminal_stray_tabs and terminal_late_stray_tabs and warned about; the
// run's own tabs are never taken for stray ones.

// strayFindCall is one look asked of the fake ps snapshot.
type strayFindCall struct {
	From, Until, Now time.Time
	Exclude          []string
}

// strayFake replaces ps for the later looks: reads counts the ps runs
// (ReadStrayTabs), readErr makes them fail, and answer decides each look's
// result, every look being recorded in calls.
type strayFake struct {
	reads   int
	readErr error
	answer  func(c strayFindCall) []string
	calls   []strayFindCall
}

func (f *strayFake) LateStrayTTYs(from, until time.Time, exclude []string, now time.Time) []string {
	c := strayFindCall{From: from, Until: until, Now: now, Exclude: append([]string(nil), exclude...)}
	f.calls = append(f.calls, c)
	return f.answer(c)
}

// fakeStrayFinder installs a strayFake answering with answer.
func (h *harness) fakeStrayFinder(answer func(c strayFindCall) []string) *strayFake {
	f := &strayFake{answer: answer}
	h.svc.ReadStrayTabs = func() (StrayTabs, error) {
		f.reads++
		if f.readErr != nil {
			return nil, f.readErr
		}
		return f, nil
	}
	return f
}

// finding returns an answer that finds ttys, minus what the call excludes
// (as the real look does).
func finding(ttys ...string) func(c strayFindCall) []string {
	return func(c strayFindCall) []string {
		var out []string
		for _, tty := range ttys {
			if !slices.Contains(c.Exclude, tty) {
				out = append(out, tty)
			}
		}
		return out
	}
}

// retriedTab is the outcome of a launch whose tab opened at the retry, in
// tty, with no stray tab seen.
func retriedTab(tty string) terminal.LaunchOutcome {
	return terminal.LaunchOutcome{UsedTab: true, RetriedTab: true, OpenMethod: "keystroke-retry", NewTTY: tty}
}

// launchSilentWith starts a ticket-mode run of a new ticket whose worker
// never reports, launched with outcome, from an orchestrator in
// /dev/ttys003.
func launchSilentWith(t *testing.T, h *harness, outcome terminal.LaunchOutcome) (runID, ticket string) {
	t.Helper()
	h.ttyOf("/dev/ttys003")
	h.launchOutcome = func(launchCall) terminal.LaunchOutcome { return outcome }
	return launchSilent(t, h)
}

// lateStrayLogs returns the warnings about stray tabs found later.
func lateStrayLogs(logs []string) []string {
	var out []string
	for _, l := range logs {
		if strings.Contains(l, "appeared later and is left empty") {
			out = append(out, l)
		}
	}
	return out
}

// waitOnce polls once.
func (h *harness) waitOnce(runID, ticketID string) WaitResult {
	h.t.Helper()
	res, err := h.svc.Wait(runID, ticketID, 0)
	if err != nil {
		h.t.Fatal(err)
	}
	return res
}

func TestStrayTab_LaterLookFindsALateTab(t *testing.T) {
	h := newHarness(t)
	logs := h.captureLogs()
	calls := h.fakeStrayFinder(finding("/dev/ttys015"))
	launchedAt := h.clock.Now()
	runID, c := launchSilentWith(t, h, retriedTab("/dev/ttys011"))

	run := h.run(runID)
	want := []autopilot.StrayTabCheck{{TicketID: c, Role: autopilot.RoleWork, From: launchedAt, Until: launchedAt.Add(StrayTabGrace), TTY: "/dev/ttys011"}}
	if !reflect.DeepEqual(run.TerminalStrayTabChecks, want) {
		t.Fatalf("checks = %+v", run.TerminalStrayTabChecks)
	}
	if len(calls.calls) != 0 {
		t.Fatalf("the launch looked already: %+v", calls.calls)
	}

	// Before Until + StraySettle: no look.
	h.clock.Advance(StrayTabGrace + autopilot.StraySettle - time.Second)
	h.waitOnce(runID, c)
	if len(calls.calls) != 0 || len(h.run(runID).TerminalStrayTabChecks) != 1 {
		t.Fatalf("looked too early: %+v", calls.calls)
	}

	// Due: one look, with the span and the run's own ttys left out.
	h.clock.Advance(time.Second)
	h.waitOnce(runID, c)
	if len(calls.calls) != 1 {
		t.Fatalf("calls = %+v", calls.calls)
	}
	call := calls.calls[0]
	if !call.From.Equal(launchedAt) || !call.Until.Equal(launchedAt.Add(StrayTabGrace)) || !call.Now.Equal(h.clock.Now()) {
		t.Fatalf("call = %+v", call)
	}
	if !slices.Contains(call.Exclude, "/dev/ttys003") || !slices.Contains(call.Exclude, "/dev/ttys011") {
		t.Fatalf("exclude = %q", call.Exclude)
	}
	run = h.run(runID)
	if run.TerminalStrayTabs == nil || run.TerminalStrayTabs.Count != 1 || run.TerminalStrayTabs.LastTicketID != c {
		t.Fatalf("stray tabs = %+v", run.TerminalStrayTabs)
	}
	if run.TerminalLateStrayTabs == nil || run.TerminalLateStrayTabs.Count != 1 || run.TerminalLateStrayTabs.LastTicketID != c {
		t.Fatalf("late stray tabs = %+v", run.TerminalLateStrayTabs)
	}
	if run.TerminalStrayTabChecks != nil {
		t.Fatalf("the check must be gone: %+v", run.TerminalStrayTabChecks)
	}
	warn := lateStrayLogs(*logs)
	if len(warn) != 1 || !strings.Contains(warn[0], "(/dev/ttys015)") || !strings.Contains(warn[0], "close it by hand") ||
		!strings.Contains(warn[0], "the work session of "+c) {
		t.Fatalf("warnings = %q", warn)
	}

	// Made once only.
	h.clock.Advance(time.Minute)
	h.waitOnce(runID, c)
	if len(calls.calls) != 1 || h.run(runID).TerminalStrayTabs.Count != 1 {
		t.Fatalf("looked again: %+v", calls.calls)
	}
}

func TestStrayTab_NoLaterLookUnlessRetriedWithoutAStrayTab(t *testing.T) {
	cases := []struct {
		name    string
		tty     string
		outcome terminal.LaunchOutcome
		recent  []string
	}{
		{name: "no retry", tty: "/dev/ttys003", outcome: terminal.LaunchOutcome{UsedTab: true, OpenMethod: "menu", NewTTY: "/dev/ttys011"}, recent: []string{"/dev/ttys011"}},
		{name: "stray tab seen at once", tty: "/dev/ttys003", outcome: terminal.LaunchOutcome{UsedTab: true, RetriedTab: true, StrayTab: true, OpenMethod: "keystroke-retry", NewTTY: "/dev/ttys011"}, recent: []string{"/dev/ttys011"}},
		{name: "no tab", tty: "/dev/ttys003", outcome: terminal.LaunchOutcome{TabError: "osascript: no tab (9102)"}},
		{name: "no orchestrator tty", tty: "", outcome: retriedTab("/dev/ttys011"), recent: []string{"/dev/ttys011"}},
		{name: "no tty logged", tty: "/dev/ttys003", outcome: terminal.LaunchOutcome{UsedTab: true, OpenMethod: "menu"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			calls := h.fakeStrayFinder(finding("/dev/ttys015"))
			h.ttyOf(tc.tty)
			h.launchOutcome = func(launchCall) terminal.LaunchOutcome { return tc.outcome }
			runID, c := launchSilent(t, h)
			run := h.run(runID)
			if run.TerminalStrayTabChecks != nil {
				t.Fatalf("checks = %+v", run.TerminalStrayTabChecks)
			}
			var recent []string
			for _, r := range run.TerminalRecentTabTTYs {
				recent = append(recent, r.TTY)
			}
			if !reflect.DeepEqual(recent, tc.recent) {
				t.Fatalf("recent = %q, want %q", recent, tc.recent)
			}
			h.clock.Advance(time.Hour)
			h.waitOnce(runID, c)
			if len(calls.calls) != 0 || h.run(runID).TerminalLateStrayTabs != nil {
				t.Fatalf("calls = %+v", calls.calls)
			}
		})
	}
}

func TestStrayTab_FailedOrEmptyLookCountsNothing(t *testing.T) {
	for _, psFails := range []bool{true, false} {
		t.Run(map[bool]string{true: "ps failed", false: "nothing new"}[psFails], func(t *testing.T) {
			h := newHarness(t)
			logs := h.captureLogs()
			calls := h.fakeStrayFinder(finding())
			if psFails {
				calls.readErr = errors.New("ps: operation not permitted")
			}
			runID, c := launchSilentWith(t, h, retriedTab("/dev/ttys011"))
			h.clock.Advance(StrayTabGrace + autopilot.StraySettle)
			h.waitOnce(runID, c)
			run := h.run(runID)
			if calls.reads != 1 || run.TerminalStrayTabs != nil || run.TerminalLateStrayTabs != nil || run.TerminalStrayTabChecks != nil {
				t.Fatalf("reads %d, stray %+v, late %+v, checks %+v", calls.reads, run.TerminalStrayTabs, run.TerminalLateStrayTabs, run.TerminalStrayTabChecks)
			}
			if w := lateStrayLogs(*logs); w != nil {
				t.Fatalf("warnings = %q", w)
			}
			// A failed ps leaves a trace on stderr, naming the session; an
			// empty look none.
			var failed []string
			for _, l := range *logs {
				if strings.Contains(l, "could not look for a stray Terminal tab") {
					failed = append(failed, l)
				}
			}
			if psFails {
				if len(failed) != 1 || !strings.Contains(failed[0], "the work session of "+c) || !strings.Contains(failed[0], "operation not permitted") {
					t.Fatalf("failure warnings = %q", failed)
				}
			} else if failed != nil {
				t.Fatalf("failure warnings = %q", failed)
			}
			// Dropped, not tried again.
			h.waitOnce(runID, c)
			if calls.reads != 1 {
				t.Fatalf("looked again: %d", calls.reads)
			}
		})
	}
}

// The poll that returns -- the session reported -- still makes the look it
// took out of the run (a look taken out and not made would be lost).
func TestStrayTab_ThePollThatReturnsStillLooks(t *testing.T) {
	h := newHarness(t)
	calls := h.fakeStrayFinder(finding("/dev/ttys015"))
	runID, c := launchSilentWith(t, h, retriedTab("/dev/ttys011"))
	if _, err := h.svc.Report(runID, c, autopilot.TicketDone, "", "done"); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(StrayTabGrace + autopilot.StraySettle)
	if res := h.waitOnce(runID, c); !res.Reported() {
		t.Fatalf("res = %+v", res)
	}
	if len(calls.calls) != 1 || h.run(runID).TerminalLateStrayTabs == nil {
		t.Fatalf("calls = %+v, late = %+v", *calls, h.run(runID).TerminalLateStrayTabs)
	}
}

// The next launch makes a look that is due, too, and never takes its own
// tab -- which opened inside the earlier launch's span -- for a stray one.
func TestStrayTab_NextLaunchLooksAndLeavesItsOwnTabOut(t *testing.T) {
	h := newHarness(t)
	h.ttyOf("/dev/ttys003")
	logs := h.captureLogs()
	r := h.ticket("R", "")
	a := h.ticket("A", r)
	// A's tab and a real stray tab (ttys015) are both empty-looking and in
	// R's span to the fake look; only the stray one may be counted.
	calls := h.fakeStrayFinder(finding("/dev/ttys012", "/dev/ttys015"))
	h.launchOutcome = func(c launchCall) terminal.LaunchOutcome {
		if c.Ticket == r {
			return retriedTab("/dev/ttys011")
		}
		return terminal.LaunchOutcome{UsedTab: true, OpenMethod: "menu", NewTTY: "/dev/ttys012"}
	}
	run := h.start(r, autopilot.ModeTree)
	launched := 0
	for i := 0; i < 20 && launched < 2; i++ {
		res, err := h.svc.Next(run.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if res.Action.Action != autopilot.ActionLaunch {
			t.Fatalf("action = %+v", res.Action)
		}
		if launched == 1 {
			if res.Ticket != a {
				t.Fatalf("second launch = %+v", res)
			}
			// R's look is due when A launches.
			h.clock.Advance(StrayTabGrace + autopilot.StraySettle)
		}
		if _, err := h.svc.Launch(run.RunID, res.Ticket, res.Role); err != nil {
			t.Fatal(err)
		}
		launched++
	}
	if len(calls.calls) != 1 {
		t.Fatalf("calls = %+v", calls.calls)
	}
	for _, tty := range []string{"/dev/ttys003", "/dev/ttys011", "/dev/ttys012"} {
		if !slices.Contains(calls.calls[0].Exclude, tty) {
			t.Fatalf("exclude %q lacks %s", calls.calls[0].Exclude, tty)
		}
	}
	got := h.run(run.RunID)
	if got.TerminalLateStrayTabs == nil || got.TerminalLateStrayTabs.Count != 1 || got.TerminalLateStrayTabs.LastTicketID != r ||
		got.TerminalStrayTabs == nil || got.TerminalStrayTabs.Count != 1 {
		t.Fatalf("late %+v, stray %+v", got.TerminalLateStrayTabs, got.TerminalStrayTabs)
	}
	warn := lateStrayLogs(*logs)
	if len(warn) != 1 || !strings.Contains(warn[0], "(/dev/ttys015)") || strings.Contains(warn[0], "ttys012") {
		t.Fatalf("warnings = %q", warn)
	}
}

// The same, made by the wait for the next session: its tab is left out.
func TestStrayTab_WaitForTheNextSessionLeavesItsTabOut(t *testing.T) {
	h := newHarness(t)
	h.ttyOf("/dev/ttys003")
	r := h.ticket("R", "")
	a := h.ticket("A", r)
	h.behave[a] = silentWorker
	calls := h.fakeStrayFinder(finding("/dev/ttys012"))
	h.launchOutcome = func(c launchCall) terminal.LaunchOutcome {
		if c.Ticket == r {
			return retriedTab("/dev/ttys011")
		}
		return terminal.LaunchOutcome{UsedTab: true, OpenMethod: "menu", NewTTY: "/dev/ttys012"}
	}
	run := h.start(r, autopilot.ModeTree)
	for _, want := range []string{r, a} {
		res, err := h.svc.Next(run.RunID)
		if err != nil || res.Action.Action != autopilot.ActionLaunch || res.Ticket != want {
			t.Fatalf("next = %+v, %v", res, err)
		}
		if want == a {
			h.clock.Advance(5 * time.Second) // inside R's span, not yet due
		}
		if _, err := h.svc.Launch(run.RunID, res.Ticket, res.Role); err != nil {
			t.Fatal(err)
		}
	}
	if len(calls.calls) != 0 {
		t.Fatalf("looked before due: %+v", calls.calls)
	}
	h.clock.Advance(StrayTabGrace + autopilot.StraySettle)
	h.waitOnce(run.RunID, a)
	if len(calls.calls) != 1 || !slices.Contains(calls.calls[0].Exclude, "/dev/ttys012") {
		t.Fatalf("calls = %+v", calls.calls)
	}
	if got := h.run(run.RunID); got.TerminalLateStrayTabs != nil || got.TerminalStrayTabs != nil {
		t.Fatalf("the next session's own tab was counted: %+v / %+v", got.TerminalLateStrayTabs, got.TerminalStrayTabs)
	}
}

// A poll with no look due writes the run once, as before; a look that finds
// nothing adds no write either, and only one that finds a tab adds one.
func TestStrayTab_PollsAddNoWriteUnlessATabIsFound(t *testing.T) {
	revisionAfterPoll := func(t *testing.T, h *harness, runID, c string) int64 {
		t.Helper()
		before := h.run(runID).Revision
		h.waitOnce(runID, c)
		return h.run(runID).Revision - before
	}
	t.Run("no look due", func(t *testing.T) {
		h := newHarness(t)
		calls := h.fakeStrayFinder(finding("/dev/ttys015"))
		runID, c := launchSilentWith(t, h, retriedTab("/dev/ttys011"))
		if d := revisionAfterPoll(t, h, runID, c); d != 1 || len(calls.calls) != 0 {
			t.Fatalf("writes %d, calls %d", d, len(calls.calls))
		}
	})
	t.Run("look finds nothing", func(t *testing.T) {
		h := newHarness(t)
		calls := h.fakeStrayFinder(finding())
		runID, c := launchSilentWith(t, h, retriedTab("/dev/ttys011"))
		h.clock.Advance(time.Minute)
		if d := revisionAfterPoll(t, h, runID, c); d != 1 || len(calls.calls) != 1 {
			t.Fatalf("writes %d, calls %d", d, len(calls.calls))
		}
	})
	t.Run("look finds a tab", func(t *testing.T) {
		h := newHarness(t)
		calls := h.fakeStrayFinder(finding("/dev/ttys015"))
		runID, c := launchSilentWith(t, h, retriedTab("/dev/ttys011"))
		h.clock.Advance(time.Minute)
		if d := revisionAfterPoll(t, h, runID, c); d != 2 || len(calls.calls) != 1 {
			t.Fatalf("writes %d, calls %d", d, len(calls.calls))
		}
	})
}

// Two looks whose spans overlap count a tab once.
func TestStrayTab_OverlappingLooksCountATabOnce(t *testing.T) {
	h := newHarness(t)
	h.fakeStrayFinder(finding("/dev/ttys015"))
	runID, c := launchSilentWith(t, h, retriedTab("/dev/ttys011"))
	if err := h.svc.withRun(runID, func(tx *autopilot.Tx, run *autopilot.Run) error {
		now := h.clock.Now()
		run.AddStrayTabCheck(autopilot.StrayTabCheck{TicketID: c, Role: autopilot.RoleWork, From: now, Until: now.Add(StrayTabGrace)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(time.Minute)
	h.waitOnce(runID, c)
	if got := h.run(runID); got.TerminalLateStrayTabs == nil || got.TerminalLateStrayTabs.Count != 1 {
		t.Fatalf("late = %+v", got.TerminalLateStrayTabs)
	}
}

// Several looks due at one poll run ps once, and are all judged with the
// same now, taken after ps (DFLT-00379 round 2).
func TestStrayTab_LooksDueTogetherRunPSOnce(t *testing.T) {
	h := newHarness(t)
	calls := h.fakeStrayFinder(finding())
	runID, c := launchSilentWith(t, h, retriedTab("/dev/ttys011"))
	if err := h.svc.withRun(runID, func(tx *autopilot.Tx, run *autopilot.Run) error {
		now := h.clock.Now()
		run.AddStrayTabCheck(autopilot.StrayTabCheck{TicketID: "X", Role: autopilot.RoleWork, From: now, Until: now.Add(StrayTabGrace), TTY: "/dev/ttys020"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(time.Minute)
	h.waitOnce(runID, c)
	if calls.reads != 1 || len(calls.calls) != 2 {
		t.Fatalf("reads %d, looks %d", calls.reads, len(calls.calls))
	}
	if !calls.calls[0].Now.Equal(calls.calls[1].Now) {
		t.Fatalf("now differs: %v / %v", calls.calls[0].Now, calls.calls[1].Now)
	}
	// Each look leaves out every due look's own tab.
	for _, call := range calls.calls {
		for _, tty := range []string{"/dev/ttys011", "/dev/ttys020"} {
			if !slices.Contains(call.Exclude, tty) {
				t.Fatalf("exclude %q lacks %s", call.Exclude, tty)
			}
		}
	}
}

// A look made long after its launch -- past RecentTabTTYAge -- still leaves
// the launch's own tab out (DFLT-00379 round 2): its session may have ended
// and left the tab at an empty prompt.
func TestStrayTab_ALookMadeLongAfterLeavesItsOwnTabOut(t *testing.T) {
	h := newHarness(t)
	h.ttyOf("/dev/ttys003")
	logs := h.captureLogs()
	r := h.ticket("R", "")
	a := h.ticket("A", r)
	calls := h.fakeStrayFinder(finding("/dev/ttys011"))
	h.launchOutcome = func(c launchCall) terminal.LaunchOutcome {
		if c.Ticket == r {
			return retriedTab("/dev/ttys011")
		}
		return terminal.LaunchOutcome{UsedTab: true, OpenMethod: "menu", NewTTY: "/dev/ttys012"}
	}
	run := h.start(r, autopilot.ModeTree)
	for _, want := range []string{r, a} {
		res, err := h.svc.Next(run.RunID)
		if err != nil || res.Action.Action != autopilot.ActionLaunch || res.Ticket != want {
			t.Fatalf("next = %+v, %v", res, err)
		}
		if want == a {
			h.clock.Advance(autopilot.RecentTabTTYAge + time.Minute)
		}
		if _, err := h.svc.Launch(run.RunID, res.Ticket, res.Role); err != nil {
			t.Fatal(err)
		}
	}
	if len(calls.calls) != 1 || !slices.Contains(calls.calls[0].Exclude, "/dev/ttys011") {
		t.Fatalf("calls = %+v", calls.calls)
	}
	if got := h.run(run.RunID); got.TerminalLateStrayTabs != nil || got.TerminalStrayTabs != nil {
		t.Fatalf("the launch's own tab was counted: %+v / %+v", got.TerminalLateStrayTabs, got.TerminalStrayTabs)
	}
	if w := lateStrayLogs(*logs); w != nil {
		t.Fatalf("warnings = %q", w)
	}
}

// Two looks whose spans overlap, made at different polls, count a tab
// found by both once (DFLT-00379 round 2): the first keeps the tab among
// the ttys no later look takes.
func TestStrayTab_LooksAtDifferentPollsCountATabOnce(t *testing.T) {
	h := newHarness(t)
	logs := h.captureLogs()
	calls := h.fakeStrayFinder(finding("/dev/ttys015"))
	runID, c := launchSilentWith(t, h, retriedTab("/dev/ttys011"))
	// A second retried launch 10 seconds later, inside the first's span.
	if err := h.svc.withRun(runID, func(tx *autopilot.Tx, run *autopilot.Run) error {
		now := h.clock.Now().Add(10 * time.Second)
		run.AddStrayTabCheck(autopilot.StrayTabCheck{TicketID: "B", Role: autopilot.RoleWork, From: now, Until: now.Add(StrayTabGrace), TTY: "/dev/ttys020"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(StrayTabGrace + autopilot.StraySettle)
	h.waitOnce(runID, c)
	if len(calls.calls) != 1 {
		t.Fatalf("first poll: %d looks", len(calls.calls))
	}
	h.clock.Advance(10 * time.Second)
	h.waitOnce(runID, c)
	if len(calls.calls) != 2 || !slices.Contains(calls.calls[1].Exclude, "/dev/ttys015") {
		t.Fatalf("second look = %+v", calls.calls)
	}
	got := h.run(runID)
	if got.TerminalLateStrayTabs == nil || got.TerminalLateStrayTabs.Count != 1 || got.TerminalStrayTabs.Count != 1 {
		t.Fatalf("late %+v, stray %+v", got.TerminalLateStrayTabs, got.TerminalStrayTabs)
	}
	if w := lateStrayLogs(*logs); len(w) != 1 || !strings.Contains(w[0], c) {
		t.Fatalf("warnings = %q", w)
	}
}

// A look that finds two tabs counts its launch once in both tallies, and
// names both tabs in one warning (DFLT-00379 round 2).
func TestStrayTab_TwoTabsOfOneLaunchCountItOnce(t *testing.T) {
	h := newHarness(t)
	logs := h.captureLogs()
	h.fakeStrayFinder(finding("/dev/ttys015", "/dev/ttys016"))
	runID, c := launchSilentWith(t, h, retriedTab("/dev/ttys011"))
	h.clock.Advance(time.Minute)
	h.waitOnce(runID, c)
	got := h.run(runID)
	if got.TerminalLateStrayTabs == nil || got.TerminalLateStrayTabs.Count != 1 || got.TerminalStrayTabs.Count != 1 {
		t.Fatalf("late %+v, stray %+v", got.TerminalLateStrayTabs, got.TerminalStrayTabs)
	}
	var recent []string
	for _, r := range got.TerminalRecentTabTTYs {
		recent = append(recent, r.TTY)
	}
	if !slices.Contains(recent, "/dev/ttys015") || !slices.Contains(recent, "/dev/ttys016") {
		t.Fatalf("recent = %q", recent)
	}
	var warn []string
	for _, l := range *logs {
		if strings.Contains(l, "appeared later and are left empty") {
			warn = append(warn, l)
		}
	}
	if len(warn) != 1 || !strings.Contains(warn[0], "(/dev/ttys015, /dev/ttys016)") {
		t.Fatalf("warnings = %q", warn)
	}
}

// A later look whose tabs cannot be written to the run file still names
// them, after a line saying they are not recorded (DFLT-00382). The record
// is made to fail inside the fake ps read, so the wait's own write has gone
// through and only the record's withRunIn fails: either its save (the runs
// directory made read-only, so the temporary file cannot be made) after the
// tabs were counted in memory, or its load (the run file made unreadable)
// before anything was counted.
func TestStrayTab_FailedRecordStillWarnsAndRecordsNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permissions do not stop reads and writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads and writes past file permissions")
	}
	cases := []struct {
		name string
		// target returns the path whose mode is set to mode.
		target func(runsDir, runID string) string
		mode   os.FileMode
		// stillWorks reports whether path can still be used as the record
		// needs it, which would mean the test no longer makes it fail.
		stillWorks func(path string) bool
	}{
		{
			name:   "save fails",
			target: func(runsDir, _ string) string { return runsDir },
			mode:   0o500,
			stillWorks: func(dir string) bool {
				f, err := os.CreateTemp(dir, ".probe-*")
				if err != nil {
					return false
				}
				f.Close()
				_ = os.Remove(f.Name())
				return true
			},
		},
		{
			name:   "load fails",
			target: func(runsDir, runID string) string { return filepath.Join(runsDir, runID+".json") },
			mode:   0o000,
			stillWorks: func(file string) bool {
				_, err := os.ReadFile(file)
				return err == nil
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			logs := h.captureLogs()
			lockPath, err := h.svc.Registry.LockPath(h.projectID)
			if err != nil {
				t.Fatal(err)
			}
			runsDir := filepath.Join(filepath.Dir(lockPath), "runs")

			calls := h.fakeStrayFinder(finding("/dev/ttys015"))
			read := h.svc.ReadStrayTabs
			var runID, target string
			var mode os.FileMode
			var chmodErr error
			stillWorks := false
			h.svc.ReadStrayTabs = func() (StrayTabs, error) {
				target = tc.target(runsDir, runID)
				info, err := os.Stat(target)
				if err != nil {
					chmodErr = err
					return read()
				}
				mode = info.Mode().Perm()
				if chmodErr = os.Chmod(target, tc.mode); chmodErr == nil {
					// Put back even when the test stops early, so the
					// temporary directory can be removed.
					t.Cleanup(func() { _ = os.Chmod(target, mode) })
					stillWorks = tc.stillWorks(target)
				}
				return read()
			}

			var c string
			runID, c = launchSilentWith(t, h, retriedTab("/dev/ttys011"))
			h.clock.Advance(StrayTabGrace + autopilot.StraySettle)
			h.waitOnce(runID, c)
			if chmodErr != nil {
				t.Fatalf("could not take the permission away: %v", chmodErr)
			}
			if target == "" {
				t.Fatal("the later look never read ps")
			}
			if err := os.Chmod(target, mode); err != nil {
				t.Fatal(err)
			}
			if stillWorks {
				t.Fatalf("%s still works without the permission; this test can no longer make the record fail", target)
			}
			if calls.reads != 1 || len(calls.calls) != 1 {
				t.Fatalf("reads %d, calls %+v", calls.reads, calls.calls)
			}

			prefix := "could not record the stray Terminal tabs found in run " + runID + ": "
			failedAt, failed := -1, 0
			for i, l := range *logs {
				if strings.HasPrefix(l, prefix) {
					failed++
					failedAt = i
					if strings.TrimSpace(strings.TrimPrefix(l, prefix)) == "" {
						t.Fatalf("no error in %q", l)
					}
				}
			}
			if failed != 1 {
				t.Fatalf("record failure lines = %d in %q", failed, *logs)
			}
			warn := lateStrayLogs(*logs)
			if len(warn) != 1 || !strings.Contains(warn[0], "the work session of "+c) ||
				!strings.Contains(warn[0], "opened in a new Terminal tab after a second attempt, and") ||
				!strings.Contains(warn[0], "(/dev/ttys015)") || !strings.Contains(warn[0], "close it by hand") {
				t.Fatalf("warnings = %q", warn)
			}
			if slices.Index(*logs, warn[0]) < failedAt {
				t.Fatalf("the tab warning came before the record failure: %q", *logs)
			}

			run := h.run(runID)
			if run.TerminalStrayTabs != nil || run.TerminalLateStrayTabs != nil {
				t.Fatalf("recorded: stray %+v, late %+v", run.TerminalStrayTabs, run.TerminalLateStrayTabs)
			}
			var recent []string
			for _, r := range run.TerminalRecentTabTTYs {
				recent = append(recent, r.TTY)
			}
			if !reflect.DeepEqual(recent, []string{"/dev/ttys011"}) {
				t.Fatalf("recent = %q", recent)
			}
			// Taken out by the wait's own write and dropped, as
			// runStrayTabChecks says, not put back.
			if run.TerminalStrayTabChecks != nil {
				t.Fatalf("checks = %+v", run.TerminalStrayTabChecks)
			}
		})
	}
}
