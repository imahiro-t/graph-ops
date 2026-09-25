package runner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00142 phase 3: worktrees, branches and merges (Gherkin: "各チケットは
// worktree-<ticketId> ブランチで作業し、子の成果は親のブランチへ fast-forward
// で反映される").

func TestLaunch_CreatesWorktreeAndOpensTheSession(t *testing.T) {
	h := newHarness(t)
	h.settings.PermissionMode = autopilot.PermissionModeAcceptEdits
	r := h.ticket("R", "")
	h.behave[r] = silentWorker
	run := h.start(r, autopilot.ModeTree)
	h.svc.Next(run.RunID)
	res, err := h.svc.Launch(run.RunID, r, "")
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(h.gitRepo, ".claude", "worktrees", r)
	if res.Worktree != wt || res.Branch != "worktree-"+r || res.BaseBranch != "main" {
		t.Fatalf("res = %+v", res)
	}
	if gitT(t, wt, "rev-parse", "HEAD") != h.tip("main") {
		t.Fatal("not cut from main")
	}
	call := h.launches[0]
	if call.WorkDir != wt || strings.Join(call.Args, " ") != "--permission-mode acceptEdits" ||
		call.Prompt != "/graph-ops:autopilot-worker "+run.RunID+" "+r+" --role work" {
		t.Fatalf("launch = %+v", call)
	}
}

func TestLaunch_ReusesAnExistingWorktree(t *testing.T) {
	h := newHarness(t)
	c := h.ticket("C", "")
	h.behave[c] = silentWorker
	wt := filepath.Join(h.gitRepo, ".claude", "worktrees", c)
	gitT(t, h.gitRepo, "worktree", "add", "-q", "-b", "worktree-"+c, wt, "main")
	marker := commitIn(t, wt, "earlier.txt", "e", "earlier work")
	run := h.start(c, autopilot.ModeTicket)
	h.svc.Next(run.RunID)
	res, err := h.svc.Launch(run.RunID, c, "work")
	if err != nil {
		t.Fatal(err)
	}
	if res.Worktree != wt || gitT(t, wt, "rev-parse", "HEAD") != marker {
		t.Fatalf("res = %+v", res)
	}
}

func TestLaunch_RequiresTheLocalPath(t *testing.T) {
	h := newHarness(t)
	c := h.ticket("C", "")
	run := h.start(c, autopilot.ModeTicket)
	h.svc.Next(run.RunID)
	h.gitRepo = ""
	_, err := h.svc.Launch(run.RunID, c, "work")
	assertAPICode(t, err, autopilot.ErrCodeLocalPathNotSet)
	if len(h.launches) != 0 {
		t.Fatal("launched")
	}
	if s := h.st(run.RunID, c); s.Status != autopilot.TicketQueued {
		t.Fatalf("C = %+v", s)
	}
}

func TestLaunch_TerminalFailureLeavesTheTicketQueued(t *testing.T) {
	h := newHarness(t)
	c := h.ticket("C", "")
	run := h.start(c, autopilot.ModeTicket)
	h.svc.Next(run.RunID)
	h.launchErr = errors.New("no terminal")
	if _, err := h.svc.Launch(run.RunID, c, "work"); err == nil {
		t.Fatal("no error")
	}
	s := h.st(run.RunID, c)
	if s.Status != autopilot.TicketQueued || s.Role != "" || h.run(run.RunID).WorkLaunches != 0 {
		t.Fatalf("C = %+v", s)
	}
	h.launchErr = nil
	if _, err := h.svc.Launch(run.RunID, c, "work"); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestLaunch_StandaloneChildIsCutFromMain(t *testing.T) {
	h := newHarness(t)
	p := h.ticket("P", "")
	c := h.ticket("C", p)
	gitT(t, h.gitRepo, "branch", "worktree-"+p, "main")
	commitIn(t, h.gitRepo, "main2.txt", "m", "main moves")
	h.behave[c] = silentWorker
	run := h.start(c, autopilot.ModeTicket)
	h.svc.Next(run.RunID)
	res, err := h.svc.Launch(run.RunID, c, "work")
	if err != nil || res.BaseBranch != "main" || gitT(t, res.Worktree, "rev-parse", "HEAD") != h.tip("main") {
		t.Fatalf("res = %+v, %v", res, err)
	}
	ctx, _ := h.svc.WorkerContext(run.RunID, c)
	if ctx.Position != "single" || ctx.TargetBranch != "" || ctx.DefaultBranch != "main" {
		t.Fatalf("ctx = %+v", ctx)
	}
}

func TestWorkerContext_TreeChild(t *testing.T) {
	h := newHarness(t)
	r := h.ticket("R", "")
	c := h.ticket("C", r)
	var ctx WorkerContext
	h.behave[c] = func(w *workerCall) { ctx = w.ctx(); defaultWorker(w) }
	run := h.start(r, autopilot.ModeTree)
	h.drive(run.RunID, nil)
	if ctx.Position != "tree_child" || ctx.Role != "work" || ctx.BaseBranch != "worktree-"+r || ctx.TargetBranch != "worktree-"+r ||
		ctx.Settings != h.settings || ctx.RunID != run.RunID || ctx.PendingDecisions == nil {
		t.Fatalf("ctx = %+v", ctx)
	}
}

func TestMergeIntoParent(t *testing.T) {
	setup := func(t *testing.T, beforeMerge func(h *harness, runID, p, c string)) (*harness, string, string, string, error) {
		h := newHarness(t)
		p := h.ticket("P", "")
		c := h.ticket("C", p)
		var mergeErr error
		h.behave[c] = func(w *workerCall) {
			w.commit()
			if beforeMerge != nil {
				beforeMerge(h, w.RunID, p, c)
			}
			mergeErr = w.mergeIntoParent()
			w.report(autopilot.TicketDone, "c")
		}
		run := h.start(p, autopilot.ModeTree)
		h.stepUntil(run.RunID, func(res NextResult) bool {
			return res.Action.Action == autopilot.ActionMergeUp || res.Action.Action == autopilot.ActionDone
		})
		return h, run.RunID, p, c, mergeErr
	}
	t.Run("fast-forward into the checked-out parent worktree", func(t *testing.T) {
		h, runID, p, c, err := setup(t, nil)
		if err != nil {
			t.Fatal(err)
		}
		if h.tip("worktree-"+p) != h.tip("worktree-"+c) {
			t.Fatal("parent not at child's tip")
		}
		if n := gitT(t, h.gitRepo, "rev-list", "--count", "--merges", "worktree-"+p); n != "0" {
			t.Fatal("merge commit created")
		}
		if data, _ := os.ReadFile(filepath.Join(h.st(runID, p).Worktree, c+".txt")); len(data) == 0 {
			t.Fatal("parent worktree files not updated")
		}
		if s := h.st(runID, c); s.Merge != autopilot.MergedSelf {
			t.Fatalf("merge = %s", s.Merge)
		}
	})
	t.Run("not a fast-forward", func(t *testing.T) {
		var before string
		h, runID, p, c, err := setup(t, func(h *harness, runID, p, c string) {
			before = commitIn(t, h.st(runID, p).Worktree, "p2.txt", "p", "p moves")
		})
		assertAPICode(t, err, autopilot.ErrCodeNotFastForward)
		if h.tip("worktree-"+p) != before || h.st(runID, c).Merge == autopilot.MergedSelf {
			t.Fatal("changed")
		}
	})
	t.Run("dirty parent worktree", func(t *testing.T) {
		var before string
		h, runID, p, c, err := setup(t, func(h *harness, runID, p, c string) {
			before = h.tip("worktree-" + p)
			os.WriteFile(filepath.Join(h.st(runID, p).Worktree, "README.md"), []byte("wip\n"), 0o644)
		})
		assertAPICode(t, err, autopilot.ErrCodeParentDirty)
		if h.tip("worktree-"+p) != before || h.st(runID, c).Merge == autopilot.MergedSelf {
			t.Fatal("changed")
		}
		if data, _ := os.ReadFile(filepath.Join(h.st(runID, p).Worktree, "README.md")); string(data) != "wip\n" {
			t.Fatal("uncommitted change lost")
		}
	})
	t.Run("the root has no parent branch", func(t *testing.T) {
		h := newHarness(t)
		r := h.ticket("R", "")
		var err error
		h.behave[r] = func(w *workerCall) { err = w.mergeIntoParent() }
		run := h.start(r, autopilot.ModeTree)
		h.svc.Next(run.RunID)
		h.svc.Launch(run.RunID, r, "work")
		assertAPICode(t, err, domain.ErrCodeValidation)
	})
}

func TestMergeUp_ChainCarriesDepthThreeToTheRoot(t *testing.T) {
	h := newHarness(t)
	r := h.ticket("R", "")
	commits := map[string]string{}
	ids := map[string]string{"R": r}
	// Each ticket's child is created after it has merged into its parent.
	var mk func(name string, rest []string) worker
	mk = func(name string, rest []string) worker {
		return func(w *workerCall) {
			commits[name] = w.commit()
			w.release()
			if len(rest) > 0 {
				id := w.child(rest[0])
				ids[rest[0]] = id
				h.behave[id] = mk(rest[0], rest[1:])
			}
			w.report(autopilot.TicketDone, name)
		}
	}
	h.behave[r] = mk("R", []string{"P", "C", "G"})
	run := h.start(r, autopilot.ModeTree)
	h.drive(run.RunID, nil)
	order := []int{indexOf(h.actions, "merge-up "+ids["G"]), indexOf(h.actions, "merge-up "+ids["C"]), indexOf(h.actions, "merge-up "+ids["P"])}
	if order[0] < 0 || !(order[0] < order[1] && order[1] < order[2]) {
		t.Fatalf("actions = %v", h.actions)
	}
	for _, n := range []string{"R", "P", "C", "G"} {
		if !h.branchContains("worktree-"+r, commits[n]) {
			t.Fatalf("worktree-R lacks %s's commit", n)
		}
	}
	for _, n := range []string{"P", "C", "G"} {
		if s := h.st(run.RunID, ids[n]); s.Merge != autopilot.MergedSubtree {
			t.Fatalf("%s merge = %s", n, s.Merge)
		}
	}
}

func TestMergeUp_FailedGrandchildStaysOutOfTheRoot(t *testing.T) {
	h := newHarness(t)
	h.settings.OnFailure = autopilot.OnFailureContinue
	r := h.ticket("R", "")
	p := h.ticket("P", r)
	g1 := h.ticket("G1", p)
	g2 := h.ticket("G2", p)
	var c1, c2 string
	h.behave[g1] = func(w *workerCall) { c1 = w.commit(); w.report(autopilot.TicketFailed, "g1 broke") }
	h.behave[g2] = func(w *workerCall) { c2 = w.commit(); w.release(); w.report(autopilot.TicketDone, "g2") }
	run := h.start(r, autopilot.ModeTree)
	h.drive(run.RunID, nil)
	if !h.branchContains("worktree-"+r, c2) || h.branchContains("worktree-"+r, c1) {
		t.Fatal("wrong content in worktree-R")
	}
	sum, _ := h.svc.Summary(run.RunID)
	if !strings.Contains(sum.Markdown, "Work not in the root branch") || !strings.Contains(sum.Markdown, "worktree-"+g1) {
		t.Fatalf("summary:\n%s", sum.Markdown)
	}
}

func TestMergeUp_NeedsASessionWhenNotFastForward(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mergeUp  worker
		wantDone bool
	}{
		{"session merges", nil, true},
		{"session cannot resolve", func(w *workerCall) { w.report(autopilot.TicketBlocked, "conflict in x") }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.settings.OnFailure = autopilot.OnFailureStop
			p := h.ticket("P", "")
			c := h.ticket("C", p)
			var pTip string
			h.behave[c] = func(w *workerCall) {
				w.commit()
				w.release()
				pTip = commitIn(t, h.st(w.RunID, p).Worktree, "human.txt", "h", "human on P")
				commitIn(t, w.WorkDir, "more.txt", "m", "more on C")
				w.report(autopilot.TicketDone, "c")
			}
			if tc.mergeUp != nil {
				h.behave[c+"/merge-up"] = tc.mergeUp
			}
			run := h.start(p, autopilot.ModeTree)
			h.stepUntil(run.RunID, func(res NextResult) bool { return res.Action.Action == autopilot.ActionMergeUp })
			mr, err := h.svc.MergeUp(run.RunID, c)
			if err != nil || mr.Result != "needs_merge_session" || h.tip("worktree-"+p) != pTip {
				t.Fatalf("merge-up = %+v, %v", mr, err)
			}
			next, _ := h.svc.Next(run.RunID)
			if next.Action.Action != autopilot.ActionLaunch || next.Role != autopilot.RoleMergeUp || next.Ticket != c ||
				next.Worktree != h.st(run.RunID, p).Worktree {
				t.Fatalf("next = %+v", next)
			}
			res := h.drive(run.RunID, nil)
			s := h.st(run.RunID, c)
			if tc.wantDone {
				if s.Merge != autopilot.MergedSubtree || res.Action.Action != autopilot.ActionDone {
					t.Fatalf("C = %+v, res = %+v", s, res)
				}
			} else if s.Status != autopilot.TicketBlocked || s.Reason != autopilot.ReasonMergeConflict || res.Action.Action != autopilot.ActionStopped {
				t.Fatalf("C = %+v, res = %+v", s, res)
			}
		})
	}
}

func TestMergeUp_SessionIsWatchedForStalls(t *testing.T) {
	h := newHarness(t)
	h.settings.OnFailure = autopilot.OnFailureContinue
	p := h.ticket("P", "")
	c := h.ticket("C", p)
	h.behave[c] = func(w *workerCall) {
		w.commit()
		w.release()
		commitIn(t, h.st(w.RunID, p).Worktree, "human.txt", "h", "human on P")
		commitIn(t, w.WorkDir, "more.txt", "m", "more on C")
		w.report(autopilot.TicketDone, "c")
	}
	h.behave[c+"/merge-up"] = silentWorker
	run := h.start(p, autopilot.ModeTree)
	h.drive(run.RunID, func(ticket, role string) {
		if role != autopilot.RoleMergeUp {
			t.Fatalf("wait for %s/%s", ticket, role)
		}
		h.clock.Advance(61 * 60e9)
	})
	if s := h.st(run.RunID, c); s.Status != autopilot.TicketFailed || s.Reason != autopilot.ReasonUnresponsive || s.Merge != autopilot.NotMerged {
		t.Fatalf("C = %+v", s)
	}
}
