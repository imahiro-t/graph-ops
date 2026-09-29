package autopilot

import (
	"errors"
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

func (m *memShared) Begin(projectID string, decide func([]*Run) (*Run, error)) error {
	runs, err := m.List(projectID)
	if err != nil {
		return err
	}
	run, err := decide(runs)
	if err != nil || run == nil {
		return err
	}
	return m.Save(run)
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

func (m *memShared) Delete(projectID, runID string) error {
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

func sharedRegistry(t *testing.T) (*Registry, *fakeClock, *memShared) {
	t.Helper()
	g, clock := newRegistry(t)
	m := newMemShared()
	g.Shared = m
	g.Actor = &StartedBy{Name: "Alice", MachineID: "machine-a"}
	return g, clock, m
}

// A new run whose local save fails is removed from the data source again.
func TestShared_BeginCompensatesACreatedRun(t *testing.T) {
	g, _, m := sharedRegistry(t)
	boom := errors.New("disk full")
	var tried string
	g.failSave = func(run *Run) error { tried = run.ID; return boom }
	if _, err := begin(g, family{}, "R", ModeTree, domain.TicketTODO); !errors.Is(err, boom) {
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
	run := mustBegin(t, g, family{}, "R", ModeTree)
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
	if _, err := begin(g, family{}, "R", ModeTree, domain.TicketTODO); err == nil {
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
	res, err := begin(g, family{}, "R", ModeTree, domain.TicketTODO)
	if err != nil || !res.Created || res.Run.ID == foreign.ID {
		t.Fatalf("Begin = %+v, %v", res, err)
	}
	if rec, _ := m.get(foreign.ID); rec.Revision != 3 || rec.State != RunStopped {
		t.Fatalf("the foreign record changed: %+v", rec)
	}
	_, err = g.Begin(BeginRequest{RootID: "R", ProjectID: "proj-A", RootStatus: domain.TicketTODO, Mode: ModeTree,
		RunID: foreign.ID, Settings: Defaults(), Descendants: family{}.descendants})
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
func (unsupportedShared) Begin(string, func([]*Run) (*Run, error)) error {
	return ErrSharedRunsUnsupported
}
func (unsupportedShared) Save(*Run) error             { return ErrSharedRunsUnsupported }
func (unsupportedShared) Delete(string, string) error { return ErrSharedRunsUnsupported }
