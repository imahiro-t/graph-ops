package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
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
				runErr = cmdCreateTicket(eng, repo, sandboxRC(t), tc.args(projectID))
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

// TestCmdCreateTicket_NoPriorityFlagDefaultsToMedium: DFLT-00083 removed the
// unset state, so omitting --priority creates a MEDIUM ticket and stdout
// always carries the priority key.
func TestCmdCreateTicket_NoPriorityFlagDefaultsToMedium(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)

	out := captureStdout(t, func() {
		if err := cmdCreateTicket(eng, repo, sandboxRC(t), []string{"t1", "--project", projectID}); err != nil {
			t.Fatalf("cmdCreateTicket: %v", err)
		}
	})
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if m["priority"] != "MEDIUM" {
		t.Errorf("omitting --priority should create a MEDIUM ticket, got %v", m)
	}
	got, err := repo.GetTicket(m["id"].(string))
	if err != nil || got == nil || got.Priority != domain.TicketPriorityMedium {
		t.Errorf("stored priority should be MEDIUM, got %v, %+v", err, got)
	}
}

func TestCmdCreateTicket_EachPriorityValue(t *testing.T) {
	for _, p := range []string{"HIGH", "MEDIUM", "LOW"} {
		t.Run(p, func(t *testing.T) {
			repo, projectID := newTestRepoWithProject(t)
			eng := engine.New(repo)
			out := captureStdout(t, func() {
				if err := cmdCreateTicket(eng, repo, sandboxRC(t), []string{"t1", "--project", projectID, "--priority", p}); err != nil {
					t.Fatalf("cmdCreateTicket: %v", err)
				}
			})
			var m map[string]any
			if err := json.Unmarshal([]byte(out), &m); err != nil {
				t.Fatalf("stdout is not JSON: %v\n%s", err, out)
			}
			if m["priority"] != p {
				t.Errorf("priority = %v, want %s", m["priority"], p)
			}
		})
	}
}

func TestCmdCreateTicket_InvalidPriorityIsUsageErrorAndCreatesNothing(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)

	err := cmdCreateTicket(eng, repo, sandboxRC(t), []string{"t1", "--project", projectID, "--priority", "URGENT"})
	if err == nil || !strings.Contains(err.Error(), createTicketUsage) {
		t.Fatalf("expected the create-ticket usage error, got %v", err)
	}
	if tickets, _ := repo.ListTicketsByProject(projectID); len(tickets) != 0 {
		t.Errorf("no ticket should be created, got %d", len(tickets))
	}
}

func TestPrintUsage_MentionsPriorityFlags(t *testing.T) {
	out := captureStdout(t, printUsage)
	if !strings.Contains(out, "create-ticket <title> [description|-] [--project <id>] [--priority <HIGH|MEDIUM|LOW>]") {
		t.Errorf("usage should list create-ticket's --priority flag:\n%s", out)
	}
	if !strings.Contains(out, "refine-ticket <ticketId> [description|-] [--priority <HIGH|MEDIUM|LOW>]") {
		t.Errorf("usage should list refine-ticket's --priority flag:\n%s", out)
	}
	// DFLT-00083: the default is documented, and nothing offers to leave
	// the priority unset or clear it.
	if !strings.Contains(out, "--priority omitted -> created with priority MEDIUM") {
		t.Errorf("usage should say an omitted --priority means MEDIUM:\n%s", out)
	}
	for _, stale := range []string{"LOW|none", "no priority set", `"none" clears`, "back to\n                                           unset"} {
		if strings.Contains(out, stale) {
			t.Errorf("usage must not mention %q any more:\n%s", stale, out)
		}
	}
}
