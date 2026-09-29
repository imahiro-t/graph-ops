package autopilot

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00326: the registry's side of shared runs, against an in-memory
// SharedRuns.

type memShared struct {
	mu      sync.Mutex
	records map[string]domain.AutopilotRunRecord
	deletes []string
	dropped []string
}

func newMemShared() *memShared { return &memShared{records: map[string]domain.AutopilotRunRecord{}} }

func (m *memShared) List(projectID string) ([]*Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Run
	for _, rec := range m.records {
		if rec.ProjectID == projectID {
			r, err := RunFromRecord(rec)
			if err != nil {
				return nil, err
			}
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memShared) Begin(projectID string, decide func([]*Run) (*Run, []string, error)) error {
	runs, err := m.List(projectID)
	if err != nil {
		return err
	}
	run, drop, err := decide(runs)
	if err != nil {
		return err
	}
	if run != nil {
		if err := m.Save(run); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range drop {
		if run != nil && id == run.ID {
			continue
		}
		delete(m.records, id)
		m.dropped = append(m.dropped, id)
	}
	return nil
}

func (m *memShared) Save(run *Run) error {
	rec, err := run.ToRecord()
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.records[rec.ID]; ok && rec.Revision <= cur.Revision {
		return nil
	}
	m.records[rec.ID] = rec
	return nil
}

func (m *memShared) Delete(runID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.records, runID)
	m.deletes = append(m.deletes, runID)
	return nil
}

func (m *memShared) get(id string) (domain.AutopilotRunRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[id]
	return rec, ok
}

// alice is the actor sharedBegin starts runs as.
var alice = &StartedBy{Name: "Alice", MachineID: "machine-a"}

func sharedRegistry(t *testing.T) (*Registry, *fakeClock, *memShared) {
	t.Helper()
	g, clock := newRegistry(t)
	m := newMemShared()
	g.Shared = m
	return g, clock, m
}

// sharedBegin is begin as alice.
func sharedBegin(g *Registry, root string) (BeginResult, error) {
	return g.Begin(BeginRequest{RootID: root, ProjectID: "proj-A", RootStatus: domain.TicketTODO, Mode: ModeTree,
		Settings: Defaults(), Descendants: family{}.descendants, Actor: alice})
}

// A new run whose local save fails is removed from the data source again.
func TestShared_BeginCompensatesACreatedRun(t *testing.T) {
	g, _, m := sharedRegistry(t)
	boom := errors.New("disk full")
	var tried string
	g.failSave = func(run *Run) error { tried = run.ID; return boom }
	if _, err := sharedBegin(g, "R"); !errors.Is(err, boom) {
		t.Fatalf("Begin = %v", err)
	}
	if _, ok := m.get(tried); ok || len(m.deletes) != 1 || m.deletes[0] != tried {
		t.Fatalf("records %v, deletes %v", m.records, m.deletes)
	}
}

// A taken-over run whose local save fails is put back in the data source,
// with a revision the stale-copy rule does not ignore.
func TestShared_BeginCompensatesATakenOverRun(t *testing.T) {
	g, _, m := sharedRegistry(t)
	first, err := sharedBegin(g, "R")
	if err != nil {
		t.Fatal(err)
	}
	run := first.Run
	var stopped *Run
	if err := g.WithLock("proj-A", func(tx *Tx) error {
		r, _ := tx.Load(run.ID)
		r.State = RunStopped
		if err := tx.Save(r); err != nil {
			return err
		}
		stopped = r
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Save(stopped); err != nil {
		t.Fatal(err)
	}
	g.failSave = func(*Run) error { return errors.New("disk full") }
	if _, err := sharedBegin(g, "R"); err == nil {
		t.Fatal("Begin should fail")
	}
	rec, ok := m.get(run.ID)
	if !ok || rec.State != RunStopped || rec.Revision <= stopped.Revision+1 {
		t.Fatalf("record after compensation = %+v (stopped at revision %d)", rec, stopped.Revision)
	}
}

// Another machine's stopped run is neither taken over nor blocking; this
// machine's is taken over.
func TestShared_TakeOverOnlyOwnMachine(t *testing.T) {
	g, clock, m := sharedRegistry(t)
	foreign := &Run{ID: "run-foreign-1", ProjectID: "proj-A", Mode: ModeTree, RootTicketID: "R", State: RunStopped,
		CreatedAt: clock.Now(), Heartbeat: clock.Now(), Generation: 1, Tickets: map[string]*TicketState{},
		StartedBy: &StartedBy{Name: "Bob", MachineID: "machine-b"}, Revision: 3}
	if err := m.Save(foreign); err != nil {
		t.Fatal(err)
	}
	res, err := sharedBegin(g, "R")
	if err != nil || !res.Created || res.Run.ID == foreign.ID {
		t.Fatalf("Begin = %+v, %v", res, err)
	}
	if rec, _ := m.get(foreign.ID); rec.Revision != 3 || rec.State != RunStopped {
		t.Fatalf("the foreign record changed: %+v", rec)
	}
	_, err = g.Begin(BeginRequest{RootID: "R", ProjectID: "proj-A", RootStatus: domain.TicketTODO, Mode: ModeTree,
		RunID: foreign.ID, Settings: Defaults(), Descendants: family{}.descendants, Actor: alice})
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != ErrCodeInvalidRunState || apiErr.Details["started_by"] != "Bob" {
		t.Fatalf("--run of a foreign run = %v", err)
	}
}

// Tx.Save outranks the revision on disk, so a run restored from an older
// copy is newer than what it replaces.
func TestShared_SaveOutranksTheDiskRevision(t *testing.T) {
	g, _ := newRegistry(t)
	run := mustBegin(t, g, family{}, "R", ModeTree)
	old := *run
	for i := 0; i < 3; i++ {
		if err := g.WithLock("proj-A", func(tx *Tx) error {
			r, _ := tx.Load(run.ID)
			return tx.Save(r)
		}); err != nil {
			t.Fatal(err)
		}
	}
	disk, _ := g.Load("proj-A", run.ID)
	if err := g.WithLock("proj-A", func(tx *Tx) error { return tx.Save(&old) }); err != nil {
		t.Fatal(err)
	}
	if old.Revision != disk.Revision+1 {
		t.Fatalf("restored revision %d, disk had %d", old.Revision, disk.Revision)
	}
}

// RunFromRecord(ToRecord(r)) keeps what is shared and the starter.
func TestShared_RecordRoundTrip(t *testing.T) {
	now := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	r := &Run{ID: "run-x-1", ProjectID: "p", Mode: ModeTree, RootTicketID: "R", State: RunRunning,
		CreatedAt: now, UpdatedAt: now, Heartbeat: now, Generation: 2, Revision: 7, TerminalTTY: "/dev/ttys001",
		StartedBy: &StartedBy{Name: "taro@mac01", NameIsFallback: true, MachineID: "m"},
		Tickets:   map[string]*TicketState{"R": {ID: "R", Status: TicketLaunched, Worktree: "/wt", Branch: "b"}}, Order: []string{"R"}}
	rec, err := r.ToRecord()
	if err != nil {
		t.Fatal(err)
	}
	if rec.StartedByName != "taro@mac01" || rec.MachineID != "m" || rec.Revision != 7 || rec.Heartbeat != "2026-09-01T09:00:00Z" {
		t.Fatalf("record = %+v", rec)
	}
	back, err := RunFromRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	if back.ID != r.ID || !back.Heartbeat.Equal(now) || back.Generation != 2 || back.TerminalTTY != "" ||
		back.StartedBy == nil || !back.StartedBy.NameIsFallback || back.Ticket("R").Status != TicketLaunched || back.Ticket("R").Worktree != "" {
		t.Fatalf("round trip = %+v", back)
	}
	if r.TerminalTTY != "/dev/ttys001" || r.Ticket("R").Worktree != "/wt" {
		t.Fatal("ToRecord changed the run itself")
	}
}

func TestShared_UnsupportedFallsBackAndWarnsOnce(t *testing.T) {
	ResetSharedUnsupportedWarning()
	t.Cleanup(ResetSharedUnsupportedWarning)
	g, _ := newRegistry(t)
	logs := &logSink{}
	g.Logf = logs.logf
	g.Shared = unsupportedShared{}
	mustBegin(t, g, family{}, "R", ModeTree)
	mustBegin(t, g, family{}, "S", ModeTree)
	n := 0
	for _, l := range logs.lines {
		if strings.Contains(l, "older than 1.2") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("warned %d times: %v", n, logs.lines)
	}
}

type unsupportedShared struct{}

func (unsupportedShared) List(string) ([]*Run, error) { return nil, ErrSharedRunsUnsupported }
func (unsupportedShared) Begin(string, func([]*Run) (*Run, []string, error)) error {
	return ErrSharedRunsUnsupported
}
func (unsupportedShared) Save(*Run) error     { return ErrSharedRunsUnsupported }
func (unsupportedShared) Delete(string) error { return ErrSharedRunsUnsupported }

// Non-functional review, round 1, finding 1: a success re-arms the
// thinning, so the same failure after a recovery is logged again, and the
// recovery itself is said once.
func TestShared_LogSharedErrorIsReArmedBySuccess(t *testing.T) {
	ResetSharedErrorLog()
	t.Cleanup(ResetSharedErrorLog)
	logs := &logSink{}
	down := errors.New("db down")
	LogSharedError(logs.logf, "listing runs", "showing this machine's runs only", down)
	LogSharedError(logs.logf, "listing runs", "showing this machine's runs only", down)
	if len(logs.lines) != 1 {
		t.Fatalf("a repeated failure was logged again: %v", logs.lines)
	}
	LogSharedError(logs.logf, "listing runs", "showing this machine's runs only", nil)
	LogSharedError(logs.logf, "listing runs", "showing this machine's runs only", nil)
	LogSharedError(logs.logf, "listing runs", "showing this machine's runs only", down)
	if len(logs.lines) != 3 || !strings.Contains(logs.lines[1], "reachable again") || !strings.Contains(logs.lines[2], "db down") {
		t.Fatalf("failure, recovery, same failure = %v", logs.lines)
	}
}

// Finding 2: the consequence is the caller's, with no fixed suffix.
func TestShared_LogSharedErrorSaysTheCallersConsequence(t *testing.T) {
	ResetSharedErrorLog()
	t.Cleanup(ResetSharedErrorLog)
	logs := &logSink{}
	LogSharedError(logs.logf, "listing runs", "showing this machine's runs only", errors.New("db down"))
	if len(logs.lines) != 1 || logs.lines[0] != "autopilot: listing runs: db down (showing this machine's runs only)" {
		t.Fatalf("logged %v", logs.lines)
	}
	if strings.Contains(logs.lines[0], "the run goes on") {
		t.Fatal("a listing failure carries a run's consequence")
	}
}

func settledRun(id string, updated time.Time, machine string) *Run {
	return &Run{ID: id, ProjectID: "proj-A", Mode: ModeTree, RootTicketID: "Z", State: RunFinished,
		CreatedAt: updated, UpdatedAt: updated, Heartbeat: updated, Tickets: map[string]*TicketState{},
		StartedBy: &StartedBy{Name: "someone", MachineID: machine}, Revision: 1}
}

// Finding 3: settled records beyond KeepSharedSettledRuns are dropped,
// newest kept, whoever started them; active ones and the begun run never.
func TestShared_SharedRetention(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	var shared []*Run
	for i := 0; i < KeepSharedSettledRuns+3; i++ {
		shared = append(shared, settledRun(fmt.Sprintf("run-old-%02d", i), now.Add(-time.Duration(100-i)*time.Hour), fmt.Sprintf("m-%d", i%3)))
	}
	active := settledRun("run-active", now.Add(-200*time.Hour), "m-x")
	active.State, active.Heartbeat = RunRunning, now
	stale := settledRun("run-stale", now.Add(-300*time.Hour), "m-y")
	stale.State = RunRunning // interrupted: its heartbeat expired long ago
	shared = append(shared, active, stale)
	drop := SharedRetention(shared, "run-old-00", now)
	want := map[string]bool{"run-stale": true, "run-old-01": true, "run-old-02": true} // run-old-00 is the one kept
	if len(drop) != len(want) {
		t.Fatalf("drop = %v", drop)
	}
	for _, id := range drop {
		if !want[id] {
			t.Fatalf("dropped %s (drop %v)", id, drop)
		}
	}
	if SharedRetention(shared[:KeepSharedSettledRuns], "", now) != nil {
		t.Fatal("dropped records within the limit")
	}
}

// A start drops other members' settled records beyond the limit in the same
// data source call, and leaves their active ones alone.
func TestShared_BeginDropsSettledRecordsBeyondTheLimit(t *testing.T) {
	g, clock, m := sharedRegistry(t)
	for i := 0; i < KeepSharedSettledRuns+2; i++ {
		if err := m.Save(settledRun(fmt.Sprintf("run-bob-%02d", i), clock.Now().Add(-time.Duration(50-i)*time.Hour), "machine-b")); err != nil {
			t.Fatal(err)
		}
	}
	bobActive := settledRun("run-bob-active", clock.Now().Add(-100*time.Hour), "machine-b")
	bobActive.State, bobActive.Heartbeat, bobActive.RootTicketID = RunRunning, clock.Now(), "Q"
	if err := m.Save(bobActive); err != nil {
		t.Fatal(err)
	}
	res, err := sharedBegin(g, "R")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.dropped) != 2 || m.dropped[0] == res.Run.ID {
		t.Fatalf("dropped %v", m.dropped)
	}
	for _, id := range []string{"run-bob-00", "run-bob-01"} {
		if _, ok := m.get(id); ok {
			t.Fatalf("%s was kept", id)
		}
	}
	for _, id := range []string{"run-bob-02", "run-bob-active", res.Run.ID} {
		if _, ok := m.get(id); !ok {
			t.Fatalf("%s was dropped", id)
		}
	}
}

// QA review, round 1, finding 1: of two overlapping active runs, the one
// begun earlier is overtaken by the one begun later -- never the reverse,
// and not by a run that does not overlap or is not active.
func TestShared_Overtaker(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	tree := family{"R": {"X", "S"}, "X": {"Y"}}
	mk := func(id, root, mode string, begun time.Time) *Run {
		return &Run{ID: id, ProjectID: "proj-A", Mode: mode, RootTicketID: root, State: RunRunning,
			CreatedAt: begun.Add(-time.Hour), BegunAt: begun, Heartbeat: now, Tickets: map[string]*TicketState{},
			StartedBy: &StartedBy{Name: "Bob", MachineID: "machine-b"}}
	}
	a := mk("run-a", "R", ModeTree, now.Add(-30*time.Minute))
	b := mk("run-b", "X", ModeTicket, now.Add(-5*time.Minute))
	all := []*Run{a, b}
	if o, err := Overtaker(a, all, tree.descendants, now); err != nil || o != b {
		t.Fatalf("Overtaker(a) = %v, %v", o, err)
	}
	if o, _ := Overtaker(b, all, tree.descendants, now); o != nil {
		t.Fatalf("the later run was overtaken by %s", o.ID)
	}
	// Not overlapping: another tree.
	other := mk("run-c", "T", ModeTree, now.Add(-time.Minute))
	if o, _ := Overtaker(a, []*Run{a, other}, tree.descendants, now); o != nil {
		t.Fatal("overtaken by a run that does not overlap")
	}
	// Not active: its heartbeat has expired, or it has finished.
	b.Heartbeat = now.Add(-ActiveThreshold - time.Minute)
	if o, _ := Overtaker(a, all, tree.descendants, now); o != nil {
		t.Fatal("overtaken by an inactive run")
	}
	// A run file from before DFLT-00326 counts from CreatedAt.
	b.Heartbeat, a.BegunAt = now, time.Time{}
	if o, _ := Overtaker(a, all, tree.descendants, now); o != b {
		t.Fatal("a legacy run was not judged by CreatedAt")
	}
	// A tie goes to the higher run ID.
	a.BegunAt, b.BegunAt = now, now
	if o, _ := Overtaker(a, all, tree.descendants, now); o != b {
		t.Fatal("tie: run-a should yield to run-b")
	}
	if o, _ := Overtaker(b, all, tree.descendants, now); o != nil {
		t.Fatal("tie: run-b should not yield")
	}
	a.StopOvertakenBy(b, now)
	if a.State != RunStopped || a.StopReason != StopOvertaken || !strings.Contains(a.StopDetail, "run-b") || !strings.Contains(a.StopDetail, "Bob") {
		t.Fatalf("stopped run = %s %s %q", a.State, a.StopReason, a.StopDetail)
	}
}

// Every start stamps BegunAt, a takeover included.
func TestShared_BeginStampsBegunAt(t *testing.T) {
	g, clock, _ := sharedRegistry(t)
	res, err := sharedBegin(g, "R")
	if err != nil || !res.Run.BegunAt.Equal(clock.Now()) {
		t.Fatalf("created: %v, %v", res.Run.BegunAt, err)
	}
	clock.Advance(ActiveThreshold + time.Minute)
	again, err := sharedBegin(g, "R")
	if err != nil || !again.TookOver || !again.Run.BegunAt.Equal(clock.Now()) || !again.Run.CreatedAt.Equal(res.Run.CreatedAt) {
		t.Fatalf("taken over: %+v, %v", again, err)
	}
}
