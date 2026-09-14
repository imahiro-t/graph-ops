package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
)

// DFLT-00059: refine-ticket gained a --priority <HIGH|MEDIUM|LOW|none> flag,
// independent of the [description|-] positional. These tests exercise the
// set/change/clear/unchanged states and the flag's interaction with the
// description positional.

const refineTicketUsage = "usage: graph-engine refine-ticket <ticketId> [description|-] [--priority <HIGH|MEDIUM|LOW|none>]"

func TestCmdRefineTicket_PrioritySetChangedCleared(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	ticket, err := eng.CreateTicket(projectID, "title", "original description")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	// Set, with no description positional -- the description must stay
	// unchanged while priority is set.
	out := captureStdout(t, func() {
		if err := cmdRefineTicket(eng, []string{ticket.ID, "--priority", "HIGH"}); err != nil {
			t.Fatalf("cmdRefineTicket: %v", err)
		}
	})
	m := map[string]any{}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if m["priority"] != "HIGH" || m["description"] != "original description" {
		t.Errorf("set: unexpected ticket: %v", m)
	}

	// Change, together with a new description in the same call.
	out = captureStdout(t, func() {
		if err := cmdRefineTicket(eng, []string{ticket.ID, "new description", "--priority", "LOW"}); err != nil {
			t.Fatalf("cmdRefineTicket: %v", err)
		}
	})
	m = map[string]any{}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if m["priority"] != "LOW" || m["description"] != "new description" {
		t.Errorf("change: unexpected ticket: %v", m)
	}

	// "none" clears it back to unset.
	out = captureStdout(t, func() {
		if err := cmdRefineTicket(eng, []string{ticket.ID, "--priority", "none"}); err != nil {
			t.Fatalf("cmdRefineTicket: %v", err)
		}
	})
	m = map[string]any{}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if _, ok := m["priority"]; ok {
		t.Errorf("clear: stdout JSON must not contain a priority key, got %v", m)
	}
}

func TestCmdRefineTicket_NoPriorityFlagLeavesPriorityUnchanged(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	ticket, err := eng.CreateTicketWithPriority(projectID, "title", "original description", nil)
	if err != nil {
		t.Fatalf("CreateTicketWithPriority: %v", err)
	}
	if _, err := eng.RefineTicket(ticket.ID, "", engine.SetPriority(domain.TicketPriorityHigh)); err != nil {
		t.Fatalf("seeding priority: %v", err)
	}

	out := captureStdout(t, func() {
		if err := cmdRefineTicket(eng, []string{ticket.ID, "updated description"}); err != nil {
			t.Fatalf("cmdRefineTicket: %v", err)
		}
	})
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if m["priority"] != "HIGH" || m["description"] != "updated description" {
		t.Errorf("omitting --priority must leave the stored priority untouched: %v", m)
	}
}

func TestCmdRefineTicket_InvalidPriorityIsUsageErrorAndLeavesTicketUnchanged(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	ticket, err := eng.CreateTicket(projectID, "title", "original description")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	err = cmdRefineTicket(eng, []string{ticket.ID, "--priority", "URGENT"})
	if err == nil || !strings.Contains(err.Error(), refineTicketUsage) {
		t.Fatalf("expected the refine-ticket usage error, got %v", err)
	}

	got, err := repo.GetTicket(ticket.ID)
	if err != nil || got == nil {
		t.Fatalf("GetTicket: %v, %+v", err, got)
	}
	if got.Priority != nil || got.Description != "original description" {
		t.Errorf("a rejected --priority must leave the ticket unchanged, got %+v", got)
	}
}

func TestCmdRefineTicket_PriorityFlagMissingValueIsUsageError(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	ticket, err := eng.CreateTicket(projectID, "title", "")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	err = cmdRefineTicket(eng, []string{ticket.ID, "--priority"})
	if err == nil || !strings.Contains(err.Error(), "--priority requires a value") {
		t.Fatalf("expected a --priority-requires-a-value error, got %v", err)
	}
}
