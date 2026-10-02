package autopilot

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00362: why the orchestrator has no tty, the launches that opened a
// window without trying a tab and the tabs that opened late are recorded
// on the run, follow the orchestrator like the tty, and stay on this
// machine.

func beginTTYReason(t *testing.T, g *Registry, runID, tty, reason string, reserve bool) BeginResult {
	t.Helper()
	res, err := g.Begin(BeginRequest{RootID: "R", ProjectID: "proj-A", RootStatus: domain.TicketTODO, Mode: ModeTree,
		RunID: runID, Reserve: reserve, Settings: Defaults(), TerminalTTY: tty, TerminalTTYReason: reason})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	return res
}

func TestRun_RecordWindowLaunchAndLateTab(t *testing.T) {
	run := &Run{}
	t0 := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	if n := run.RecordWindowLaunch("no-tty", "A", t0); n != 1 {
		t.Fatalf("count = %d", n)
	}
	if n := run.RecordWindowLaunch("no-tty", "B", t1); n != 2 {
		t.Fatalf("count = %d", n)
	}
	run.RecordWindowLaunch("tab-disabled", "C", t1)
	want := LaunchTally{Count: 2, FirstAt: t0, LastAt: t1, LastTicketID: "B"}
	if got := run.TerminalWindowLaunches["no-tty"]; got != want || run.TerminalWindowLaunches["tab-disabled"].Count != 1 || len(run.TerminalWindowLaunches) != 2 {
		t.Fatalf("tallies = %+v", run.TerminalWindowLaunches)
	}
	if run.TerminalTabFailures != nil {
		t.Fatal("a window launch must not be a tab failure")
	}
	run.RecordLateTab("D", t0)
	run.RecordLateTab("E", t1)
	if lt := run.TerminalLateTabs; lt == nil || *lt != (LaunchTally{Count: 2, FirstAt: t0, LastAt: t1, LastTicketID: "E"}) {
		t.Fatalf("late tabs = %+v", run.TerminalLateTabs)
	}
}

func TestBegin_TTYReasonAndTalliesFollowTheOrchestrator(t *testing.T) {
	g, _ := newRegistry(t)
	run := beginTTYReason(t, g, "", "", "term-program", false).Run
	if run.TerminalTTYReason != "term-program" {
		t.Fatalf("reason = %q", run.TerminalTTYReason)
	}
	loaded, _ := g.Load("proj-A", run.ID)
	if loaded.TerminalTTYReason != "term-program" {
		t.Fatalf("loaded reason = %q", loaded.TerminalTTYReason)
	}
	// The first orchestrator's launches.
	now := g.now()
	loaded.RecordWindowLaunch("no-tty", "A", now)
	loaded.RecordLateTab("B", now)
	loaded.RecordTabFailure(TabFailureRecord{At: now, TicketID: "C", Message: "x"})
	loaded.stop(now, StopTicketFailed, "A", "x")
	saveRun(t, g, loaded)

	// A takeover from a Terminal.app tab: a tty, no reason, the counts
	// start over, the failure history stays.
	took := beginTTYReason(t, g, "", "/dev/ttys004", "", false)
	if !took.TookOver {
		t.Fatalf("res = %+v", took)
	}
	r := took.Run
	if r.TerminalTTY != "/dev/ttys004" || r.TerminalTTYReason != "" || r.TerminalWindowLaunches != nil || r.TerminalLateTabs != nil || len(r.TerminalTabFailures) != 1 {
		t.Fatalf("after takeover: %+v", r)
	}

	// A reservation holds neither a tty nor a reason; the adopter's apply.
	r.RecordWindowLaunch("tab-disabled", "D", now)
	r.stop(now, StopTicketFailed, "A", "x")
	saveRun(t, g, r)
	reserved := beginTTYReason(t, g, "", "/dev/ttys005", "tmux", true).Run
	if reserved.TerminalTTY != "" || reserved.TerminalTTYReason != "" || reserved.TerminalWindowLaunches != nil {
		t.Fatalf("reservation: %+v", reserved)
	}
	adopted := beginTTYReason(t, g, reserved.ID, "", "tmux", false).Run
	if adopted.TerminalTTY != "" || adopted.TerminalTTYReason != "tmux" || adopted.TerminalWindowLaunches != nil {
		t.Fatalf("adopted: %+v", adopted)
	}
}

func TestRun_TerminalRecordsStayLocalAndOldFilesLoad(t *testing.T) {
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	run := &Run{ID: "run-1", TerminalTTYReason: "no-tty"}
	run.RecordWindowLaunch("no-tty", "A", now)
	run.RecordLateTab("B", now)
	run.RecordTabFailure(TabFailureRecord{At: now, TicketID: "C", Diagnostics: &TabDiagnosticsRecord{FrontmostApp: "Google Chrome", ScreenLock: "unlocked"}})
	v, err := run.SharedView()
	if err != nil {
		t.Fatal(err)
	}
	if v.TerminalTTYReason != "" || v.TerminalWindowLaunches != nil || v.TerminalLateTabs != nil || v.TerminalTabFailures != nil {
		t.Fatalf("shared view kept terminal records: %+v", v)
	}
	if run.TerminalTTYReason == "" || run.TerminalWindowLaunches == nil || run.TerminalLateTabs == nil {
		t.Fatal("SharedView changed the run itself")
	}

	// The JSON names, and a record of before DFLT-00362 still reads.
	data, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"terminal_tty_reason", "terminal_window_launches", "terminal_late_tabs"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("no %s in %s", key, data)
		}
	}
	diag := raw["terminal_tab_failures"].([]any)[0].(map[string]any)["diagnostics"].(map[string]any)
	if diag["frontmost_app"] != "Google Chrome" || diag["screen_lock"] != "unlocked" || len(diag) != 2 {
		t.Fatalf("diagnostics = %v", diag)
	}
	old := []byte(`{"id":"run-0","terminal_tty":"/dev/ttys003","terminal_tab_failures":[{"at":"2026-10-01T09:51:43Z","ticket_id":"X","role":"work","kind":"script-error","error_number":9102,"message":"m","phase":"keystroke-sent","keystroke_sent":true,"disabled_run":false}]}`)
	var back Run
	if err := json.Unmarshal(old, &back); err != nil {
		t.Fatal(err)
	}
	if back.TerminalTabFailures[0].Diagnostics != nil || back.TerminalWindowLaunches != nil || back.TerminalTTYReason != "" || back.TerminalTabFailures[0].ErrorNumber != 9102 {
		t.Fatalf("old record = %+v", back)
	}
	empty, err := json.Marshal(&Run{})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"terminal_tty_reason", "terminal_window_launches", "terminal_late_tabs"} {
		if containsKey(empty, key) {
			t.Errorf("%s written for a run without it", key)
		}
	}
}

func containsKey(data []byte, key string) bool {
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	_, ok := m[key]
	return ok
}
