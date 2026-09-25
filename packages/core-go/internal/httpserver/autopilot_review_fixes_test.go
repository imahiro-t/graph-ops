package httpserver

import (
	"bytes"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/autopilot/runner"
	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00142 iteration 1: the review gates' findings on the launch and runs
// APIs.

// QA review 2: the "waiting" badge follows pending -- what the run may still
// launch -- not every member.
func TestAutopilotRuns_PendingLeavesOutTicketsTheRunWillNotLaunch(t *testing.T) {
	e := newAutopilotEnv(t)
	r := e.ticket(t, "R", "")
	a := e.ticket(t, "A", r)
	a1 := e.ticket(t, "A1", a)
	b := e.ticket(t, "B", r)
	b1 := e.ticket(t, "B1", b)
	c := e.ticket(t, "C", r)
	e.setStatus(t, a, domain.TicketDone)
	e.setStatus(t, b, domain.TicketInProgress)
	started, err := e.svc().Start(r, autopilot.ModeTree, "", false)
	if err != nil {
		t.Fatal(err)
	}
	e.modify(t, started.RunID, func(run *autopilot.Run) {
		run.Tickets = map[string]*autopilot.TicketState{
			r: {ID: r, Status: autopilot.TicketLaunched, Role: autopilot.RoleWork, Counted: true},
		}
		run.Order = []string{r}
		run.WorkLaunches = 1
	})
	runs := decodeRuns(t, doJSON(t, e.s, http.MethodGet, "/api/autopilot/runs?project_id="+e.pid, nil))
	if len(runs) != 1 {
		t.Fatalf("runs = %+v", runs)
	}
	// A is DONE (its child A1 is still processed); B is in progress
	// elsewhere, so neither B nor B1 will be launched.
	if got := strings.Join(runs[0].Pending, ","); got != a1+","+c {
		t.Fatalf("pending = %s (members %v)", got, runs[0].Members)
	}
	if !slices.Contains(runs[0].Members, b1) {
		t.Fatalf("members = %v", runs[0].Members)
	}
}

// Non-functional review 3: an unreadable run file refuses a start instead of
// letting a duplicate through unnoticed.
func TestAutopilotStart_UnreadableRunFileIsRefusedWith409(t *testing.T) {
	e := newAutopilotEnv(t)
	r := e.ticket(t, "R", "")
	dir := filepath.Join(runner.RegistryRoot(e.s.cfg.HomeDir), e.pid, "runs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run-20260901-090000-broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, e.s, http.MethodPost, startAutopilotPath(r), map[string]any{"mode": "tree"})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), string(autopilot.ErrCodeRegistryCorrupt)) {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if e.launcher.count() != 0 {
		t.Fatal("launched")
	}
}

// Non-functional review 3/4: the launch, its failure, and an unreadable run
// file are written to the server log.
func TestAutopilot_LaunchesAndRegistryProblemsAreLogged(t *testing.T) {
	e := newAutopilotEnv(t)
	var buf bytes.Buffer
	e.s.logger = slog.New(slog.NewTextHandler(&buf, nil))
	r := e.ticket(t, "R", "")
	res := decodeAutopilotStart(t, doJSON(t, e.s, http.MethodPost, startAutopilotPath(r), map[string]any{"mode": "ticket"}))
	if !strings.Contains(buf.String(), "event=autopilot_launched") || !strings.Contains(buf.String(), res.RunID) {
		t.Fatalf("log = %s", buf.String())
	}

	c := e.ticket(t, "C", "")
	e.launcher.err = os.ErrPermission
	if rec := doJSON(t, e.s, http.MethodPost, startAutopilotPath(c), map[string]any{"mode": "ticket"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(buf.String(), "event=autopilot_launch_failed") {
		t.Fatalf("log = %s", buf.String())
	}

	dir := filepath.Join(runner.RegistryRoot(e.s.cfg.HomeDir), e.pid, "runs")
	if err := os.WriteFile(filepath.Join(dir, "run-20260901-090000-broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	doJSON(t, e.s, http.MethodGet, "/api/autopilot/runs?project_id="+e.pid, nil)
	if !strings.Contains(buf.String(), "event=autopilot_registry") || !strings.Contains(buf.String(), "broken.json") {
		t.Fatalf("log = %s", buf.String())
	}
}
