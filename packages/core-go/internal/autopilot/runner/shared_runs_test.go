package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
	"github.com/graph-ops/core-go/internal/store/httpdatasourcetest"
)

// DFLT-00326: two members -- two Services with their own HOME (their own
// machine ID and local registry) -- sharing one data source.

type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logSink) count(substr string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range l.lines {
		if strings.Contains(line, substr) {
			n++
		}
	}
	return n
}

// memberHome makes a HOME whose config.json names the member (myName "" for
// none).
func memberHome(t *testing.T, myName string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".graph-ops")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(map[string]string{"myName": myName})
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// memberService is a Service for one member on repo: its own HOME and
// registry, the harness's clock and settings.
func memberService(h *harness, repo store.GraphRepository, myName string, logs *logSink) *Service {
	home := memberHome(h.t, myName)
	svc := &Service{
		Repo:         repo,
		HomeDir:      home,
		Registry:     &autopilot.Registry{Root: RegistryRoot(home)},
		Launcher:     LauncherFunc(h.launch),
		Settings:     func(string) (autopilot.Settings, error) { return h.settings, nil },
		LocalPath:    func(string) string { return h.gitRepo },
		Now:          h.clock.Now,
		PollInterval: time.Millisecond,
		Sleep:        func(time.Duration) {},
		ResolveActor: IdentityActor(home),
	}
	if logs != nil {
		svc.Logf = logs.logf
		svc.Registry.Logf = logs.logf
	}
	if rs, ok := repo.(store.AutopilotRunStore); ok {
		svc.Registry.Shared = StoreSharedRuns{Store: rs, Logf: svc.Logf}
	}
	return svc
}

func (h *harness) next(runID string) NextResult {
	h.t.Helper()
	res, err := h.svc.Next(runID)
	if err != nil {
		h.t.Fatalf("Next: %v", err)
	}
	return res
}

func (h *harness) launch1(runID, ticketID string) {
	h.t.Helper()
	if _, err := h.svc.Launch(runID, ticketID, autopilot.RoleWork); err != nil {
		h.t.Fatalf("Launch(%s): %v", ticketID, err)
	}
}

type sharedTree struct {
	R, X, Y, S, T, U string
}

// newSharedHarness is a harness with the tree R -> X -> Y, R -> S and the
// separate tree T -> U, and members A ("Alice") and B ("Bob") on its DB.
func newSharedHarness(t *testing.T) (*harness, sharedTree, *Service, *Service) {
	h := newHarness(t)
	var tr sharedTree
	tr.R = h.ticket("R", "")
	tr.X = h.ticket("X", tr.R)
	tr.Y = h.ticket("Y", tr.X)
	tr.S = h.ticket("S", tr.R)
	tr.T = h.ticket("T", "")
	tr.U = h.ticket("U", tr.T)
	a := memberService(h, h.repo, "Alice", nil)
	b := memberService(h, h.repo, "Bob", nil)
	return h, tr, a, b
}

func sharedRecord(t *testing.T, repo store.GraphRepository, projectID, runID string) *domain.AutopilotRunRecord {
	t.Helper()
	recs, err := repo.(store.AutopilotRunStore).ListAutopilotRuns(projectID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range recs {
		if recs[i].ID == runID {
			return &recs[i]
		}
	}
	return nil
}

func sharedRecords(t *testing.T, repo store.GraphRepository, projectID string) []domain.AutopilotRunRecord {
	t.Helper()
	recs, err := repo.(store.AutopilotRunStore).ListAutopilotRuns(projectID)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func localRuns(t *testing.T, svc *Service, projectID string) []*autopilot.Run {
	t.Helper()
	runs, err := svc.Registry.List(projectID)
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

func localRun(t *testing.T, svc *Service, projectID, runID string) *autopilot.Run {
	t.Helper()
	run, err := svc.Registry.Load(projectID, runID)
	if err != nil || run == nil {
		t.Fatalf("local run %s: %v", runID, err)
	}
	return run
}

func mustStart(t *testing.T, svc *Service, id, mode string) StartResult {
	t.Helper()
	res, err := svc.Start(id, mode, "", false)
	if err != nil {
		t.Fatalf("Start(%s, %s): %v", id, mode, err)
	}
	return res
}

func wantAPIError(t *testing.T, err error, code domain.ErrorCode) *domain.APIError {
	t.Helper()
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
	return apiErr
}

func wantRefusedBy(t *testing.T, err error, name string) {
	t.Helper()
	apiErr := wantAPIError(t, err, autopilot.ErrCodeAlreadyRunning)
	if apiErr.Details["started_by"] != name || !strings.Contains(apiErr.Message, name) {
		t.Fatalf("refusal does not name %s: %s %v", name, apiErr.Message, apiErr.Details)
	}
}

// modifyRun rewrites a member's local run and shares the result, as a
// command of that member would.
func modifyRun(t *testing.T, svc *Service, projectID, runID string, fn func(run *autopilot.Run)) {
	t.Helper()
	if err := svc.withRunIn(projectID, runID, func(tx *autopilot.Tx, run *autopilot.Run) error {
		fn(run)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Completion criterion 1: A's run reaches the DB with its starter, and B
// reads it; what only means something on A's machine stays there.
func TestSharedRuns_RunIsSharedWithoutLocalOnlyFields(t *testing.T) {
	h, tr, a, b := newSharedHarness(t)
	res := mustStart(t, a, tr.R, autopilot.ModeTree)
	rec := sharedRecord(t, h.repo, h.projectID, res.RunID)
	if rec == nil {
		t.Fatal("A's run is not in the DB")
	}
	actorA, _ := a.actor()
	if rec.RootTicketID != tr.R || rec.Mode != autopilot.ModeTree || rec.State != autopilot.RunRunning ||
		rec.StartedByName != "Alice" || rec.MachineID != actorA.MachineID || rec.Heartbeat == "" {
		t.Fatalf("record = %+v", rec)
	}
	// B reads it through its own Service.
	shared, err := b.Registry.Shared.List(h.projectID)
	if err != nil || len(shared) != 1 || shared[0].ID != res.RunID {
		t.Fatalf("B's listing = %+v, %v", shared, err)
	}

	// Let the run launch R: it then has a worktree, branch and fingerprints.
	h.svc = a
	act := h.next(res.RunID)
	if act.Action.Action != autopilot.ActionLaunch {
		t.Fatalf("action = %+v", act)
	}
	modifyRun(t, a, h.projectID, res.RunID, func(run *autopilot.Run) {
		run.TerminalTTY, run.TerminalTabDisabled = "/dev/ttys009", "denied"
		for _, st := range run.Tickets {
			st.Worktree, st.Branch, st.BaseBranch = "/tmp/wt", "b", "base"
			st.DBFingerprint, st.WorktreeFingerprint = "db", "wt"
		}
		run.Reservation = &autopilot.Reservation{Previous: []byte(`{"secret":"x"}`)}
	})
	rec = sharedRecord(t, h.repo, h.projectID, res.RunID)
	snap := string(rec.Snapshot)
	for _, leaked := range []string{"ttys009", "denied", "/tmp/wt", `"branch":`, `"base_branch":`, "db_fingerprint", "worktree_fingerprint", "secret", `"previous"`} {
		if strings.Contains(snap, leaked) {
			t.Fatalf("snapshot carries %s: %s", leaked, snap)
		}
	}
	if !strings.Contains(snap, `"`+tr.R+`"`) {
		t.Fatalf("snapshot lacks the run's tickets: %s", snap)
	}
	local := localRun(t, a, h.projectID, res.RunID)
	if local.TerminalTTY != "/dev/ttys009" || local.Ticket(tr.R).Worktree != "/tmp/wt" {
		t.Fatalf("the local run lost its local fields: %+v", local)
	}
	if local.Revision != rec.Revision {
		t.Fatalf("local revision %d, shared %d", local.Revision, rec.Revision)
	}
	lockPath, _ := a.Registry.LockPath(h.projectID)
	if _, err := os.Stat(filepath.Dir(lockPath)); err != nil {
		t.Fatalf("the local registry is gone: %v", err)
	}
}

// Completion criterion 2: overlapping starts by B are refused, naming A.
func TestSharedRuns_OverlapWithAnotherMemberIsRefused(t *testing.T) {
	h, tr, a, b := newSharedHarness(t)
	mustStart(t, a, tr.R, autopilot.ModeTree)
	for _, c := range []struct{ id, mode string }{
		{tr.R, autopilot.ModeTree}, {tr.R, autopilot.ModeTicket}, {tr.X, autopilot.ModeTicket}, {tr.Y, autopilot.ModeTree},
	} {
		_, err := b.Start(c.id, c.mode, "", false)
		wantRefusedBy(t, err, "Alice")
	}
	if n := len(localRuns(t, b, h.projectID)); n != 0 {
		t.Fatalf("B has %d local run(s)", n)
	}
	if n := len(sharedRecords(t, h.repo, h.projectID)); n != 1 {
		t.Fatalf("the DB has %d run(s)", n)
	}
	// A tree unrelated to R is not blocked.
	mustStart(t, b, tr.T, autopilot.ModeTree)
}

func TestSharedRuns_AncestorAndSiblingStarts(t *testing.T) {
	h, tr, a, b := newSharedHarness(t)
	mustStart(t, a, tr.X, autopilot.ModeTree)
	_, err := b.Start(tr.R, autopilot.ModeTree, "", false)
	wantRefusedBy(t, err, "Alice")
	res := mustStart(t, b, tr.S, autopilot.ModeTree)
	if rec := sharedRecord(t, h.repo, h.projectID, res.RunID); rec == nil || rec.StartedByName != "Bob" {
		t.Fatalf("B's run record = %+v", rec)
	}
}

func TestSharedRuns_FallbackNameIsMarked(t *testing.T) {
	h, tr, _, b := newSharedHarness(t)
	a := memberService(h, h.repo, "", nil)
	mustStart(t, a, tr.R, autopilot.ModeTree)
	actorA, _ := a.actor()
	if !actorA.NameIsFallback {
		t.Fatalf("actor = %+v", actorA)
	}
	_, err := b.Start(tr.R, autopilot.ModeTree, "", false)
	apiErr := wantAPIError(t, err, autopilot.ErrCodeAlreadyRunning)
	if apiErr.Details["started_by"] != actorA.Name || apiErr.Details["name_is_fallback"] != true {
		t.Fatalf("details = %v", apiErr.Details)
	}
}

// Completion criterion 3: a heartbeat older than ActiveThreshold stops
// blocking, and nothing rewrites the stale run.
func TestSharedRuns_StaleHeartbeatDoesNotBlock(t *testing.T) {
	h, tr, a, b := newSharedHarness(t)
	resA := mustStart(t, a, tr.R, autopilot.ModeTree)
	h.clock.Advance(9 * time.Minute)
	_, err := b.Start(tr.R, autopilot.ModeTree, "", false)
	wantRefusedBy(t, err, "Alice")
	before := sharedRecord(t, h.repo, h.projectID, resA.RunID)
	h.clock.Advance(2 * time.Minute) // 11 minutes since A's heartbeat
	resB := mustStart(t, b, tr.R, autopilot.ModeTree)
	if !resB.Created || resB.RunID == resA.RunID {
		t.Fatalf("B's start = %+v", resB)
	}
	after := sharedRecord(t, h.repo, h.projectID, resA.RunID)
	if after.State != autopilot.RunRunning || after.Revision != before.Revision || after.Heartbeat != before.Heartbeat {
		t.Fatalf("A's stale record was rewritten: %+v -> %+v", before, after)
	}
	if local := localRun(t, a, h.projectID, resA.RunID); local.State != autopilot.RunRunning {
		t.Fatalf("A's local run was rewritten: %s", local.State)
	}
}

// Every poll of wait saves the heartbeat, locally and in the DB, and the
// poll interval is far inside ActiveThreshold.
func TestSharedRuns_WaitRefreshesTheSharedHeartbeat(t *testing.T) {
	if DefaultPollInterval >= autopilot.ActiveThreshold {
		t.Fatalf("DefaultPollInterval %s is not below ActiveThreshold %s", DefaultPollInterval, autopilot.ActiveThreshold)
	}
	h, tr, a, _ := newSharedHarness(t)
	h.svc = a
	res := mustStart(t, a, tr.T, autopilot.ModeTicket)
	h.behave[tr.T] = func(w *workerCall) {} // the session never reports
	act := h.next(res.RunID)
	h.launch1(res.RunID, act.Ticket)
	polls := 0
	a.Sleep = func(time.Duration) {
		polls++
		h.clock.Advance(DefaultPollInterval)
		rec := sharedRecord(t, h.repo, h.projectID, res.RunID)
		local := localRun(t, a, h.projectID, res.RunID)
		if rec.Heartbeat != local.Heartbeat.UTC().Format(time.RFC3339Nano) || rec.Revision != local.Revision {
			t.Fatalf("poll %d: shared %s/%d, local %s/%d", polls, rec.Heartbeat, rec.Revision, local.Heartbeat, local.Revision)
		}
		if h.clock.Now().Sub(local.Heartbeat) > DefaultPollInterval {
			t.Fatalf("poll %d: heartbeat %s is older than one poll", polls, local.Heartbeat)
		}
	}
	if _, err := a.Wait(res.RunID, act.Ticket, 30*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if polls == 0 {
		t.Fatal("wait did not poll")
	}
}

// Completion criterion 5: only the starting machine takes a run over.
func TestSharedRuns_TakeOverIsLimitedToTheSameMachine(t *testing.T) {
	for _, state := range []string{"stopped", "interrupted"} {
		t.Run(state, func(t *testing.T) {
			h, tr, a, b := newSharedHarness(t)
			resA := mustStart(t, a, tr.R, autopilot.ModeTree)
			if state == "stopped" {
				modifyRun(t, a, h.projectID, resA.RunID, func(run *autopilot.Run) { run.State = autopilot.RunStopped })
			} else {
				h.clock.Advance(11 * time.Minute)
			}
			before := sharedRecord(t, h.repo, h.projectID, resA.RunID)

			// B names A's run: refused.
			_, err := b.Start(tr.R, autopilot.ModeTree, resA.RunID, false)
			apiErr := wantAPIError(t, err, autopilot.ErrCodeInvalidRunState)
			if apiErr.Details["run_id"] != resA.RunID || apiErr.Details["started_by"] != "Alice" || !strings.Contains(apiErr.Message, "another machine") {
				t.Fatalf("refusal = %s %v", apiErr.Message, apiErr.Details)
			}
			// B's plain start creates its own run.
			resB := mustStart(t, b, tr.R, autopilot.ModeTree)
			if !resB.Created || resB.RunID == resA.RunID {
				t.Fatalf("B's start = %+v", resB)
			}
			if after := sharedRecord(t, h.repo, h.projectID, resA.RunID); after.Revision != before.Revision || after.State != before.State {
				t.Fatalf("A's record changed: %+v -> %+v", before, after)
			}
			if len(localRuns(t, b, h.projectID)) != 1 {
				t.Fatal("B's registry should hold only its own run")
			}
		})
	}
}

func TestSharedRuns_NamingAnotherMembersActiveRunIsAlreadyRunning(t *testing.T) {
	h, tr, a, b := newSharedHarness(t)
	resA := mustStart(t, a, tr.R, autopilot.ModeTree)
	before := sharedRecord(t, h.repo, h.projectID, resA.RunID)
	_, err := b.Start(tr.R, autopilot.ModeTree, resA.RunID, false)
	wantRefusedBy(t, err, "Alice")
	if after := sharedRecord(t, h.repo, h.projectID, resA.RunID); after.Revision != before.Revision {
		t.Fatal("A's record changed")
	}
	if len(localRuns(t, b, h.projectID)) != 0 || len(sharedRecords(t, h.repo, h.projectID)) != 1 {
		t.Fatal("B's refused start left a run behind")
	}
}

func TestSharedRuns_OwnInterruptedRunIsTakenOver(t *testing.T) {
	h, tr, a, _ := newSharedHarness(t)
	resA := mustStart(t, a, tr.R, autopilot.ModeTree)
	before := sharedRecord(t, h.repo, h.projectID, resA.RunID)
	h.clock.Advance(11 * time.Minute)
	again := mustStart(t, a, tr.R, autopilot.ModeTree)
	if !again.Resumed || again.Created || again.RunID != resA.RunID {
		t.Fatalf("A's restart = %+v", again)
	}
	after := sharedRecord(t, h.repo, h.projectID, resA.RunID)
	if after.Revision <= before.Revision || after.Heartbeat == before.Heartbeat || after.State != autopilot.RunRunning {
		t.Fatalf("record %+v -> %+v", before, after)
	}
	if local := localRun(t, a, h.projectID, resA.RunID); local.Revision != after.Revision {
		t.Fatalf("local revision %d, shared %d", local.Revision, after.Revision)
	}
}

// A run file written before DFLT-00326 (no started_by, no revision) is this
// machine's: it is taken over and shared with its starter.
func TestSharedRuns_LegacyRunFileIsTakenOver(t *testing.T) {
	h, tr, a, _ := newSharedHarness(t)
	legacy := map[string]any{
		"id": "run-20260901-080000-abcd0123", "project_id": h.projectID, "mode": "tree", "root_ticket_id": tr.R,
		"state": "running", "created_at": h.clock.Now().Add(-time.Hour), "updated_at": h.clock.Now().Add(-time.Hour),
		"heartbeat": h.clock.Now().Add(-time.Hour), "settings": h.settings, "generation": 1, "work_launches": 0,
		"tickets": map[string]any{}, "order": []string{},
	}
	data, _ := json.MarshalIndent(legacy, "", "  ")
	dir := filepath.Join(a.Registry.Root, h.projectID, "runs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run-20260901-080000-abcd0123.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	res := mustStart(t, a, tr.R, autopilot.ModeTree)
	if !res.Resumed || res.RunID != "run-20260901-080000-abcd0123" {
		t.Fatalf("start = %+v", res)
	}
	actorA, _ := a.actor()
	rec := sharedRecord(t, h.repo, h.projectID, res.RunID)
	if rec == nil || rec.StartedByName != "Alice" || rec.MachineID != actorA.MachineID || rec.Revision != 1 {
		t.Fatalf("record = %+v", rec)
	}
}

// Right after every kind of start, and after later saves, the local file
// and the shared record carry the same revision.
func TestSharedRuns_RevisionsAgreeAfterEveryStart(t *testing.T) {
	h, tr, a, _ := newSharedHarness(t)
	agree := func(runID, what string) int64 {
		t.Helper()
		local := localRun(t, a, h.projectID, runID)
		rec := sharedRecord(t, h.repo, h.projectID, runID)
		if rec == nil || rec.Revision != local.Revision {
			t.Fatalf("%s: local %d, shared %+v", what, local.Revision, rec)
		}
		return local.Revision
	}
	created := mustStart(t, a, tr.R, autopilot.ModeTree)
	r1 := agree(created.RunID, "created")
	h.svc = a
	h.next(created.RunID)
	r2 := agree(created.RunID, "after next")
	if r2 != r1+1 {
		t.Fatalf("next moved the revision from %d to %d", r1, r2)
	}
	modifyRun(t, a, h.projectID, created.RunID, func(run *autopilot.Run) { run.State = autopilot.RunStopped })
	agree(created.RunID, "stopped")
	tookOver := mustStart(t, a, tr.R, autopilot.ModeTree)
	agree(tookOver.RunID, "taken over")
	modifyRun(t, a, h.projectID, created.RunID, func(run *autopilot.Run) { run.State = autopilot.RunStopped })
	reserved, err := a.Start(tr.R, autopilot.ModeTree, "", true)
	if err != nil || !reserved.Resumed {
		t.Fatalf("reserve = %+v, %v", reserved, err)
	}
	agree(reserved.RunID, "reserved")
	adopted, err := a.Start(tr.R, autopilot.ModeTree, reserved.RunID, false)
	if err != nil || !adopted.Adopted {
		t.Fatalf("adopt = %+v, %v", adopted, err)
	}
	agree(adopted.RunID, "adopted")
}

// A cancelled takeover reservation puts the shared record back as it was;
// a cancelled new reservation removes it.
func TestSharedRuns_CancelReservationRestoresTheSharedRecord(t *testing.T) {
	h, tr, a, b := newSharedHarness(t)
	resA := mustStart(t, a, tr.R, autopilot.ModeTree)
	modifyRun(t, a, h.projectID, resA.RunID, func(run *autopilot.Run) { run.State = autopilot.RunStopped })
	stopped := sharedRecord(t, h.repo, h.projectID, resA.RunID)
	h.clock.Advance(time.Minute)
	reserved, err := a.Start(tr.R, autopilot.ModeTree, "", true)
	if err != nil || reserved.RunID != resA.RunID || reserved.State != autopilot.RunStarting {
		t.Fatalf("reserve = %+v, %v", reserved, err)
	}
	reservation := sharedRecord(t, h.repo, h.projectID, resA.RunID)
	if reservation.State != autopilot.RunStarting {
		t.Fatalf("reservation record = %+v", reservation)
	}
	// While reserved, B is blocked.
	_, err = b.Start(tr.R, autopilot.ModeTree, "", false)
	wantRefusedBy(t, err, "Alice")

	if err := a.CancelReservation(reserved.RunID); err != nil {
		t.Fatal(err)
	}
	back := sharedRecord(t, h.repo, h.projectID, resA.RunID)
	if back.State != autopilot.RunStopped || back.Heartbeat != stopped.Heartbeat || back.Revision <= reservation.Revision {
		t.Fatalf("after cancelling: %+v (reservation %+v)", back, reservation)
	}
	if local := localRun(t, a, h.projectID, resA.RunID); local.Revision != back.Revision || local.State != autopilot.RunStopped {
		t.Fatalf("local = %s/%d, shared %d", local.State, local.Revision, back.Revision)
	}
	mustStart(t, b, tr.R, autopilot.ModeTree)

	// A new reservation that is cancelled leaves nothing behind.
	created, err := a.Start(tr.T, autopilot.ModeTree, "", true)
	if err != nil || !created.Created {
		t.Fatalf("reserve = %+v, %v", created, err)
	}
	if sharedRecord(t, h.repo, h.projectID, created.RunID) == nil {
		t.Fatal("the reservation is not in the DB")
	}
	if err := a.CancelReservation(created.RunID); err != nil {
		t.Fatal(err)
	}
	if sharedRecord(t, h.repo, h.projectID, created.RunID) != nil {
		t.Fatal("the cancelled reservation is still in the DB")
	}
	if run, _ := a.Registry.Load(h.projectID, created.RunID); run != nil {
		t.Fatal("the cancelled reservation is still in the local registry")
	}
}

// Pruning on a new start removes the pruned runs' records too, never
// another member's, and does not deadlock on SQLite's single connection.
func TestSharedRuns_PruneDeletesOnlyOwnRecords(t *testing.T) {
	h, tr, a, b := newSharedHarness(t)
	resB := mustStart(t, b, tr.T, autopilot.ModeTree)
	bBefore := sharedRecord(t, h.repo, h.projectID, resB.RunID)
	// A has more settled runs than KeepSettledRuns, each shared.
	var old []string
	for i := 0; i < autopilot.KeepSettledRuns+3; i++ {
		res := mustStart(t, a, tr.S, autopilot.ModeTicket)
		modifyRun(t, a, h.projectID, res.RunID, func(run *autopilot.Run) { run.State = autopilot.RunFinished })
		old = append(old, res.RunID)
		h.clock.Advance(time.Second)
	}
	done := make(chan error, 1)
	go func() { _, err := a.Start(tr.S, autopilot.ModeTree, "", false); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the start did not finish (a data source call inside the decision?)")
	}
	pruned := 0
	for _, id := range old {
		local, _ := a.Registry.Load(h.projectID, id)
		rec := sharedRecord(t, h.repo, h.projectID, id)
		if (local == nil) != (rec == nil) {
			t.Fatalf("run %s: local %v, shared %v", id, local != nil, rec != nil)
		}
		if local == nil {
			pruned++
		}
	}
	if pruned == 0 {
		t.Fatal("nothing was pruned")
	}
	bAfter := sharedRecord(t, h.repo, h.projectID, resB.RunID)
	if bAfter == nil || bAfter.Revision != bBefore.Revision || bAfter.State != bBefore.State {
		t.Fatalf("B's record: %+v -> %+v", bBefore, bAfter)
	}
}

// Completion criterion 2 on SQL: two members starting the same tree at the
// same moment never both pass (two repositories on one SQLite file, as two
// processes would have).
func TestSharedRuns_ConcurrentStartsOnSQLite(t *testing.T) {
	h := newHarness(t)
	repoB, err := store.NewSQLiteRepository(h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	a := memberService(h, h.repo, "Alice", nil)
	b := memberService(h, repoB, "Bob", nil)
	for i := 0; i < 15; i++ {
		root := h.ticket(fmt.Sprintf("R%d", i), "")
		h.ticket(fmt.Sprintf("R%d child", i), root)
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for j, svc := range []*Service{a, b} {
			wg.Add(1)
			go func(j int, svc *Service) {
				defer wg.Done()
				_, errs[j] = svc.Start(root, autopilot.ModeTree, "", false)
			}(j, svc)
		}
		wg.Wait()
		passed := 0
		for _, err := range errs {
			if err == nil {
				passed++
				continue
			}
			wantAPIError(t, err, autopilot.ErrCodeAlreadyRunning)
		}
		if passed != 1 {
			t.Fatalf("round %d: %d starts passed (%v)", i, passed, errs)
		}
		active := 0
		for _, rec := range sharedRecords(t, h.repo, h.projectID) {
			if rec.RootTicketID == root {
				active++
			}
		}
		if active != 1 {
			t.Fatalf("round %d: %d records for the tree", i, active)
		}
	}
}

// A data source error while deciding fails the start: whether it would
// duplicate a run cannot be told.
type failingShared struct {
	autopilot.SharedRuns
	listErr, beginErr error
}

func (f failingShared) List(projectID string) ([]*autopilot.Run, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.SharedRuns.List(projectID)
}

func (f failingShared) Begin(projectID string, decide func([]*autopilot.Run) (*autopilot.Run, []string, error)) error {
	if f.beginErr != nil {
		return f.beginErr
	}
	return f.SharedRuns.Begin(projectID, decide)
}

func TestSharedRuns_DataSourceErrorFailsTheStart(t *testing.T) {
	h, tr, a, _ := newSharedHarness(t)
	a.Registry.Shared = failingShared{SharedRuns: a.Registry.Shared, beginErr: errors.New("connection refused")}
	if _, err := a.Start(tr.R, autopilot.ModeTree, "", false); err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("start = %v", err)
	}
	if len(localRuns(t, a, h.projectID)) != 0 {
		t.Fatal("a run was created")
	}
}

// A failed shared save does not stop the run: it is logged.
func TestSharedRuns_SaveFailureOnlyWarns(t *testing.T) {
	h, tr, a, _ := newSharedHarness(t)
	logs := &logSink{}
	a.Logf = logs.logf
	res := mustStart(t, a, tr.T, autopilot.ModeTicket)
	a.Registry.Shared = saveFails{a.Registry.Shared}
	h.svc = a
	act := h.next(res.RunID)
	if act.Action.Action != autopilot.ActionLaunch {
		t.Fatalf("action = %+v", act)
	}
	if logs.count("save refused") == 0 {
		t.Fatalf("no warning: %v", logs.lines)
	}
}

type saveFails struct{ autopilot.SharedRuns }

func (saveFails) Save(*autopilot.Run) error { return errors.New("save refused") }

// Completion criterion 4: B's runs listing shows A's run, marked as not
// B's, with its members and pending tickets computed.
func TestSharedRuns_RunsListsOtherMembersRuns(t *testing.T) {
	h, tr, a, b := newSharedHarness(t)
	resA := mustStart(t, a, tr.R, autopilot.ModeTree)
	resB := mustStart(t, b, tr.T, autopilot.ModeTree)
	views, err := b.Runs(h.projectID)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]RunView{}
	for _, v := range views {
		byID[v.RunID] = v
	}
	va, vb := byID[resA.RunID], byID[resB.RunID]
	if va.StartedBy == nil || va.StartedBy.Name != "Alice" || va.StartedBy.NameIsFallback || va.Mine || !va.Active {
		t.Fatalf("A's run in B's listing = %+v", va)
	}
	if strings.Join(va.Members, ",") != strings.Join([]string{tr.R, tr.X, tr.S, tr.Y}, ",") || len(va.Pending) == 0 {
		t.Fatalf("A's members/pending = %v / %v", va.Members, va.Pending)
	}
	if !vb.Mine || vb.StartedBy == nil || vb.StartedBy.Name != "Bob" {
		t.Fatalf("B's own run = %+v", vb)
	}
	body, _ := json.Marshal(views)
	actorA, _ := a.actor()
	if strings.Contains(string(body), actorA.MachineID) || strings.Contains(string(body), "machine_id") {
		t.Fatalf("the listing carries a machine id: %s", body)
	}
}

// The listing's order and inactive cap apply to the merged list, and a run
// held locally and in the DB appears once, from the local copy.
func TestSharedRuns_RunsMergesAndCaps(t *testing.T) {
	h, tr, a, b := newSharedHarness(t)
	for i := 0; i < RecentInactiveRuns+2; i++ {
		res := mustStart(t, a, tr.S, autopilot.ModeTicket)
		modifyRun(t, a, h.projectID, res.RunID, func(run *autopilot.Run) { run.State = autopilot.RunFinished })
		h.clock.Advance(time.Second)
	}
	active := mustStart(t, b, tr.T, autopilot.ModeTree)
	// Make B's local copy differ from its shared one.
	if err := b.Registry.WithLock(h.projectID, func(tx *autopilot.Tx) error {
		run, _ := tx.Load(active.RunID)
		run.StopReason = "local-only"
		return tx.Save(run)
	}); err != nil {
		t.Fatal(err)
	}
	views, err := b.Runs(h.projectID)
	if err != nil {
		t.Fatal(err)
	}
	inactive, seen := 0, map[string]int{}
	for i, v := range views {
		seen[v.RunID]++
		if !v.Active {
			inactive++
		}
		if i > 0 && views[i-1].RunID != active.RunID && v.Heartbeat.After(views[i-1].Heartbeat) && !v.Active {
			t.Fatalf("not newest first at %d", i)
		}
		if v.RunID == active.RunID && v.StopReason != "local-only" {
			t.Fatal("the local copy was not used")
		}
	}
	if inactive != RecentInactiveRuns {
		t.Fatalf("%d inactive runs, want %d", inactive, RecentInactiveRuns)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("run %s listed %d times", id, n)
		}
	}
	if views[0].RunID != active.RunID {
		t.Fatalf("the newest run is not first: %s", views[0].RunID)
	}
}

func TestSharedRuns_RunsDegradesToLocalOnError(t *testing.T) {
	h, tr, a, b := newSharedHarness(t)
	mustStart(t, a, tr.R, autopilot.ModeTree)
	resB := mustStart(t, b, tr.T, autopilot.ModeTree)
	logs := &logSink{}
	b.Logf = logs.logf
	b.Registry.Shared = failingShared{SharedRuns: b.Registry.Shared, listErr: errors.New("db down")}
	views, err := b.Runs(h.projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].RunID != resB.RunID || !views[0].Mine {
		t.Fatalf("views = %+v", views)
	}
	if logs.count("db down") == 0 {
		t.Fatal("no warning")
	}
}

// A broken machine-id: the listing still answers (mine = in the local
// registry), but a start fails and writes nothing.
func TestSharedRuns_BrokenMachineID(t *testing.T) {
	h, tr, a, b := newSharedHarness(t)
	resA := mustStart(t, a, tr.R, autopilot.ModeTree)
	resB := mustStart(t, b, tr.T, autopilot.ModeTree)
	if err := os.WriteFile(filepath.Join(b.HomeDir, ".graph-ops", "machine-id"), []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b2 := memberService(h, h.repo, "Bob", nil)
	b2.HomeDir, b2.Registry.Root, b2.ResolveActor = b.HomeDir, b.Registry.Root, IdentityActor(b.HomeDir)
	views, err := b2.Runs(h.projectID)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.Mine != (v.RunID == resB.RunID) {
			t.Fatalf("run %s: mine %v", v.RunID, v.Mine)
		}
	}
	if len(views) != 2 || resA.RunID == "" {
		t.Fatalf("views = %+v", views)
	}
	before := len(sharedRecords(t, h.repo, h.projectID))
	_, err = b2.Start(tr.S, autopilot.ModeTicket, "", false)
	apiErr := wantAPIError(t, err, autopilot.ErrCodeMachineIDUnreadable)
	if !strings.Contains(apiErr.Message, "machine id") || apiErr.Details["path"] != filepath.Join(b.HomeDir, ".graph-ops", "machine-id") {
		t.Fatalf("start = %s %v", apiErr.Message, apiErr.Details)
	}
	if after := len(sharedRecords(t, h.repo, h.projectID)); after != before {
		t.Fatalf("records %d -> %d", before, after)
	}
}

// --- HTTP data source ---

// httpMembers opens two members on one fake plugin of the given version.
func httpMembers(t *testing.T, version string) (*harness, *httpdatasourcetest.Plugin, sharedTree, *Service, *Service, *logSink) {
	t.Helper()
	h := newHarness(t)
	plugin := httpdatasourcetest.New("")
	plugin.Version = version
	srv := httptest.NewServer(plugin)
	t.Cleanup(srv.Close)
	open := func() store.GraphRepository {
		repo, err := store.Open(store.Config{Backend: "http", HTTPURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		return repo
	}
	repoA, repoB := open(), open()
	proj, err := repoA.CreateProject("HTTP", "HT")
	if err != nil {
		t.Fatal(err)
	}
	h.projectID = proj.ID
	eng := engine.New(repoA)
	mk := func(title, parent string) string {
		opts := engine.CreateTicketOptions{}
		if parent != "" {
			opts.ParentTicketID = &parent
		}
		tk, err := eng.CreateTicketWithOptions(proj.ID, title, "", opts)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
		return tk.ID
	}
	var tr sharedTree
	tr.R = mk("R", "")
	tr.X = mk("X", tr.R)
	tr.Y = mk("Y", tr.X)
	tr.S = mk("S", tr.R)
	tr.T = mk("T", "")
	tr.U = mk("U", tr.T)
	logs := &logSink{}
	a := memberService(h, repoA, "Alice", logs)
	b := memberService(h, repoB, "Bob", logs)
	return h, plugin, tr, a, b, logs
}

func TestSharedRuns_HTTP12SharesRuns(t *testing.T) {
	h, plugin, tr, a, b, _ := httpMembers(t, "1.2")
	res := mustStart(t, a, tr.R, autopilot.ModeTree)
	if _, ok := plugin.AutopilotRuns()[res.RunID]; !ok {
		t.Fatal("the run did not reach the plugin")
	}
	_, err := b.Start(tr.R, autopilot.ModeTree, "", false)
	wantRefusedBy(t, err, "Alice")
	views, err := b.Runs(h.projectID)
	if err != nil || len(views) != 1 || views[0].Mine {
		t.Fatalf("B's listing = %+v, %v", views, err)
	}
}

func TestSharedRuns_HTTP11FallsBackToLocal(t *testing.T) {
	autopilot.ResetSharedUnsupportedWarning()
	t.Cleanup(autopilot.ResetSharedUnsupportedWarning)
	h, plugin, tr, a, _, logs := httpMembers(t, "1.1")
	plugin.ResetRequests()
	mustStart(t, a, tr.R, autopilot.ModeTree)
	mustStart(t, a, tr.T, autopilot.ModeTree)
	for i := 0; i < 3; i++ {
		views, err := a.Runs(h.projectID)
		if err != nil || len(views) != 2 {
			t.Fatalf("listing = %+v, %v", views, err)
		}
		for _, v := range views {
			if !v.Mine {
				t.Fatalf("run %s is not mine", v.RunID)
			}
		}
	}
	for _, req := range plugin.Requests() {
		if strings.Contains(req.Path, "autopilot-runs") {
			t.Fatalf("a request was sent to a 1.1 plugin: %s %s", req.Method, req.Path)
		}
	}
	if n := logs.count("older than 1.2"); n != 1 {
		t.Fatalf("the warning was logged %d times: %v", n, logs.lines)
	}
}

// --- review round 1 ---

// QA review, round 1, finding 1: A's run comes back after its heartbeat
// expired and B started the same tree meanwhile. A's next stops the run
// (overtaken) without launching anything more; B goes on.
func TestSharedRuns_RevivedRunIsOvertakenAtNext(t *testing.T) {
	h, tr, a, b := newSharedHarness(t)
	h.behave[tr.R] = func(w *workerCall) {} // A's session keeps running
	resA := mustStart(t, a, tr.R, autopilot.ModeTree)
	h.svc = a
	act := h.next(resA.RunID)
	h.launch1(resA.RunID, act.Ticket)
	launched := len(h.launches)

	h.clock.Advance(autopilot.ActiveThreshold + time.Minute) // A's machine sleeps
	resB := mustStart(t, b, tr.X, autopilot.ModeTree)        // B starts inside A's tree
	h.clock.Advance(time.Minute)                             // A wakes up

	stopped := h.next(resA.RunID)
	if stopped.Action.Action != autopilot.ActionStopped || stopped.Reason != autopilot.StopOvertaken ||
		!strings.Contains(stopped.Detail, resB.RunID) || !strings.Contains(stopped.Detail, "Bob") {
		t.Fatalf("A's next = %+v", stopped)
	}
	if len(h.launches) != launched {
		t.Fatal("A launched a session after being overtaken")
	}
	run := localRun(t, a, h.projectID, resA.RunID)
	if run.State != autopilot.RunStopped || run.StopReason != autopilot.StopOvertaken {
		t.Fatalf("A's local run = %s/%s", run.State, run.StopReason)
	}
	if rec := sharedRecord(t, h.repo, h.projectID, resA.RunID); rec.State != autopilot.RunStopped || rec.Revision != run.Revision {
		t.Fatalf("A's shared record = %+v", rec)
	}

	// B is not overtaken by A's revived (and now stopped) run.
	h.svc = b
	if next := h.next(resB.RunID); next.Action.Action == autopilot.ActionStopped {
		t.Fatalf("B's next = %+v", next)
	}
	// And A cannot resume while B's run is active.
	_, err := a.Start(tr.R, autopilot.ModeTree, "", false)
	wantRefusedBy(t, err, "Bob")
}

// The same when the overtaking start falls between A's next and its launch:
// the launch stops the run instead of opening a session, and next then
// answers stopped.
func TestSharedRuns_RevivedRunIsOvertakenAtLaunch(t *testing.T) {
	h, tr, a, b := newSharedHarness(t)
	resA := mustStart(t, a, tr.R, autopilot.ModeTree)
	h.svc = a
	act := h.next(resA.RunID)
	if act.Action.Action != autopilot.ActionLaunch {
		t.Fatalf("A's next = %+v", act)
	}
	h.clock.Advance(autopilot.ActiveThreshold + time.Minute)
	mustStart(t, b, tr.R, autopilot.ModeTree)
	h.clock.Advance(time.Minute)

	_, err := a.Launch(resA.RunID, act.Ticket, act.Role)
	apiErr := wantAPIError(t, err, autopilot.ErrCodeInvalidRunState)
	if !strings.Contains(apiErr.Message, "autopilot next") {
		t.Fatalf("launch error = %s", apiErr.Message)
	}
	if len(h.launches) != 0 {
		t.Fatal("a session was launched")
	}
	if next := h.next(resA.RunID); next.Action.Action != autopilot.ActionStopped || next.Reason != autopilot.StopOvertaken {
		t.Fatalf("A's next after the refused launch = %+v", next)
	}
}

// A run that was never overtaken is not stopped by an overlapping run that
// is inactive, nor by one that does not overlap; and a data source that
// cannot be read does not stop it either.
func TestSharedRuns_NotOvertakenWithoutAnActiveLaterOverlap(t *testing.T) {
	h, tr, a, b := newSharedHarness(t)
	resA := mustStart(t, a, tr.R, autopilot.ModeTree)
	mustStart(t, b, tr.T, autopilot.ModeTree) // another tree
	h.svc = a
	if next := h.next(resA.RunID); next.Action.Action != autopilot.ActionLaunch {
		t.Fatalf("A's next = %+v", next)
	}
	logs := &logSink{}
	a.Logf = logs.logf
	a.Registry.Shared = failingShared{SharedRuns: a.Registry.Shared, listErr: errors.New("db down")}
	autopilot.ResetSharedErrorLog()
	t.Cleanup(autopilot.ResetSharedErrorLog)
	if next := h.next(resA.RunID); next.Action.Action != autopilot.ActionLaunch {
		t.Fatalf("A's next with the DB down = %+v", next)
	}
	if logs.count("db down") != 1 {
		t.Fatalf("logs = %v", logs.lines)
	}
}

// Non-functional review, round 1, finding 1: a listing failure that comes
// back after a successful listing is logged again.
func TestSharedRuns_RunsLogsARecurringFailureAgain(t *testing.T) {
	autopilot.ResetSharedErrorLog()
	t.Cleanup(autopilot.ResetSharedErrorLog)
	h, tr, _, b := newSharedHarness(t)
	mustStart(t, b, tr.T, autopilot.ModeTree)
	logs := &logSink{}
	b.Logf = logs.logf
	healthy := b.Registry.Shared
	failing := failingShared{SharedRuns: healthy, listErr: errors.New("db down")}
	for _, shared := range []autopilot.SharedRuns{failing, failing, healthy, failing} {
		b.Registry.Shared = shared
		if _, err := b.Runs(h.projectID); err != nil {
			t.Fatal(err)
		}
	}
	if logs.count("db down") != 2 || logs.count("reachable again") != 1 {
		t.Fatalf("logs = %v", logs.lines)
	}
	for _, l := range logs.lines {
		if strings.Contains(l, "db down") && (!strings.Contains(l, "showing this machine's runs only") || strings.Contains(l, "the run goes on")) {
			t.Fatalf("listing failure logged as %q", l)
		}
	}
}

// Non-functional review, round 1, finding 3: another member's settled
// records beyond KeepSharedSettledRuns are dropped by a start, while that
// member's local runs stay and its listing still shows them.
func TestSharedRuns_SettledRecordsAreKeptWithinTheRetention(t *testing.T) {
	h, tr, a, b := newSharedHarness(t)
	var bobs []string
	for i := 0; i < autopilot.KeepSharedSettledRuns+3; i++ {
		res := mustStart(t, b, tr.S, autopilot.ModeTicket)
		modifyRun(t, b, h.projectID, res.RunID, func(run *autopilot.Run) { run.State = autopilot.RunFinished })
		bobs = append(bobs, res.RunID)
		h.clock.Advance(time.Second)
	}
	mustStart(t, a, tr.T, autopilot.ModeTree)
	settled := 0
	for _, rec := range sharedRecords(t, h.repo, h.projectID) {
		if rec.State == autopilot.RunFinished {
			settled++
		}
	}
	if settled > autopilot.KeepSharedSettledRuns {
		t.Fatalf("%d settled records kept, limit %d", settled, autopilot.KeepSharedSettledRuns)
	}
	if sharedRecord(t, h.repo, h.projectID, bobs[0]) != nil {
		t.Fatal("the oldest settled record was kept")
	}
	if sharedRecord(t, h.repo, h.projectID, bobs[len(bobs)-1]) == nil {
		t.Fatal("the newest settled record was dropped")
	}
	views, err := b.Runs(h.projectID)
	if err != nil || len(views) == 0 {
		t.Fatalf("B's listing = %+v, %v", views, err)
	}
}
