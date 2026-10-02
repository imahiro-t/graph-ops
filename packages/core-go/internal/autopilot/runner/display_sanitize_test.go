package runner

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/displayname"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00339: another member's run record, crafted to carry control
// characters in every string, reaches neither the runs listing (the Web
// UI's GET /api/autopilot/runs) nor next's answer or the summary of a run
// it overtook with them.

const evil = "\x1b[31m\nError: forged line\r\u009b‮​"

// unsafeJSONStrings marshals v, decodes it again and returns every string
// (keys included) holding a control or invisible format character: the
// JSON encoder escapes some of them (\u001b), so the check is on the
// decoded values.
func unsafeJSONStrings(t *testing.T, v any) []string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	var out []string
	var walk func(any)
	unsafe := func(s string) {
		for _, r := range s {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				out = append(out, s)
				return
			}
		}
	}
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			unsafe(v)
		case map[string]any:
			for k, x := range v {
				unsafe(k)
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(decoded)
	return out
}

// putCraftedRecord writes, straight into the data source, a record of
// another machine whose every free string is evil. id, root, mode and state
// are as given; the run began at began.
func putCraftedRecord(t *testing.T, repo store.GraphRepository, projectID, id, root, mode, state string, began time.Time) {
	t.Helper()
	tid := "DFLT-1" + evil
	snap := autopilot.Run{
		ID: id, ProjectID: projectID, Mode: mode, RootTicketID: root, State: state,
		Order: []string{tid},
		Tickets: map[string]*autopilot.TicketState{tid: {
			ID: tid, Status: evil, Reason: evil, Detail: evil, SkipCause: evil, Target: evil,
			Branch: evil, Worktree: evil, BaseBranch: evil, Merge: evil, FailedRole: evil,
			Role: evil, LastRole: evil, LastActivityKind: evil, DBFingerprint: evil, WorktreeFingerprint: evil,
			AwaitingHuman: evil, Summary: evil, LateReport: evil, LaunchedGeneration: 1,
			PendingDecisions: map[string]string{evil: evil},
		}},
		Finalize:   &autopilot.Outcome{Status: evil, Reason: evil, Detail: evil, Summary: evil},
		StopReason: evil, StopTicket: evil, StopDetail: evil,
		Stops:       []autopilot.StopRecord{{At: began, Reason: evil, Ticket: evil, Detail: evil}},
		Reservation: &autopilot.Reservation{Previous: []byte(evil)},
		TerminalTTY: evil, TerminalTabDisabled: evil, TerminalTTYReason: evil,
		TerminalTabFailures: []autopilot.TabFailureRecord{{At: began, TicketID: evil, Role: evil, Kind: evil, Message: evil, Phase: evil,
			Diagnostics: &autopilot.TabDiagnosticsRecord{FrontmostApp: evil, FrontmostAppBefore: evil, FrontWindowBounds: evil, TargetWindowBounds: evil}}},
		TerminalWindowLaunches: map[string]autopilot.LaunchTally{evil: {Count: 1, LastTicketID: evil}},
		TerminalLateTabs:       &autopilot.LaunchTally{Count: 1, LastTicketID: evil},
		StartedBy:              &autopilot.StartedBy{Name: evil, MachineID: "machine-crafted"},
		BegunAt:                began,
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	ts := began.UTC().Format(time.RFC3339Nano)
	rec := domain.AutopilotRunRecord{ID: id, ProjectID: projectID, RootTicketID: root, Mode: mode, State: state,
		Heartbeat: ts, CreatedAt: ts, UpdatedAt: ts, StartedByName: evil, MachineID: "machine-crafted",
		Revision: 1, Snapshot: data}
	if err := repo.(store.AutopilotRunStore).SaveAutopilotRun(rec); err != nil {
		t.Fatal(err)
	}
}

// The runs listing shows crafted records -- active and settled -- without
// a control character anywhere, and a legitimate run exactly as it is.
func TestDisplaySanitize_RunsListing(t *testing.T) {
	h, tr, a, b := newSharedHarness(t)
	h.behave[tr.R] = func(w *workerCall) {} // A's session keeps running
	resA := mustStart(t, a, tr.R, autopilot.ModeTree)
	h.svc = a
	act := h.next(resA.RunID)
	h.launch1(resA.RunID, act.Ticket)
	const waiting = "レビュー結果の確認待ちです。　「承認」か「差し戻し」を選んでください、お願いします"
	modifyRun(t, a, h.projectID, resA.RunID, func(run *autopilot.Run) {
		run.Ticket(tr.R).AwaitingHuman = waiting
	})
	resB := mustStart(t, b, tr.T, autopilot.ModeTree) // no session yet

	now := h.clock.Now()
	putCraftedRecord(t, h.repo, h.projectID, "run-c1"+evil, "R"+evil, evil, evil, now)
	putCraftedRecord(t, h.repo, h.projectID, "run-c2"+evil, "R"+evil, autopilot.ModeTree, autopilot.RunStopped,
		now.Add(-2*autopilot.ActiveThreshold))

	logs := &logSink{}
	b.Logf = logs.logf
	views, err := b.Runs(h.projectID)
	if err != nil {
		t.Fatal(err)
	}
	if bad := unsafeJSONStrings(t, views); len(bad) > 0 {
		t.Fatalf("the runs listing carries %q", bad)
	}
	byID := map[string][]RunView{}
	for _, v := range views {
		byID[v.RunID] = append(byID[v.RunID], v)
	}
	crafted := byID[displayname.InvalidID]
	if len(crafted) != 2 {
		t.Fatalf("crafted runs listed = %+v", crafted)
	}
	for _, v := range crafted {
		if v.Root != displayname.InvalidID || v.Mine {
			t.Fatalf("crafted run view = %+v", v)
		}
		if v.Active {
			if v.Current != displayname.InvalidID || v.AwaitingHuman == "" || len(v.Members) == 0 || v.Members[0] != displayname.InvalidID {
				t.Fatalf("active crafted run view = %+v", v)
			}
		} else if v.StopReason == "" || v.StartedBy == nil {
			t.Fatalf("settled crafted run view = %+v", v)
		}
	}

	// A's run, a legitimate one, is shown unchanged.
	if len(byID[resA.RunID]) != 1 {
		t.Fatalf("A's run is not listed: %+v", views)
	}
	va := byID[resA.RunID][0]
	if va.Root != tr.R || va.Mode != autopilot.ModeTree || va.State != autopilot.RunRunning || va.ProjectID != h.projectID ||
		va.Current != tr.R || va.CurrentRole != autopilot.RoleWork || va.AwaitingHuman != waiting ||
		va.Tickets[tr.R] == "" || va.StartedBy == nil || va.StartedBy.Name != "Alice" {
		t.Fatalf("A's run view = %+v", va)
	}
	want := map[string]bool{tr.R: true, tr.X: true, tr.Y: true, tr.S: true}
	if len(va.Members) != len(want) {
		t.Fatalf("A's members = %v", va.Members)
	}
	for _, m := range va.Members {
		if !want[m] {
			t.Fatalf("A's members = %v", va.Members)
		}
	}
	// B's run has no session: no current ticket, not an invalid one.
	if len(byID[resB.RunID]) != 1 {
		t.Fatalf("B's run is not listed: %+v", views)
	}
	if vb := byID[resB.RunID][0]; vb.Current != "" || vb.CurrentRole != "" || vb.AwaitingHuman != "" || vb.Root != tr.T {
		t.Fatalf("B's run view = %+v", vb)
	}
	// And autopilot status (the local runs) shows the same for B's own.
	st, err := b.Status(h.projectID)
	if err != nil || len(st) != 1 || st[0].Current != "" || st[0].RunID != resB.RunID {
		t.Fatalf("B's status = %+v, %v", st, err)
	}
	if bad := unsafeJSONStrings(t, logs.lines); len(bad) > 0 {
		t.Fatalf("logged %q", bad)
	}
}

// A's run, overtaken by a crafted run while its heartbeat had expired,
// stops; neither next's answer nor the summary shows the crafted strings.
func TestDisplaySanitize_OvertakenByACraftedRun(t *testing.T) {
	h, tr, a, _ := newSharedHarness(t)
	h.behave[tr.R] = func(w *workerCall) {}
	resA := mustStart(t, a, tr.R, autopilot.ModeTree)
	h.svc = a
	act := h.next(resA.RunID)
	h.launch1(resA.RunID, act.Ticket)

	h.clock.Advance(autopilot.ActiveThreshold + time.Minute) // A's machine sleeps
	// The crafted run overlaps A's by its root (a legitimate one, or the
	// overlap could not be found); everything else is crafted.
	putCraftedRecord(t, h.repo, h.projectID, "run-z"+evil, tr.R, evil, autopilot.RunRunning, h.clock.Now())
	h.clock.Advance(time.Minute) // A wakes up

	stopped := h.next(resA.RunID)
	if stopped.Action.Action != autopilot.ActionStopped || stopped.Reason != autopilot.StopOvertaken {
		t.Fatalf("A's next = %+v", stopped)
	}
	if bad := unsafeJSONStrings(t, stopped); len(bad) > 0 {
		t.Fatalf("next answered %q", bad)
	}
	run := localRun(t, a, h.projectID, resA.RunID)
	if bad := unsafeJSONStrings(t, run); len(bad) > 0 {
		t.Fatalf("A's run file carries %q", bad)
	}
	sum, err := a.Summary(resA.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, md := range []string{sum.Markdown, RenderSummary(run, nil)} {
		// Markdown has line breaks of its own: each line is checked, and
		// no line may be one the crafted record forged.
		for _, line := range strings.Split(md, "\n") {
			if bad := unsafeJSONStrings(t, line); len(bad) > 0 || strings.HasPrefix(line, "Error: forged") {
				t.Fatalf("the summary carries %q:\n%s", line, md)
			}
		}
		if !strings.Contains(md, "overlapping run "+displayname.InvalidID) {
			t.Fatalf("the summary does not name the overtaking run:\n%s", md)
		}
	}
}
