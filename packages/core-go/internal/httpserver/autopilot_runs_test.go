package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/autopilot/runner"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
	"github.com/graph-ops/core-go/internal/terminal"
)

// DFLT-00142 phase 5: POST /api/tickets/{id}/autopilot and
// GET /api/autopilot/runs.

type fakeAutopilotLauncher struct {
	mu    sync.Mutex
	calls []launchedTerminal
	err   error
}

type launchedTerminal struct {
	WorkDir     string
	Args        []string
	Prompt      string
	TerminalTTY string
}

func (f *fakeAutopilotLauncher) Launch(req runner.LaunchRequest) (terminal.LaunchOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, launchedTerminal{WorkDir: req.WorkDir, Args: append([]string(nil), req.ExtraArgs...), Prompt: req.Prompt, TerminalTTY: req.TerminalTTY})
	return terminal.LaunchOutcome{}, f.err
}

func (f *fakeAutopilotLauncher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type autopilotEnv struct {
	s        *Server
	repo     store.GraphRepository
	pid      string
	launcher *fakeAutopilotLauncher
}

func newAutopilotEnv(t *testing.T) *autopilotEnv {
	t.Helper()
	s, repo, pid := newTestServer(t)
	l := &fakeAutopilotLauncher{}
	s.cfg.AutopilotLauncher = l
	return &autopilotEnv{s: s, repo: repo, pid: pid, launcher: l}
}

func (e *autopilotEnv) ticket(t *testing.T, title, parent string) string {
	t.Helper()
	created, err := e.s.engine.CreateTicketWithOptions(e.pid, title, "", engineCreateOptions(parent))
	if err != nil {
		t.Fatal(err)
	}
	// Distinct created_at values keep the children's order unambiguous.
	time.Sleep(2 * time.Millisecond)
	return created.ID
}

func engineCreateOptions(parent string) engine.CreateTicketOptions {
	opts := engine.CreateTicketOptions{}
	if parent != "" {
		opts.ParentTicketID = &parent
	}
	return opts
}

func (e *autopilotEnv) setStatus(t *testing.T, id string, status domain.TicketStatus) {
	t.Helper()
	if _, err := e.repo.UpdateTicket(id, store.TicketPatch{Status: &status}); err != nil {
		t.Fatal(err)
	}
}

func (e *autopilotEnv) svc() *runner.Service { return e.s.autopilotService() }

func (e *autopilotEnv) runs(t *testing.T) []*autopilot.Run {
	t.Helper()
	runs, err := e.svc().Registry.List(e.pid)
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

func (e *autopilotEnv) localPath() string {
	return e.s.projectLocalPath(e.pid)
}

// modify rewrites a saved run under the registry lock.
func (e *autopilotEnv) modify(t *testing.T, runID string, fn func(run *autopilot.Run)) {
	t.Helper()
	if err := e.svc().Registry.WithLock(e.pid, func(tx *autopilot.Tx) error {
		run, err := tx.Load(runID)
		if err != nil || run == nil {
			return errors.New("run not found")
		}
		fn(run)
		return tx.Save(run)
	}); err != nil {
		t.Fatal(err)
	}
}

func startAutopilotPath(id string) string { return "/api/tickets/" + id + "/autopilot" }

func decodeAutopilotStart(t *testing.T, rec *httptest.ResponseRecorder) autopilotStartResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var res autopilotStartResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestAutopilotStart_ReservesAndOpensTheOrchestrator(t *testing.T) {
	for _, mode := range []string{autopilot.ModeTicket, autopilot.ModeTree} {
		t.Run(mode, func(t *testing.T) {
			e := newAutopilotEnv(t)
			r := e.ticket(t, "R", "")
			res := decodeAutopilotStart(t, doJSON(t, e.s, http.MethodPost, startAutopilotPath(r), map[string]any{"mode": mode}))
			if res.RunID == "" || res.Mode != mode || res.Root != r || res.State != autopilot.RunStarting || !res.Created || res.Resumed {
				t.Fatalf("res = %+v", res)
			}
			runs := e.runs(t)
			if len(runs) != 1 || runs[0].ID != res.RunID || runs[0].State != autopilot.RunStarting || runs[0].Reservation == nil {
				t.Fatalf("runs = %+v", runs)
			}
			if e.launcher.count() != 1 {
				t.Fatalf("launches = %+v", e.launcher.calls)
			}
			call := e.launcher.calls[0]
			want := "/graph-ops:autopilot-" + mode + " " + r + " --run " + res.RunID
			// The orchestrator itself always opens in a new window
			// (DFLT-00154): no Terminal.app tty is passed.
			if call.WorkDir != e.localPath() || call.Prompt != want || call.TerminalTTY != "" ||
				strings.Join(call.Args, " ") != "--permission-mode "+autopilot.Defaults().PermissionMode {
				t.Fatalf("call = %+v, want prompt %q in %s", call, want, e.localPath())
			}
			// The orchestrator's `start --run` adopts the reservation.
			adopted, err := e.svc().Start(r, mode, res.RunID, false)
			if err != nil || !adopted.Adopted || adopted.State != autopilot.RunRunning {
				t.Fatalf("adopt = %+v, %v", adopted, err)
			}
		})
	}
}

func TestAutopilotStart_PassesThePermissionModeSetting(t *testing.T) {
	e := newAutopilotEnv(t)
	setLocalAutopilot(t, e.s, e.pid, autopilot.LocalSettings{autopilot.KeyPermissionMode: "acceptEdits"})
	r := e.ticket(t, "R", "")
	decodeAutopilotStart(t, doJSON(t, e.s, http.MethodPost, startAutopilotPath(r), map[string]any{"mode": "tree"}))
	if got := strings.Join(e.launcher.calls[0].Args, " "); got != "--permission-mode acceptEdits" {
		t.Fatalf("args = %q", got)
	}
}

func TestAutopilotStart_OverlapIsRefusedWith409(t *testing.T) {
	e := newAutopilotEnv(t)
	r := e.ticket(t, "R", "")
	c := e.ticket(t, "C", r)
	other := e.ticket(t, "Other", "")
	if _, err := e.svc().Start(r, autopilot.ModeTree, "", false); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, mode string }{{r, "ticket"}, {r, "tree"}, {c, "ticket"}, {c, "tree"}} {
		rec := doJSON(t, e.s, http.MethodPost, startAutopilotPath(tc.id), map[string]any{"mode": tc.mode})
		if rec.Code != http.StatusConflict || decodeError(t, rec).Code != autopilot.ErrCodeAlreadyRunning {
			t.Errorf("%s %s: %d %s", tc.id, tc.mode, rec.Code, rec.Body.String())
		}
	}
	if e.launcher.count() != 0 || len(e.runs(t)) != 1 {
		t.Fatalf("launches = %d, runs = %d", e.launcher.count(), len(e.runs(t)))
	}
	// A ticket outside the tree is not affected.
	decodeAutopilotStart(t, doJSON(t, e.s, http.MethodPost, startAutopilotPath(other), map[string]any{"mode": "tree"}))
}

func TestAutopilotStart_WithoutCSRFHeaderNothingHappens(t *testing.T) {
	e := newAutopilotEnv(t)
	r := e.ticket(t, "R", "")
	rec := doAutopilotRaw(e.s, http.MethodPost, startAutopilotPath(r), `{"mode":"tree"}`, false)
	if rec.Code != http.StatusForbidden || decodeError(t, rec).Code != domain.ErrCodeCSRFHeaderRequired {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if e.launcher.count() != 0 || len(e.runs(t)) != 0 {
		t.Fatalf("launches = %d, runs = %d", e.launcher.count(), len(e.runs(t)))
	}
}

func TestAutopilotStart_InvalidModeIs400(t *testing.T) {
	e := newAutopilotEnv(t)
	r := e.ticket(t, "R", "")
	for _, body := range []string{`{"mode": "forest"}`, `{"mode": ""}`, `{}`, `{"mode": 1}`, `not json`, ``} {
		rec := doAutopilotRaw(e.s, http.MethodPost, startAutopilotPath(r), body, true)
		if rec.Code != http.StatusBadRequest || decodeError(t, rec).Code != domain.ErrCodeValidation {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if e.launcher.count() != 0 || len(e.runs(t)) != 0 {
		t.Fatalf("launches = %d, runs = %d", e.launcher.count(), len(e.runs(t)))
	}
}

func TestAutopilotStart_UnknownTicketIs404(t *testing.T) {
	e := newAutopilotEnv(t)
	rec := doJSON(t, e.s, http.MethodPost, startAutopilotPath("TEST-99999"), map[string]any{"mode": "tree"})
	if rec.Code != http.StatusNotFound || decodeError(t, rec).Code != domain.ErrCodeTicketNotFound {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestAutopilotStart_LocalPathNotSetReservesNothing(t *testing.T) {
	e := newAutopilotEnv(t)
	r := e.ticket(t, "R", "")
	if _, err := runtimeconfig.SetProjectPath(e.s.cfg.HomeDir, e.pid, ""); err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, e.s, http.MethodPost, startAutopilotPath(r), map[string]any{"mode": "tree"})
	if rec.Code != http.StatusBadRequest || decodeError(t, rec).Code != autopilot.ErrCodeLocalPathNotSet {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if e.launcher.count() != 0 || len(e.runs(t)) != 0 {
		t.Fatalf("launches = %d, runs = %d", e.launcher.count(), len(e.runs(t)))
	}
}

func TestAutopilotStart_FinishedRootIsRefused(t *testing.T) {
	for _, status := range []domain.TicketStatus{domain.TicketDone, domain.TicketClosed} {
		e := newAutopilotEnv(t)
		r := e.ticket(t, "R", "")
		e.setStatus(t, r, status)
		rec := doJSON(t, e.s, http.MethodPost, startAutopilotPath(r), map[string]any{"mode": "tree"})
		if rec.Code != http.StatusConflict || decodeError(t, rec).Code != autopilot.ErrCodeRootFinished {
			t.Fatalf("%s: %d %s", status, rec.Code, rec.Body.String())
		}
		if e.launcher.count() != 0 || len(e.runs(t)) != 0 {
			t.Fatalf("launches = %d, runs = %d", e.launcher.count(), len(e.runs(t)))
		}
	}
}

func TestAutopilotStart_TerminalFailureCancelsTheReservation(t *testing.T) {
	e := newAutopilotEnv(t)
	r := e.ticket(t, "R", "")
	e.launcher.err = errors.New("no terminal")
	rec := doJSON(t, e.s, http.MethodPost, startAutopilotPath(r), map[string]any{"mode": "tree"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if runs := e.runs(t); len(runs) != 0 {
		t.Fatalf("runs = %+v", runs)
	}
	e.launcher.err = nil
	decodeAutopilotStart(t, doJSON(t, e.s, http.MethodPost, startAutopilotPath(r), map[string]any{"mode": "tree"}))
}

// The spec review's carry-over 1: the launch API decides like `autopilot
// start` (S5 before S4): a stopped run of the same root and mode is taken
// over under its own run ID -- even though the root is DONE by then -- and
// that run ID is what the orchestrator is told to adopt.
func TestAutopilotStart_TakesOverAStoppedRunLikeTheCLI(t *testing.T) {
	e := newAutopilotEnv(t)
	r := e.ticket(t, "R", "")
	e.ticket(t, "C", r)
	first, err := e.svc().Start(r, autopilot.ModeTree, "", false)
	if err != nil {
		t.Fatal(err)
	}
	e.modify(t, first.RunID, func(run *autopilot.Run) {
		run.State = autopilot.RunStopped
		run.StopReason = autopilot.StopTicketFailed
	})
	e.setStatus(t, r, domain.TicketDone)

	res := decodeAutopilotStart(t, doJSON(t, e.s, http.MethodPost, startAutopilotPath(r), map[string]any{"mode": "tree"}))
	if res.RunID != first.RunID || !res.Resumed || res.Created {
		t.Fatalf("res = %+v, want run %s taken over", res, first.RunID)
	}
	if runs := e.runs(t); len(runs) != 1 || runs[0].State != autopilot.RunStarting {
		t.Fatalf("runs = %+v", runs)
	}
	if want := "/graph-ops:autopilot-tree " + r + " --run " + first.RunID; e.launcher.calls[0].Prompt != want {
		t.Fatalf("prompt = %q, want %q", e.launcher.calls[0].Prompt, want)
	}
	adopted, err := e.svc().Start(r, autopilot.ModeTree, first.RunID, false)
	if err != nil || !adopted.Adopted || adopted.RunID != first.RunID {
		t.Fatalf("adopt = %+v, %v", adopted, err)
	}

	// A different mode is a new run, and with the root DONE that is S4's
	// refusal.
	rec := doJSON(t, e.s, http.MethodPost, startAutopilotPath(r), map[string]any{"mode": "ticket"})
	if rec.Code != http.StatusConflict || decodeError(t, rec).Code != autopilot.ErrCodeRootFinished {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestAutopilotStart_TerminalFailureRestoresATakenOverRun(t *testing.T) {
	e := newAutopilotEnv(t)
	r := e.ticket(t, "R", "")
	first, err := e.svc().Start(r, autopilot.ModeTree, "", false)
	if err != nil {
		t.Fatal(err)
	}
	e.modify(t, first.RunID, func(run *autopilot.Run) { run.State = autopilot.RunStopped })
	e.launcher.err = errors.New("no terminal")
	rec := doJSON(t, e.s, http.MethodPost, startAutopilotPath(r), map[string]any{"mode": "tree"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	runs := e.runs(t)
	if len(runs) != 1 || runs[0].ID != first.RunID || runs[0].State != autopilot.RunStopped || runs[0].Reservation != nil {
		t.Fatalf("runs = %+v", runs)
	}
}

func decodeRuns(t *testing.T, rec *httptest.ResponseRecorder) []runner.RunView {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var runs []runner.RunView
	if err := json.Unmarshal(rec.Body.Bytes(), &runs); err != nil {
		t.Fatal(err)
	}
	return runs
}

func TestAutopilotRuns_ActiveRunWithItsCurrentTicketAndMembers(t *testing.T) {
	e := newAutopilotEnv(t)
	r := e.ticket(t, "R", "")
	c := e.ticket(t, "C", r)
	d := e.ticket(t, "D", r)
	started, err := e.svc().Start(r, autopilot.ModeTree, "", false)
	if err != nil {
		t.Fatal(err)
	}
	e.modify(t, started.RunID, func(run *autopilot.Run) {
		run.Tickets = map[string]*autopilot.TicketState{
			r: {ID: r, Status: autopilot.TicketDone},
			c: {ID: c, ParentID: r, Depth: 1, Status: autopilot.TicketLaunched, Role: autopilot.RoleWork, AwaitingHuman: "plan approval"},
		}
		run.Order = []string{r, c}
	})

	// Another project's run is never listed.
	otherProject, err := e.repo.CreateProject("Other", "OTH")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtimeconfig.SetProjectPath(e.s.cfg.HomeDir, otherProject.ID, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ot, err := e.s.engine.CreateTicketWithOptions(otherProject.ID, "O", "", engineCreateOptions(""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc().Start(ot.ID, autopilot.ModeTree, "", false); err != nil {
		t.Fatal(err)
	}

	runs := decodeRuns(t, doJSON(t, e.s, http.MethodGet, "/api/autopilot/runs?project_id="+e.pid, nil))
	if len(runs) != 1 {
		t.Fatalf("runs = %+v", runs)
	}
	got := runs[0]
	if got.RunID != started.RunID || got.Root != r || got.Mode != autopilot.ModeTree || got.State != autopilot.RunRunning || !got.Active {
		t.Fatalf("run = %+v", got)
	}
	if got.Current != c || got.CurrentRole != autopilot.RoleWork || got.AwaitingHuman != "plan approval" {
		t.Fatalf("current = %q %q %q", got.Current, got.CurrentRole, got.AwaitingHuman)
	}
	if got.Tickets[r] != autopilot.TicketDone || got.Tickets[c] != autopilot.TicketLaunched {
		t.Fatalf("tickets = %+v", got.Tickets)
	}
	if strings.Join(got.Members, ",") != strings.Join([]string{r, c, d}, ",") {
		t.Fatalf("members = %v", got.Members)
	}
}

func TestAutopilotRuns_InactiveRunsHaveNoMembers(t *testing.T) {
	e := newAutopilotEnv(t)
	r := e.ticket(t, "R", "")
	e.ticket(t, "C", r)
	started, err := e.svc().Start(r, autopilot.ModeTree, "", false)
	if err != nil {
		t.Fatal(err)
	}
	e.modify(t, started.RunID, func(run *autopilot.Run) { run.State = autopilot.RunFinished })
	runs := decodeRuns(t, doJSON(t, e.s, http.MethodGet, "/api/autopilot/runs?project_id="+e.pid, nil))
	if len(runs) != 1 || runs[0].Active || len(runs[0].Members) != 0 || runs[0].Members == nil {
		t.Fatalf("runs = %+v", runs)
	}
	if body := doJSON(t, e.s, http.MethodGet, "/api/autopilot/runs?project_id="+e.pid, nil).Body.String(); !strings.Contains(body, `"members":[]`) {
		t.Fatalf("body = %s", body)
	}
}

func TestAutopilotRuns_EmptyAndErrors(t *testing.T) {
	e := newAutopilotEnv(t)
	if body := strings.TrimSpace(doJSON(t, e.s, http.MethodGet, "/api/autopilot/runs?project_id="+e.pid, nil).Body.String()); body != "[]" {
		t.Fatalf("body = %s", body)
	}
	rec := doJSON(t, e.s, http.MethodGet, "/api/autopilot/runs", nil)
	if rec.Code != http.StatusBadRequest || decodeError(t, rec).Code != domain.ErrCodeValidation {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e.s, http.MethodGet, "/api/autopilot/runs?project_id=proj-nope", nil)
	if rec.Code != http.StatusNotFound || decodeError(t, rec).Code != domain.ErrCodeProjectNotFound {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}
