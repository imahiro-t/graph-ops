package autopilot

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00142 phase 3: the run registry and the one start decision (S4/S5,
// double-start refusal).

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)}
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

func newRegistry(t *testing.T) (*Registry, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	return &Registry{Root: t.TempDir(), Now: clock.Now}, clock
}

// family is a tiny parent->children map for Descendants.
type family map[string][]string

func (f family) descendants(id string) ([]string, error) {
	var out []string
	for _, c := range f[id] {
		out = append(out, c)
		d, _ := f.descendants(c)
		out = append(out, d...)
	}
	return out, nil
}

func begin(g *Registry, f family, root, mode string, status domain.TicketStatus) (BeginResult, error) {
	return g.Begin(BeginRequest{RootID: root, ProjectID: "proj-A", RootStatus: status, Mode: mode,
		Settings: Defaults(), Descendants: f.descendants})
}

func mustBegin(t *testing.T, g *Registry, f family, root, mode string) *Run {
	t.Helper()
	res, err := begin(g, f, root, mode, domain.TicketTODO)
	if err != nil {
		t.Fatalf("Begin(%s, %s): %v", root, mode, err)
	}
	return res.Run
}

func saveRun(t *testing.T, g *Registry, run *Run) {
	t.Helper()
	if err := g.WithLock(run.ProjectID, func(tx *Tx) error { return tx.Save(run) }); err != nil {
		t.Fatal(err)
	}
}

func TestRegistry_BeginCreatesRunFile(t *testing.T) {
	g, _ := newRegistry(t)
	run := mustBegin(t, g, nil, "R", ModeTree)
	if run.Mode != ModeTree || run.RootTicketID != "R" || run.State != RunRunning || run.Generation != 1 {
		t.Fatalf("run = %+v", run)
	}
	if _, err := os.Stat(filepath.Join(g.Root, "proj-A", "runs", run.ID+".json")); err != nil {
		t.Fatalf("run file: %v", err)
	}
	if !ValidRunID(run.ID) {
		t.Fatalf("run id %q", run.ID)
	}
	if proj, _ := g.FindProject(run.ID); proj != "proj-A" {
		t.Fatalf("FindProject = %q", proj)
	}
}

func TestRegistry_RefusesOverlappingActiveRuns(t *testing.T) {
	f := family{"R": {"C"}}
	t.Run("ticket inside an active tree", func(t *testing.T) {
		g, _ := newRegistry(t)
		mustBegin(t, g, f, "R", ModeTree)
		_, err := begin(g, f, "C", ModeTicket, domain.TicketTODO)
		assertCode(t, err, ErrCodeAlreadyRunning)
		runs, _ := g.List("proj-A")
		if len(runs) != 1 {
			t.Fatalf("runs = %d", len(runs))
		}
	})
	t.Run("tree containing an active run's root", func(t *testing.T) {
		g, _ := newRegistry(t)
		mustBegin(t, g, f, "C", ModeTicket)
		_, err := begin(g, f, "R", ModeTree, domain.TicketTODO)
		assertCode(t, err, ErrCodeAlreadyRunning)
	})
	t.Run("same tree twice", func(t *testing.T) {
		g, _ := newRegistry(t)
		mustBegin(t, g, f, "R", ModeTree)
		_, err := begin(g, f, "R", ModeTree, domain.TicketTODO)
		assertCode(t, err, ErrCodeAlreadyRunning)
	})
	t.Run("a ticket run does not own the children", func(t *testing.T) {
		g, _ := newRegistry(t)
		mustBegin(t, g, f, "R", ModeTicket)
		mustBegin(t, g, f, "C", ModeTicket)
	})
}

func TestRegistry_ConcurrentBeginOnlyOneWins(t *testing.T) {
	g, _ := newRegistry(t)
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Separate Registry values, as separate processes would have.
			g2 := &Registry{Root: g.Root, Now: g.Now}
			_, errs[i] = begin(g2, nil, "R", ModeTree, domain.TicketTODO)
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			assertCode(t, err, ErrCodeAlreadyRunning)
		}
	}
	if ok != 1 {
		t.Fatalf("%d starts succeeded, want exactly 1", ok)
	}
}

func TestRegistry_OtherProjectIsUntouched(t *testing.T) {
	g, _ := newRegistry(t)
	mustBegin(t, g, nil, "R1", ModeTree)
	dirA := filepath.Join(g.Root, "proj-A")
	before := snapshotDir(t, dirA)
	res, err := g.Begin(BeginRequest{RootID: "R2", ProjectID: "proj-B", RootStatus: domain.TicketTODO, Mode: ModeTree, Settings: Defaults()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(g.Root, "proj-B", "runs", res.Run.ID+".json")); err != nil {
		t.Fatal(err)
	}
	if after := snapshotDir(t, dirA); after != before {
		t.Fatalf("proj-A's registry changed:\n%s\n---\n%s", before, after)
	}
}

func snapshotDir(t *testing.T, dir string) string {
	t.Helper()
	out := ""
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			data, _ := os.ReadFile(p)
			out += p + "\n" + string(data) + "\n"
		}
		return nil
	})
	return out
}

func TestRegistry_TakesOverInterruptedRun(t *testing.T) {
	g, clock := newRegistry(t)
	run := mustBegin(t, g, nil, "R", ModeTree)
	clock.Advance(11 * time.Minute)
	res, err := begin(g, nil, "R", ModeTree, domain.TicketTODO)
	if err != nil {
		t.Fatal(err)
	}
	if !res.TookOver || res.Run.ID != run.ID || res.Run.Generation != 2 {
		t.Fatalf("res = %+v", res)
	}
	if runs, _ := g.List("proj-A"); len(runs) != 1 {
		t.Fatalf("runs = %d", len(runs))
	}
}

func TestRegistry_StoppedFinishedAndModeRules(t *testing.T) {
	t.Run("stopped is taken over, done kept, DB/limit skips forgotten, stop kept as history", func(t *testing.T) {
		g, _ := newRegistry(t)
		run := mustBegin(t, g, nil, "R", ModeTree)
		run.addTicket(&TicketState{ID: "R", Status: TicketDone})
		run.addTicket(&TicketState{ID: "B", Status: TicketSkipped, Reason: ReasonAlreadyDone})
		run.addTicket(&TicketState{ID: "A", Status: TicketFailed, FailedRole: RoleWork})
		run.stop(g.now(), StopTicketFailed, "A", "A failed")
		saveRun(t, g, run)
		// The root being DONE does not refuse a takeover (S4).
		res, err := begin(g, nil, "R", ModeTree, domain.TicketDone)
		if err != nil {
			t.Fatal(err)
		}
		r := res.Run
		if !res.TookOver || r.ID != run.ID || r.State != RunRunning || r.StopReason != "" || len(r.Stops) != 1 {
			t.Fatalf("run = %+v", r)
		}
		// B's already_done skip is decided again by the planner (QA review
		// 3); its place in Order is kept.
		if r.Tickets["R"].Status != TicketDone || r.Tickets["B"] != nil || !r.Tickets["A"].RetryPending {
			t.Fatalf("tickets = %+v %+v %+v", r.Tickets["R"], r.Tickets["B"], r.Tickets["A"])
		}
		if len(r.Order) != 3 || r.Order[1] != "B" {
			t.Fatalf("order = %v", r.Order)
		}
	})
	t.Run("finished is not taken over", func(t *testing.T) {
		g, _ := newRegistry(t)
		run := mustBegin(t, g, nil, "R", ModeTicket)
		run.State = RunFinished
		saveRun(t, g, run)
		res, err := begin(g, nil, "R", ModeTicket, domain.TicketInProgress)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Created || res.Run.ID == run.ID {
			t.Fatalf("res = %+v", res)
		}
		old, _ := g.Load("proj-A", run.ID)
		if old == nil || old.State != RunFinished {
			t.Fatalf("old run = %+v", old)
		}
	})
	t.Run("finished run and a DONE root is refused", func(t *testing.T) {
		g, _ := newRegistry(t)
		run := mustBegin(t, g, nil, "R", ModeTree)
		run.State = RunFinished
		saveRun(t, g, run)
		_, err := begin(g, nil, "R", ModeTree, domain.TicketDone)
		assertCode(t, err, ErrCodeRootFinished)
	})
	t.Run("another mode is a new run", func(t *testing.T) {
		g, _ := newRegistry(t)
		run := mustBegin(t, g, nil, "R", ModeTicket)
		run.stop(g.now(), StopTicketFailed, "R", "")
		saveRun(t, g, run)
		res, err := begin(g, nil, "R", ModeTree, domain.TicketTODO)
		if err != nil || !res.Created || res.Run.ID == run.ID {
			t.Fatalf("res = %+v, err = %v", res, err)
		}
	})
}

func TestRegistry_RefusesFinishedRoot(t *testing.T) {
	for _, status := range []domain.TicketStatus{domain.TicketDone, domain.TicketClosed} {
		for _, mode := range []string{ModeTicket, ModeTree} {
			g, _ := newRegistry(t)
			_, err := begin(g, family{"R": {"C"}}, "R", mode, status)
			assertCode(t, err, ErrCodeRootFinished)
			if runs, _ := g.List("proj-A"); len(runs) != 0 {
				t.Fatalf("%s/%s: a run was created", status, mode)
			}
		}
	}
}

func TestRegistry_ReserveAdoptAndCancel(t *testing.T) {
	g, _ := newRegistry(t)
	res, err := g.Begin(BeginRequest{RootID: "R", ProjectID: "proj-A", RootStatus: domain.TicketTODO, Mode: ModeTree, Reserve: true, Settings: Defaults()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Run.State != RunStarting {
		t.Fatalf("state = %s", res.Run.State)
	}
	// A reserved run is active: another start is refused...
	_, err = begin(g, nil, "R", ModeTree, domain.TicketTODO)
	assertCode(t, err, ErrCodeAlreadyRunning)
	// ...but adopting it with --run is not.
	adopted, err := g.Begin(BeginRequest{RootID: "R", ProjectID: "proj-A", RootStatus: domain.TicketTODO, Mode: ModeTree, RunID: res.Run.ID, Settings: Defaults()})
	if err != nil || !adopted.Adopted || adopted.Run.State != RunRunning || adopted.Run.Reservation != nil {
		t.Fatalf("adopt: %+v, %v", adopted, err)
	}

	// Cancelling a reservation that created the run deletes it.
	g2, _ := newRegistry(t)
	res2, _ := g2.Begin(BeginRequest{RootID: "R", ProjectID: "proj-A", RootStatus: domain.TicketTODO, Mode: ModeTree, Reserve: true, Settings: Defaults()})
	if err := g2.CancelReservation("proj-A", res2.Run.ID); err != nil {
		t.Fatal(err)
	}
	if runs, _ := g2.List("proj-A"); len(runs) != 0 {
		t.Fatalf("reservation not removed")
	}
	if _, err := begin(g2, nil, "R", ModeTree, domain.TicketTODO); err != nil {
		t.Fatalf("start after cancel: %v", err)
	}

	// Cancelling a reservation that took a stopped run over puts it back.
	g3, _ := newRegistry(t)
	stopped := mustBegin(t, g3, nil, "R", ModeTree)
	stopped.stop(g3.now(), StopTicketFailed, "A", "x")
	saveRun(t, g3, stopped)
	res3, err := g3.Begin(BeginRequest{RootID: "R", ProjectID: "proj-A", RootStatus: domain.TicketDone, Mode: ModeTree, Reserve: true, Settings: Defaults()})
	if err != nil || !res3.TookOver || res3.Run.ID != stopped.ID {
		t.Fatalf("reserve takeover: %+v %v", res3, err)
	}
	if err := g3.CancelReservation("proj-A", stopped.ID); err != nil {
		t.Fatal(err)
	}
	back, _ := g3.Load("proj-A", stopped.ID)
	if back.State != RunStopped || back.Generation != 1 || back.StopReason != StopTicketFailed {
		t.Fatalf("restored = %+v", back)
	}
}

func TestRegistry_RejectsBadIDs(t *testing.T) {
	g, _ := newRegistry(t)
	if _, err := g.Load("proj-A", "../../etc/passwd"); err == nil {
		t.Fatal("path traversal run id accepted")
	}
	if _, err := g.List("../x"); err == nil {
		t.Fatal("path traversal project id accepted")
	}
}

func TestTruncateSummary(t *testing.T) {
	long := ""
	for i := 0; i < 10; i++ {
		long += "line " + string(rune('a'+i)) + " " + string(make([]byte, 0)) + stringsRepeat("x", 200) + "\n"
	}
	got := TruncateSummary(long)
	if n := len([]rune(got)); n > SummaryMaxChars {
		t.Fatalf("chars = %d", n)
	}
	if lines := len(splitLines(got)); lines > SummaryMaxLines {
		t.Fatalf("lines = %d", lines)
	}
	if TruncateSummary("a\nb") != "a\nb" {
		t.Fatal("short summary changed")
	}
}

func stringsRepeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}
