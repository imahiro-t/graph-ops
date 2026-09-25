package runner

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/store"
)

// Plan phase 6-1: the scale test. A tree of more than 20 tickets, three
// levels below the root, is grown by fake workers -- each commits in its
// worktree, merges into its parent's branch, creates its children (from a
// fixed seed) and reports -- and driven by the orchestrator loop from start
// to summary, as an LLM orchestrator would drive it through the CLI.

// scaleFanout is how many children a ticket at each depth creates: 1 root,
// 3 + 6 + 12 below it -- 22 tickets, depth 3.
var scaleFanout = []int{3, 2, 2}

const scaleTickets = 1 + 3 + 6 + 12

// scaleSeed makes the per-ticket commit contents (and so the whole run)
// reproducible.
const scaleSeed = 20260925

type scaleTree struct {
	h *harness
	// path is a ticket's position ("R", "R.2", "R.2.1", ...) by ID, and id
	// the reverse.
	path map[string]string
	id   map[string]string
	// commits is the commit each ticket's work session made.
	commits map[string]string
	// fail makes the work session of a path report failed (after creating
	// its children, so that there is a subtree to skip).
	fail map[string]bool
	// silent makes the work session of a path do nothing and never report.
	silent map[string]bool
	// slow makes the work session of a path return without reporting; the
	// test's onWait keeps it active and finishes it later.
	slow map[string]bool
	rng  *rand.Rand
	// concurrent is set if a work session ever starts while another
	// session of the run is still running.
	concurrent bool
}

func newScaleTree(t *testing.T) *scaleTree {
	h := newHarness(t)
	h.settings.MaxTickets = 100
	h.settings.MainReflection = autopilot.MainReflectionPullRequest
	s := &scaleTree{
		h: h, path: map[string]string{}, id: map[string]string{}, commits: map[string]string{},
		fail: map[string]bool{}, silent: map[string]bool{}, slow: map[string]bool{},
		rng: rand.New(rand.NewSource(scaleSeed)),
	}
	root := h.ticket("R", "")
	s.path[root], s.id["R"] = "R", root
	return s
}

func (s *scaleTree) root() string { return s.id["R"] }

// expectedOrder is the DFS pre-order of the full tree, by path.
func expectedOrder() []string {
	var out []string
	var walk func(p string, depth int)
	walk = func(p string, depth int) {
		out = append(out, p)
		if depth >= len(scaleFanout) {
			return
		}
		for i := 1; i <= scaleFanout[depth]; i++ {
			walk(fmt.Sprintf("%s.%d", p, i), depth+1)
		}
	}
	walk("R", 0)
	return out
}

func depthOf(p string) int { return strings.Count(p, ".") }

// worker is the scale test's fake worker: every work session is routed
// here, so the harness's per-ticket behave map is not used.
func (s *scaleTree) worker(w *workerCall) {
	if w.Role != autopilot.RoleWork {
		defaultWorker(w)
		return
	}
	running := 0
	for _, st := range s.h.run(w.RunID).Tickets {
		if st.Role != "" {
			running++
		}
	}
	if running != 1 {
		s.concurrent = true
	}
	p := s.path[w.Ticket]
	switch {
	case s.silent[p], s.slow[p]:
		return
	case s.fail[p]:
		s.commit(w)
		s.createChildren(w)
		w.reportReason(autopilot.TicketFailed, "tests_failed", "failed "+p)
	default:
		s.finish(w)
	}
}

// finish is a successful work session's end: commit, release (merge into
// the parent's branch), create the children, report done.
func (s *scaleTree) finish(w *workerCall) {
	s.commit(w)
	w.release()
	s.createChildren(w)
	w.report(autopilot.TicketDone, "did "+s.path[w.Ticket])
}

func (s *scaleTree) commit(w *workerCall) {
	content := fmt.Sprintf("work of %s (%d)\n", s.path[w.Ticket], s.rng.Intn(1_000_000))
	s.commits[w.Ticket] = commitIn(w.h.t, w.WorkDir, w.Ticket+".txt", content, "work on "+w.Ticket)
}

func (s *scaleTree) createChildren(w *workerCall) {
	p := s.path[w.Ticket]
	d := depthOf(p)
	if d >= len(scaleFanout) {
		return
	}
	for i := 1; i <= scaleFanout[d]; i++ {
		cp := fmt.Sprintf("%s.%d", p, i)
		id := w.child(cp)
		s.path[id], s.id[cp] = cp, id
	}
}

// drive is the orchestrator loop the autopilot-tree skill runs, counting
// everything the orchestrator reads: next, then launch followed by wait (the
// skill always waits after a launch), wait for a session still running (after
// onWait has moved time or activity along), or merge-up; until done or
// stopped. Unlike harness.drive it includes the wait lines, since those are
// part of what an orchestrator session has to hold.
func (s *scaleTree) drive(runID string, onWait func(ticket, role string)) NextResult {
	h := s.h
	h.t.Helper()
	wait := func(ticket string) {
		wr, err := h.svc.Wait(runID, ticket, 0)
		if err != nil {
			h.t.Fatalf("Wait(%s): %v", ticket, err)
		}
		h.outputBytes += jsonLen(wr)
	}
	for i := 0; i < 1000; i++ {
		res, err := h.svc.Next(runID)
		if err != nil {
			h.t.Fatalf("Next: %v", err)
		}
		h.outputBytes += jsonLen(res)
		switch res.Action.Action {
		case autopilot.ActionLaunch:
			lr, err := h.svc.Launch(runID, res.Ticket, res.Role)
			if err != nil {
				h.t.Fatalf("Launch(%s, %s): %v", res.Ticket, res.Role, err)
			}
			h.outputBytes += jsonLen(lr)
			wait(res.Ticket)
		case autopilot.ActionWait:
			if onWait == nil {
				h.t.Fatalf("unexpected wait for %s (%s)", s.path[res.Ticket], res.Role)
			}
			onWait(res.Ticket, res.Role)
			wait(res.Ticket)
		case autopilot.ActionMergeUp:
			mr, err := h.svc.MergeUp(runID, res.Ticket)
			if err != nil {
				h.t.Fatalf("MergeUp(%s): %v", res.Ticket, err)
			}
			h.outputBytes += jsonLen(mr)
		case autopilot.ActionDone, autopilot.ActionStopped:
			return res
		default:
			h.t.Fatalf("unknown action %+v", res)
		}
	}
	h.t.Fatal("drive did not finish")
	return NextResult{}
}

// install routes every launch through s.worker.
func (s *scaleTree) install() {
	s.h.launchWorker = s.worker
}

// launchedPaths is the work launches, as paths.
func (s *scaleTree) launchedPaths() []string {
	var out []string
	for _, id := range s.h.workLaunches() {
		out = append(out, s.path[id])
	}
	return out
}

func (s *scaleTree) inRootBranch(path string) bool {
	id := s.id[path]
	return s.h.branchContains(autopilot.BranchFor(s.root()), s.commits[id])
}

func (s *scaleTree) summary(runID string) string {
	s.h.t.Helper()
	sum, err := s.h.svc.Summary(runID)
	if err != nil {
		s.h.t.Fatal(err)
	}
	s.h.outputBytes += len(sum.Markdown)
	return sum.Markdown
}

func (s *scaleTree) doneCount(runID string) int {
	n := 0
	for _, st := range s.h.run(runID).Tickets {
		if st.Status == autopilot.TicketDone {
			n++
		}
	}
	return n
}

// (a) serial DFS over the whole tree, (b) the orchestrator's output stays
// under 1KB a ticket, (e) every done ticket's commit -- depth 2 and 3
// included -- reaches the root branch through the merge-up chain.
func TestScale_TwentyPlusTicketsEndToEnd(t *testing.T) {
	s := newScaleTree(t)
	s.install()
	run := s.h.start(s.root(), autopilot.ModeTree)
	final := s.drive(run.RunID, nil)
	if final.Action.Action != autopilot.ActionDone {
		t.Fatalf("final = %+v", final)
	}
	md := s.summary(run.RunID)

	want := expectedOrder()
	if len(want) != scaleTickets || scaleTickets <= 20 {
		t.Fatalf("the tree has %d tickets", len(want))
	}
	if got := s.launchedPaths(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("work launches:\n got %v\nwant %v", got, want)
	}
	if s.concurrent {
		t.Fatal("a session started while another was running")
	}
	if n := s.doneCount(run.RunID); n != scaleTickets {
		t.Fatalf("done = %d", n)
	}
	if got := s.h.run(run.RunID).State; got != autopilot.RunFinished {
		t.Fatalf("state = %s", got)
	}
	if f := s.h.roleLaunches(autopilot.RoleFinalize); len(f) != 1 || f[0] != s.root() {
		t.Fatalf("finalize launches = %v", f)
	}
	for _, p := range want {
		if !s.inRootBranch(p) {
			t.Errorf("the commit of %s (depth %d) is not in the root branch", p, depthOf(p))
		}
	}
	if per := s.h.outputBytes / scaleTickets; per >= 1024 {
		t.Fatalf("the orchestrator read %d bytes, %d a ticket", s.h.outputBytes, per)
	}
	t.Logf("orchestrator output: %d bytes for %d tickets (%d a ticket)", s.h.outputBytes, scaleTickets, s.h.outputBytes/scaleTickets)
	if rows := strings.Count(md, "\n| "+s.root()[:strings.Index(s.root(), "-")]); rows != scaleTickets {
		t.Fatalf("summary has %d ticket rows:\n%s", rows, md)
	}
}

// (c) an interrupted orchestrator (heartbeat gone stale) is resumed by a
// second start: nothing processed is launched again, the rest is.
func TestScale_ResumeAfterInterruption(t *testing.T) {
	s := newScaleTree(t)
	s.install()
	run := s.h.start(s.root(), autopilot.ModeTree)
	// Drive until 10 tickets have been worked on, then walk away.
	for len(s.h.workLaunches()) < 10 {
		res, err := s.h.svc.Next(run.RunID)
		if err != nil {
			t.Fatal(err)
		}
		switch res.Action.Action {
		case autopilot.ActionLaunch:
			if _, err := s.h.svc.Launch(run.RunID, res.Ticket, res.Role); err != nil {
				t.Fatal(err)
			}
		case autopilot.ActionMergeUp:
			if _, err := s.h.svc.MergeUp(run.RunID, res.Ticket); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unexpected %+v", res)
		}
	}
	before := append([]string(nil), s.launchedPaths()...)
	s.h.makeStale()
	again := s.h.start(s.root(), autopilot.ModeTree)
	if again.RunID != run.RunID || !again.Resumed {
		t.Fatalf("again = %+v", again)
	}
	if final := s.drive(run.RunID, nil); final.Action.Action != autopilot.ActionDone {
		t.Fatalf("final = %+v", final)
	}
	got := s.launchedPaths()
	if strings.Join(got[:len(before)], " ") != strings.Join(before, " ") {
		t.Fatalf("launches were rewritten: %v", got)
	}
	seen := map[string]bool{}
	for _, p := range got {
		if seen[p] {
			t.Fatalf("%s was launched twice: %v", p, got)
		}
		seen[p] = true
	}
	if strings.Join(got, " ") != strings.Join(expectedOrder(), " ") {
		t.Fatalf("launches = %v", got)
	}
	if n := s.doneCount(run.RunID); n != scaleTickets {
		t.Fatalf("done = %d, want %d as without the interruption", n, scaleTickets)
	}
	for _, p := range expectedOrder() {
		if !s.inRootBranch(p) {
			t.Errorf("the commit of %s is not in the root branch", p)
		}
	}
}

// (d) onFailure stop: the first failure stops the run, with the reason in
// the summary and nothing of the failed ticket in the root branch.
func TestScale_OnFailureStop(t *testing.T) {
	s := newScaleTree(t)
	s.h.settings.OnFailure = autopilot.OnFailureStop
	s.fail["R.2.1"] = true
	s.install()
	run := s.h.start(s.root(), autopilot.ModeTree)
	if final := s.drive(run.RunID, nil); final.Action.Action != autopilot.ActionStopped {
		t.Fatalf("final = %+v", final)
	}
	r := s.h.run(run.RunID)
	if r.State != autopilot.RunStopped || r.StopReason != autopilot.StopTicketFailed || r.StopTicket != s.id["R.2.1"] {
		t.Fatalf("run = %s %s %s", r.State, r.StopReason, r.StopTicket)
	}
	got := s.launchedPaths()
	if got[len(got)-1] != "R.2.1" {
		t.Fatalf("launches after the failure: %v", got)
	}
	md := s.summary(run.RunID)
	if !strings.Contains(md, "Stopped: "+autopilot.StopTicketFailed) || !strings.Contains(md, "tests_failed") {
		t.Fatalf("summary:\n%s", md)
	}
	if s.inRootBranch("R.2.1") {
		t.Fatal("the failed ticket's commit is in the root branch")
	}
	for _, p := range []string{"R.1", "R.1.1", "R.1.2.2"} {
		if !s.inRootBranch(p) {
			t.Errorf("%s (a finished subtree) is not in the root branch", p)
		}
	}
	if len(s.h.roleLaunches(autopilot.RoleFinalize)) != 0 {
		t.Fatal("finalize ran after a stop")
	}
}

// (d) onFailure continue: only the failed ticket's subtree is skipped
// (parent_failed); its work is left out of the root branch and listed in the
// summary; everything else is done and merged.
func TestScale_OnFailureContinue(t *testing.T) {
	s := newScaleTree(t)
	s.h.settings.OnFailure = autopilot.OnFailureContinue
	s.fail["R.2"] = true
	s.install()
	run := s.h.start(s.root(), autopilot.ModeTree)
	if final := s.drive(run.RunID, nil); final.Action.Action != autopilot.ActionDone {
		t.Fatalf("final = %+v", final)
	}
	for _, p := range []string{"R.2.1", "R.2.2"} {
		if st := s.h.st(run.RunID, s.id[p]); st.Status != autopilot.TicketSkipped || st.Reason != autopilot.ReasonParentFailed {
			t.Fatalf("%s = %+v", p, st)
		}
	}
	for _, p := range s.launchedPaths() {
		if strings.HasPrefix(p, "R.2.") {
			t.Fatalf("%s under the failed ticket was launched", p)
		}
	}
	if !contains(s.launchedPaths(), "R.3.2.2") {
		t.Fatal("the rest of the tree was not processed")
	}
	if s.inRootBranch("R.2") {
		t.Fatal("the failed ticket's commit is in the root branch")
	}
	for _, p := range s.launchedPaths() {
		if p != "R.2" && !s.inRootBranch(p) {
			t.Errorf("%s is not in the root branch", p)
		}
	}
	md := s.summary(run.RunID)
	if !strings.Contains(md, "Work not in the root branch") || !strings.Contains(md, autopilot.BranchFor(s.id["R.2"])) ||
		!strings.Contains(md, autopilot.ReasonParentFailed) {
		t.Fatalf("summary:\n%s", md)
	}
}

// (d) maxTickets below the tree's size: the rest is skipped as
// limit_tickets, and what was done is still merged and finalized.
func TestScale_MaxTickets(t *testing.T) {
	s := newScaleTree(t)
	s.h.settings.MaxTickets = 10
	s.install()
	run := s.h.start(s.root(), autopilot.ModeTree)
	if final := s.drive(run.RunID, nil); final.Action.Action != autopilot.ActionDone {
		t.Fatalf("final = %+v", final)
	}
	got := s.launchedPaths()
	if len(got) != 10 {
		t.Fatalf("launches = %v", got)
	}
	for _, p := range got {
		if !s.inRootBranch(p) {
			t.Errorf("%s is not in the root branch", p)
		}
	}
	md := s.summary(run.RunID)
	if !strings.Contains(md, autopilot.ReasonLimitTickets) {
		t.Fatalf("summary:\n%s", md)
	}
	if len(s.h.roleLaunches(autopilot.RoleFinalize)) != 1 {
		t.Fatal("finalize did not run")
	}
}

// (f) a worker that never does or reports anything fails as unresponsive
// once stallTimeoutMinutes pass on the injected clock, and the run follows
// onFailure.
func TestScale_SilentWorkerIsUnresponsive(t *testing.T) {
	for _, onFailure := range []string{autopilot.OnFailureStop, autopilot.OnFailureContinue} {
		t.Run(onFailure, func(t *testing.T) {
			s := newScaleTree(t)
			s.h.settings.OnFailure = onFailure
			s.silent["R.1.2"] = true
			s.install()
			run := s.h.start(s.root(), autopilot.ModeTree)
			waits := 0
			final := s.drive(run.RunID, func(ticket, role string) {
				waits++
				if ticket != s.id["R.1.2"] || waits > 1 {
					t.Fatalf("wait #%d for %s", waits, s.path[ticket])
				}
				s.h.clock.Advance(time.Duration(s.h.settings.StallTimeoutMinutes+1) * time.Minute)
			})
			st := s.h.st(run.RunID, s.id["R.1.2"])
			if st.Status != autopilot.TicketFailed || st.Reason != autopilot.ReasonUnresponsive {
				t.Fatalf("R.1.2 = %+v", st)
			}
			md := s.summary(run.RunID)
			if !strings.Contains(md, "Unresponsive sessions") || !strings.Contains(md, st.Worktree) {
				t.Fatalf("summary:\n%s", md)
			}
			switch onFailure {
			case autopilot.OnFailureStop:
				if final.Action.Action != autopilot.ActionStopped || !strings.Contains(md, "Stopped: "+autopilot.StopTicketFailed) {
					t.Fatalf("final = %+v\n%s", final, md)
				}
			case autopilot.OnFailureContinue:
				if final.Action.Action != autopilot.ActionDone {
					t.Fatalf("final = %+v", final)
				}
				// The silent ticket creates no children, so the full tree
				// minus its two would-be children is launched.
				if n := len(s.launchedPaths()); n != scaleTickets-2 {
					t.Fatalf("launches = %d", n)
				}
				if n := s.doneCount(run.RunID); n != scaleTickets-3 {
					t.Fatalf("done = %d", n)
				}
			}
		})
	}
}

// (g) a worker that takes far longer than stallTimeoutMinutes to report but
// keeps changing the DB and its worktree is never judged unresponsive.
func TestScale_SlowButActiveWorkerIsNotUnresponsive(t *testing.T) {
	s := newScaleTree(t)
	s.h.settings.OnFailure = autopilot.OnFailureStop
	s.slow["R.2.1"] = true
	s.install()
	run := s.h.start(s.root(), autopilot.ModeTree)
	slowID := ""
	waits := 0
	final := s.drive(run.RunID, func(ticket, role string) {
		if s.path[ticket] != "R.2.1" {
			t.Fatalf("unexpected wait for %s", s.path[ticket])
		}
		slowID = ticket
		waits++
		st := s.h.st(run.RunID, ticket)
		if waits%2 == 1 {
			// The DB moves: the ticket is updated.
			desc := fmt.Sprintf("progress %d", waits)
			if _, err := s.h.repo.UpdateTicket(ticket, store.TicketPatch{Description: &desc}); err != nil {
				t.Fatal(err)
			}
		} else {
			// The worktree moves: a file is edited, not yet committed.
			commitlessEdit(t, st.Worktree, fmt.Sprintf("wip-%d.txt", waits))
		}
		// 20 minutes pass between observations -- 140 in all, more than
		// twice the 60-minute stall timeout.
		s.h.clock.Advance(20 * time.Minute)
		if waits == 7 {
			s.finish(&workerCall{h: s.h, launchCall: launchCall{WorkDir: st.Worktree, RunID: run.RunID, Ticket: ticket, Role: autopilot.RoleWork}})
		}
	})
	if final.Action.Action != autopilot.ActionDone {
		t.Fatalf("final = %+v", final)
	}
	if st := s.h.st(run.RunID, slowID); st.Status != autopilot.TicketDone || st.Reason != "" {
		t.Fatalf("R.2.1 = %+v", st)
	}
	if n := s.doneCount(run.RunID); n != scaleTickets {
		t.Fatalf("done = %d", n)
	}
	if !s.inRootBranch("R.2.1") || !s.inRootBranch("R.2.1.2") {
		t.Fatal("the slow ticket's work is not in the root branch")
	}
}

// commitlessEdit writes a new untracked file in dir.
func commitlessEdit(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
