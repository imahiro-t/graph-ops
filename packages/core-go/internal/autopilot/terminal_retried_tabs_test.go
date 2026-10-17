package autopilot

import (
	"encoding/json"
	"testing"
	"time"
)

// DFLT-00377: the tabs that opened only at the tab script's retry are
// counted like the late ones (they follow the orchestrator and stay on this
// machine), and a failure's new diagnostics are kept in the run record
// under their JSON names, while a record without them still reads.

func TestRun_RecordRetriedTab(t *testing.T) {
	run := &Run{TerminalTTY: "/dev/ttys003"}
	t0 := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	run.RecordRetriedTab("A", t0)
	run.RecordRetriedTab("B", t1)
	if rt := run.TerminalRetriedTabs; rt == nil || *rt != (LaunchTally{Count: 2, FirstAt: t0, LastAt: t1, LastTicketID: "B"}) {
		t.Fatalf("retried tabs = %+v", run.TerminalRetriedTabs)
	}
	if run.TerminalLateTabs != nil || run.TerminalTabFailures != nil {
		t.Fatal("a retried tab is neither a late tab nor a failure")
	}
	v, err := run.SharedView()
	if err != nil {
		t.Fatal(err)
	}
	if v.TerminalRetriedTabs != nil {
		t.Fatal("the shared view must not carry the retried tabs")
	}
	run.resetTerminal("/dev/ttys004", "")
	if run.TerminalRetriedTabs != nil {
		t.Fatal("a new orchestrator starts the count over")
	}
}

func TestTabDiagnosticsRecord_DFLT00377Fields(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	run := &Run{ID: "run-1"}
	run.RecordRetriedTab("A", now)
	run.RecordTabFailure(TabFailureRecord{At: now, TicketID: "C", Diagnostics: &TabDiagnosticsRecord{
		FrontWindowIDBefore: "55701", OpenMethod: "menu", Retried: true, RetryFrontmostApp: "Google Chrome",
		Refocused: true, SecureInput: "412 loginwindow", HIDIdleMS: "250",
	}})
	data, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["terminal_retried_tabs"]; !ok {
		t.Fatalf("no terminal_retried_tabs in %s", data)
	}
	diag := raw["terminal_tab_failures"].([]any)[0].(map[string]any)["diagnostics"].(map[string]any)
	want := map[string]any{
		"front_window_id_before": "55701", "open_method": "menu", "retried": true, "retry_frontmost_app": "Google Chrome",
		"refocused": true, "secure_input": "412 loginwindow", "hid_idle_ms": "250",
	}
	if len(diag) != len(want) {
		t.Fatalf("diagnostics = %v", diag)
	}
	for k, v := range want {
		if diag[k] != v {
			t.Errorf("%s = %v, want %v", k, diag[k], v)
		}
	}
	// A DFLT-00362 record (no new fields) reads with them empty.
	old := []byte(`{"id":"run-0","terminal_tab_failures":[{"at":"2026-10-18T00:16:49Z","ticket_id":"X","role":"work","kind":"script-error","error_number":9102,"message":"m","phase":"keystroke-sent","keystroke_sent":true,"disabled_run":false,"diagnostics":{"screen_lock":"unlocked","frontmost_app_before":"Terminal","tabs_before":"1"}}],"terminal_late_tabs":{"count":1,"first_at":"2026-10-18T00:16:49Z","last_at":"2026-10-18T00:16:49Z","last_ticket_id":"X"}}`)
	var back Run
	if err := json.Unmarshal(old, &back); err != nil {
		t.Fatal(err)
	}
	d := back.TerminalTabFailures[0].Diagnostics
	if d == nil || d.FrontmostAppBefore != "Terminal" || d.OpenMethod != "" || d.Retried || d.HIDIdleMS != "" || back.TerminalRetriedTabs != nil {
		t.Fatalf("old record = %+v / %+v", back, d)
	}
}
