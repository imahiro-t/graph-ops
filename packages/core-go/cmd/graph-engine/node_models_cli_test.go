package main

// DFLT-00375: get-executable prints, for each node it hands out, the model
// to start that node's subagent on -- min(node_models assignment, cap), ""
// when it equals the session's own model family -- and get-workflow-catalog
// prints the merged node_models.

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// expandedModelTicket creates a ticket whose seed is DONE and whose graph
// is expanded so that an implementation, a report and a gherkin_test node
// are executable at once.
func expandedModelTicket(t *testing.T, eng *engine.GraphEngine, rc runtimeConfig, projectID string) string {
	t.Helper()
	cat, err := config.LoadWithRoots(rc.UserExtensionsDir, rc.TeamExtensionsDir, "")
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := eng.CreateTicket(projectID, "title", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		exec, err := eng.GetExecutableNodes(ticket.ID, cat)
		if err != nil || len(exec) != 1 {
			t.Fatalf("seed step %d: %v %v", i, exec, err)
		}
		if _, err := eng.CompleteNode(exec[0].ID, true, nil); err != nil {
			t.Fatal(err)
		}
	}
	patch := &engine.Patch{ExtraNodes: []engine.ExtraNode{
		{ID: "impl", Name: "impl", Type: "implementation", DependsOn: []string{"plan_review"}},
		{ID: "rep", Name: "rep", Type: "report", DependsOn: []string{"plan_review"}},
		{ID: "gt", Name: "gt", Type: "gherkin_test", DependsOn: []string{"plan_review"}},
		{ID: "release", Name: "release", Type: "release", DependsOn: []string{"impl", "rep", "gt"}, IsManual: true},
	}}
	if err := eng.ExpandGraph(ticket.ID, cat, patch); err != nil {
		t.Fatalf("ExpandGraph: %v", err)
	}
	return ticket.ID
}

type modelNodeView struct {
	Type   string  `json:"type"`
	Status string  `json:"status"`
	Model  *string `json:"model"`
}

func runGetExecutableModels(t *testing.T, eng *engine.GraphEngine, repo store.GraphRepository, rc runtimeConfig, args ...string) (map[string]string, error) {
	t.Helper()
	var cmdErr error
	out := captureStdout(t, func() { cmdErr = cmdGetExecutable(eng, repo, rc, args) })
	if cmdErr != nil {
		return nil, cmdErr
	}
	var nodes []modelNodeView
	if err := json.Unmarshal([]byte(out), &nodes); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	models := map[string]string{}
	for _, n := range nodes {
		if n.Model == nil {
			t.Fatalf("node %s has no model field:\n%s", n.Type, out)
		}
		models[n.Type] = *n.Model
	}
	return models, nil
}

func TestGetExecutable_ModelPerNode(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want map[string]string
	}{
		{"no flags", nil, map[string]string{"implementation": "", "report": "haiku", "gherkin_test": "sonnet"}},
		{"session only", []string{"--session-model", "claude-opus-4-5"}, map[string]string{"implementation": "", "report": "haiku", "gherkin_test": "sonnet"}},
		{"session sonnet", []string{"--session-model", "claude-sonnet-4-5-20250929"}, map[string]string{"implementation": "", "report": "haiku", "gherkin_test": ""}},
		{"cap haiku", []string{"--session-model", "claude-opus-4-5", "--model-cap", "haiku"}, map[string]string{"implementation": "haiku", "report": "haiku", "gherkin_test": "haiku"}},
		{"cap equals session variant", []string{"--session-model", "opus[1m]", "--model-cap", "opus"}, map[string]string{"implementation": "", "report": "haiku", "gherkin_test": "sonnet"}},
		{"cap above session", []string{"--session-model", "claude-sonnet-4-5", "--model-cap", "Opus"}, map[string]string{"implementation": "opus", "report": "haiku", "gherkin_test": ""}},
		{"unknown session", []string{"--session-model", "unknown-model"}, map[string]string{"implementation": "", "report": "haiku", "gherkin_test": "sonnet"}},
	}
	forEachBackend(t, func(t *testing.T, repo store.GraphRepository, projectID string) {
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				rc := runtimeConfig{WorkDir: t.TempDir(), UserExtensionsDir: t.TempDir(), HomeDir: t.TempDir()}
				eng := engine.New(repo)
				ticketID := expandedModelTicket(t, eng, rc, projectID)
				got, err := runGetExecutableModels(t, eng, repo, rc, append([]string{ticketID}, c.args...)...)
				if err != nil {
					t.Fatalf("get-executable: %v", err)
				}
				if len(got) != len(c.want) {
					t.Fatalf("nodes = %v, want %v", got, c.want)
				}
				for typ, w := range c.want {
					if got[typ] != w {
						t.Errorf("%s model = %q, want %q", typ, got[typ], w)
					}
				}
			})
		}
	})
}

// The user/team tiers' node_models feed get-executable.
func TestGetExecutable_ModelFollowsNodeModelsOverride(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	rc := runtimeConfig{WorkDir: t.TempDir(), UserExtensionsDir: t.TempDir(), TeamExtensionsDir: t.TempDir(), HomeDir: t.TempDir()}
	writeFile(t, filepath.Join(rc.UserExtensionsDir, "config.yaml"), "version: 1\nnode_models:\n  report: inherit\n")
	writeFile(t, filepath.Join(rc.TeamExtensionsDir, "workflow.yaml"), "version: 1\nnode_models:\n  implementation: sonnet\n")
	eng := engine.New(repo)
	ticketID := expandedModelTicket(t, eng, rc, projectID)
	got, err := runGetExecutableModels(t, eng, repo, rc, ticketID, "--session-model", "claude-opus-4-5")
	if err != nil {
		t.Fatal(err)
	}
	if got["report"] != "" || got["implementation"] != "sonnet" || got["gherkin_test"] != "sonnet" {
		t.Errorf("models = %v", got)
	}
}

// A bad cap -- inherit included -- is VALIDATION_ERROR, and nothing is
// claimed.
func TestGetExecutable_InvalidModelCapClaimsNothing(t *testing.T) {
	for _, v := range []string{"fable", "inherit", "gpt-4"} {
		t.Run(v, func(t *testing.T) {
			repo, projectID := newTestRepoWithProject(t)
			rc := runtimeConfig{WorkDir: t.TempDir(), UserExtensionsDir: t.TempDir(), HomeDir: t.TempDir()}
			eng := engine.New(repo)
			ticketID := expandedModelTicket(t, eng, rc, projectID)
			_, err := runGetExecutableModels(t, eng, repo, rc, ticketID, "--model-cap", v)
			assertAPIErrorCode(t, err, domain.ErrCodeValidation)
			nodes, err := repo.ListNodesByTicket(ticketID)
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range nodes {
				if n.Status == domain.NodeInProgress {
					t.Errorf("node %s (%s) was claimed", n.ID, n.Type)
				}
			}
		})
	}
}

func TestGetWorkflowCatalog_ReportsNodeModels(t *testing.T) {
	env := newCatalogWarningEnv(t, "")
	writeFile(t, filepath.Join(env.rc.UserExtensionsDir, "config.yaml"), "version: 1\nnode_models:\n  report: inherit\n  security_scan: haiku\n")
	writeFile(t, filepath.Join(env.rc.TeamExtensionsDir, "workflow.yaml"), "version: 1\nnode_models:\n  report: sonnet\n")
	out := captureStdout(t, func() {
		if err := cmdGetWorkflowCatalog(env.rc, nil); err != nil {
			t.Fatal(err)
		}
	})
	var resp struct {
		NodeModels map[string]string `json:"node_models"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	for typ, w := range map[string]string{"report": "sonnet", "gherkin_test": "sonnet", "release": "sonnet", "plan": "inherit", "security_scan": "haiku"} {
		if resp.NodeModels[typ] != w {
			t.Errorf("node_models[%s] = %q, want %q (all: %v)", typ, resp.NodeModels[typ], w, resp.NodeModels)
		}
	}
}
