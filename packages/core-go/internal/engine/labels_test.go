package engine

import (
	"errors"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00084: labels given by name on ticket creation and refine.

func engineLabelSetup(t *testing.T) (*GraphEngine, store.GraphRepository, string, map[string]domain.Label) {
	t.Helper()
	eng, repo, projectID := newTestEngine(t)
	labels := map[string]domain.Label{}
	for _, l := range []struct{ name, color string }{{"バグ", "red"}, {"機能追加", "blue"}, {"UI", "purple"}, {"Bug", "gray"}} {
		created, err := repo.CreateLabel(projectID, l.name, l.color)
		if err != nil {
			t.Fatalf("CreateLabel: %v", err)
		}
		labels[l.name] = created
	}
	return eng, repo, projectID, labels
}

func namesOf(labels []domain.Label) string {
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		out = append(out, l.Name)
	}
	return strings.Join(out, ",")
}

func requireLabelNotFound(t *testing.T, err error) *domain.APIError {
	t.Helper()
	var apiErr *domain.APIError
	if err == nil || !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeLabelNotFound {
		t.Fatalf("expected LABEL_NOT_FOUND, got %v", err)
	}
	return apiErr
}

func TestCreateTicketWithOptions_LabelsByName(t *testing.T) {
	eng, repo, projectID, _ := engineLabelSetup(t)

	tk, err := eng.CreateTicketWithOptions(projectID, "T", "D", CreateTicketOptions{LabelNames: []string{"UI", "バグ"}})
	if err != nil {
		t.Fatalf("CreateTicketWithOptions: %v", err)
	}
	if got := namesOf(tk.Labels); got != "UI,バグ" {
		t.Errorf("labels = %q, want UI,バグ", got)
	}

	// Trimmed, case-insensitive, duplicates collapsed.
	tk, err = eng.CreateTicketWithOptions(projectID, "T", "D", CreateTicketOptions{LabelNames: []string{"bug", " BUG "}})
	if err != nil {
		t.Fatalf("CreateTicketWithOptions: %v", err)
	}
	if got := namesOf(tk.Labels); got != "Bug" {
		t.Errorf("labels = %q, want Bug", got)
	}

	// No labels.
	tk, err = eng.CreateTicket(projectID, "T", "D")
	if err != nil || tk.Labels == nil || len(tk.Labels) != 0 {
		t.Errorf("CreateTicket without labels: %+v, %v", tk.Labels, err)
	}

	// Unregistered name: error naming it and the registered labels; nothing created.
	before, _ := repo.ListTicketsByProject(projectID)
	_, err = eng.CreateTicketWithOptions(projectID, "T", "D", CreateTicketOptions{LabelNames: []string{"バグ", "未登録"}})
	apiErr := requireLabelNotFound(t, err)
	for _, want := range []string{"未登録", "バグ", "機能追加", "UI", "LABEL_NOT_FOUND"} {
		if !strings.Contains(apiErr.Message, want) {
			t.Errorf("message %q should mention %q", apiErr.Message, want)
		}
	}
	after, _ := repo.ListTicketsByProject(projectID)
	if len(after) != len(before) {
		t.Errorf("an unregistered label must not create a ticket: %d -> %d", len(before), len(after))
	}

	// A label that only exists in another project is unregistered here.
	other, err := repo.CreateProject("Beta", "BETA")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := repo.CreateLabel(other.ID, "Betaだけ", "red"); err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	_, err = eng.CreateTicketWithOptions(projectID, "T", "D", CreateTicketOptions{LabelNames: []string{"Betaだけ"}})
	requireLabelNotFound(t, err)
}

func TestRefineTicketWithLabels(t *testing.T) {
	eng, repo, projectID, _ := engineLabelSetup(t)
	low := domain.TicketPriorityLow
	tk, err := eng.CreateTicketWithOptions(projectID, "T", "元の説明", CreateTicketOptions{Priority: &low, LabelNames: []string{"バグ", "UI"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Replace.
	got, err := eng.RefineTicketWithLabels(tk.ID, "新しい説明", NoPriorityChange(), SetLabelsByName([]string{"機能追加", "UI"}))
	if err != nil {
		t.Fatalf("refine: %v", err)
	}
	if namesOf(got.Labels) != "UI,機能追加" || got.Description != "新しい説明" {
		t.Errorf("unexpected refined ticket: %+v", got)
	}

	// No label change.
	got, err = eng.RefineTicket(tk.ID, "もう一度", NoPriorityChange())
	if err != nil || namesOf(got.Labels) != "UI,機能追加" {
		t.Errorf("NoLabelChange must keep labels: %+v, %v", got, err)
	}

	// With priority.
	got, err = eng.RefineTicketWithLabels(tk.ID, "説明", SetPriority(domain.TicketPriorityHigh), SetLabelsByName([]string{"UI"}))
	if err != nil || got.Priority != domain.TicketPriorityHigh || namesOf(got.Labels) != "UI" {
		t.Errorf("priority + labels: %+v, %v", got, err)
	}

	// Unregistered: nothing written, not even status/refined_at/updated_at.
	fresh, err := eng.CreateTicketWithOptions(projectID, "T2", "元の説明", CreateTicketOptions{Priority: &low, LabelNames: []string{"バグ"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = eng.RefineTicketWithLabels(fresh.ID, "新しい説明", SetPriority(domain.TicketPriorityHigh), SetLabelsByName([]string{"UI", "未登録"}))
	requireLabelNotFound(t, err)
	stored, _ := repo.GetTicket(fresh.ID)
	if stored.Description != "元の説明" || stored.Priority != low || namesOf(stored.Labels) != "バグ" ||
		stored.Status != domain.TicketTODO || stored.RefinedAt != nil || stored.UpdatedAt != fresh.UpdatedAt {
		t.Errorf("a failed refine must change nothing: %+v", stored)
	}

	// CLOSED is rejected before labels are resolved.
	if _, err := eng.CloseTicket(fresh.ID, ""); err != nil {
		t.Fatalf("CloseTicket: %v", err)
	}
	_, err = eng.RefineTicketWithLabels(fresh.ID, "説明", NoPriorityChange(), SetLabelsByName([]string{"UI", "未登録"}))
	if err == nil || !strings.Contains(err.Error(), "ticket "+fresh.ID+" is CLOSED; reopen it first with reopen-ticket") {
		t.Fatalf("expected the CLOSED error, got %v", err)
	}
	var apiErr *domain.APIError
	if errors.As(err, &apiErr) && apiErr.Code == domain.ErrCodeLabelNotFound {
		t.Error("the CLOSED check must run before label resolution")
	}
	stored, _ = repo.GetTicket(fresh.ID)
	if namesOf(stored.Labels) != "バグ" || stored.Status != domain.TicketClosed {
		t.Errorf("a CLOSED ticket must be unchanged: %+v", stored)
	}
}
