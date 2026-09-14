package main

import (
	"encoding/json"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
)

// DFLT-00043: cmdCloseTicket/cmdReopenTicket are CLI-only plumbing around
// engine.CloseTicket/engine.ReopenTicket, whose own behavior is exercised in
// depth by internal/engine's tests. These tests cover the CLI-level argument
// handling and wiring only.

func TestCmdCloseTicket_SavesReasonAndPrintsTicketJSON(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	var cmdErr error
	out := captureStdout(t, func() {
		cmdErr = cmdCloseTicket(eng, []string{ticket.ID, "--reason", "対応不要になったため"})
	})
	if cmdErr != nil {
		t.Fatalf("cmdCloseTicket: %v", cmdErr)
	}
	var got domain.Ticket
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal stdout %q: %v", out, err)
	}
	if got.Status != domain.TicketClosed {
		t.Errorf("expected status CLOSED, got %+v", got)
	}
	if got.ClosedReason == nil || *got.ClosedReason != "対応不要になったため" {
		t.Errorf("expected closed_reason to be saved, got %+v", got.ClosedReason)
	}
}

func TestCmdCloseTicket_ReasonIsOptional(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	var cmdErr error
	captureStdout(t, func() {
		cmdErr = cmdCloseTicket(eng, []string{ticket.ID})
	})
	if cmdErr != nil {
		t.Fatalf("cmdCloseTicket without --reason: %v", cmdErr)
	}
	got, err := repo.GetTicket(ticket.ID)
	if err != nil || got.Status != domain.TicketClosed {
		t.Fatalf("expected ticket to be CLOSED, got %+v (err=%v)", got, err)
	}
}

func TestCmdCloseTicket_UsageErrors(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	if err := cmdCloseTicket(eng, nil); err == nil {
		t.Error("expected an error with no arguments")
	}
	if err := cmdCloseTicket(eng, []string{ticket.ID, "--reason"}); err == nil {
		t.Error("expected an error when --reason has no value")
	}
	if err := cmdCloseTicket(eng, []string{ticket.ID, "--unknown-flag"}); err == nil {
		t.Error("expected an error for an unrecognized argument")
	}
}

func TestCmdReopenTicket_MovesTicketOutOfClosed(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if _, err := eng.CloseTicket(ticket.ID, "reason"); err != nil {
		t.Fatalf("CloseTicket: %v", err)
	}

	var cmdErr error
	out := captureStdout(t, func() {
		cmdErr = cmdReopenTicket(eng, []string{ticket.ID})
	})
	if cmdErr != nil {
		t.Fatalf("cmdReopenTicket: %v", cmdErr)
	}
	var got domain.Ticket
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal stdout %q: %v", out, err)
	}
	if got.Status == domain.TicketClosed {
		t.Errorf("expected the ticket to no longer be CLOSED, got %+v", got)
	}
}

func TestCmdReopenTicket_UsageErrorAndPropagatesEngineError(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)

	if err := cmdReopenTicket(eng, nil); err == nil {
		t.Error("expected an error with no arguments")
	}

	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	// Never closed, so engine.ReopenTicket must reject this.
	if err := cmdReopenTicket(eng, []string{ticket.ID}); err == nil {
		t.Error("expected engine.ReopenTicket's error (not CLOSED) to propagate")
	}
}
