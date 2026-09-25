package runner

import (
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00142 phase 3: `next` drives the tree serially (Gherkin: "next は次の
// 行動を 1 つだけ返し、ツリーを DFS で直列に処理する").

func TestFlow_TicketModeProcessesOnlyTheRoot(t *testing.T) {
	h := newHarness(t)
	r := h.ticket("R", "")
	h.ticket("C", r)
	run := h.start(r, autopilot.ModeTicket)
	res := h.drive(run.RunID, nil)
	if res.Action.Action != autopilot.ActionDone {
		t.Fatalf("last action = %+v", res)
	}
	if got := h.workLaunches(); len(got) != 1 || got[0] != r {
		t.Fatalf("work launches = %v", got)
	}
	if h.run(run.RunID).State != autopilot.RunFinished {
		t.Fatal("run not finished")
	}
}

func TestFlow_TreeIsDepthFirstPreOrderAndSerial(t *testing.T) {
	h := newHarness(t)
	r := h.ticket("R", "")
	a := h.ticket("A", r)
	a1 := h.ticket("A1", a)
	b := h.ticket("B", r)
	// At most one session is ever running: the launcher checks it.
	for _, id := range []string{r, a, a1, b} {
		h.behave[id] = func(w *workerCall) {
			if active := h.svcRunActive(w.RunID); active != 1 {
				t.Errorf("%d sessions running while %s works", active, w.Ticket)
			}
			defaultWorker(w)
		}
	}
	run := h.start(r, autopilot.ModeTree)
	h.drive(run.RunID, nil)
	want := []string{r, a, a1, b}
	if got := h.workLaunches(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("launch order = %v, want %v", got, want)
	}
	// Post-order merge-up: A1 into A, then A into R -- before B starts.
	mA1, mA, lB := indexOf(h.actions, "merge-up "+a1), indexOf(h.actions, "merge-up "+a), indexOf(h.actions, "launch "+b+" work")
	if mA1 < 0 || mA < 0 || lB < 0 || !(mA1 < mA && mA < lB) {
		t.Fatalf("actions = %v", h.actions)
	}
}

func (h *harness) svcRunActive(runID string) int {
	n := 0
	for _, st := range h.run(runID).Tickets {
		if st.Role != "" {
			n++
		}
	}
	return n
}

func TestFlow_DiscoversChildrenCreatedWhileProcessing(t *testing.T) {
	h := newHarness(t)
	r := h.ticket("R", "")
	var n string
	h.behave[r] = func(w *workerCall) {
		w.commit()
		n = w.child("N")
		w.report(autopilot.TicketDone, "created N")
	}
	run := h.start(r, autopilot.ModeTree)
	res, err := h.svc.Next(run.RunID)
	if err != nil || res.Ticket != r {
		t.Fatalf("next = %+v %v", res, err)
	}
	if _, err := h.svc.Launch(run.RunID, r, "work"); err != nil {
		t.Fatal(err)
	}
	res, err = h.svc.Next(run.RunID)
	if err != nil || res.Action.Action != autopilot.ActionLaunch || res.Ticket != n {
		t.Fatalf("next = %+v %v", res, err)
	}
}

func TestFlow_SkipsFinishedAndElsewhereTickets(t *testing.T) {
	for _, tc := range []struct {
		status domain.TicketStatus
		reason string
	}{
		{domain.TicketDone, autopilot.ReasonAlreadyDone},
		{domain.TicketClosed, autopilot.ReasonClosed},
		{domain.TicketInProgress, autopilot.ReasonInProgressElsewhere},
	} {
		t.Run(string(tc.status), func(t *testing.T) {
			h := newHarness(t)
			r := h.ticket("R", "")
			c := h.ticket("C", r)
			h.setStatus(c, tc.status)
			run := h.start(r, autopilot.ModeTree)
			h.drive(run.RunID, nil)
			if contains(h.workLaunches(), c) {
				t.Fatal("C was launched")
			}
			st := h.st(run.RunID, c)
			if st.Status != autopilot.TicketSkipped || st.Reason != tc.reason {
				t.Fatalf("C = %s/%s", st.Status, st.Reason)
			}
			sum, err := h.svc.Summary(run.RunID)
			if err != nil || !strings.Contains(sum.Markdown, c) || !strings.Contains(sum.Markdown, tc.reason) {
				t.Fatalf("summary: %v\n%s", err, sum.Markdown)
			}
		})
	}
}

func TestFlow_UnfinishedDescendantsOfFinishedTicketsAreProcessedIntoTheRoot(t *testing.T) {
	for _, status := range []domain.TicketStatus{domain.TicketDone, domain.TicketClosed} {
		t.Run(string(status), func(t *testing.T) {
			h := newHarness(t)
			r := h.ticket("R", "")
			c := h.ticket("C", r)
			g := h.ticket("G", c)
			h.setStatus(c, status)
			// A branch worktree-C left over from earlier work.
			gitT(t, h.gitRepo, "branch", "worktree-"+c, "main")
			leftover := h.tip("worktree-" + c)
			var gCommit string
			var ctx WorkerContext
			h.behave[g] = func(w *workerCall) {
				ctx = w.ctx()
				if base := gitT(t, w.WorkDir, "rev-parse", "HEAD"); base != h.tip("worktree-"+r) {
					t.Errorf("G was not cut from worktree-R's tip")
				}
				gCommit = w.commit()
				w.release()
				w.report(autopilot.TicketDone, "g")
			}
			run := h.start(r, autopilot.ModeTree)
			h.drive(run.RunID, nil)
			if st := h.st(run.RunID, c); st.Status != autopilot.TicketSkipped || contains(h.workLaunches(), c) {
				t.Fatalf("C = %+v", st)
			}
			if ctx.TargetBranch != "worktree-"+r || ctx.BaseBranch != "worktree-"+r {
				t.Fatalf("G's context = %+v", ctx)
			}
			if !h.branchContains("worktree-"+r, gCommit) {
				t.Fatal("worktree-R lacks G's commit")
			}
			if h.tip("worktree-"+c) != leftover {
				t.Fatal("worktree-C was changed")
			}
		})
	}
}

func TestFlow_DescendantsOfElsewhereInProgressAreSkipped(t *testing.T) {
	h := newHarness(t)
	r := h.ticket("R", "")
	c := h.ticket("C", r)
	g := h.ticket("G", c)
	h.setStatus(c, domain.TicketInProgress)
	run := h.start(r, autopilot.ModeTree)
	h.drive(run.RunID, nil)
	if s := h.st(run.RunID, c); s.Reason != autopilot.ReasonInProgressElsewhere {
		t.Fatalf("C = %+v", s)
	}
	if s := h.st(run.RunID, g); s.Status != autopilot.TicketSkipped || s.Reason != autopilot.ReasonAncestorInProgress {
		t.Fatalf("G = %+v", s)
	}
	if contains(h.workLaunches(), c) || contains(h.workLaunches(), g) {
		t.Fatal("launched")
	}
}

func TestFlow_MaxDepthCountsRealDepthPastSkippedAncestors(t *testing.T) {
	h := newHarness(t)
	h.settings.MaxDepth = 1
	r := h.ticket("R", "")
	c := h.ticket("C", r)
	g := h.ticket("G", c)
	h.setStatus(c, domain.TicketDone)
	run := h.start(r, autopilot.ModeTree)
	h.drive(run.RunID, nil)
	if s := h.st(run.RunID, g); s.Reason != autopilot.ReasonLimitDepth {
		t.Fatalf("G = %+v", s)
	}
}

func TestFlow_MaxDepth(t *testing.T) {
	h := newHarness(t)
	h.settings.MaxDepth = 1
	r := h.ticket("R", "")
	c := h.ticket("C", r)
	g := h.ticket("G", c)
	run := h.start(r, autopilot.ModeTree)
	h.drive(run.RunID, nil)
	if got := h.workLaunches(); strings.Join(got, ",") != r+","+c {
		t.Fatalf("launches = %v", got)
	}
	if s := h.st(run.RunID, g); s.Reason != autopilot.ReasonLimitDepth {
		t.Fatalf("G = %+v", s)
	}
}

func TestFlow_LeftoverInProgressOfAnEarlierRunIsNotElsewhere(t *testing.T) {
	h := newHarness(t)
	h.settings.OnFailure = autopilot.OnFailureContinue
	r := h.ticket("R", "")
	a := h.ticket("A", r)
	b := h.ticket("B", r)
	h.setStatus(b, domain.TicketDone)
	// run-1: A alone, failed; A stays IN PROGRESS in the DB.
	h.behave[a] = func(w *workerCall) { w.report(autopilot.TicketFailed, "broke") }
	run1 := h.start(a, autopilot.ModeTicket)
	h.drive(run1.RunID, nil)
	if h.run(run1.RunID).State != autopilot.RunFinished {
		t.Fatal("run-1 not finished")
	}
	h.setStatus(a, domain.TicketInProgress)
	delete(h.behave, a)

	run2 := h.start(r, autopilot.ModeTree)
	h.drive(run2.RunID, nil)
	if !contains(h.workLaunches()[1:], a) {
		t.Fatalf("A not launched by run-2: %v", h.workLaunches())
	}
	if s := h.st(run2.RunID, b); s.Reason != autopilot.ReasonAlreadyDone {
		t.Fatalf("B = %+v", s)
	}
}

func TestFlow_MaxTicketsSkipsTheRestButStillMergesAndFinalizes(t *testing.T) {
	h := newHarness(t)
	h.settings.MaxTickets = 3
	h.settings.MainReflection = autopilot.MainReflectionPullRequest
	r := h.ticket("R", "")
	var kids []string
	for _, n := range []string{"C1", "C2", "C3", "C4"} {
		kids = append(kids, h.ticket(n, r))
	}
	run := h.start(r, autopilot.ModeTree)
	h.drive(run.RunID, nil)
	if got := h.workLaunches(); len(got) != 3 {
		t.Fatalf("launches = %v", got)
	}
	for _, k := range kids[2:] {
		if s := h.st(run.RunID, k); s.Reason != autopilot.ReasonLimitTickets {
			t.Fatalf("%s = %+v", k, s)
		}
	}
	if !contains(h.actions, "merge-up "+kids[0]) || !contains(h.actions, "merge-up "+kids[1]) {
		t.Fatalf("merge-ups missing: %v", h.actions)
	}
	if got := h.roleLaunches(autopilot.RoleFinalize); len(got) != 1 {
		t.Fatalf("finalize launches = %v", got)
	}
	sum, _ := h.svc.Summary(run.RunID)
	if !strings.Contains(sum.Markdown, autopilot.ReasonLimitTickets) {
		t.Fatalf("summary:\n%s", sum.Markdown)
	}
}

func TestFlow_MaxTicketsDoesNotCountMergeUpAndFinalizeSessions(t *testing.T) {
	h := newHarness(t)
	h.settings.MaxTickets = 2
	h.settings.MainReflection = autopilot.MainReflectionPullRequest
	r := h.ticket("R", "")
	c := h.ticket("C", r)
	d := h.ticket("D", r)
	h.behave[c] = func(w *workerCall) {
		w.commit()
		w.release()
		// Somebody commits straight onto R's branch: C cannot be merged
		// up by fast-forward any more.
		rt := h.st(w.RunID, r)
		commitIn(t, rt.Worktree, "human.txt", "x", "human commit on R")
		// ...while C's branch moves on too (as a grandchild's merge would).
		commitIn(t, w.WorkDir, "more.txt", "y", "more on C")
		w.report(autopilot.TicketDone, "c")
	}
	run := h.start(r, autopilot.ModeTree)
	h.drive(run.RunID, nil)
	if got := h.workLaunches(); strings.Join(got, ",") != r+","+c {
		t.Fatalf("work launches = %v", got)
	}
	if s := h.st(run.RunID, d); s.Reason != autopilot.ReasonLimitTickets {
		t.Fatalf("D = %+v", s)
	}
	if got := h.roleLaunches(autopilot.RoleMergeUp); len(got) != 1 || got[0] != c {
		t.Fatalf("merge-up launches = %v; actions = %v", got, h.actions)
	}
	if got := h.roleLaunches(autopilot.RoleFinalize); len(got) != 1 {
		t.Fatalf("finalize launches = %v", got)
	}
	if s := h.st(run.RunID, c); s.Merge != autopilot.MergedSubtree {
		t.Fatalf("C merge = %s", s.Merge)
	}
}

func TestFlow_OnFailureStopStopsWithoutMergeUpOrFinalize(t *testing.T) {
	h := newHarness(t)
	h.settings.MainReflection = autopilot.MainReflectionPullRequest
	r := h.ticket("R", "")
	a := h.ticket("A", r)
	b := h.ticket("B", r)
	h.behave[a] = func(w *workerCall) { w.commit(); w.report(autopilot.TicketFailed, "tests keep failing") }
	run := h.start(r, autopilot.ModeTree)
	res := h.drive(run.RunID, nil)
	if res.Action.Action != autopilot.ActionStopped || res.Reason != autopilot.StopTicketFailed || res.Ticket != a {
		t.Fatalf("res = %+v", res)
	}
	if h.run(run.RunID).State != autopilot.RunStopped {
		t.Fatal("run not stopped")
	}
	if contains(h.workLaunches(), b) || contains(h.actions, "merge-up "+a) || len(h.roleLaunches(autopilot.RoleFinalize)) != 0 {
		t.Fatalf("actions = %v", h.actions)
	}
	sum, _ := h.svc.Summary(run.RunID)
	if !strings.Contains(sum.Markdown, "tests keep failing") || !strings.Contains(sum.Markdown, autopilot.StopTicketFailed) {
		t.Fatalf("summary:\n%s", sum.Markdown)
	}
	// next keeps answering stopped.
	again, _ := h.svc.Next(run.RunID)
	if again.Action.Action != autopilot.ActionStopped {
		t.Fatalf("again = %+v", again)
	}
}

func TestFlow_OnFailureContinueSkipsOnlyTheFailedSubtree(t *testing.T) {
	h := newHarness(t)
	h.settings.OnFailure = autopilot.OnFailureContinue
	r := h.ticket("R", "")
	a := h.ticket("A", r)
	b := h.ticket("B", r)
	var a1 string
	h.behave[a] = func(w *workerCall) {
		w.commit()
		a1 = w.child("A1")
		w.report(autopilot.TicketBlocked, "needs a decision")
	}
	run := h.start(r, autopilot.ModeTree)
	h.drive(run.RunID, nil)
	if s := h.st(run.RunID, a1); s.Status != autopilot.TicketSkipped || s.Reason != autopilot.ReasonParentFailed {
		t.Fatalf("A1 = %+v", s)
	}
	if !contains(h.workLaunches(), b) {
		t.Fatal("B not processed")
	}
	if s := h.st(run.RunID, a); s.Merge != autopilot.NotMerged {
		t.Fatalf("A merge = %q", s.Merge)
	}
	sum, _ := h.svc.Summary(run.RunID)
	if !strings.Contains(sum.Markdown, "Work not in the root branch") || !strings.Contains(sum.Markdown, "worktree-"+a) {
		t.Fatalf("summary:\n%s", sum.Markdown)
	}
}

func TestFlow_FailedRootNeverFinalizes(t *testing.T) {
	for _, tc := range []struct{ onFailure, result, state string }{
		{autopilot.OnFailureContinue, autopilot.TicketFailed, autopilot.RunFinished},
		{autopilot.OnFailureContinue, autopilot.TicketBlocked, autopilot.RunFinished},
		{autopilot.OnFailureStop, autopilot.TicketFailed, autopilot.RunStopped},
	} {
		t.Run(tc.onFailure+"/"+tc.result, func(t *testing.T) {
			h := newHarness(t)
			h.settings.OnFailure = tc.onFailure
			h.settings.MainReflection = autopilot.MainReflectionMerge
			r := h.ticket("R", "")
			mainBefore := h.tip("main")
			h.behave[r] = func(w *workerCall) { w.commit(); w.report(tc.result, "root broke") }
			run := h.start(r, autopilot.ModeTree)
			h.drive(run.RunID, nil)
			if len(h.roleLaunches(autopilot.RoleFinalize)) != 0 || h.tip("main") != mainBefore {
				t.Fatal("finalized or main changed")
			}
			if st := h.run(run.RunID).State; st != tc.state {
				t.Fatalf("state = %s", st)
			}
			sum, _ := h.svc.Summary(run.RunID)
			if !strings.Contains(sum.Markdown, "NOT reflected") || !strings.Contains(sum.Markdown, "worktree-"+r) || !strings.Contains(sum.Markdown, "root broke") {
				t.Fatalf("summary:\n%s", sum.Markdown)
			}
		})
	}
}

func TestFlow_FinalizeFailureStopsAndRetriesOnlyFinalize(t *testing.T) {
	for _, tc := range []struct{ name, onFailure string }{
		{"failed", autopilot.OnFailureStop},
		{"blocked", autopilot.OnFailureContinue},
		{"unresponsive", autopilot.OnFailureContinue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.settings.OnFailure = tc.onFailure
			h.settings.MainReflection = autopilot.MainReflectionPullRequest
			r := h.ticket("R", "")
			c := h.ticket("C", r)
			switch tc.name {
			case "failed":
				h.behave[r+"/finalize"] = func(w *workerCall) { w.report(autopilot.TicketFailed, "gh failed") }
			case "blocked":
				h.behave[r+"/finalize"] = func(w *workerCall) { w.report(autopilot.TicketBlocked, "protected branch") }
			case "unresponsive":
				h.behave[r+"/finalize"] = silentWorker
			}
			run := h.start(r, autopilot.ModeTree)
			res := h.drive(run.RunID, func(ticket, role string) {
				h.clock.Advance(61 * 60e9)
			})
			if res.Action.Action != autopilot.ActionStopped || res.Reason != autopilot.StopFinalizeFailed {
				t.Fatalf("res = %+v", res)
			}
			sum, _ := h.svc.Summary(run.RunID)
			if !strings.Contains(sum.Markdown, "NOT reflected") || !strings.Contains(sum.Markdown, "worktree-"+r) {
				t.Fatalf("summary:\n%s", sum.Markdown)
			}
			workBefore := len(h.workLaunches())
			delete(h.behave, r+"/finalize")
			again := h.start(r, autopilot.ModeTree)
			if again.RunID != run.RunID || !again.Resumed {
				t.Fatalf("again = %+v", again)
			}
			next, _ := h.svc.Next(run.RunID)
			if next.Action.Action != autopilot.ActionLaunch || next.Role != autopilot.RoleFinalize || next.Ticket != r ||
				next.Worktree != h.st(run.RunID, r).Worktree {
				t.Fatalf("next = %+v", next)
			}
			h.drive(run.RunID, nil)
			if len(h.workLaunches()) != workBefore || h.run(run.RunID).State != autopilot.RunFinished {
				t.Fatalf("work relaunched or not finished: %v", h.workLaunches())
			}
			_ = c
		})
	}
}

func TestFlow_FinalizeFollowsMainReflection(t *testing.T) {
	for _, tc := range []struct {
		setting  string
		finalize bool
	}{
		{autopilot.MainReflectionBranch, false},
		{autopilot.MainReflectionPullRequest, true},
		{autopilot.MainReflectionMerge, true},
	} {
		t.Run(tc.setting, func(t *testing.T) {
			h := newHarness(t)
			h.settings.MainReflection = tc.setting
			r := h.ticket("R", "")
			run := h.start(r, autopilot.ModeTree)
			h.drive(run.RunID, nil)
			if got := len(h.roleLaunches(autopilot.RoleFinalize)); (got == 1) != tc.finalize {
				t.Fatalf("finalize launches = %d", got)
			}
			if contains(h.actions, "launch "+r+" finalize") != tc.finalize {
				t.Fatalf("actions = %v", h.actions)
			}
		})
	}
}

func TestFlow_ResumeAfterInterruptionRecomputesFromTheDB(t *testing.T) {
	h := newHarness(t)
	r := h.ticket("R", "")
	a := h.ticket("A", r)
	b := h.ticket("B", r)
	run := h.start(r, autopilot.ModeTree)
	// Drive until B is about to be launched, then "interrupt".
	for {
		res, err := h.svc.Next(run.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if res.Action.Action == autopilot.ActionLaunch && res.Ticket == b {
			break
		}
		switch res.Action.Action {
		case autopilot.ActionLaunch:
			h.svc.Launch(run.RunID, res.Ticket, res.Role)
		case autopilot.ActionMergeUp:
			h.svc.MergeUp(run.RunID, res.Ticket)
		default:
			t.Fatalf("unexpected %+v", res)
		}
	}
	h.makeStale()
	again := h.start(r, autopilot.ModeTree)
	if again.RunID != run.RunID || !again.Resumed {
		t.Fatalf("again = %+v", again)
	}
	next, _ := h.svc.Next(run.RunID)
	if next.Action.Action != autopilot.ActionLaunch || next.Ticket != b {
		t.Fatalf("next = %+v", next)
	}
	h.drive(run.RunID, nil)
	if got := h.workLaunches(); strings.Join(got, ",") != strings.Join([]string{r, a, b}, ",") {
		t.Fatalf("launches = %v", got)
	}
}

func TestFlow_FailedTicketsAreRetriedOncePerStart(t *testing.T) {
	h := newHarness(t)
	h.settings.OnFailure = autopilot.OnFailureContinue
	r := h.ticket("R", "")
	a := h.ticket("A", r)
	b := h.ticket("B", r)
	h.behave[a] = func(w *workerCall) { w.report(autopilot.TicketFailed, "flaky") }
	run := h.start(r, autopilot.ModeTree)
	isLaunchB := func(res NextResult) bool { return res.Action.Action == autopilot.ActionLaunch && res.Ticket == b }
	h.stepUntil(run.RunID, isLaunchB)
	// Interrupted before B; A is left IN PROGRESS in the DB.
	h.setStatus(a, domain.TicketInProgress)
	launches := h.run(run.RunID).WorkLaunches

	countA := func() int {
		n := 0
		for _, id := range h.workLaunches() {
			if id == a {
				n++
			}
		}
		return n
	}
	for round := 2; round <= 3; round++ {
		h.makeStale()
		again := h.start(r, autopilot.ModeTree)
		if again.RunID != run.RunID || !again.Resumed {
			t.Fatalf("round %d: %+v", round, again)
		}
		next, _ := h.svc.Next(run.RunID)
		if next.Action.Action != autopilot.ActionLaunch || next.Ticket != a {
			t.Fatalf("round %d: next = %+v", round, next)
		}
		// A fails again: within this start it is not launched again, and
		// next moves on to B.
		h.stepUntil(run.RunID, isLaunchB)
		if got := countA(); got != round {
			t.Fatalf("round %d: A launched %d times", round, got)
		}
		if n := h.run(run.RunID).WorkLaunches; n != launches {
			t.Fatalf("round %d: the re-launch was counted (%d -> %d)", round, launches, n)
		}
	}
}

func TestFlow_StoppedRunResumesFromTheFailedTicket(t *testing.T) {
	h := newHarness(t)
	h.settings.MainReflection = autopilot.MainReflectionPullRequest
	r := h.ticket("R", "")
	a := h.ticket("A", r)
	b := h.ticket("B", r)
	h.behave[a] = func(w *workerCall) { w.report(autopilot.TicketFailed, "broken") }
	run := h.start(r, autopilot.ModeTree)
	if res := h.drive(run.RunID, nil); res.Action.Action != autopilot.ActionStopped {
		t.Fatalf("res = %+v", res)
	}
	delete(h.behave, a)
	h.setStatus(r, domain.TicketDone)
	again := h.start(r, autopilot.ModeTree)
	if again.RunID != run.RunID {
		t.Fatalf("again = %+v", again)
	}
	next, _ := h.svc.Next(run.RunID)
	if next.Action.Action != autopilot.ActionLaunch || next.Ticket != a {
		t.Fatalf("next = %+v", next)
	}
	h.drive(run.RunID, nil)
	if got := h.workLaunches(); strings.Join(got, ",") != strings.Join([]string{r, a, a, b}, ",") {
		t.Fatalf("launches = %v", got)
	}
	if !contains(h.actions, "merge-up "+a) || len(h.roleLaunches(autopilot.RoleFinalize)) != 1 {
		t.Fatalf("actions = %v", h.actions)
	}
	if h.run(run.RunID).State != autopilot.RunFinished {
		t.Fatal("not finished")
	}
}

// stepUntil carries out next's actions until one satisfies stop, which is
// returned without being carried out.
func (h *harness) stepUntil(runID string, stop func(NextResult) bool) NextResult {
	h.t.Helper()
	for i := 0; i < 200; i++ {
		res, err := h.svc.Next(runID)
		if err != nil {
			h.t.Fatal(err)
		}
		h.actions = append(h.actions, strings.TrimSpace(res.Action.Action+" "+res.Ticket+" "+res.Role))
		if stop(res) {
			return res
		}
		switch res.Action.Action {
		case autopilot.ActionLaunch:
			if _, err := h.svc.Launch(runID, res.Ticket, res.Role); err != nil {
				h.t.Fatal(err)
			}
		case autopilot.ActionMergeUp:
			if _, err := h.svc.MergeUp(runID, res.Ticket); err != nil {
				h.t.Fatal(err)
			}
		default:
			h.t.Fatalf("stepUntil: unexpected %+v", res)
		}
	}
	h.t.Fatal("stepUntil: no stop")
	return NextResult{}
}
