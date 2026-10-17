package autopilot

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// DFLT-00379: the later looks for a stray tab, the ttys of the run's recent
// tabs they leave out, and the count of the stray tabs they find are kept
// on the run, on this machine only, and follow the orchestrator.

var lateT0 = time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

func TestRun_RecordLateStrayTab(t *testing.T) {
	run := &Run{TerminalTTY: "/dev/ttys003"}
	t1 := lateT0.Add(time.Minute)
	run.RecordLateStrayTab("A", lateT0)
	run.RecordLateStrayTab("B", t1)
	if lt := run.TerminalLateStrayTabs; lt == nil || *lt != (LaunchTally{Count: 2, FirstAt: lateT0, LastAt: t1, LastTicketID: "B"}) {
		t.Fatalf("late stray tabs = %+v", run.TerminalLateStrayTabs)
	}
	if run.TerminalStrayTabs != nil {
		t.Fatal("RecordLateStrayTab counts only the late ones; the caller counts the stray tab itself")
	}
}

func TestRun_StrayTabChecksAreBounded(t *testing.T) {
	run := &Run{}
	for i := 0; i < MaxStrayTabChecks+3; i++ {
		run.AddStrayTabCheck(StrayTabCheck{TicketID: fmt.Sprint(i), From: lateT0, Until: lateT0})
	}
	if len(run.TerminalStrayTabChecks) != MaxStrayTabChecks {
		t.Fatalf("checks = %d", len(run.TerminalStrayTabChecks))
	}
	if first := run.TerminalStrayTabChecks[0].TicketID; first != "3" {
		t.Fatalf("the oldest must go first; first = %s", first)
	}
}

func TestRun_TakeDueStrayTabChecks(t *testing.T) {
	run := &Run{}
	early := StrayTabCheck{TicketID: "A", Role: "work", From: lateT0, Until: lateT0.Add(20 * time.Second)}
	late := StrayTabCheck{TicketID: "B", Role: "work", From: lateT0.Add(time.Minute), Until: lateT0.Add(80 * time.Second)}
	run.AddStrayTabCheck(early)
	run.AddStrayTabCheck(late)

	// Before Until + StraySettle: nothing is due, nothing is taken.
	if due := run.TakeDueStrayTabChecks(early.Until.Add(StraySettle - time.Nanosecond)); due != nil {
		t.Fatalf("due = %+v", due)
	}
	if len(run.TerminalStrayTabChecks) != 2 {
		t.Fatalf("checks = %+v", run.TerminalStrayTabChecks)
	}
	// At Until + StraySettle: the first is due and taken out.
	due := run.TakeDueStrayTabChecks(early.Until.Add(StraySettle))
	if !reflect.DeepEqual(due, []StrayTabCheck{early}) {
		t.Fatalf("due = %+v", due)
	}
	if !reflect.DeepEqual(run.TerminalStrayTabChecks, []StrayTabCheck{late}) {
		t.Fatalf("left = %+v", run.TerminalStrayTabChecks)
	}
	// Taken out, it is never due again.
	due = run.TakeDueStrayTabChecks(lateT0.Add(time.Hour))
	if !reflect.DeepEqual(due, []StrayTabCheck{late}) || run.TerminalStrayTabChecks != nil {
		t.Fatalf("due = %+v, left = %+v", due, run.TerminalStrayTabChecks)
	}
	if due := run.TakeDueStrayTabChecks(lateT0.Add(time.Hour)); due != nil {
		t.Fatalf("due again = %+v", due)
	}
}

func TestRun_RecordRecentTabTTY(t *testing.T) {
	run := &Run{TerminalTTY: "/dev/ttys000"}
	run.RecordRecentTabTTY("/dev/ttys001", lateT0)
	run.RecordRecentTabTTY("", lateT0.Add(time.Second))
	run.RecordRecentTabTTY("/dev/ttys002", lateT0.Add(5*time.Minute))
	if got := run.StrayTabExclusions(); !reflect.DeepEqual(got, []string{"/dev/ttys000", "/dev/ttys001", "/dev/ttys002"}) {
		t.Fatalf("exclusions = %q", got)
	}
	// Older than RecentTabTTYAge: dropped by the next record.
	run.RecordRecentTabTTY("/dev/ttys003", lateT0.Add(RecentTabTTYAge+time.Second))
	if got := run.StrayTabExclusions(); !reflect.DeepEqual(got, []string{"/dev/ttys000", "/dev/ttys002", "/dev/ttys003"}) {
		t.Fatalf("exclusions after aging = %q", got)
	}
	// Beyond MaxRecentTabTTYs: the oldest go.
	at := lateT0.Add(RecentTabTTYAge + 2*time.Second)
	for i := 0; i < MaxRecentTabTTYs+5; i++ {
		run.RecordRecentTabTTY(fmt.Sprintf("/dev/ttys1%02d", i), at)
	}
	if n := len(run.TerminalRecentTabTTYs); n != MaxRecentTabTTYs {
		t.Fatalf("recent = %d", n)
	}
	if first := run.TerminalRecentTabTTYs[0].TTY; first != "/dev/ttys105" {
		t.Fatalf("first = %s", first)
	}
	if got := (&Run{}).StrayTabExclusions(); got != nil {
		t.Fatalf("no tty, no recent tabs: %q", got)
	}
}

// A tty is kept past RecentTabTTYAge while a look still to be made could
// take it, and the looks still to be made leave their own launch's tab out
// (DFLT-00379 round 2).
func TestRun_RecentTabTTYsOutliveTheirAgeForPendingLooks(t *testing.T) {
	run := &Run{TerminalTTY: "/dev/ttys000"}
	run.AddStrayTabCheck(StrayTabCheck{TicketID: "A", From: lateT0, Until: lateT0.Add(20 * time.Second), TTY: "/dev/ttys009"})
	run.RecordRecentTabTTY("/dev/ttys001", lateT0.Add(-2*time.Minute)) // before the look's span
	run.RecordRecentTabTTY("/dev/ttys002", lateT0.Add(30*time.Second)) // a tab counted in it
	run.RecordRecentTabTTY("/dev/ttys003", lateT0.Add(RecentTabTTYAge+time.Hour))
	want := []string{"/dev/ttys000", "/dev/ttys002", "/dev/ttys003", "/dev/ttys009"}
	if got := run.StrayTabExclusions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("exclusions = %q, want %q", got, want)
	}
	// Once the look is made, the age applies again.
	run.TakeDueStrayTabChecks(lateT0.Add(RecentTabTTYAge + time.Hour))
	run.RecordRecentTabTTY("/dev/ttys004", lateT0.Add(RecentTabTTYAge+time.Hour))
	want = []string{"/dev/ttys000", "/dev/ttys003", "/dev/ttys004"}
	if got := run.StrayTabExclusions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("exclusions = %q, want %q", got, want)
	}
}

func TestRun_LateStrayTabFieldsStayLocalAndFollowTheOrchestrator(t *testing.T) {
	run := &Run{TerminalTTY: "/dev/ttys003"}
	run.RecordLateStrayTab("A", lateT0)
	run.AddStrayTabCheck(StrayTabCheck{TicketID: "A", Role: "work", From: lateT0, Until: lateT0.Add(20 * time.Second)})
	run.RecordRecentTabTTY("/dev/ttys004", lateT0)
	data, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"terminal_late_stray_tabs", "terminal_stray_tab_checks", "terminal_recent_tab_ttys"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("no %s in %s", key, data)
		}
	}
	var back Run
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.TerminalStrayTabChecks, run.TerminalStrayTabChecks) || !reflect.DeepEqual(back.TerminalRecentTabTTYs, run.TerminalRecentTabTTYs) ||
		back.TerminalLateStrayTabs == nil || *back.TerminalLateStrayTabs != *run.TerminalLateStrayTabs {
		t.Fatalf("read back = %+v", back)
	}
	// A run file written before DFLT-00379 still reads.
	var old Run
	if err := json.Unmarshal([]byte(`{"id":"run-1","terminal_tty":"/dev/ttys003"}`), &old); err != nil || old.TerminalStrayTabChecks != nil {
		t.Fatalf("old = %+v, %v", old, err)
	}

	v, err := run.SharedView()
	if err != nil {
		t.Fatal(err)
	}
	if v.TerminalLateStrayTabs != nil || v.TerminalStrayTabChecks != nil || v.TerminalRecentTabTTYs != nil {
		t.Fatal("the shared view must not carry the later looks for stray tabs")
	}
	if run.TerminalLateStrayTabs == nil || run.TerminalStrayTabChecks == nil || run.TerminalRecentTabTTYs == nil {
		t.Fatal("SharedView must leave the run itself alone")
	}
	run.resetTerminal("/dev/ttys009", "")
	if run.TerminalLateStrayTabs != nil || run.TerminalStrayTabChecks != nil || run.TerminalRecentTabTTYs != nil {
		t.Fatal("a new orchestrator starts over")
	}
}
