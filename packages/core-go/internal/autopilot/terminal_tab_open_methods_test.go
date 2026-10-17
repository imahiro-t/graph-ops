package autopilot

import (
	"encoding/json"
	"testing"
	"time"
)

// DFLT-00378: the tabs that opened are counted by how the tab script's last
// attempt opened them (terminal_tab_open_methods), and those not opened by
// pressing the New Tab menu item once more on their own
// (terminal_non_menu_tabs). Both follow the orchestrator and stay on this
// machine, like the late, retried and stray tabs.

func TestRun_RecordTabOpenMethod_Menu(t *testing.T) {
	run := &Run{}
	t0 := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	run.RecordTabOpenMethod(TabOpenMethodMenu, "A", t0)
	run.RecordTabOpenMethod("menu", "B", t1)
	if len(run.TerminalTabOpenMethods) != 1 || run.TerminalTabOpenMethods["menu"] != (LaunchTally{Count: 2, FirstAt: t0, LastAt: t1, LastTicketID: "B"}) {
		t.Fatalf("open methods = %+v", run.TerminalTabOpenMethods)
	}
	if run.TerminalNonMenuTabs != nil {
		t.Fatalf("a menu tab is not a non-menu tab: %+v", run.TerminalNonMenuTabs)
	}
	if run.TerminalLateTabs != nil || run.TerminalRetriedTabs != nil || run.TerminalStrayTabs != nil || run.TerminalTabFailures != nil {
		t.Fatal("the open method is counted on its own")
	}
}

func TestRun_RecordTabOpenMethod_NonMenu(t *testing.T) {
	for _, method := range []string{TabOpenMethodMenuFailed, TabOpenMethodKeystrokeNoMenu, TabOpenMethodKeystrokeRetry} {
		t.Run(method, func(t *testing.T) {
			run := &Run{}
			t0 := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
			run.RecordTabOpenMethod(method, "A", t0)
			want := LaunchTally{Count: 1, FirstAt: t0, LastAt: t0, LastTicketID: "A"}
			if len(run.TerminalTabOpenMethods) != 1 || run.TerminalTabOpenMethods[method] != want {
				t.Fatalf("open methods = %+v", run.TerminalTabOpenMethods)
			}
			if run.TerminalNonMenuTabs == nil || *run.TerminalNonMenuTabs != want {
				t.Fatalf("non-menu tabs = %+v", run.TerminalNonMenuTabs)
			}
		})
	}
}

// "" and any value the script is not known to log are counted as unknown --
// and as non-menu, since they do not show the menu item was used -- and
// never become keys of their own.
func TestRun_RecordTabOpenMethod_UnknownValues(t *testing.T) {
	run := &Run{}
	t0 := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	values := []string{"", "bogus", "Menu", "menu\x1b[2J", " menu"}
	for i, v := range values {
		run.RecordTabOpenMethod(v, "T"+v, t0.Add(time.Duration(i)*time.Minute))
	}
	last := t0.Add(time.Duration(len(values)-1) * time.Minute)
	want := LaunchTally{Count: len(values), FirstAt: t0, LastAt: last, LastTicketID: "T" + values[len(values)-1]}
	if len(run.TerminalTabOpenMethods) != 1 || run.TerminalTabOpenMethods[TabOpenMethodUnknown] != want {
		t.Fatalf("open methods = %+v", run.TerminalTabOpenMethods)
	}
	if run.TerminalNonMenuTabs == nil || *run.TerminalNonMenuTabs != want {
		t.Fatalf("non-menu tabs = %+v", run.TerminalNonMenuTabs)
	}
}

// Every way of opening has its key, and there are no others.
func TestRun_RecordTabOpenMethod_KeysAreBounded(t *testing.T) {
	run := &Run{}
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	for _, v := range []string{"menu", "menu-failed", "keystroke-no-menu", "keystroke-retry", "", "x", "y", "menu", "keystroke-retry"} {
		run.RecordTabOpenMethod(v, "A", now)
	}
	want := map[string]int{"menu": 2, "menu-failed": 1, "keystroke-no-menu": 1, "keystroke-retry": 2, "unknown": 3}
	if len(run.TerminalTabOpenMethods) != len(want) {
		t.Fatalf("open methods = %+v", run.TerminalTabOpenMethods)
	}
	for k, n := range want {
		if run.TerminalTabOpenMethods[k].Count != n {
			t.Errorf("%s = %d, want %d", k, run.TerminalTabOpenMethods[k].Count, n)
		}
	}
	if run.TerminalNonMenuTabs == nil || run.TerminalNonMenuTabs.Count != 7 {
		t.Fatalf("non-menu tabs = %+v", run.TerminalNonMenuTabs)
	}
}

func TestRun_TabOpenMethodsJSON(t *testing.T) {
	data, err := json.Marshal(&Run{ID: "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["terminal_tab_open_methods"]; ok {
		t.Fatalf("an empty tally must be left out: %s", data)
	}
	if _, ok := raw["terminal_non_menu_tabs"]; ok {
		t.Fatalf("an empty tally must be left out: %s", data)
	}

	run := &Run{ID: "run-1"}
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	run.RecordTabOpenMethod("keystroke-no-menu", "A", now)
	if data, err = json.Marshal(run); err != nil {
		t.Fatal(err)
	}
	raw = nil
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	methods, ok := raw["terminal_tab_open_methods"].(map[string]any)
	if !ok || methods["keystroke-no-menu"] == nil {
		t.Fatalf("no terminal_tab_open_methods in %s", data)
	}
	if _, ok := raw["terminal_non_menu_tabs"]; !ok {
		t.Fatalf("no terminal_non_menu_tabs in %s", data)
	}
	var back Run
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.TerminalTabOpenMethods["keystroke-no-menu"] != run.TerminalTabOpenMethods["keystroke-no-menu"] || back.TerminalNonMenuTabs == nil || *back.TerminalNonMenuTabs != *run.TerminalNonMenuTabs {
		t.Fatalf("read back = %+v / %+v", back.TerminalTabOpenMethods, back.TerminalNonMenuTabs)
	}
}

// A new orchestrator starts both counts over, and the shared view carries
// neither; the history of tab failures is kept by both as before.
func TestRun_TabOpenMethodsAreLocalAndFollowTheOrchestrator(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	run := &Run{TerminalTTY: "/dev/ttys003"}
	run.RecordTabOpenMethod("keystroke-retry", "A", now)
	run.RecordTabFailure(TabFailureRecord{At: now, TicketID: "B", Diagnostics: &TabDiagnosticsRecord{OpenMethod: "menu"}})

	v, err := run.SharedView()
	if err != nil {
		t.Fatal(err)
	}
	if v.TerminalTabOpenMethods != nil || v.TerminalNonMenuTabs != nil {
		t.Fatal("the shared view must not carry the open methods")
	}
	if run.TerminalTabOpenMethods == nil || run.TerminalNonMenuTabs == nil {
		t.Fatal("the shared view must leave the run itself as it is")
	}

	run.resetTerminal("/dev/ttys004", "")
	if run.TerminalTabOpenMethods != nil || run.TerminalNonMenuTabs != nil {
		t.Fatal("a new orchestrator starts the counts over")
	}
	if len(run.TerminalTabFailures) != 1 || run.TerminalTabFailures[0].Diagnostics.OpenMethod != "menu" {
		t.Fatalf("tab failures = %+v", run.TerminalTabFailures)
	}
}
