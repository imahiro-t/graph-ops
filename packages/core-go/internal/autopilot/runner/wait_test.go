package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00142 phase 3: wait and the stall check (Gherkin: "wait は報告を待ち、
// 活動の兆候が stallTimeoutMinutes 途絶えたら無応答として失敗に確定する").

// launchSilent starts a ticket-mode run of a new ticket whose worker never
// reports, and returns the run and ticket.
func launchSilent(t *testing.T, h *harness) (runID, ticket string) {
	t.Helper()
	c := h.ticket("C", "")
	h.behave[c] = silentWorker
	run := h.start(c, autopilot.ModeTicket)
	if _, err := h.svc.Next(run.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Launch(run.RunID, c, "work"); err != nil {
		t.Fatal(err)
	}
	return run.RunID, c
}

func TestWait_ReturnsTheReportedSummary(t *testing.T) {
	h := newHarness(t)
	runID, c := launchSilent(t, h)
	long := strings.Repeat("line of text that goes on\n", 10)
	if _, err := h.svc.Report(runID, c, autopilot.TicketDone, "", long); err != nil {
		t.Fatal(err)
	}
	res, err := h.svc.Wait(runID, c, time.Second)
	if err != nil || !res.Reported() || res.Result != autopilot.TicketDone {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if lines := strings.Count(res.Summary, "\n") + 1; lines > 3 || len([]rune(res.Summary)) > 500 {
		t.Fatalf("summary too long: %q", res.Summary)
	}
}

func TestWait_TimesOutWithOneLineWhileActive(t *testing.T) {
	h := newHarness(t)
	runID, c := launchSilent(t, h)
	h.clock.Advance(5 * time.Minute)
	res, err := h.svc.Wait(runID, c, time.Millisecond)
	if err != nil || res.Reported() || res.State != "waiting" || res.IdleMinutes == nil || *res.IdleMinutes != 5 ||
		res.StallTimeoutMinutes == nil || *res.StallTimeoutMinutes != 60 {
		t.Fatalf("res = %+v, %v", res, err)
	}
	b, _ := jsonMarshal(res)
	for _, key := range []string{`"state":"waiting"`, `"idle_minutes":5`, `"stall_timeout_minutes":60`} {
		if !strings.Contains(string(b), key) {
			t.Fatalf("output %s lacks %s", b, key)
		}
	}
	if s := h.st(runID, c); s.Status != autopilot.TicketLaunched {
		t.Fatalf("status = %s", s.Status)
	}
}

func TestWait_PollsRefreshTheHeartbeat(t *testing.T) {
	h := newHarness(t)
	runID, c := launchSilent(t, h)
	for i := 0; i < 5; i++ {
		h.clock.Advance(4 * time.Minute)
		h.svc.Touch(runID, c, "") // the session itself stays alive
		if _, err := h.svc.Wait(runID, c, time.Millisecond); err != nil {
			t.Fatal(err)
		}
		if hb := h.run(runID).Heartbeat; !hb.Equal(h.clock.Now()) {
			t.Fatalf("poll %d: heartbeat %s, now %s", i, hb, h.clock.Now())
		}
	}
	// 20 minutes in -- past ActiveThreshold -- the run is still active.
	if !h.run(runID).IsActive(h.clock.Now()) {
		t.Fatal("run looks interrupted while wait is polling")
	}
}

func TestWait_ActivityKeepsASessionAlive(t *testing.T) {
	for _, tc := range []struct {
		name string
		act  func(h *harness, runID, c string)
	}{
		{"touch", func(h *harness, runID, c string) { h.svc.Touch(runID, c, "") }},
		{"node update", func(h *harness, runID, c string) {
			n, err := h.repo.CreateNode(domain.GraphNode{TicketID: c, Name: "plan", Type: domain.NodeTypePlan, Status: domain.NodeTODO, MaxIterations: 3})
			if err != nil {
				h.t.Fatal(err)
			}
			st := domain.NodeInProgress
			time.Sleep(5 * time.Millisecond)
			h.repo.UpdateNode(n.ID, store.NodePatch{Status: &st})
		}},
		{"artifact", func(h *harness, runID, c string) {
			n, _ := h.repo.CreateNode(domain.GraphNode{TicketID: c, Name: "plan", Type: domain.NodeTypePlan, Status: domain.NodeTODO, MaxIterations: 3})
			content := "x"
			h.repo.CreateArtifact(domain.Artifact{ID: "art-test1", TicketID: c, NodeID: n.ID, Name: "a", Type: domain.ArtifactText, Content: &content})
		}},
		{"commit", func(h *harness, runID, c string) {
			commitIn(h.t, h.st(runID, c).Worktree, "new.txt", "n", "new commit")
		}},
		{"uncommitted change", func(h *harness, runID, c string) {
			os.WriteFile(filepath.Join(h.st(runID, c).Worktree, "README.md"), []byte("changed\n"), 0o644)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			runID, c := launchSilent(t, h)
			// For the DB signals: the node must exist before the baseline for
			// "node update" to be an update; creating it is activity too, so
			// either way the observation below must see a change.
			h.clock.Advance(50 * time.Minute)
			tc.act(h, runID, c)
			// A poll observes the change (for touch, the call itself counts).
			if res, err := h.svc.Wait(runID, c, time.Millisecond); err != nil || res.Reported() {
				t.Fatalf("poll: %+v %v", res, err)
			}
			h.clock.Advance(50 * time.Minute)
			res, err := h.svc.Wait(runID, c, time.Millisecond)
			if err != nil || res.Reported() {
				t.Fatalf("failed after activity: %+v %v", res, err)
			}
			if res.IdleMinutes == nil || *res.IdleMinutes >= 60 {
				t.Fatalf("idle = %v", res.IdleMinutes)
			}
		})
	}
}

func TestWait_NoActivityFailsAsUnresponsive(t *testing.T) {
	h := newHarness(t)
	runID, c := launchSilent(t, h)
	h.clock.Advance(61 * time.Minute)
	res, err := h.svc.Wait(runID, c, time.Second)
	if err != nil || !res.Reported() || res.Result != autopilot.TicketFailed || res.Reason != autopilot.ReasonUnresponsive {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if !strings.Contains(res.Detail, "last activity") || !strings.Contains(res.Detail, autopilot.ActivityLaunch) {
		t.Fatalf("detail = %q", res.Detail)
	}
	sum, _ := h.svc.Summary(runID)
	if !strings.Contains(sum.Markdown, "If the terminal is still open") || !strings.Contains(sum.Markdown, h.st(runID, c).Worktree) {
		t.Fatalf("summary:\n%s", sum.Markdown)
	}
}

func TestNext_FailsAStalledSessionWithoutWait(t *testing.T) {
	h := newHarness(t)
	h.settings.OnFailure = autopilot.OnFailureStop
	runID, c := launchSilent(t, h)
	h.clock.Advance(59 * time.Minute)
	next, _ := h.svc.Next(runID)
	if next.Action.Action != autopilot.ActionWait || next.Ticket != c {
		t.Fatalf("before the threshold: %+v", next)
	}
	h.clock.Advance(2 * time.Minute)
	next, _ = h.svc.Next(runID)
	if next.Action.Action != autopilot.ActionStopped {
		t.Fatalf("after the threshold: %+v", next)
	}
	if s := h.st(runID, c); s.Status != autopilot.TicketFailed || s.Reason != autopilot.ReasonUnresponsive {
		t.Fatalf("C = %+v", s)
	}
}

func TestWait_AwaitingHumanIsNotUnresponsive(t *testing.T) {
	h := newHarness(t)
	h.settings.AutoApproveGates = false
	runID, c := launchSilent(t, h)
	if err := h.svc.Touch(runID, c, "計画承認の判断待ち"); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(10 * time.Hour)
	res, err := h.svc.Wait(runID, c, time.Millisecond)
	if err != nil || res.Reported() || res.State != "awaiting_human" || res.Awaiting != "計画承認の判断待ち" {
		t.Fatalf("res = %+v, %v", res, err)
	}
	// The next activity ends the wait for a person; the stall check applies
	// again.
	if err := h.svc.Touch(runID, c, ""); err != nil {
		t.Fatal(err)
	}
	if s := h.st(runID, c); s.AwaitingHuman != "" {
		t.Fatal("awaiting not cleared")
	}
	h.clock.Advance(61 * time.Minute)
	res, _ = h.svc.Wait(runID, c, time.Millisecond)
	if res.Reason != autopilot.ReasonUnresponsive {
		t.Fatalf("res = %+v", res)
	}
}

func TestReport_LateReportDoesNotRewind(t *testing.T) {
	h := newHarness(t)
	runID, c := launchSilent(t, h)
	h.clock.Advance(61 * time.Minute)
	h.svc.Wait(runID, c, time.Millisecond)
	res, err := h.svc.Report(runID, c, autopilot.TicketDone, "", "finished after all")
	if err != nil || res.Recorded || !res.LateReport {
		t.Fatalf("res = %+v, %v", res, err)
	}
	s := h.st(runID, c)
	if s.Status != autopilot.TicketFailed || s.LateReport != "finished after all" {
		t.Fatalf("C = %+v", s)
	}
	sum, _ := h.svc.Summary(runID)
	if !strings.Contains(sum.Markdown, "Late reports") || !strings.Contains(sum.Markdown, "finished after all") {
		t.Fatalf("summary:\n%s", sum.Markdown)
	}
}

func TestNext_ResumedRunWaitsForALaunchedTicket(t *testing.T) {
	h := newHarness(t)
	runID, c := launchSilent(t, h)
	h.makeStale()
	// Keep the session itself from stalling: some activity 1 minute ago.
	h.svc.Touch(runID, c, "")
	again := h.start(c, autopilot.ModeTicket)
	if again.RunID != runID {
		t.Fatalf("again = %+v", again)
	}
	next, _ := h.svc.Next(runID)
	if next.Action.Action != autopilot.ActionWait || next.Ticket != c {
		t.Fatalf("next = %+v", next)
	}
}

func TestReport_LastReportWins(t *testing.T) {
	h := newHarness(t)
	runID, c := launchSilent(t, h)
	h.svc.Report(runID, c, autopilot.TicketBlocked, "", "stuck")
	h.svc.Report(runID, c, autopilot.TicketDone, "", "fixed it")
	if s := h.st(runID, c); s.Status != autopilot.TicketDone || s.Summary != "fixed it" {
		t.Fatalf("C = %+v", s)
	}
}

func TestReport_Validation(t *testing.T) {
	h := newHarness(t)
	runID, c := launchSilent(t, h)
	if _, err := h.svc.Report(runID, c, "maybe", "", "x"); err == nil {
		t.Fatal("bad result accepted")
	}
	if _, err := h.svc.Report(runID, c, autopilot.TicketBlocked, "Not A Code", "x"); err == nil {
		t.Fatal("bad reason accepted")
	}
	other := h.ticket("X", "")
	if _, err := h.svc.Report(runID, other, autopilot.TicketDone, "", "x"); err == nil {
		t.Fatal("report for a ticket outside the run accepted")
	}
}
