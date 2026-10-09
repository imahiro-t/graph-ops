package httpserver

import (
	"net/http"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00375: POST /api/tickets/{id}/autopilot's model -- the run's model
// cap, recorded on the reservation, passed to the orchestrator as --model
// and in its prompt.

func TestAutopilotStart_ModelIsRecordedAndPassedToTheOrchestrator(t *testing.T) {
	e := newAutopilotEnv(t)
	r := e.ticket(t, "R", "")
	res := decodeAutopilotStart(t, doJSON(t, e.s, http.MethodPost, startAutopilotPath(r), map[string]any{"mode": "ticket", "model": "sonnet"}))
	if res.ModelCap != "sonnet" {
		t.Fatalf("res = %+v", res)
	}
	runs := e.runs(t)
	if len(runs) != 1 || runs[0].ModelCap != "sonnet" {
		t.Fatalf("runs = %+v", runs)
	}
	call := e.launcher.calls[0]
	if got := strings.Join(call.Args, " "); got != "--permission-mode "+autopilot.Defaults().PermissionMode+" --model sonnet" {
		t.Errorf("args = %q", got)
	}
	if want := "/graph-ops:autopilot-ticket " + r + " --run " + res.RunID + " --model sonnet"; call.Prompt != want {
		t.Errorf("prompt = %q, want %q", call.Prompt, want)
	}
	// The orchestrator's `start --run <id> --model sonnet` adopts it, cap kept.
	cap := "sonnet"
	adopted, err := e.svc().StartWithModel(r, "ticket", res.RunID, false, &cap)
	if err != nil || !adopted.Adopted || adopted.ModelCap != "sonnet" {
		t.Fatalf("adopt = %+v, %v", adopted, err)
	}
}

// "Not specified" in the Web UI is sent as inherit: a resumed run's cap is
// cleared, and the orchestrator gets no --model.
func TestAutopilotStart_InheritClearsAResumedRunsCap(t *testing.T) {
	e := newAutopilotEnv(t)
	r := e.ticket(t, "R", "")
	haiku := "haiku"
	first, err := e.svc().StartWithModel(r, autopilot.ModeTicket, "", false, &haiku)
	if err != nil || first.ModelCap != "haiku" {
		t.Fatalf("first = %+v, %v", first, err)
	}
	e.modify(t, first.RunID, func(run *autopilot.Run) { run.State = autopilot.RunStopped })

	res := decodeAutopilotStart(t, doJSON(t, e.s, http.MethodPost, startAutopilotPath(r), map[string]any{"mode": "ticket", "model": "inherit"}))
	if res.RunID != first.RunID || !res.Resumed || res.ModelCap != "" {
		t.Fatalf("res = %+v", res)
	}
	if runs := e.runs(t); runs[0].ModelCap != "" {
		t.Fatalf("model_cap = %q, want none", runs[0].ModelCap)
	}
	call := e.launcher.calls[0]
	if strings.Contains(strings.Join(call.Args, " "), "--model") || strings.Contains(call.Prompt, "--model") {
		t.Errorf("call = %+v, want no --model", call)
	}
	// The orchestrator's `start --run` without --model keeps the cleared cap.
	adopted, err := e.svc().Start(r, autopilot.ModeTicket, res.RunID, false)
	if err != nil || adopted.ModelCap != "" {
		t.Fatalf("adopt = %+v, %v", adopted, err)
	}
}

// For a new run, inherit and no model at all are the same: no cap.
func TestAutopilotStart_NewRunInheritEqualsUnspecified(t *testing.T) {
	for name, body := range map[string]map[string]any{
		"inherit": {"mode": "ticket", "model": "inherit"},
		"absent":  {"mode": "ticket"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newAutopilotEnv(t)
			r := e.ticket(t, "R", "")
			res := decodeAutopilotStart(t, doJSON(t, e.s, http.MethodPost, startAutopilotPath(r), body))
			if !res.Created || res.ModelCap != "" {
				t.Fatalf("res = %+v", res)
			}
			if runs := e.runs(t); runs[0].ModelCap != "" {
				t.Fatalf("model_cap = %q", runs[0].ModelCap)
			}
			if strings.Contains(strings.Join(e.launcher.calls[0].Args, " "), "--model") {
				t.Errorf("args = %q", e.launcher.calls[0].Args)
			}
		})
	}
}

func TestAutopilotStart_InvalidModelIs400AndReservesNothing(t *testing.T) {
	e := newAutopilotEnv(t)
	r := e.ticket(t, "R", "")
	for _, body := range []string{`{"mode": "ticket", "model": "fable"}`, `{"mode": "ticket", "model": ""}`, `{"mode": "ticket", "model": "opus; rm -rf /"}`, `{"mode": "ticket", "model": 3}`} {
		rec := doAutopilotRaw(e.s, http.MethodPost, startAutopilotPath(r), body, true)
		if rec.Code != http.StatusBadRequest || decodeError(t, rec).Code != domain.ErrCodeValidation {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if e.launcher.count() != 0 || len(e.runs(t)) != 0 {
		t.Fatalf("launches = %d, runs = %d", e.launcher.count(), len(e.runs(t)))
	}
}

// The runs API shows the cap.
func TestAutopilotRuns_ShowModelCap(t *testing.T) {
	e := newAutopilotEnv(t)
	r := e.ticket(t, "R", "")
	sonnet := "sonnet"
	if _, err := e.svc().StartWithModel(r, autopilot.ModeTicket, "", false, &sonnet); err != nil {
		t.Fatal(err)
	}
	runs := decodeRuns(t, doJSON(t, e.s, http.MethodGet, "/api/autopilot/runs?project_id="+e.pid, nil))
	if len(runs) != 1 || runs[0].ModelCap != "sonnet" {
		t.Errorf("runs = %+v, want model_cap sonnet", runs)
	}
}
