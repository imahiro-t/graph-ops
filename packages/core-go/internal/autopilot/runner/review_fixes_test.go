package runner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/autopilot"
)

// DFLT-00142 iteration 1: the QA review's unclassified launch and merge-up
// failures, takeover under raised limits, and the summary's account of a
// failed intermediate merge-up.

// driveTolerant is the skill's loop including its error handling: a launch
// or merge-up that exits non-zero sends the orchestrator back to next. It
// fails the test when the run does not end within a few steps -- the loop
// the QA review found.
func (h *harness) driveTolerant(runID string) (NextResult, int) {
	h.t.Helper()
	failures := 0
	for i := 0; i < 60; i++ {
		res, err := h.svc.Next(runID)
		if err != nil {
			h.t.Fatalf("Next: %v", err)
		}
		h.actions = append(h.actions, strings.TrimSpace(res.Action.Action+" "+res.Ticket+" "+res.Role))
		switch res.Action.Action {
		case autopilot.ActionLaunch:
			if _, err := h.svc.Launch(runID, res.Ticket, res.Role); err != nil {
				failures++
			}
		case autopilot.ActionMergeUp:
			if _, err := h.svc.MergeUp(runID, res.Ticket); err != nil {
				failures++
			}
		case autopilot.ActionWait:
			h.t.Fatalf("unexpected wait for %s (%s)", res.Ticket, res.Role)
		case autopilot.ActionDone, autopilot.ActionStopped:
			return res, failures
		}
	}
	h.t.Fatalf("the run did not end; actions: %v", h.actions)
	return NextResult{}, failures
}

// failLaunchesOf makes every launch of ticket fail as a terminal that does
// not open.
func (h *harness) failLaunchesOf(ticket string) {
	h.svc.Launcher = LauncherFunc(func(workDir string, args []string, prompt string) error {
		if strings.Contains(prompt, " "+ticket+" ") {
			return errors.New("terminal command exited 127")
		}
		return h.launch(workDir, args, prompt)
	})
}

func TestLaunch_RepeatedFailureIsRecordedAndStopsTheRun(t *testing.T) {
	h := newHarness(t)
	h.settings.OnFailure = autopilot.OnFailureStop
	c := h.ticket("C", "")
	h.failLaunchesOf(c)
	run := h.start(c, autopilot.ModeTicket)
	res, failures := h.driveTolerant(run.RunID)
	if failures != MaxLaunchAttempts {
		t.Fatalf("launch failures = %d", failures)
	}
	s := h.st(run.RunID, c)
	if res.Action.Action != autopilot.ActionStopped || s.Status != autopilot.TicketFailed || s.Reason != autopilot.ReasonLaunchFailed ||
		!strings.Contains(s.Detail, "exited 127") || s.LaunchFailures != 0 || s.RetryPending {
		t.Fatalf("res = %+v, C = %+v", res, s)
	}
	sum, _ := h.svc.Summary(run.RunID)
	if !strings.Contains(sum.Markdown, autopilot.ReasonLaunchFailed) || !strings.Contains(sum.Markdown, autopilot.StopTicketFailed) {
		t.Fatalf("summary:\n%s", sum.Markdown)
	}
	// Running the same command again retries it once more (S6).
	h.svc.Launcher = LauncherFunc(h.launch)
	h.makeStale()
	h.start(c, autopilot.ModeTicket)
	if res := h.drive(run.RunID, nil); res.Action.Action != autopilot.ActionDone || h.st(run.RunID, c).Status != autopilot.TicketDone {
		t.Fatalf("res = %+v", res)
	}
}

// DFLT-00142 iteration 2 (QA carry-over A): a project whose local path is
// no longer set is a launch failure like any other -- counted, and recorded
// as launch_failed on the second one in a row, so the run stops with the
// reason in the summary instead of staying running.
func TestLaunch_MissingLocalPathIsCountedAndRecordedAsLaunchFailed(t *testing.T) {
	h := newHarness(t)
	h.settings.OnFailure = autopilot.OnFailureStop
	c := h.ticket("C", "")
	run := h.start(c, autopilot.ModeTicket)
	h.gitRepo = ""
	res, failures := h.driveTolerant(run.RunID)
	if failures != MaxLaunchAttempts || len(h.launches) != 0 {
		t.Fatalf("launch failures = %d, launches = %d", failures, len(h.launches))
	}
	s := h.st(run.RunID, c)
	if res.Action.Action != autopilot.ActionStopped || s.Status != autopilot.TicketFailed || s.Reason != autopilot.ReasonLaunchFailed ||
		!strings.Contains(s.Detail, string(autopilot.ErrCodeLocalPathNotSet)) || s.LaunchFailures != 0 {
		t.Fatalf("res = %+v, C = %+v", res, s)
	}
	sum, _ := h.svc.Summary(run.RunID)
	if !strings.Contains(sum.Markdown, autopilot.ReasonLaunchFailed) || !strings.Contains(sum.Markdown, autopilot.StopTicketFailed) {
		t.Fatalf("summary:\n%s", sum.Markdown)
	}
}

func TestLaunch_RepeatedFailureContinuesWithTheRestOfTheTree(t *testing.T) {
	h := newHarness(t)
	h.settings.OnFailure = autopilot.OnFailureContinue
	r := h.ticket("R", "")
	c := h.ticket("C", r)
	g := h.ticket("G", c)
	d := h.ticket("D", r)
	h.failLaunchesOf(c)
	run := h.start(r, autopilot.ModeTree)
	res, _ := h.driveTolerant(run.RunID)
	if res.Action.Action != autopilot.ActionDone {
		t.Fatalf("res = %+v", res)
	}
	if s := h.st(run.RunID, c); s.Reason != autopilot.ReasonLaunchFailed {
		t.Fatalf("C = %+v", s)
	}
	if s := h.st(run.RunID, g); s.Reason != autopilot.ReasonParentFailed {
		t.Fatalf("G = %+v", s)
	}
	if s := h.st(run.RunID, d); s.Status != autopilot.TicketDone {
		t.Fatalf("D = %+v", s)
	}
}

func TestLaunch_FinalizeThatCannotLaunchStopsAsFinalizeFailed(t *testing.T) {
	h := newHarness(t)
	h.settings.MainReflection = autopilot.MainReflectionPullRequest
	r := h.ticket("R", "")
	run := h.start(r, autopilot.ModeTree)
	h.svc.Launcher = LauncherFunc(func(workDir string, args []string, prompt string) error {
		if strings.HasSuffix(prompt, "--role finalize") {
			return errors.New("no terminal")
		}
		return h.launch(workDir, args, prompt)
	})
	res, _ := h.driveTolerant(run.RunID)
	fin := h.run(run.RunID).Finalize
	if res.Action.Action != autopilot.ActionStopped || res.Reason != autopilot.StopFinalizeFailed || fin == nil || fin.Reason != autopilot.ReasonLaunchFailed {
		t.Fatalf("res = %+v, finalize = %+v", res, fin)
	}
}

func TestMergeUp_UnexpectedGitErrorHandsOverToASession(t *testing.T) {
	h := newHarness(t)
	p := h.ticket("P", "")
	c := h.ticket("C", p)
	h.behave[c] = func(w *workerCall) {
		w.commit()
		w.release()
		// C's branch moves on with a file that P's worktree has, untracked:
		// the fast-forward's checkout would overwrite it, so git refuses.
		commitIn(t, w.WorkDir, "blocker.txt", "from C\n", "more on C")
		pw := h.st(w.RunID, p).Worktree
		if err := os.WriteFile(filepath.Join(pw, "blocker.txt"), []byte("local\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		w.report(autopilot.TicketDone, "c")
	}
	h.behave[c+"/merge-up"] = func(w *workerCall) {
		ctx := w.ctx()
		_ = os.Remove(filepath.Join(ctx.MergeWorktree, "blocker.txt"))
		gitT(t, ctx.MergeWorktree, "merge", "-q", "--no-edit", ctx.MergeSourceBranch)
		w.report(autopilot.TicketDone, "moved the blocker away and merged")
	}
	run := h.start(p, autopilot.ModeTree)
	h.stepUntil(run.RunID, func(res NextResult) bool { return res.Action.Action == autopilot.ActionMergeUp })
	mr, err := h.svc.MergeUp(run.RunID, c)
	if err != nil || mr.Result != "needs_merge_session" || mr.Reason != "GIT_ERROR" || !strings.Contains(mr.Detail, "blocker.txt") {
		t.Fatalf("merge-up = %+v, %v", mr, err)
	}
	res, failures := h.driveTolerant(run.RunID)
	if res.Action.Action != autopilot.ActionDone || failures != 0 {
		t.Fatalf("res = %+v, failures = %d, actions = %v", res, failures, h.actions)
	}
	if got := h.roleLaunches(autopilot.RoleMergeUp); len(got) != 1 {
		t.Fatalf("merge-up sessions = %v", got)
	}
	if s := h.st(run.RunID, c); s.Merge != autopilot.MergedSubtree {
		t.Fatalf("C = %+v", s)
	}
}

func TestTakeOver_RaisedMaxTicketsProcessesWhatTheLimitSkipped(t *testing.T) {
	h := newHarness(t)
	h.settings.MaxTickets = 2
	h.settings.MainReflection = autopilot.MainReflectionPullRequest
	r := h.ticket("R", "")
	c1 := h.ticket("C1", r)
	c2 := h.ticket("C2", r)
	finalizeFails := true
	h.behave[r+"/finalize"] = func(w *workerCall) {
		if finalizeFails {
			w.reportReason(autopilot.TicketFailed, "pr_failed", "gh not logged in")
			return
		}
		w.report(autopilot.TicketDone, "pr opened")
	}
	run := h.start(r, autopilot.ModeTree)
	if res := h.drive(run.RunID, nil); res.Action.Action != autopilot.ActionStopped {
		t.Fatalf("res = %+v", res)
	}
	if s := h.st(run.RunID, c2); s.Reason != autopilot.ReasonLimitTickets {
		t.Fatalf("C2 = %+v", s)
	}
	// The person raises maxTickets, fixes gh, and runs the same command.
	h.settings.MaxTickets = 5
	finalizeFails = false
	h.start(r, autopilot.ModeTree)
	if res := h.drive(run.RunID, nil); res.Action.Action != autopilot.ActionDone {
		t.Fatalf("res = %+v", res)
	}
	if s := h.st(run.RunID, c2); s.Status != autopilot.TicketDone || s.Merge != autopilot.MergedSubtree {
		t.Fatalf("C2 = %+v", s)
	}
	if got := h.workLaunches(); strings.Join(got, ",") != r+","+c1+","+c2 {
		t.Fatalf("work launches = %v", got)
	}
}

func TestSummary_FailedIntermediateMergeUpNamesWhatIsMissing(t *testing.T) {
	h := newHarness(t)
	h.settings.OnFailure = autopilot.OnFailureContinue
	r := h.ticket("R", "")
	c := h.ticket("C", r)
	g := h.ticket("G", c)
	var cOwn, gWork string
	h.behave[c] = func(w *workerCall) {
		cOwn = w.commit()
		w.release()
		// Somebody commits on R: C's later merge-up cannot fast-forward.
		commitIn(t, h.st(w.RunID, r).Worktree, "human.txt", "h", "human on R")
		w.report(autopilot.TicketDone, "c")
	}
	h.behave[g] = func(w *workerCall) {
		gWork = w.commit()
		w.release()
		w.report(autopilot.TicketDone, "g")
	}
	h.behave[c+"/merge-up"] = func(w *workerCall) {
		w.reportReason(autopilot.TicketBlocked, autopilot.ReasonMergeConflict, "conflict")
	}
	run := h.start(r, autopilot.ModeTree)
	h.drive(run.RunID, nil)
	sc := h.st(run.RunID, c)
	if sc.Status != autopilot.TicketBlocked || sc.Merge != autopilot.MergedSelf {
		t.Fatalf("C = %+v", sc)
	}
	if !h.branchContains("worktree-"+r, cOwn) || h.branchContains("worktree-"+r, gWork) {
		t.Fatal("wrong content in worktree-R")
	}
	sum, _ := h.svc.Summary(run.RunID)
	want := "its own merge into its parent is in"
	if !strings.Contains(sum.Markdown, want) {
		t.Fatalf("summary:\n%s", sum.Markdown)
	}
	gID := ""
	for _, id := range h.run(run.RunID).Order {
		if id != r && id != c {
			gID = id
		}
	}
	if gID == "" || !strings.Contains(sum.Markdown, "including the work of "+gID) {
		t.Fatalf("summary does not name G (%s):\n%s", gID, sum.Markdown)
	}
}
