package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
)

// DFLT-00084: refine-ticket --label <name> (repeatable) replaces the
// ticket's labels; omitted leaves them untouched.

func TestCmdRefineTicket_LabelsReplaced(t *testing.T) {
	repo, projectID := cliLabelSetup(t)
	eng := engine.New(repo)
	tk, err := eng.CreateTicketWithOptions(projectID, "A-1", "元の説明", engine.CreateTicketOptions{LabelNames: []string{"バグ", "UI"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	out := captureStdout(t, func() {
		if err := cmdRefineTicket(eng, []string{tk.ID, "新しい説明", "--label", "機能追加", "--label", "UI"}); err != nil {
			t.Fatalf("cmdRefineTicket: %v", err)
		}
	})
	got := decodeCLITicket(t, out)
	if cliLabelNames(got.Labels) != "UI,機能追加" || got.Description != "新しい説明" {
		t.Errorf("unexpected ticket: %+v", got)
	}
	stored, _ := repo.GetTicket(tk.ID)
	if cliLabelNames(stored.Labels) != "UI,機能追加" {
		t.Errorf("stored labels = %q", cliLabelNames(stored.Labels))
	}
}

func TestCmdRefineTicket_NoLabelFlagLeavesLabels(t *testing.T) {
	repo, projectID := cliLabelSetup(t)
	eng := engine.New(repo)
	tk, err := eng.CreateTicketWithOptions(projectID, "A-1", "元の説明", engine.CreateTicketOptions{LabelNames: []string{"バグ", "UI"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	out := captureStdout(t, func() {
		if err := cmdRefineTicket(eng, []string{tk.ID, "新しい説明"}); err != nil {
			t.Fatalf("cmdRefineTicket: %v", err)
		}
	})
	if got := decodeCLITicket(t, out); cliLabelNames(got.Labels) != "UI,バグ" {
		t.Errorf("labels must be unchanged: %+v", got)
	}
}

func TestCmdRefineTicket_PriorityAndLabels(t *testing.T) {
	repo, projectID := cliLabelSetup(t)
	eng := engine.New(repo)
	low := domain.TicketPriorityLow
	tk, err := eng.CreateTicketWithOptions(projectID, "A-1", "元の説明", engine.CreateTicketOptions{Priority: &low, LabelNames: []string{"バグ"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	out := captureStdout(t, func() {
		if err := cmdRefineTicket(eng, []string{tk.ID, "説明", "--priority", "HIGH", "--label", "UI"}); err != nil {
			t.Fatalf("cmdRefineTicket: %v", err)
		}
	})
	got := decodeCLITicket(t, out)
	if got.Priority != "HIGH" || cliLabelNames(got.Labels) != "UI" {
		t.Errorf("unexpected ticket: %+v", got)
	}
}

func TestCmdRefineTicket_UnregisteredLabelChangesNothing(t *testing.T) {
	repo, projectID := cliLabelSetup(t)
	eng := engine.New(repo)
	low := domain.TicketPriorityLow
	tk, err := eng.CreateTicketWithOptions(projectID, "A-1", "元の説明", engine.CreateTicketOptions{Priority: &low, LabelNames: []string{"バグ"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var runErr error
	out := captureStdout(t, func() {
		runErr = cmdRefineTicket(eng, []string{tk.ID, "新しい説明", "--priority", "HIGH", "--label", "UI", "--label", "未登録"})
	})
	var apiErr *domain.APIError
	if runErr == nil || !errors.As(runErr, &apiErr) || apiErr.Code != domain.ErrCodeLabelNotFound {
		t.Fatalf("expected LABEL_NOT_FOUND, got %v", runErr)
	}
	if out != "" {
		t.Errorf("nothing must be printed, got %q", out)
	}
	stored, _ := repo.GetTicket(tk.ID)
	if stored.Description != "元の説明" || stored.Priority != low || cliLabelNames(stored.Labels) != "バグ" ||
		stored.Status != domain.TicketTODO || stored.RefinedAt != nil || stored.UpdatedAt != tk.UpdatedAt {
		t.Errorf("the ticket must be unchanged: %+v", stored)
	}
}

func TestCmdRefineTicket_ClosedTicketWithLabels(t *testing.T) {
	repo, projectID := cliLabelSetup(t)
	eng := engine.New(repo)
	tk, err := eng.CreateTicketWithOptions(projectID, "A-9", "元の説明", engine.CreateTicketOptions{LabelNames: []string{"バグ"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := eng.CloseTicket(tk.ID, ""); err != nil {
		t.Fatalf("CloseTicket: %v", err)
	}
	err = cmdRefineTicket(eng, []string{tk.ID, "説明", "--label", "UI", "--label", "未登録"})
	if err == nil || !strings.Contains(err.Error(), "ticket "+tk.ID+" is CLOSED; reopen it first with reopen-ticket") {
		t.Fatalf("expected the CLOSED error, got %v", err)
	}
	var apiErr *domain.APIError
	if errors.As(err, &apiErr) && apiErr.Code == domain.ErrCodeLabelNotFound {
		t.Error("the error must not be LABEL_NOT_FOUND")
	}
	stored, _ := repo.GetTicket(tk.ID)
	if stored.Description != "元の説明" || cliLabelNames(stored.Labels) != "バグ" || stored.Status != domain.TicketClosed {
		t.Errorf("the CLOSED ticket must be unchanged: %+v", stored)
	}
}
