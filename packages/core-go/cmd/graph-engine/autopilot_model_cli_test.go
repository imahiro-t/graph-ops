package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00375: `autopilot start --model` records the run's model cap, the
// sessions it launches get --model <cap>, worker-context and summary show
// it, and a --model-less restart keeps it.
func TestAutopilotCLI_ModelCap(t *testing.T) {
	repo, rc, projectID, _, launches := autopilotCLISetup(t)
	eng := engine.New(repo)
	root, _ := eng.CreateTicket(projectID, "Root", "")

	// An invalid --model creates nothing.
	for _, v := range []string{"gpt-4", "fable", ""} {
		_, err := runAutopilot(t, repo, rc, "", "start", root.ID, "--mode", "ticket", "--model", v)
		assertAPIErrorCode(t, err, domain.ErrCodeValidation)
	}
	if status := mustAutopilotList(t, repo, rc, projectID); len(status) != 0 {
		t.Fatalf("runs after refused starts = %v", status)
	}

	start := mustAutopilot(t, repo, rc, "", "start", root.ID, "--mode", "ticket", "--model", "sonnet")
	runID, _ := start["run_id"].(string)
	if start["model_cap"] != "sonnet" {
		t.Fatalf("start = %v", start)
	}
	mustAutopilot(t, repo, rc, "", "next", runID)
	mustAutopilot(t, repo, rc, "", "launch", runID, root.ID)
	if got := strings.Join((*launches)[0].args, " "); got != "--permission-mode acceptEdits --model sonnet" {
		t.Fatalf("launch args = %q", got)
	}
	if runs := mustAutopilotList(t, repo, rc, projectID); len(runs) != 1 || runs[0]["model_cap"] != "sonnet" {
		t.Fatalf("status runs = %v", runs)
	}
	ctx := mustAutopilot(t, repo, rc, "", "worker-context", runID, root.ID)
	if ctx["model_cap"] != "sonnet" {
		t.Fatalf("worker-context = %v", ctx)
	}
	out, err := runAutopilot(t, repo, rc, "", "summary", runID)
	if err != nil || !strings.Contains(out, "Model cap: sonnet") {
		t.Fatalf("summary = %v\n%s", err, out)
	}
}

func TestAutopilotCLI_NoModelMeansNoCap(t *testing.T) {
	repo, rc, projectID, _, launches := autopilotCLISetup(t)
	eng := engine.New(repo)
	root, _ := eng.CreateTicket(projectID, "Root", "")
	start := mustAutopilot(t, repo, rc, "", "start", root.ID, "--mode", "ticket")
	runID, _ := start["run_id"].(string)
	if start["model_cap"] != "" {
		t.Fatalf("start = %v", start)
	}
	mustAutopilot(t, repo, rc, "", "next", runID)
	mustAutopilot(t, repo, rc, "", "launch", runID, root.ID)
	if got := strings.Join((*launches)[0].args, " "); strings.Contains(got, "--model") {
		t.Fatalf("launch args = %q", got)
	}
	if ctx := mustAutopilot(t, repo, rc, "", "worker-context", runID, root.ID); ctx["model_cap"] != "" {
		t.Fatalf("worker-context = %v", ctx)
	}
}

// mustAutopilotList returns `autopilot status`'s runs for the project.
func mustAutopilotList(t *testing.T, repo store.GraphRepository, rc runtimeConfig, projectID string) []map[string]any {
	t.Helper()
	out, err := runAutopilot(t, repo, rc, "", "status", "--project", projectID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var resp struct {
		Runs []map[string]any `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("status output: %v\n%s", err, out)
	}
	return resp.Runs
}
