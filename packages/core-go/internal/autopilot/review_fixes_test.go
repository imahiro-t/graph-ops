package autopilot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00142 iteration 1: the review gates' findings on the registry lock,
// the run files' growth and corruption, takeover reclassification, the
// pending set behind the Web UI's "waiting" badge, and ticket ID checks.

// logSink collects Registry.Logf output.
type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logSink) has(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func shortLockTimings(t *testing.T, stale, refresh time.Duration) {
	t.Helper()
	oldStale, oldRefresh := StaleLockAge, lockRefreshInterval
	StaleLockAge, lockRefreshInterval = stale, refresh
	t.Cleanup(func() { StaleLockAge, lockRefreshInterval = oldStale, oldRefresh })
}

func TestLock_HolderRefreshesSoALongHoldIsNeverTakenOver(t *testing.T) {
	shortLockTimings(t, 300*time.Millisecond, 50*time.Millisecond)
	g, _ := newRegistry(t)
	logs := &logSink{}
	g.Logf = logs.logf
	held := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- g.WithLock("proj-A", func(tx *Tx) error {
			close(held)
			// Held for more than three times StaleLockAge, as a slow
			// command would.
			time.Sleep(time.Second)
			return tx.Save(&Run{ID: "run-a", ProjectID: "proj-A", State: RunRunning})
		})
	}()
	<-held
	waiter := &Registry{Root: g.Root, Now: g.Now, LockTimeout: 600 * time.Millisecond, Logf: logs.logf}
	err := waiter.WithLock("proj-A", func(tx *Tx) error { return nil })
	assertCode(t, err, ErrCodeRegistryLockTimed)
	if err := <-done; err != nil {
		t.Fatalf("holder: %v", err)
	}
	if logs.has("stale") {
		t.Fatalf("a refreshed lock was taken for stale: %v", logs.lines)
	}
	lock, _ := g.LockPath("proj-A")
	if _, err := os.Stat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock left behind: %v", err)
	}
}

func TestLock_StaleLockOfAGoneHolderIsRemovedAndLogged(t *testing.T) {
	g, _ := newRegistry(t)
	logs := &logSink{}
	g.Logf = logs.logf
	lock, _ := g.LockPath("proj-A")
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, []byte("99999 2026-01-01T00:00:00Z deadbeef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-StaleLockAge - time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	ran := false
	if err := g.WithLock("proj-A", func(tx *Tx) error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("WithLock = %v (ran %v)", err, ran)
	}
	if !logs.has("removed a stale autopilot registry lock") || !logs.has("deadbeef") {
		t.Fatalf("logs = %v", logs.lines)
	}
}

func TestLock_FreshLockIsNotRemoved(t *testing.T) {
	g, _ := newRegistry(t)
	g.LockTimeout = 100 * time.Millisecond
	lock, _ := g.LockPath("proj-A")
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, []byte("other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertCode(t, g.WithLock("proj-A", func(tx *Tx) error { return nil }), ErrCodeRegistryLockTimed)
	if data, _ := os.ReadFile(lock); string(data) != "other\n" {
		t.Fatalf("lock = %q", data)
	}
}

func TestLock_ALostLockSavesNothingAndIsNotRemoved(t *testing.T) {
	g, _ := newRegistry(t)
	logs := &logSink{}
	g.Logf = logs.logf
	lock, _ := g.LockPath("proj-A")
	err := g.WithLock("proj-A", func(tx *Tx) error {
		// Another process took the lock over (it judged ours stale).
		if err := os.WriteFile(lock, []byte("thief\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return tx.Save(&Run{ID: "run-a", ProjectID: "proj-A", State: RunRunning})
	})
	assertCode(t, err, ErrCodeRegistryLockTimed)
	if r, _ := g.Load("proj-A", "run-a"); r != nil {
		t.Fatal("saved without the lock")
	}
	if data, _ := os.ReadFile(lock); string(data) != "thief\n" {
		t.Fatalf("the new holder's lock was removed: %q", data)
	}
	if !logs.has("taken over by another process") {
		t.Fatalf("logs = %v", logs.lines)
	}
}

func TestList_UnreadableRunIsLoggedAndBlocksAStart(t *testing.T) {
	g, _ := newRegistry(t)
	logs := &logSink{}
	g.Logf = logs.logf
	mustBegin(t, g, nil, "R", ModeTicket)
	dir, _ := g.projectDir("proj-A")
	bad := filepath.Join(dir, "runs", "run-20260901-090000-broken.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	runs, unreadable, err := g.ListChecked("proj-A")
	if err != nil || len(runs) != 1 || len(unreadable) != 1 || unreadable[0].Path != bad {
		t.Fatalf("ListChecked = %d runs, %+v, %v", len(runs), unreadable, err)
	}
	if !logs.has(bad) {
		t.Fatalf("logs = %v", logs.lines)
	}
	// It may be an active run of this very tree: a start is refused.
	_, err = begin(g, nil, "X", ModeTicket, domain.TicketTODO)
	assertCode(t, err, ErrCodeRegistryCorrupt)
}

func TestPrune_KeepsTheNewestSettledRunsAndEveryResumableOne(t *testing.T) {
	g, clock := newRegistry(t)
	// A stopped run that is the newest of its root: resumable, kept.
	stopped := mustBegin(t, g, nil, "S", ModeTree)
	stopped.State = RunStopped
	saveRun(t, g, stopped)
	// A stopped run superseded by a newer run of the same root and mode.
	old := mustBegin(t, g, nil, "T", ModeTicket)
	old.State = RunStopped
	saveRun(t, g, old)
	clock.Advance(time.Second)
	// Taking it over does not create a new run; force a newer one.
	newer := &Run{ID: NewRunID(clock.Now()), ProjectID: "proj-A", Mode: ModeTicket, RootTicketID: "T",
		State: RunFinished, CreatedAt: clock.Now(), Heartbeat: clock.Now(), Tickets: map[string]*TicketState{}}
	saveRun(t, g, newer)
	var finished []string
	for i := 0; i < KeepSettledRuns+3; i++ {
		clock.Advance(time.Second)
		r := mustBegin(t, g, nil, fmt.Sprintf("F%02d", i), ModeTicket)
		r.State = RunFinished
		saveRun(t, g, r)
		finished = append(finished, r.ID)
	}
	clock.Advance(time.Second)
	mustBegin(t, g, nil, "NEW", ModeTicket)
	runs, _ := g.List("proj-A")
	have := map[string]bool{}
	for _, r := range runs {
		have[r.ID] = true
	}
	if !have[stopped.ID] {
		t.Fatal("the resumable stopped run was pruned")
	}
	if have[old.ID] {
		t.Fatal("the superseded stopped run was kept")
	}
	settled := 0
	for _, r := range runs {
		if r.State == RunFinished || r.ID == old.ID {
			settled++
		}
	}
	if settled != KeepSettledRuns {
		t.Fatalf("settled runs kept = %d, want %d", settled, KeepSettledRuns)
	}
	if !have[finished[len(finished)-1]] || have[finished[0]] {
		t.Fatal("pruned the wrong end")
	}
}

func TestTakeOver_ForgetsSkipsDecidedFromSettingsOrTheDB(t *testing.T) {
	run := &Run{ID: "run-x", Mode: ModeTree, RootTicketID: "R", State: RunStopped, Generation: 1,
		Settings: Defaults(), WorkLaunches: 2}
	run.Settings.MaxTickets = 2
	run.addTicket(&TicketState{ID: "R", Status: TicketDone, Branch: "worktree-R", LaunchedGeneration: 1, Counted: true})
	run.addTicket(&TicketState{ID: "A", Status: TicketDone, Target: "R", Branch: "worktree-A", Merge: MergedSubtree, LaunchedGeneration: 1, Counted: true})
	run.addTicket(&TicketState{ID: "B", Status: TicketSkipped, Reason: ReasonLimitTickets, Target: "R"})
	run.addTicket(&TicketState{ID: "P", Status: TicketSkipped, Reason: ReasonParentFailed, SkipCause: "Q"})
	raised := run.Settings
	raised.MaxTickets = 5
	run.TakeOver(time.Now(), raised)
	if run.Ticket("B") != nil || run.Ticket("P") == nil || run.Ticket("A").Status != TicketDone {
		t.Fatalf("tickets = %+v", run.Tickets)
	}
	tree := Tree{
		"R": {ID: "R", Status: domain.TicketDone, Children: []string{"A", "B"}},
		"A": {ID: "A", ParentID: "R", Status: domain.TicketDone},
		"B": {ID: "B", ParentID: "R", Status: domain.TicketTODO},
	}
	a := Next(PlanInput{Run: run, Tree: tree, Now: time.Now()})
	if a.Action != ActionLaunch || a.Ticket != "B" {
		t.Fatalf("next = %+v", a)
	}
	if strings.Join(run.Order, ",") != "R,A,B,P" {
		t.Fatalf("order = %v", run.Order)
	}
}

func TestPending_LeavesOutWhatThePlannerWouldNotLaunch(t *testing.T) {
	settings := Defaults()
	settings.MaxDepth = 2
	settings.MaxTickets = 4
	// R
	// ├─ A (DONE)             skipped, but its child A1 is processed
	// │  └─ A1
	// ├─ B (IN PROGRESS)      elsewhere: B and B1 are not
	// │  └─ B1
	// ├─ C                    failed (no retry): C1 is not
	// │  └─ C1
	// ├─ D
	// │  └─ D1
	// │     └─ D11            deeper than maxDepth
	// └─ E                    beyond maxTickets
	tree := Tree{
		"R":   {ID: "R", Status: domain.TicketInProgress, Children: []string{"A", "B", "C", "D", "E"}},
		"A":   {ID: "A", ParentID: "R", Status: domain.TicketDone, Children: []string{"A1"}},
		"A1":  {ID: "A1", ParentID: "A", Status: domain.TicketTODO},
		"B":   {ID: "B", ParentID: "R", Status: domain.TicketInProgress, Children: []string{"B1"}},
		"B1":  {ID: "B1", ParentID: "B", Status: domain.TicketTODO},
		"C":   {ID: "C", ParentID: "R", Status: domain.TicketInProgress, Children: []string{"C1"}},
		"C1":  {ID: "C1", ParentID: "C", Status: domain.TicketTODO},
		"D":   {ID: "D", ParentID: "R", Status: domain.TicketTODO, Children: []string{"D1"}},
		"D1":  {ID: "D1", ParentID: "D", Status: domain.TicketTODO, Children: []string{"D11"}},
		"D11": {ID: "D11", ParentID: "D1", Status: domain.TicketTODO},
		"E":   {ID: "E", ParentID: "R", Status: domain.TicketTODO},
	}
	run := &Run{ID: "run-x", Mode: ModeTree, RootTicketID: "R", State: RunRunning, Settings: settings, WorkLaunches: 2}
	run.addTicket(&TicketState{ID: "R", Status: TicketDone, Counted: true})
	run.addTicket(&TicketState{ID: "C", Status: TicketFailed, Counted: true, Target: "R"})
	got := Pending(run, tree, map[string]bool{"C": true})
	// Budget 4-2 = 2: A1 and D; D1 and E fall beyond maxTickets.
	if strings.Join(got, ",") != "A1,D" {
		t.Fatalf("pending = %v", got)
	}
	settings.MaxTickets = 10
	run.Settings = settings
	if got := Pending(run, tree, nil); strings.Join(got, ",") != "A1,D,D1,E" {
		t.Fatalf("pending = %v", got)
	}
	run.Tickets["C"].RetryPending = true
	if got := Pending(run, tree, nil); strings.Join(got, ",") != "A1,C,C1,D,D1,E" {
		t.Fatalf("pending with C's retry = %v", got)
	}
	run.State = RunStopped
	if got := Pending(run, tree, nil); len(got) != 0 {
		t.Fatalf("pending of a stopped run = %v", got)
	}
}

func TestValidTicketID(t *testing.T) {
	for _, id := range []string{"DFLT-00142", "GRAP-12", "a.b_c-1"} {
		if !ValidTicketID(id) {
			t.Errorf("%q refused", id)
		}
	}
	for _, id := range []string{"", "../x", "a/b", "-rf", ".hidden", "a..b", "a b", `a\b`, strings.Repeat("a", 129)} {
		if ValidTicketID(id) {
			t.Errorf("%q accepted", id)
		}
	}
}

func TestEnsureWorktree_RefusesATicketIDThatIsNotAPathElement(t *testing.T) {
	repo := t.TempDir()
	_, _, _, err := Git{}.EnsureWorktree(repo, "../../escape", "main")
	assertCode(t, err, domain.ErrCodeValidation)
	if _, err := os.Stat(filepath.Join(repo, ".claude")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("created directories for an invalid ID")
	}
}

func TestStatusPaths_SkipsTheSourceRecordOfARename(t *testing.T) {
	status := "R  new.txt\x00old.txt\x00 M changed.txt\x00?? untracked.txt\x00C  copy.txt\x00orig.txt\x00"
	got := strings.Join(statusPaths(status), ",")
	if got != "changed.txt,copy.txt,new.txt,untracked.txt" {
		t.Fatalf("paths = %s", got)
	}
}
