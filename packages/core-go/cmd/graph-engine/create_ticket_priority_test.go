package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/engine"
)

// DFLT-00059: create-ticket gained a --priority <HIGH|MEDIUM|LOW> flag,
// interspersed among the positionals the same way --project already is (see
// create_ticket_assignee_test.go's table for that precedent). These tests
// mirror that file's shape for the new flag.

func TestCmdCreateTicket_PriorityFlag(t *testing.T) {
	cases := []struct {
		name string
		args func(projectID string) []string
	}{
		{"--priority last", func(p string) []string { return []string{"t1", "d1", "--project", p, "--priority", "HIGH"} }},
		{"--priority first", func(p string) []string { return []string{"--priority", "HIGH", "t1", "d1", "--project", p} }},
		{"--priority between positionals", func(p string) []string {
			return []string{"t1", "--priority", "HIGH", "d1", "--project", p}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, projectID := newTestRepoWithProject(t)
			eng := engine.New(repo)

			var runErr error
			out := captureStdout(t, func() {
				runErr = cmdCreateTicket(eng, repo, runtimeConfig{}, tc.args(projectID))
			})
			if runErr != nil {
				t.Fatalf("cmdCreateTicket: %v", runErr)
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(out), &m); err != nil {
				t.Fatalf("stdout is not JSON: %v\n%s", err, out)
			}
			if m["title"] != "t1" || m["description"] != "d1" || m["priority"] != "HIGH" {
				t.Errorf("unexpected ticket: %v", m)
			}
		})
	}
}

func TestCmdCreateTicket_NoPriorityFlagLeavesPriorityUnset(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)

	out := captureStdout(t, func() {
		if err := cmdCreateTicket(eng, repo, runtimeConfig{}, []string{"t1", "--project", projectID}); err != nil {
			t.Fatalf("cmdCreateTicket: %v", err)
		}
	})
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if _, ok := m["priority"]; ok {
		t.Errorf("stdout JSON must not contain a priority key when --priority is omitted: %v", m)
	}
}

func TestCmdCreateTicket_InvalidPriorityIsUsageErrorAndCreatesNothing(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)

	err := cmdCreateTicket(eng, repo, runtimeConfig{}, []string{"t1", "--project", projectID, "--priority", "URGENT"})
	if err == nil || !strings.Contains(err.Error(), createTicketUsage) {
		t.Fatalf("expected the create-ticket usage error, got %v", err)
	}
	if tickets, _ := repo.ListTicketsByProject(projectID); len(tickets) != 0 {
		t.Errorf("no ticket should be created, got %d", len(tickets))
	}
}

func TestPrintUsage_MentionsPriorityFlags(t *testing.T) {
	out := captureStdout(t, printUsage)
	if !strings.Contains(out, "create-ticket <title> [description] [--project <id>] [--priority <HIGH|MEDIUM|LOW>]") {
		t.Errorf("usage should list create-ticket's --priority flag:\n%s", out)
	}
	if !strings.Contains(out, "refine-ticket <ticketId> [description|-] [--priority <HIGH|MEDIUM|LOW|none>]") {
		t.Errorf("usage should list refine-ticket's --priority flag:\n%s", out)
	}
}
