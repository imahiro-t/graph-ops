package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
)

// --- DFLT-00101 / BUG-14: grant-iterations is CLI-only plumbing around
// engine.GrantIterations, whose contract (budget only, no partial
// application, the three-step recovery it starts) is exercised in depth by
// internal/engine's tests. These cover the CLI-level argument handling and
// wiring only. ---

func TestCmdGrantIterations_RaisesBudgetAndPrintsNodes(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	ticketID, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeImplementation)
	before, _ := repo.GetNode(nodeID)

	var cmdErr error
	out := captureStdout(t, func() {
		cmdErr = cmdGrantIterations(eng, []string{ticketID, nodeID, "--extra", "2"})
	})
	if cmdErr != nil {
		t.Fatalf("cmdGrantIterations: %v", cmdErr)
	}
	var printed []map[string]any
	if err := json.Unmarshal([]byte(out), &printed); err != nil {
		t.Fatalf("unmarshal stdout %q: %v", out, err)
	}
	if len(printed) != 1 {
		t.Fatalf("expected one node printed, got %s", out)
	}
	if got := printed[0]["max_iterations"]; got != float64(before.MaxIterations+2) {
		t.Errorf("printed max_iterations = %v, want %d", got, before.MaxIterations+2)
	}
	after, _ := repo.GetNode(nodeID)
	if after.MaxIterations != before.MaxIterations+2 {
		t.Errorf("stored max_iterations = %d, want %d", after.MaxIterations, before.MaxIterations+2)
	}
	if after.IterationCount != before.IterationCount || after.Status != before.Status {
		t.Errorf("iteration_count/status must not change: %+v -> %+v", before, after)
	}
}

// --extra is optional: one extra iteration is the default, since each grant is
// meant to be a deliberate decision rather than a big lump of retries.
func TestCmdGrantIterations_DefaultsToOneExtra(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	ticketID, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeImplementation)
	before, _ := repo.GetNode(nodeID)

	captureStdout(t, func() {
		if err := cmdGrantIterations(eng, []string{ticketID, nodeID}); err != nil {
			t.Errorf("cmdGrantIterations: %v", err)
		}
	})

	after, _ := repo.GetNode(nodeID)
	if after.MaxIterations != before.MaxIterations+1 {
		t.Errorf("stored max_iterations = %d, want %d", after.MaxIterations, before.MaxIterations+1)
	}
}

func TestCmdGrantIterations_UsageErrorsWriteNothing(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	ticketID, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeImplementation)
	before, _ := repo.GetNode(nodeID)

	cases := []struct {
		name string
		args []string
	}{
		{"no arguments", nil},
		{"ticket id only", []string{ticketID}},
		{"empty id list after trimming", []string{ticketID, "  ,  ,"}},
		{"--extra without a value", []string{ticketID, nodeID, "--extra"}},
		{"--extra not a number", []string{ticketID, nodeID, "--extra", "lots"}},
		{"unrecognized argument", []string{ticketID, nodeID, "--unknown-flag"}},
		{"zero extra", []string{ticketID, nodeID, "--extra", "0"}},
		{"negative extra", []string{ticketID, nodeID, "--extra", "-2"}},
		{"absurd extra", []string{ticketID, nodeID, "--extra", "1000000"}},
		{"unknown node id", []string{ticketID, "NOPE-00001-01"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := cmdGrantIterations(eng, tc.args); err == nil {
				t.Fatalf("expected an error")
			}
			after, _ := repo.GetNode(nodeID)
			if after.MaxIterations != before.MaxIterations || after.IterationCount != before.IterationCount || after.Status != before.Status {
				t.Errorf("the node must be untouched: %+v -> %+v", before, after)
			}
		})
	}
}

// The comma-separated list is split and forwarded, and a node belonging to a
// different ticket is caught by engine.GrantIterations' ownership check -- the
// reason this command takes a ticket id at all, matching reopen-nodes.
func TestCmdGrantIterations_SplitsIDsAndChecksTicketOwnership(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	ticketID, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeImplementation)
	otherTicketID, otherNodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeImplementation)
	before, _ := repo.GetNode(nodeID)

	err := cmdGrantIterations(eng, []string{ticketID, nodeID + "," + otherNodeID})
	if err == nil {
		t.Fatalf("expected an error for a node from another ticket")
	}
	if !strings.Contains(err.Error(), "does not belong") {
		t.Errorf("expected a 'does not belong' error, got %v", err)
	}
	after, _ := repo.GetNode(nodeID)
	if after.MaxIterations != before.MaxIterations {
		t.Errorf("no partial application: this ticket's node must be untouched")
	}

	// Same two ids, each against its own ticket: both succeed.
	captureStdout(t, func() {
		if err := cmdGrantIterations(eng, []string{ticketID, nodeID}); err != nil {
			t.Errorf("cmdGrantIterations(%s): %v", ticketID, err)
		}
		if err := cmdGrantIterations(eng, []string{otherTicketID, otherNodeID}); err != nil {
			t.Errorf("cmdGrantIterations(%s): %v", otherTicketID, err)
		}
	})
}
