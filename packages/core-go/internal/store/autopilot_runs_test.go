package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store/httpdatasourcetest"
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
	if dropErr, err := s.BeginAutopilotRun(projectID, func([]domain.AutopilotRunRecord) (*domain.AutopilotRunRecord, []string, error) {
		return nil, nil, boom
	}); !errors.Is(err, boom) || dropErr != nil {
		t.Fatalf("BeginAutopilotRun with a failing decide: %v (dropErr %v)", err, dropErr)
	}
	if recs, _ := s.ListAutopilotRuns(projectID); len(recs) != 0 {
		t.Fatalf("a failing decide wrote %d record(s)", len(recs))
	}
	if dropErr, err := s.BeginAutopilotRun(projectID, func(existing []domain.AutopilotRunRecord) (*domain.AutopilotRunRecord, []string, error) {
		r := runRecord(projectID, "run-2", "T-2", 1)
		return &r, nil, nil
	}); err != nil || dropErr != nil {
		t.Fatalf("BeginAutopilotRun: %v (dropErr %v)", err, dropErr)
	}
	if findRecord(t, s, projectID, "run-2") == nil {
		t.Fatal("the record decide returned was not stored")
	}

	// The records decide drops are deleted along with the save (the
	// retention of settled records); the saved one is never dropped, nor
	// counted against the HTTP data source's per-start limit (the two
	// records to delete are within it there).
	for _, id := range []string{"run-old-1", "run-old-2", "run-kept"} {
		if err := s.SaveAutopilotRun(runRecord(projectID, id, "T-9", 1)); err != nil {
			t.Fatal(err)
		}
	}
	if dropErr, err := s.BeginAutopilotRun(projectID, func(existing []domain.AutopilotRunRecord) (*domain.AutopilotRunRecord, []string, error) {
		r := runRecord(projectID, "run-3", "T-3", 1)
		return &r, []string{"run-old-1", "run-3", "run-old-2"}, nil
	}); err != nil || dropErr != nil {
		t.Fatalf("BeginAutopilotRun with drops: %v (dropErr %v)", err, dropErr)
	}
	// Dropping a record that is not there is no failure either.
	if dropErr, err := s.BeginAutopilotRun(projectID, func(existing []domain.AutopilotRunRecord) (*domain.AutopilotRunRecord, []string, error) {
		return nil, []string{"run-missing"}, nil
	}); err != nil || dropErr != nil {
		t.Fatalf("BeginAutopilotRun dropping a missing record: %v (dropErr %v)", err, dropErr)
	}
	if findRecord(t, s, projectID, "run-old-1") != nil || findRecord(t, s, projectID, "run-old-2") != nil {
		t.Fatal("a dropped record is still listed")
	}
	if findRecord(t, s, projectID, "run-3") == nil || findRecord(t, s, projectID, "run-kept") == nil {
		t.Fatal("a record that was not to be dropped is gone")
	}
}

func TestSQLiteAutopilotRuns(t *testing.T) {
	repo, proj := newTestRepoWithProject(t)
	exerciseAutopilotRunStore(t, repo, proj.ID)
}

func TestSQLiteBeginAutopilotRunUnknownProject(t *testing.T) {
	repo := newTestRepo(t)
	_, err := repo.BeginAutopilotRun("nope", func([]domain.AutopilotRunRecord) (*domain.AutopilotRunRecord, []string, error) {
		return nil, nil, nil
	})
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
	_, err := s.BeginAutopilotRun(projectID, func(existing []domain.AutopilotRunRecord) (*domain.AutopilotRunRecord, []string, error) {
		for _, r := range existing {
			if r.RootTicketID == root {
				return nil, nil, nil
			}
		}
		time.Sleep(time.Millisecond) // widen the window a race would need
		rec := runRecord(projectID, runID, root, 1)
		started = true
		return &rec, nil, nil
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
		func() error {
			_, err := repo.BeginAutopilotRun("proj", func([]domain.AutopilotRunRecord) (*domain.AutopilotRunRecord, []string, error) {
				called = true
				return nil, nil, nil
			})
			return err
		}(),
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

// failingDeletes wraps the reference plugin: a DELETE /autopilot-runs/{id}
// of an ID in fail answers 500 with body (and deletes nothing), and every
// such DELETE is recorded in order.
type failingDeletes struct {
	next http.Handler
	body string

	mu      sync.Mutex
	fail    map[string]bool
	deletes []string
}

func (f *failingDeletes) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if id, ok := strings.CutPrefix(r.URL.Path, "/autopilot-runs/"); ok && r.Method == http.MethodDelete {
		f.mu.Lock()
		f.deletes = append(f.deletes, id)
		fail := f.fail[id]
		f.mu.Unlock()
		if fail {
			http.Error(w, f.body, http.StatusInternalServerError)
			return
		}
	}
	f.next.ServeHTTP(w, r)
}

func (f *failingDeletes) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deletes...)
}

// startFailingDeletes opens an HTTP repository against the reference plugin
// behind failingDeletes, with a project and the settled records ids saved.
func startFailingDeletes(t *testing.T, body string, ids ...string) (*HTTPRepository, *failingDeletes, string) {
	t.Helper()
	f := &failingDeletes{next: httpdatasourcetest.New(testToken), body: body, fail: map[string]bool{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	repo := openHTTP(t, srv.URL, testToken)
	proj, err := repo.CreateProject("HTTP", "HT")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := repo.SaveAutopilotRun(runRecord(proj.ID, id, "T-9", 1)); err != nil {
			t.Fatal(err)
		}
	}
	return repo, f, proj.ID
}

func beginDropping(repo *HTTPRepository, projectID, saveID string, drop ...string) (dropErr, err error) {
	return repo.BeginAutopilotRun(projectID, func([]domain.AutopilotRunRecord) (*domain.AutopilotRunRecord, []string, error) {
		rec := runRecord(projectID, saveID, "T-1", 1)
		return &rec, drop, nil
	})
}

// A DELETE that fails does not fail the start -- the record is saved -- but
// comes back in dropErr, naming the record, on one line (DFLT-00337).
func TestHTTPBeginAutopilotRunReportsAFailedDrop(t *testing.T) {
	repo, f, projectID := startFailingDeletes(t, "", "run-old-1")
	f.fail["run-old-1"] = true
	dropErr, err := beginDropping(repo, projectID, "run-new", "run-old-1")
	if err != nil {
		t.Fatalf("a failed DELETE failed the start: %v", err)
	}
	if dropErr == nil || !strings.HasPrefix(dropErr.Error(), "run-old-1: ") || strings.ContainsAny(dropErr.Error(), "\r\n") {
		t.Fatalf("dropErr = %v, want one line starting with run-old-1", dropErr)
	}
	if findRecord(t, repo, projectID, "run-new") == nil {
		t.Fatal("the record was not saved (no PUT)")
	}
	if findRecord(t, repo, projectID, "run-old-1") == nil {
		t.Fatal("the record whose DELETE failed is gone")
	}
}

// An error body spanning lines still makes a one-line dropErr.
func TestHTTPBeginAutopilotRunDropErrorIsOneLine(t *testing.T) {
	repo, f, projectID := startFailingDeletes(t, "upstream failed\r\nat line 2\nand 3", "run-old-1")
	f.fail["run-old-1"] = true
	dropErr, err := beginDropping(repo, projectID, "run-new", "run-old-1")
	if err != nil || dropErr == nil {
		t.Fatalf("got err %v, dropErr %v; want only a dropErr", err, dropErr)
	}
	if msg := dropErr.Error(); strings.ContainsAny(msg, "\r\n") || !strings.Contains(msg, "upstream failed at line 2 and 3") {
		t.Fatalf("dropErr = %q, want the body on one line", msg)
	}
}

// At most httpAutopilotRunDropLimit DELETEs go per start, the first ones of
// drop; the rest are left for a later start and are no failure.
func TestHTTPBeginAutopilotRunDropsAtMostTheLimit(t *testing.T) {
	ids := []string{"run-old-1", "run-old-2", "run-old-3", "run-old-4", "run-old-5"}
	repo, f, projectID := startFailingDeletes(t, "", ids...)
	dropErr, err := beginDropping(repo, projectID, "run-new", ids...)
	if err != nil || dropErr != nil {
		t.Fatalf("got err %v, dropErr %v", err, dropErr)
	}
	if got := f.sent(); len(got) != httpAutopilotRunDropLimit || got[0] != "run-old-1" || got[1] != "run-old-2" {
		t.Fatalf("DELETEs sent: %v, want the first %d of drop", got, httpAutopilotRunDropLimit)
	}
	for i, id := range ids {
		if gone := findRecord(t, repo, projectID, id) == nil; gone != (i < httpAutopilotRunDropLimit) {
			t.Fatalf("%s gone = %v", id, gone)
		}
	}
}

// A failed DELETE does not stop the next one; when both fail, dropErr names
// both on one line and still unwraps to each error.
func TestHTTPBeginAutopilotRunGoesOnAfterAFailedDrop(t *testing.T) {
	repo, f, projectID := startFailingDeletes(t, "", "run-old-1", "run-old-2")
	f.fail["run-old-1"] = true
	dropErr, err := beginDropping(repo, projectID, "run-new", "run-old-1", "run-old-2")
	if err != nil || dropErr == nil {
		t.Fatalf("got err %v, dropErr %v; want only a dropErr", err, dropErr)
	}
	if got := f.sent(); len(got) != 2 || got[1] != "run-old-2" {
		t.Fatalf("DELETEs sent: %v, want the second one after the failure", got)
	}
	if findRecord(t, repo, projectID, "run-old-2") != nil {
		t.Fatal("run-old-2 was not deleted after run-old-1's DELETE failed")
	}

	if err := repo.SaveAutopilotRun(runRecord(projectID, "run-old-2", "T-9", 1)); err != nil {
		t.Fatal(err)
	}
	f.fail["run-old-2"] = true
	dropErr, err = beginDropping(repo, projectID, "run-new-2", "run-old-1", "run-old-2")
	if err != nil || dropErr == nil {
		t.Fatalf("got err %v, dropErr %v; want only a dropErr", err, dropErr)
	}
	msg := dropErr.Error()
	if strings.ContainsAny(msg, "\r\n") || !strings.HasPrefix(msg, "run-old-1: ") || !strings.Contains(msg, "; run-old-2: ") {
		t.Fatalf("dropErr = %q, want both runs on one line, separated by \"; \"", msg)
	}
	var status *httpStatusError
	if !errors.As(dropErr, &status) || status.status != http.StatusInternalServerError {
		t.Fatalf("dropErr does not unwrap to the DELETE's error: %#v", dropErr)
	}
	if !errors.Is(dropErr, dropErr.(runDropErrors)[1].err) {
		t.Fatal("errors.Is does not reach the second DELETE's error")
	}
}

// The record being saved is never deleted, nor counted against the limit.
func TestHTTPBeginAutopilotRunSkipsTheSavedRecord(t *testing.T) {
	repo, f, projectID := startFailingDeletes(t, "", "run-old-1", "run-old-2")
	dropErr, err := beginDropping(repo, projectID, "run-new", "run-new", "run-old-1", "run-old-2")
	if err != nil || dropErr != nil {
		t.Fatalf("got err %v, dropErr %v", err, dropErr)
	}
	if got := f.sent(); len(got) != 2 || got[0] != "run-old-1" || got[1] != "run-old-2" {
		t.Fatalf("DELETEs sent: %v, want run-old-1 and run-old-2 only", got)
	}
	if findRecord(t, repo, projectID, "run-new") == nil {
		t.Fatal("the saved record was deleted")
	}
}
