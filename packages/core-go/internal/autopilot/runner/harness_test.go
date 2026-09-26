package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
	"github.com/graph-ops/core-go/internal/terminal"
)

// This file is the test harness for the runner: a SQLite DB, a throwaway git
// repository as the project's local path, a fake clock, and a fake launcher
// whose "terminal" runs a fake worker synchronously. Phase 6's scale test is
// meant to reuse it.

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// launchCall is one call to the fake launcher.
type launchCall struct {
	WorkDir string
	Args    []string
	Prompt  string
	RunID   string
	Ticket  string
	Role    string
	// TerminalTTY / SkipTab are what the launch passed for the Terminal.app
	// tab path (DFLT-00154).
	TerminalTTY string
	SkipTab     bool
}

// worker is a fake child session. It runs inside the fake launcher, i.e.
// after launch has recorded the session and prepared the worktree.
type worker func(w *workerCall)

type harness struct {
	t         *testing.T
	repo      store.GraphRepository
	eng       *engine.GraphEngine
	projectID string
	gitRepo   string
	clock     *fakeClock
	svc       *Service
	settings  autopilot.Settings

	mu       sync.Mutex
	launches []launchCall
	// behave overrides the default worker per ticket (and role, keyed
	// "<ticket>/<role>").
	behave map[string]worker
	// launchWorker, when set, handles every launch instead of behave and
	// the default worker (the scale test's tree-growing worker).
	launchWorker worker
	// launchErr makes the launcher fail.
	launchErr error
	// launchOutcome, when set, is the outcome of each successful launch
	// (the Terminal.app tab path's fallback report).
	launchOutcome func(call launchCall) terminal.LaunchOutcome
	// actions is every action next returned, as "action ticket role".
	actions []string
	// outputBytes is the total size of what the orchestrator would have
	// read (next/launch/wait/merge-up outputs), for the scale test.
	outputBytes int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo, err := store.NewSQLiteRepository(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Init(); err != nil {
		t.Fatal(err)
	}
	proj, err := repo.CreateProject("Autopilot", "AP")
	if err != nil {
		t.Fatal(err)
	}
	gitRepo := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(gitRepo); err == nil {
		gitRepo = resolved
	}
	gitT(t, gitRepo, "init", "-q", "-b", "main")
	commitIn(t, gitRepo, "README.md", "hello\n", "initial")

	h := &harness{
		t: t, repo: repo, eng: engine.New(repo), projectID: proj.ID, gitRepo: gitRepo,
		clock:    &fakeClock{t: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)},
		settings: autopilot.Defaults(),
		behave:   map[string]worker{},
	}
	h.svc = &Service{
		Repo:         repo,
		Registry:     &autopilot.Registry{Root: filepath.Join(t.TempDir(), "autopilot")},
		Launcher:     LauncherFunc(h.launch),
		Settings:     func(string) (autopilot.Settings, error) { return h.settings, nil },
		LocalPath:    func(string) string { return h.gitRepo },
		Now:          h.clock.Now,
		PollInterval: time.Millisecond,
		Sleep:        func(time.Duration) {},
	}
	return h
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitIn(t *testing.T, dir, name, content, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", name)
	gitT(t, dir, "commit", "-q", "-m", msg)
	return gitT(t, dir, "rev-parse", "HEAD")
}

// branchContains reports whether branch contains commit.
func (h *harness) branchContains(branch, commit string) bool {
	cmd := exec.Command("git", "-C", h.gitRepo, "merge-base", "--is-ancestor", commit, branch)
	return cmd.Run() == nil
}

func (h *harness) tip(branch string) string {
	return gitT(h.t, h.gitRepo, "rev-parse", branch)
}

// ticket creates a ticket (a child of parent when parent != "").
func (h *harness) ticket(title, parent string) string {
	h.t.Helper()
	opts := engine.CreateTicketOptions{}
	if parent != "" {
		opts.ParentTicketID = &parent
	}
	tk, err := h.eng.CreateTicketWithOptions(h.projectID, title, "", opts)
	if err != nil {
		h.t.Fatal(err)
	}
	// Distinct created_at values keep the creation order unambiguous.
	time.Sleep(2 * time.Millisecond)
	return tk.ID
}

func (h *harness) setStatus(id string, status domain.TicketStatus) {
	h.t.Helper()
	if _, err := h.repo.UpdateTicket(id, store.TicketPatch{Status: &status}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) start(root, mode string) StartResult {
	h.t.Helper()
	res, err := h.svc.Start(root, mode, "", false)
	if err != nil {
		h.t.Fatalf("Start(%s, %s): %v", root, mode, err)
	}
	h.outputBytes += len(fmt.Sprintf("%+v", res))
	return res
}

func (h *harness) run(runID string) *autopilot.Run {
	h.t.Helper()
	run, err := h.svc.Registry.Load(h.projectID, runID)
	if err != nil || run == nil {
		h.t.Fatalf("Load(%s): %v", runID, err)
	}
	return run
}

func (h *harness) st(runID, ticketID string) *autopilot.TicketState {
	h.t.Helper()
	st := h.run(runID).Ticket(ticketID)
	if st == nil {
		h.t.Fatalf("ticket %s is not in run %s", ticketID, runID)
	}
	return st
}

// makeStale ages the run's heartbeat past ActiveThreshold (an interrupted
// orchestrator).
func (h *harness) makeStale() { h.clock.Advance(autopilot.ActiveThreshold + time.Minute) }

func (h *harness) launch(req LaunchRequest) (terminal.LaunchOutcome, error) {
	if h.launchErr != nil {
		return terminal.LaunchOutcome{}, h.launchErr
	}
	f := strings.Fields(req.Prompt)
	if len(f) != 5 || f[0] != "/graph-ops:autopilot-worker" || f[3] != "--role" {
		return terminal.LaunchOutcome{}, fmt.Errorf("unexpected prompt %q", req.Prompt)
	}
	call := launchCall{
		WorkDir: req.WorkDir, Args: append([]string(nil), req.ExtraArgs...), Prompt: req.Prompt, RunID: f[1], Ticket: f[2], Role: f[4],
		TerminalTTY: req.TerminalTTY, SkipTab: req.SkipTab,
	}
	h.mu.Lock()
	h.launches = append(h.launches, call)
	h.mu.Unlock()
	var outcome terminal.LaunchOutcome
	if h.launchOutcome != nil {
		outcome = h.launchOutcome(call)
	}
	if h.launchWorker != nil {
		h.launchWorker(&workerCall{h: h, launchCall: call})
		return outcome, nil
	}
	w := h.behave[call.Ticket+"/"+call.Role]
	if w == nil {
		w = h.behave[call.Ticket]
		if call.Role != autopilot.RoleWork {
			w = nil
		}
	}
	if w == nil {
		w = defaultWorker
	}
	w(&workerCall{h: h, launchCall: call})
	return outcome, nil
}

// workLaunches returns the tickets launched with role work, in order.
func (h *harness) workLaunches() []string {
	var out []string
	for _, c := range h.launches {
		if c.Role == autopilot.RoleWork {
			out = append(out, c.Ticket)
		}
	}
	return out
}

func (h *harness) roleLaunches(role string) []string {
	var out []string
	for _, c := range h.launches {
		if c.Role == role {
			out = append(out, c.Ticket)
		}
	}
	return out
}

// drive runs the orchestrator loop: next, then do what it says, until done
// or stopped (or maxSteps). onWait handles a wait action (a worker that did
// not report); nil fails the test.
func (h *harness) drive(runID string, onWait func(ticket, role string)) NextResult {
	h.t.Helper()
	for i := 0; i < 500; i++ {
		res, err := h.svc.Next(runID)
		if err != nil {
			h.t.Fatalf("Next: %v", err)
		}
		h.outputBytes += jsonLen(res)
		h.actions = append(h.actions, strings.TrimSpace(res.Action.Action+" "+res.Ticket+" "+res.Role))
		switch res.Action.Action {
		case autopilot.ActionLaunch:
			lr, err := h.svc.Launch(runID, res.Ticket, res.Role)
			if err != nil {
				h.t.Fatalf("Launch(%s, %s): %v", res.Ticket, res.Role, err)
			}
			h.outputBytes += jsonLen(lr)
		case autopilot.ActionWait:
			if onWait == nil {
				h.t.Fatalf("unexpected wait for %s (%s); actions so far: %v", res.Ticket, res.Role, h.actions)
			}
			onWait(res.Ticket, res.Role)
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
	h.t.Fatalf("drive did not finish; actions: %v", h.actions)
	return NextResult{}
}

var jsonMarshal = json.Marshal

func jsonLen(v any) int {
	b, _ := jsonMarshal(v)
	return len(b)
}

// workerCall is what a fake worker can do.
type workerCall struct {
	h *harness
	launchCall
}

func (w *workerCall) ctx() WorkerContext {
	w.h.t.Helper()
	c, err := w.h.svc.WorkerContext(w.RunID, w.Ticket)
	if err != nil {
		w.h.t.Fatalf("WorkerContext: %v", err)
	}
	return c
}

// commit commits a file named after the ticket in the session's worktree
// and returns the commit.
func (w *workerCall) commit() string {
	return commitIn(w.h.t, w.WorkDir, w.Ticket+".txt", "work of "+w.Ticket+"\n", "work on "+w.Ticket)
}

func (w *workerCall) mergeIntoParent() error {
	_, err := w.h.svc.MergeIntoParent(w.RunID, w.Ticket)
	return err
}

func (w *workerCall) report(result, summary string) {
	w.reportReason(result, "", summary)
}

func (w *workerCall) reportReason(result, reason, summary string) {
	w.h.t.Helper()
	if _, err := w.h.svc.Report(w.RunID, w.Ticket, result, reason, summary); err != nil {
		w.h.t.Fatalf("Report(%s): %v", w.Ticket, err)
	}
}

func (w *workerCall) child(title string) string {
	return w.h.ticket(title, w.Ticket)
}

// release does what the worker's release step does for its position: a
// tree child merges into its target; others only commit.
func (w *workerCall) release() {
	w.h.t.Helper()
	if c := w.ctx(); c.Position == "tree_child" {
		// Take the target branch in first, as the worker's release step
		// does (a no-op when the branch was cut from its tip).
		gitT(w.h.t, w.WorkDir, "merge", "-q", "--no-edit", c.TargetBranch)
		if err := w.mergeIntoParent(); err != nil {
			w.h.t.Fatalf("merge-into-parent %s: %v", w.Ticket, err)
		}
	}
}

// defaultWorker: work commits, releases and reports done; merge-up merges
// the child's branch into the target worktree; finalize reports done.
func defaultWorker(w *workerCall) {
	switch w.Role {
	case autopilot.RoleWork:
		w.commit()
		w.release()
		w.report(autopilot.TicketDone, "did "+w.Ticket)
	case autopilot.RoleMergeUp:
		c := w.ctx()
		gitT(w.h.t, c.MergeWorktree, "merge", "-q", "--no-edit", c.MergeSourceBranch)
		w.report(autopilot.TicketDone, "merged "+c.MergeSourceBranch)
	case autopilot.RoleFinalize:
		w.report(autopilot.TicketDone, "finalized")
	}
}

// silentWorker launches and never reports or does anything.
func silentWorker(w *workerCall) {}

func assertAPICode(t *testing.T, err error, code domain.ErrorCode) {
	t.Helper()
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}
