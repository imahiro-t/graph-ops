package autopilot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// DFLT-00361: the run keeps each launch's tab failure and the count of slow
// timeouts in a row; the starts that re-enable the tab reset the count and
// keep the history, and neither leaves this machine.

// withTabHistory records a tab failure history and a slow-timeout count on
// the run, as an earlier orchestrator's launches would have.
func withTabHistory(run *Run, at time.Time) {
	run.TerminalTabSlowTimeouts = 1
	run.RecordTabFailure(TabFailureRecord{At: at, TicketID: "A", Role: RoleWork, Kind: "timeout", Message: "osascript: slow", Phase: "keystroke-sent", KeystrokeSent: true})
}

func assertTabHistoryKeptAndCountReset(t *testing.T, run *Run) {
	t.Helper()
	if run.TerminalTabSlowTimeouts != 0 {
		t.Fatalf("slow timeouts = %d, want 0", run.TerminalTabSlowTimeouts)
	}
	if len(run.TerminalTabFailures) != 1 || run.TerminalTabFailures[0].TicketID != "A" || !run.TerminalTabFailures[0].KeystrokeSent {
		t.Fatalf("failures = %+v, want the history kept", run.TerminalTabFailures)
	}
}

func TestBegin_TakeoverResetsSlowTimeoutsAndKeepsTabFailures(t *testing.T) {
	g, _ := newRegistry(t)
	run := beginTTY(t, g, "", "/dev/ttys003", false).Run
	withTabHistory(run, g.now())
	withTabDisabled(t, g, run, "/dev/ttys003")
	res := beginTTY(t, g, "", "/dev/ttys009", false)
	if !res.TookOver {
		t.Fatalf("res = %+v", res)
	}
	assertTTY(t, res.Run, "/dev/ttys009")
	assertTabHistoryKeptAndCountReset(t, res.Run)
}

func TestBegin_AdoptionResetsSlowTimeoutsAndKeepsTabFailures(t *testing.T) {
	g, _ := newRegistry(t)
	run := beginTTY(t, g, "", "/dev/ttys003", false).Run
	withTabHistory(run, g.now())
	withTabDisabled(t, g, run, "/dev/ttys003")
	reserved := beginTTY(t, g, "", "", true)
	if !reserved.TookOver {
		t.Fatalf("reserved = %+v", reserved)
	}
	assertTabHistoryKeptAndCountReset(t, reserved.Run)
	// Cancelling puts the count back with everything else.
	if err := g.CancelReservation("proj-A", run.ID); err != nil {
		t.Fatal(err)
	}
	back, _ := g.Load("proj-A", run.ID)
	if back.TerminalTabSlowTimeouts != 1 || len(back.TerminalTabFailures) != 1 {
		t.Fatalf("restored: slow %d, failures %+v", back.TerminalTabSlowTimeouts, back.TerminalTabFailures)
	}
	// Adoption of a fresh reservation over the run resets the count.
	beginTTY(t, g, "", "", true)
	withCount, _ := g.Load("proj-A", run.ID)
	withCount.TerminalTabSlowTimeouts = 1
	saveRun(t, g, withCount)
	adopted := beginTTY(t, g, run.ID, "/dev/ttys007", false)
	if !adopted.Adopted {
		t.Fatalf("adopted = %+v", adopted)
	}
	assertTTY(t, adopted.Run, "/dev/ttys007")
	assertTabHistoryKeptAndCountReset(t, adopted.Run)
}

func TestSharedView_DropsTheTabFailures(t *testing.T) {
	run := &Run{ID: "run-1", TerminalTTY: "/dev/ttys003", TerminalTabSlowTimeouts: 1}
	withTabHistory(run, time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))
	v, err := run.SharedView()
	if err != nil {
		t.Fatal(err)
	}
	if v.TerminalTabSlowTimeouts != 0 || v.TerminalTabFailures != nil {
		t.Fatalf("shared view kept slow %d, failures %+v", v.TerminalTabSlowTimeouts, v.TerminalTabFailures)
	}
	if run.TerminalTabSlowTimeouts != 1 || len(run.TerminalTabFailures) != 1 {
		t.Fatal("SharedView changed the run itself")
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"terminal_tab_failures", "terminal_tab_slow_timeouts"} {
		if _, ok := m[key]; ok {
			t.Errorf("the shared view's JSON has %s", key)
		}
	}
}

func TestRun_TabFailureFieldsInTheRunFile(t *testing.T) {
	g, _ := newRegistry(t)
	run := beginTTY(t, g, "", "/dev/ttys003", false).Run
	path := filepath.Join(g.Root, "proj-A", "runs", run.ID+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	// Empty: omitted, so a file without them (written before DFLT-00361)
	// is what a run with none looks like.
	for _, key := range []string{"terminal_tab_failures", "terminal_tab_slow_timeouts"} {
		if _, ok := m[key]; ok {
			t.Errorf("an empty %s should be omitted", key)
		}
	}
	withTabHistory(run, g.now())
	saveRun(t, g, run)
	loaded, err := g.Load("proj-A", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TerminalTabSlowTimeouts != 1 || len(loaded.TerminalTabFailures) != 1 || loaded.TerminalTabFailures[0].Phase != "keystroke-sent" {
		t.Fatalf("loaded: slow %d, failures %+v", loaded.TerminalTabSlowTimeouts, loaded.TerminalTabFailures)
	}
	raw, _ = os.ReadFile(path)
	var withFields struct {
		Failures []map[string]any `json:"terminal_tab_failures"`
	}
	if err := json.Unmarshal(raw, &withFields); err != nil {
		t.Fatal(err)
	}
	if len(withFields.Failures) != 1 {
		t.Fatalf("file failures = %+v", withFields.Failures)
	}
	rec := withFields.Failures[0]
	for _, key := range []string{"at", "ticket_id", "role", "kind", "message", "phase", "keystroke_sent", "disabled_run"} {
		if _, ok := rec[key]; !ok {
			t.Errorf("the record lacks %s: %v", key, rec)
		}
	}
	if _, ok := rec["error_number"]; ok {
		t.Errorf("a record without an error number should omit it: %v", rec)
	}
}

func TestRun_RecordTabFailureKeepsTheLatest(t *testing.T) {
	run := &Run{}
	for i := 0; i < MaxTabFailureRecords*2+1; i++ {
		run.RecordTabFailure(TabFailureRecord{ErrorNumber: i + 1})
	}
	if len(run.TerminalTabFailures) != MaxTabFailureRecords || run.TerminalTabFailures[0].ErrorNumber != MaxTabFailureRecords+2 ||
		run.TerminalTabFailures[MaxTabFailureRecords-1].ErrorNumber != MaxTabFailureRecords*2+1 {
		t.Fatalf("kept %d, first %d", len(run.TerminalTabFailures), run.TerminalTabFailures[0].ErrorNumber)
	}
}

// At the cap, screen-locked records go first, so a long screen lock does not
// push out the failures of tabs that were tried (DFLT-00362).
func TestRun_RecordTabFailureDropsScreenLockedFirst(t *testing.T) {
	run := &Run{}
	for i := 0; i < MaxTabFailureRecords-1; i++ {
		run.RecordTabFailure(TabFailureRecord{ErrorNumber: 9102, TicketID: fmt.Sprintf("F%d", i)})
	}
	for i := 0; i < MaxTabFailureRecords*2; i++ {
		run.RecordTabFailure(TabFailureRecord{Kind: TabFailureKindScreenLocked, TicketID: fmt.Sprintf("L%d", i)})
	}
	recs := run.TerminalTabFailures
	if len(recs) != MaxTabFailureRecords || recs[0].TicketID != "F0" || recs[MaxTabFailureRecords-2].TicketID != "F48" ||
		recs[MaxTabFailureRecords-1].TicketID != fmt.Sprintf("L%d", MaxTabFailureRecords*2-1) {
		t.Fatalf("kept %d: first %s, last %s", len(recs), recs[0].TicketID, recs[len(recs)-1].TicketID)
	}
	// A failure of a tried tab then drops the screen-locked record, not the
	// oldest failure.
	run.RecordTabFailure(TabFailureRecord{ErrorNumber: 9103, TicketID: "N"})
	recs = run.TerminalTabFailures
	if len(recs) != MaxTabFailureRecords || recs[0].TicketID != "F0" || recs[MaxTabFailureRecords-1].TicketID != "N" {
		t.Fatalf("kept %d: first %s, last %s", len(recs), recs[0].TicketID, recs[len(recs)-1].TicketID)
	}
	for _, r := range recs {
		if r.Kind == TabFailureKindScreenLocked {
			t.Fatalf("a screen-locked record outlived a tried tab's failure: %+v", r)
		}
	}
}
