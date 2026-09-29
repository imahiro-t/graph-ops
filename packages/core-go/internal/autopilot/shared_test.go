package autopilot

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/graph-ops/core-go/internal/displayname"
	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00326: the registry's side of shared runs, against an in-memory
// SharedRuns.

type memShared struct {
	mu      sync.Mutex
	records map[string]domain.AutopilotRunRecord
	deletes []string
	dropped []string
	// dropFail makes Begin's drop of these IDs fail (as an HTTP data
	// source's DELETE can), leaving the record; beginDrops counts the drops
	// Begin was asked for (the begun run not counted).
	dropFail   map[string]error
	beginDrops int
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

func (m *memShared) Begin(projectID string, decide func([]*Run) (*Run, []string, error)) (dropErr, err error) {
	runs, err := m.List(projectID)
	if err != nil {
		return nil, err
	}
	run, drop, err := decide(runs)
	if err != nil {
		return nil, err
	}
	if run != nil {
		if err := m.Save(run); err != nil {
			return nil, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var failed []string
	for _, id := range drop {
		if run != nil && id == run.ID {
			continue
		}
		m.beginDrops++
		if ferr := m.dropFail[id]; ferr != nil {
			failed = append(failed, id+": "+ferr.Error())
			continue
		}
		delete(m.records, id)
		m.dropped = append(m.dropped, id)
	}
	if len(failed) > 0 {
		return errors.New("deleting settled autopilot run records: " + strings.Join(failed, "; ")), nil
	}
	return nil, nil
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
func (unsupportedShared) Begin(string, func([]*Run) (*Run, []string, error)) (dropErr, err error) {
	return nil, ErrSharedRunsUnsupported
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
	LogSharedError(logs.logf, SharedOpList, "listing runs", "showing this machine's runs only", down)
	LogSharedError(logs.logf, SharedOpList, "listing runs", "showing this machine's runs only", down)
	if len(logs.lines) != 1 {
		t.Fatalf("a repeated failure was logged again: %v", logs.lines)
	}
	LogSharedError(logs.logf, SharedOpList, "listing runs", "showing this machine's runs only", nil)
	LogSharedError(logs.logf, SharedOpList, "listing runs", "showing this machine's runs only", nil)
	LogSharedError(logs.logf, SharedOpList, "listing runs", "showing this machine's runs only", down)
	if len(logs.lines) != 3 || !strings.Contains(logs.lines[1], "reachable again") || !strings.Contains(logs.lines[2], "db down") {
		t.Fatalf("failure, recovery, same failure = %v", logs.lines)
	}
}

// Finding 2: the consequence is the caller's, with no fixed suffix.
func TestShared_LogSharedErrorSaysTheCallersConsequence(t *testing.T) {
	ResetSharedErrorLog()
	t.Cleanup(ResetSharedErrorLog)
	logs := &logSink{}
	LogSharedError(logs.logf, SharedOpList, "listing runs", "showing this machine's runs only", errors.New("db down"))
	if len(logs.lines) != 1 || logs.lines[0] != "autopilot: listing runs: db down (showing this machine's runs only)" {
		t.Fatalf("logged %v", logs.lines)
	}
	if strings.Contains(logs.lines[0], "the run goes on") {
		t.Fatal("a listing failure carries a run's consequence")
	}
}

// DFLT-00337: the thinning is per kind of call. Another kind succeeding
// while listing fails says nothing and does not re-arm listing; only
// listing succeeding does.
func TestShared_LogSharedErrorIsPerKind(t *testing.T) {
	ResetSharedErrorLog()
	t.Cleanup(ResetSharedErrorLog)
	logs := &logSink{}
	down := errors.New("db down")
	LogSharedError(logs.logf, SharedOpList, "listing runs", "showing this machine's runs only", down)
	LogSharedError(logs.logf, SharedOpShare, "sharing run-1", "the run goes on", nil)
	LogSharedError(logs.logf, SharedOpRetention, "deleting settled runs", "later", nil)
	if len(logs.lines) != 1 {
		t.Fatalf("another kind's success was logged while listing fails: %v", logs.lines)
	}
	LogSharedError(logs.logf, SharedOpList, "listing runs", "showing this machine's runs only", down)
	if len(logs.lines) != 1 {
		t.Fatalf("another kind's success re-armed listing's thinning: %v", logs.lines)
	}
	// A failure of another kind is reported on its own, even while listing
	// fails with the same error.
	LogSharedError(logs.logf, SharedOpShare, "listing runs", "showing this machine's runs only", down)
	if len(logs.lines) != 2 {
		t.Fatalf("a failure of another kind was thinned out: %v", logs.lines)
	}
	LogSharedError(logs.logf, SharedOpList, "listing runs", "showing this machine's runs only", nil)
	if len(logs.lines) != 3 || logs.lines[2] != "autopilot: the shared autopilot runs are reachable again (listing runs)" {
		t.Fatalf("listing's recovery = %v", logs.lines)
	}
	LogSharedError(logs.logf, SharedOpList, "listing runs", "showing this machine's runs only", nil)
	if len(logs.lines) != 3 {
		t.Fatalf("a recovery was said twice: %v", logs.lines)
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
	// run-old-00 is the one kept. The least recently updated come first
	// (DFLT-00337), so a data source deleting only the first few drops the
	// oldest.
	want := []string{"run-stale", "run-old-01", "run-old-02"}
	if fmt.Sprint(drop) != fmt.Sprint(want) {
		t.Fatalf("drop = %v, want %v", drop, want)
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

// retentionLines is what logs holds about the retention's deletes.
func retentionLines(logs *logSink) []string {
	logs.mu.Lock()
	defer logs.mu.Unlock()
	var out []string
	for _, l := range logs.lines {
		if strings.Contains(l, "deleting settled shared autopilot runs") {
			out = append(out, l)
		}
	}
	return out
}

// sharedRegistryWithSettled is sharedRegistry with KeepSharedSettledRuns+2
// of Bob's settled records (run-bob-00 the oldest) and its log captured.
func sharedRegistryWithSettled(t *testing.T) (*Registry, *memShared, *logSink) {
	t.Helper()
	ResetSharedErrorLog()
	t.Cleanup(ResetSharedErrorLog)
	g, clock, m := sharedRegistry(t)
	logs := &logSink{}
	g.Logf = logs.logf
	for i := 0; i < KeepSharedSettledRuns+2; i++ {
		if err := m.Save(settledRun(fmt.Sprintf("run-bob-%02d", i), clock.Now().Add(-time.Duration(50-i)*time.Hour), "machine-b")); err != nil {
			t.Fatal(err)
		}
	}
	return g, m, logs
}

// DFLT-00337: a retention delete that fails does not fail the start, and is
// logged on one line -- once while it keeps failing the same way, then a
// recovery line when it succeeds. A start with nothing to delete logs
// nothing.
func TestShared_BeginLogsAFailedRetentionDelete(t *testing.T) {
	g, m, logs := sharedRegistryWithSettled(t)
	m.dropFail = map[string]error{"run-bob-00": errors.New("status 500")}

	res, err := sharedBegin(g, "R1")
	if err != nil {
		t.Fatalf("a failed retention delete failed the start: %v", err)
	}
	if _, ok := m.get(res.Run.ID); !ok {
		t.Fatal("the run's shared record was not saved")
	}
	if _, err := g.Load("proj-A", res.Run.ID); err != nil {
		t.Fatalf("the run was not saved locally: %v", err)
	}
	want := "autopilot: deleting settled shared autopilot runs of project proj-A: deleting settled autopilot run records: run-bob-00: status 500 (they are deleted by a later start)"
	if got := retentionLines(logs); len(got) != 1 || got[0] != want {
		t.Fatalf("logged %q, want %q", got, want)
	}

	// The same failure at the next start is thinned out.
	if _, err := sharedBegin(g, "R2"); err != nil {
		t.Fatal(err)
	}
	if got := retentionLines(logs); len(got) != 1 {
		t.Fatalf("the same failure was logged again: %q", got)
	}

	// The delete getting through says so, once.
	m.dropFail = nil
	if _, err := sharedBegin(g, "R3"); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.get("run-bob-00"); ok {
		t.Fatal("run-bob-00 was not deleted once its delete worked")
	}
	got := retentionLines(logs)
	if len(got) != 2 || got[1] != "autopilot: the shared autopilot runs are reachable again (deleting settled shared autopilot runs of project proj-A)" {
		t.Fatalf("logged %q, want one recovery line", got)
	}

	// Nothing left to delete: no DELETE asked for, nothing logged.
	logs.mu.Lock()
	before := len(logs.lines)
	logs.mu.Unlock()
	drops := m.beginDrops
	if _, err := sharedBegin(g, "R4"); err != nil {
		t.Fatal(err)
	}
	if m.beginDrops != drops {
		t.Fatalf("a start within the limit asked for %d drop(s)", m.beginDrops-drops)
	}
	logs.mu.Lock()
	after := logs.lines[before:]
	logs.mu.Unlock()
	if len(after) != 0 {
		t.Fatalf("a start with nothing to delete logged %q", after)
	}
}

// Plan review, round 1, finding 1: when the local save fails after the data
// source's Begin (which already sent the deletes), a failed delete is still
// logged -- on one line, with its consequence -- besides the start failing
// and being compensated.
func TestShared_BeginLogsAFailedRetentionDeleteWhenTheLocalSaveFails(t *testing.T) {
	g, m, logs := sharedRegistryWithSettled(t)
	m.dropFail = map[string]error{"run-bob-00": errors.New("status 500")}
	boom := errors.New("disk full")
	var tried string
	g.failSave = func(run *Run) error { tried = run.ID; return boom }

	if _, err := sharedBegin(g, "R"); !errors.Is(err, boom) {
		t.Fatalf("Begin = %v, want the local save's failure", err)
	}
	if _, ok := m.get(tried); ok || len(m.deletes) != 1 || m.deletes[0] != tried {
		t.Fatalf("not compensated: deletes %v", m.deletes)
	}
	got := retentionLines(logs)
	if len(got) != 1 || strings.ContainsAny(got[0], "\r\n") ||
		!strings.Contains(got[0], "run-bob-00: status 500") || !strings.HasSuffix(got[0], " (they are deleted by a later start)") {
		t.Fatalf("logged %q, want the failed delete on one line with its consequence", got)
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

// DFLT-00336: a starter's name read back from the data source is sanitized
// and capped, whether it came in the column or only in the snapshot.
const craftedName = "\x1b[2J\x1b[31mMallory\u202e\n[graph-engine] ok\r\x07"

const craftedClean = "[2J[31mMallory [graph-engine] ok"

func TestShared_RunFromRecordSanitizesTheStarter(t *testing.T) {
	base := func() domain.AutopilotRunRecord {
		return domain.AutopilotRunRecord{ID: "run-x-1", ProjectID: "p", RootTicketID: "R", Mode: ModeTree, State: RunRunning,
			Heartbeat: "2026-09-01T09:00:00Z", Revision: 1}
	}
	// The column.
	rec := base()
	rec.StartedByName, rec.MachineID = craftedName, "m"
	r, err := RunFromRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	if r.StartedBy == nil || r.StartedBy.Name != craftedClean || r.StartedBy.MachineID != "m" {
		t.Fatalf("column: started by %+v", r.StartedBy)
	}
	// The snapshot only (a record written by hand, columns left empty).
	rec = base()
	rec.Snapshot = []byte(`{"started_by":{"name":` + jsonString(t, craftedName+strings.Repeat("山", 300)) + `,"name_is_fallback":true,"machine_id":""}}`)
	r, err = RunFromRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	if r.StartedBy == nil || !strings.HasPrefix(r.StartedBy.Name, craftedClean+" 山") ||
		utf8.RuneCountInString(r.StartedBy.Name) != displayname.MaxRunes || !r.StartedBy.NameIsFallback {
		t.Fatalf("snapshot: started by %+v", r.StartedBy)
	}
	// A name of control and invisible characters only is unknown; the
	// machine ID stays, so which machine the run belongs to is unchanged.
	rec = base()
	rec.StartedByName, rec.MachineID = "\x1b\n\u200b\u202e", "machine-b"
	rec.Snapshot = []byte(`{"started_by":{"name":"x","name_is_fallback":true,"machine_id":"machine-b"}}`)
	r, err = RunFromRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	if r.StartedBy == nil || r.StartedBy.Name != "" || r.StartedBy.NameIsFallback || r.StartedBy.MachineID != "machine-b" ||
		r.BelongsTo("machine-a") || !r.BelongsTo("machine-b") {
		t.Fatalf("empty once sanitized: started by %+v", r.StartedBy)
	}
	// Ordinary names are left alone.
	for _, name := range []string{"山田 太郎", "taro@mac01"} {
		rec = base()
		rec.StartedByName, rec.MachineID = name, "m"
		if r, _ := RunFromRecord(rec); r.StartedBy.Name != name {
			t.Fatalf("%q became %q", name, r.StartedBy.Name)
		}
	}
}

func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The displays sanitize even a run that did not come through a sanitizing
// read, and show a name that sanitizes to nothing as unknown.
func TestShared_StarterDisplaysAreSanitized(t *testing.T) {
	r := &Run{ID: "run-x-1", StartedBy: &StartedBy{Name: craftedName, MachineID: "m"}}
	if got := r.StartedByName(); got != craftedClean {
		t.Fatalf("StartedByName = %q", got)
	}
	if got := startedBySuffix(r); got != " (started by "+craftedClean+")" {
		t.Fatalf("startedBySuffix = %q", got)
	}
	d := startedByDetails(r, map[string]any{})
	if d["started_by"] != craftedClean || d["name_is_fallback"] != false {
		t.Fatalf("details = %v", d)
	}
	r.StartedBy.NameIsFallback = true
	if got := startedBySuffix(r); got != " (started by "+craftedClean+", name not set)" {
		t.Fatalf("fallback startedBySuffix = %q", got)
	}

	blank := &Run{ID: "run-x-2", StartedBy: &StartedBy{Name: "\x1b\r\n\u2066", NameIsFallback: true, MachineID: "m"}}
	unknown := &Run{ID: "run-x-3"}
	for _, r := range []*Run{blank, unknown} {
		if r.StartedByName() != "" || startedBySuffix(r) != "" || len(startedByDetails(r, map[string]any{})) != 0 {
			t.Fatalf("%s: shown as %q / %q / %v", r.ID, r.StartedByName(), startedBySuffix(r), startedByDetails(r, map[string]any{}))
		}
	}
}

// The column written to the data source is sanitized and capped too.
func TestShared_ToRecordSanitizesTheStarter(t *testing.T) {
	r := &Run{ID: "run-x-1", ProjectID: "p", Mode: ModeTree, RootTicketID: "R", State: RunRunning,
		StartedBy: &StartedBy{Name: craftedName + strings.Repeat("a", 300), MachineID: "m"}, Tickets: map[string]*TicketState{}}
	rec, err := r.ToRecord()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rec.StartedByName, craftedClean+" a") || utf8.RuneCountInString(rec.StartedByName) != displayname.MaxRunes {
		t.Fatalf("started_by_name = %q", rec.StartedByName)
	}
}

// A start refused by another member's run, whose record carries a crafted
// name, says who started it without the escapes and line breaks.
func TestShared_AlreadyRunningShowsASanitizedStarter(t *testing.T) {
	g, clock, m := sharedRegistry(t)
	now := clock.Now()
	m.records["run-bob-1"] = domain.AutopilotRunRecord{ID: "run-bob-1", ProjectID: "proj-A", RootTicketID: "R", Mode: ModeTree,
		State: RunRunning, Heartbeat: formatTime(now), CreatedAt: formatTime(now), UpdatedAt: formatTime(now),
		StartedByName: craftedName, MachineID: "machine-b", Revision: 1}
	_, err := sharedBegin(g, "R")
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != ErrCodeAlreadyRunning {
		t.Fatalf("Begin = %v", err)
	}
	if strings.ContainsAny(apiErr.Message, "\x1b\r\n\x07\u202e") || !strings.Contains(apiErr.Message, "(started by "+craftedClean+")") {
		t.Fatalf("message = %q", apiErr.Message)
	}
	if apiErr.Details["started_by"] != craftedClean {
		t.Fatalf("details = %v", apiErr.Details)
	}
}
