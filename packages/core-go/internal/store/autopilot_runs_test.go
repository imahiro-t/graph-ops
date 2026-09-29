package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

func runRecord(projectID, id, root string, rev int64) domain.AutopilotRunRecord {
	ts := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	return domain.AutopilotRunRecord{
		ID: id, ProjectID: projectID, RootTicketID: root, Mode: "tree", State: "running",
		Heartbeat: ts, CreatedAt: ts, UpdatedAt: ts, StartedByName: "Alice", MachineID: "m-a",
		Revision: rev, Snapshot: json.RawMessage(fmt.Sprintf(`{"id":%q,"tickets":{"%s":{"status":"queued"}}}`, id, root)),
	}
}

func findRecord(t *testing.T, s AutopilotRunStore, projectID, id string) *domain.AutopilotRunRecord {
	t.Helper()
	recs, err := s.ListAutopilotRuns(projectID)
	if err != nil {
		t.Fatalf("ListAutopilotRuns: %v", err)
	}
	for i := range recs {
		if recs[i].ID == id {
			return &recs[i]
		}
	}
	return nil
}

// exerciseAutopilotRunStore is the contract every backend that shares runs
// meets: save, list, revision-guarded update, delete.
func exerciseAutopilotRunStore(t *testing.T, s AutopilotRunStore, projectID string) {
	t.Helper()
	rec := runRecord(projectID, "run-1", "T-1", 5)
	if err := s.SaveAutopilotRun(rec); err != nil {
		t.Fatalf("SaveAutopilotRun: %v", err)
	}
	got := findRecord(t, s, projectID, "run-1")
	if got == nil {
		t.Fatal("the saved record is not listed")
	}
	if got.StartedByName != "Alice" || got.MachineID != "m-a" || got.Revision != 5 || got.RootTicketID != "T-1" || got.Mode != "tree" {
		t.Fatalf("record read back as %+v", got)
	}
	var snap map[string]any
	if err := json.Unmarshal(got.Snapshot, &snap); err != nil || snap["id"] != "run-1" {
		t.Fatalf("snapshot read back as %s (%v)", got.Snapshot, err)
	}

	// An older copy arriving late does not roll the record back.
	stale := rec
	stale.Revision, stale.State = 4, "stopped"
	if err := s.SaveAutopilotRun(stale); err != nil {
		t.Fatalf("SaveAutopilotRun(stale): %v", err)
	}
	if got := findRecord(t, s, projectID, "run-1"); got.Revision != 5 || got.State != "running" {
		t.Fatalf("a stale copy overwrote the record: %+v", got)
	}
	// A newer one does update it.
	newer := rec
	newer.Revision, newer.State, newer.Heartbeat = 6, "finished", "2026-09-01T09:05:00Z"
	if err := s.SaveAutopilotRun(newer); err != nil {
		t.Fatalf("SaveAutopilotRun(newer): %v", err)
	}
	if got := findRecord(t, s, projectID, "run-1"); got.Revision != 6 || got.State != "finished" || got.Heartbeat != "2026-09-01T09:05:00Z" {
		t.Fatalf("a newer copy was not stored: %+v", got)
	}

	if err := s.DeleteAutopilotRun("run-1"); err != nil {
		t.Fatalf("DeleteAutopilotRun: %v", err)
	}
	if findRecord(t, s, projectID, "run-1") != nil {
		t.Fatal("the deleted record is still listed")
	}
	if err := s.DeleteAutopilotRun("run-1"); err != nil {
		t.Fatalf("deleting a missing record: %v", err)
	}

	// decide failing writes nothing; decide returning a record writes it.
	boom := errors.New("boom")
	if err := s.BeginAutopilotRun(projectID, func([]domain.AutopilotRunRecord) (*domain.AutopilotRunRecord, error) {
		return nil, boom
	}); !errors.Is(err, boom) {
		t.Fatalf("BeginAutopilotRun with a failing decide: %v", err)
	}
	if recs, _ := s.ListAutopilotRuns(projectID); len(recs) != 0 {
		t.Fatalf("a failing decide wrote %d record(s)", len(recs))
	}
	if err := s.BeginAutopilotRun(projectID, func(existing []domain.AutopilotRunRecord) (*domain.AutopilotRunRecord, error) {
		r := runRecord(projectID, "run-2", "T-2", 1)
		return &r, nil
	}); err != nil {
		t.Fatalf("BeginAutopilotRun: %v", err)
	}
	if findRecord(t, s, projectID, "run-2") == nil {
		t.Fatal("the record decide returned was not stored")
	}
}

func TestSQLiteAutopilotRuns(t *testing.T) {
	repo, proj := newTestRepoWithProject(t)
	exerciseAutopilotRunStore(t, repo, proj.ID)
}

func TestSQLiteBeginAutopilotRunUnknownProject(t *testing.T) {
	repo := newTestRepo(t)
	err := repo.BeginAutopilotRun("nope", func([]domain.AutopilotRunRecord) (*domain.AutopilotRunRecord, error) { return nil, nil })
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeProjectNotFound {
		t.Fatalf("got %v, want PROJECT_NOT_FOUND", err)
	}
}

// Deleting the project deletes its run records; deleting the root ticket
// neither fails nor takes the record with it.
func TestSQLiteAutopilotRunsFollowProjectNotTicket(t *testing.T) {
	repo, proj := newTestRepoWithProject(t)
	tk, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "root", Status: domain.TicketTODO})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveAutopilotRun(runRecord(proj.ID, "run-1", tk.ID, 1)); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteTicket(tk.ID); err != nil {
		t.Fatalf("DeleteTicket with a run record rooted at it: %v", err)
	}
	if findRecord(t, repo, proj.ID, "run-1") == nil {
		t.Fatal("deleting the root ticket removed the run record")
	}
	if err := repo.DeleteProject(proj.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM autopilot_runs`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d run record(s) left after deleting the project (%v)", n, err)
	}
}

// Init on a DB from before DFLT-00326 adds the table and keeps the data.
func TestSQLiteInitAddsAutopilotRunsToAnExistingDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	repo, err := NewSQLiteRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Init(); err != nil {
		t.Fatal(err)
	}
	proj, err := repo.CreateProject("Old", "OLD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`DROP TABLE autopilot_runs`); err != nil {
		t.Fatal(err)
	}
	if err := repo.Init(); err != nil {
		t.Fatalf("Init on an existing DB: %v", err)
	}
	if got, err := repo.GetProject(proj.ID); err != nil || got == nil || got.Name != "Old" {
		t.Fatalf("existing data changed: %+v %v", got, err)
	}
	if err := repo.SaveAutopilotRun(runRecord(proj.ID, "run-1", "OLD-00001", 1)); err != nil {
		t.Fatalf("the added table is not usable: %v", err)
	}
}

// startOverlapping is the check the autopilot runs inside BeginAutopilotRun:
// start only when no record of root exists yet.
func startOverlapping(s AutopilotRunStore, projectID, runID, root string) (bool, error) {
	started := false
	err := s.BeginAutopilotRun(projectID, func(existing []domain.AutopilotRunRecord) (*domain.AutopilotRunRecord, error) {
		for _, r := range existing {
			if r.RootTicketID == root {
				return nil, nil
			}
		}
		time.Sleep(time.Millisecond) // widen the window a race would need
		rec := runRecord(projectID, runID, root, 1)
		started = true
		return &rec, nil
	})
	return started, err
}

func raceStarts(t *testing.T, a, b AutopilotRunStore, projectID string, rounds int) {
	t.Helper()
	for i := 0; i < rounds; i++ {
		root := fmt.Sprintf("ROOT-%d", i)
		var wg sync.WaitGroup
		results := make([]bool, 2)
		errs := make([]error, 2)
		for j, s := range []AutopilotRunStore{a, b} {
			wg.Add(1)
			go func(j int, s AutopilotRunStore) {
				defer wg.Done()
				results[j], errs[j] = startOverlapping(s, projectID, fmt.Sprintf("run-%d-%d", i, j), root)
			}(j, s)
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatalf("round %d: %v", i, err)
			}
		}
		if results[0] && results[1] {
			t.Fatalf("round %d: both overlapping starts passed", i)
		}
		if !results[0] && !results[1] {
			t.Fatalf("round %d: neither start passed", i)
		}
	}
}

// Two repositories on the same SQLite file (as two members' processes
// would be): BeginAutopilotRun serializes them, so of two overlapping starts
// at the same moment exactly one passes.
func TestSQLiteBeginAutopilotRunSerializesAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	a, err := NewSQLiteRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Init(); err != nil {
		t.Fatal(err)
	}
	b, err := NewSQLiteRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := a.CreateProject("Shared", "SH")
	if err != nil {
		t.Fatal(err)
	}
	raceStarts(t, a, b, proj.ID, 30)
}

func TestMySQLBeginAutopilotRunSerializes(t *testing.T) {
	a := newTestMySQLRepo(t)
	b, err := NewMySQLRepository(mysqlTestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.db.Close() })
	proj, err := a.CreateProject("Shared", "SH")
	if err != nil {
		t.Fatal(err)
	}
	exerciseAutopilotRunStore(t, a, proj.ID)
	raceStarts(t, a, b, proj.ID, 30)
}

func TestHTTPAutopilotRunsProtocol12(t *testing.T) {
	p, srv := startPlugin(t, testToken)
	repo := openHTTP(t, srv.URL, testToken)
	proj, err := repo.CreateProject("HTTP", "HT")
	if err != nil {
		t.Fatal(err)
	}
	p.ResetRequests()
	exerciseAutopilotRunStore(t, repo, proj.ID)
	seen := map[string]bool{}
	for _, req := range p.Requests() {
		seen[req.Method+" "+req.Path] = true
		if v := req.Header.Get(HTTPDataSourceProtocolHeader); v != "1.2" {
			t.Fatalf("%s %s carried protocol version %q, want 1.2", req.Method, req.Path, v)
		}
	}
	for _, want := range []string{
		"PUT /autopilot-runs/run-1",
		"GET /projects/" + proj.ID + "/autopilot-runs",
		"DELETE /autopilot-runs/run-1",
	} {
		if !seen[want] {
			t.Fatalf("no %s request; saw %v", want, seen)
		}
	}
}

// Against a 1.1 plugin every method says ErrAutopilotRunsUnsupported and
// sends nothing.
func TestHTTPAutopilotRunsUnsupportedBefore12(t *testing.T) {
	p, srv := startPlugin(t, testToken)
	p.Version = "1.1"
	repo := openHTTP(t, srv.URL, testToken)
	p.ResetRequests()
	called := false
	errs := []error{
		func() error { _, err := repo.ListAutopilotRuns("proj"); return err }(),
		repo.BeginAutopilotRun("proj", func([]domain.AutopilotRunRecord) (*domain.AutopilotRunRecord, error) {
			called = true
			return nil, nil
		}),
		repo.SaveAutopilotRun(runRecord("proj", "run-1", "T-1", 1)),
		repo.DeleteAutopilotRun("run-1"),
	}
	for i, err := range errs {
		if !errors.Is(err, ErrAutopilotRunsUnsupported) {
			t.Fatalf("call %d: got %v, want ErrAutopilotRunsUnsupported", i, err)
		}
	}
	if called {
		t.Fatal("decide was called against a 1.1 plugin")
	}
	if reqs := p.Requests(); len(reqs) != 0 {
		t.Fatalf("%d request(s) sent to a 1.1 plugin", len(reqs))
	}
}

// The fake 1.1 plugin itself answers 404 on the new endpoints, like a real
// one that has never heard of them.
func TestFakePlugin11Answers404ForAutopilotRuns(t *testing.T) {
	p, srv := startPlugin(t, "")
	p.Version = "1.1"
	resp, err := http.Get(srv.URL + "/projects/x/autopilot-runs")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", resp.StatusCode)
	}
}
