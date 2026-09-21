package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00084: create-ticket --label <name> (repeatable), get-ticket and
// list-tickets output "labels", and the help text.

func cliLabelSetup(t *testing.T) (store.GraphRepository, string) {
	t.Helper()
	repo, projectID := newTestRepoWithProject(t)
	for _, l := range []struct{ name, color string }{{"バグ", "red"}, {"機能追加", "blue"}, {"UI", "purple"}} {
		if _, err := repo.CreateLabel(projectID, l.name, l.color); err != nil {
			t.Fatalf("CreateLabel: %v", err)
		}
	}
	return repo, projectID
}

type cliTicketJSON struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Priority    string         `json:"priority"`
	Labels      []domain.Label `json:"labels"`
}

func decodeCLITicket(t *testing.T, out string) cliTicketJSON {
	t.Helper()
	var tk cliTicketJSON
	if err := json.Unmarshal([]byte(out), &tk); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	return tk
}

func cliLabelNames(labels []domain.Label) string {
	names := make([]string, 0, len(labels))
	for _, l := range labels {
		names = append(names, l.Name)
	}
	return strings.Join(names, ",")
}

func countProjectTickets(t *testing.T, repo store.GraphRepository, projectID string) int {
	t.Helper()
	list, err := repo.ListTicketsByProject(projectID)
	if err != nil {
		t.Fatalf("ListTicketsByProject: %v", err)
	}
	return len(list)
}

func TestCmdCreateTicket_MultipleLabels(t *testing.T) {
	repo, projectID := cliLabelSetup(t)
	eng := engine.New(repo)
	var runErr error
	out := captureStdout(t, func() {
		runErr = cmdCreateTicket(eng, repo, sandboxRC(t), []string{"--label", "バグ", "ログイン不具合", "説明", "--label", "UI", "--project", projectID})
	})
	if runErr != nil {
		t.Fatalf("cmdCreateTicket: %v", runErr)
	}
	tk := decodeCLITicket(t, out)
	if tk.Title != "ログイン不具合" || cliLabelNames(tk.Labels) != "UI,バグ" {
		t.Errorf("unexpected ticket: %+v", tk)
	}
}

func TestCmdCreateTicket_LabelFlagPositions(t *testing.T) {
	cases := [][]string{
		{"--label", "バグ", "T", "D"},
		{"T", "--label", "バグ", "D"},
		{"T", "D", "--label", "バグ"},
		{"--project", "PROJECT", "T", "D", "--label", "バグ", "--priority", "HIGH"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			repo, projectID := cliLabelSetup(t)
			eng := engine.New(repo)
			resolved := make([]string, len(args))
			for i, a := range args {
				if a == "PROJECT" {
					a = projectID
				}
				resolved[i] = a
			}
			if resolved[0] != "--project" {
				resolved = append(resolved, "--project", projectID)
			}
			var runErr error
			out := captureStdout(t, func() {
				runErr = cmdCreateTicket(eng, repo, sandboxRC(t), resolved)
			})
			if runErr != nil {
				t.Fatalf("cmdCreateTicket: %v", runErr)
			}
			tk := decodeCLITicket(t, out)
			if tk.Title != "T" || tk.Description != "D" || cliLabelNames(tk.Labels) != "バグ" {
				t.Errorf("unexpected ticket: %+v", tk)
			}
		})
	}
}

func TestCmdCreateTicket_LabelNamesTrimmedCaseInsensitiveDeduplicated(t *testing.T) {
	repo, projectID := cliLabelSetup(t)
	if _, err := repo.CreateLabel(projectID, "Bug", "gray"); err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	eng := engine.New(repo)
	var runErr error
	out := captureStdout(t, func() {
		runErr = cmdCreateTicket(eng, repo, sandboxRC(t), []string{"T", "D", "--label", "bug", "--label", " BUG ", "--project", projectID})
	})
	if runErr != nil {
		t.Fatalf("cmdCreateTicket: %v", runErr)
	}
	if got := cliLabelNames(decodeCLITicket(t, out).Labels); got != "Bug" {
		t.Errorf("labels = %q, want Bug", got)
	}
}

func TestCmdCreateTicket_NoLabelFlagGivesEmptyLabels(t *testing.T) {
	repo, projectID := cliLabelSetup(t)
	eng := engine.New(repo)
	out := captureStdout(t, func() {
		if err := cmdCreateTicket(eng, repo, sandboxRC(t), []string{"T", "D", "--project", projectID}); err != nil {
			t.Fatalf("cmdCreateTicket: %v", err)
		}
	})
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	labels, ok := m["labels"].([]any)
	if !ok || len(labels) != 0 {
		t.Errorf("labels must be [], got %#v", m["labels"])
	}
}

func TestCmdCreateTicket_UnregisteredLabelIsErrorAndCreatesNothing(t *testing.T) {
	repo, projectID := cliLabelSetup(t)
	beta, err := repo.CreateProject("Beta", "BETA")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := repo.CreateLabel(beta.ID, "Betaだけ", "red"); err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	eng := engine.New(repo)
	before := countProjectTickets(t, repo, projectID)

	for _, args := range [][]string{
		{"T", "D", "--label", "バグ", "--label", "未登録", "--project", projectID},
		{"--project", projectID, "T", "D", "--label", "Betaだけ"},
	} {
		var runErr error
		out := captureStdout(t, func() {
			runErr = cmdCreateTicket(eng, repo, sandboxRC(t), args)
		})
		var apiErr *domain.APIError
		if runErr == nil || !errors.As(runErr, &apiErr) || apiErr.Code != domain.ErrCodeLabelNotFound {
			t.Fatalf("args %v: expected LABEL_NOT_FOUND, got %v", args, runErr)
		}
		if out != "" {
			t.Errorf("args %v: nothing must be printed on stdout, got %q", args, out)
		}
	}
	msg := func() string {
		_, err := eng.CreateTicketWithOptions(projectID, "T", "D", engine.CreateTicketOptions{LabelNames: []string{"未登録"}})
		return err.Error()
	}()
	for _, want := range []string{"LABEL_NOT_FOUND", "未登録", "バグ", "機能追加", "UI"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message %q should mention %q", msg, want)
		}
	}
	if after := countProjectTickets(t, repo, projectID); after != before {
		t.Errorf("no ticket may be created: %d -> %d", before, after)
	}
}

func TestCmdLabelFlagWithoutValueIsUsageError(t *testing.T) {
	repo, projectID := cliLabelSetup(t)
	eng := engine.New(repo)
	tk, err := eng.CreateTicket(projectID, "A-1", "元の説明")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	before := countProjectTickets(t, repo, projectID)

	err = cmdCreateTicket(eng, repo, sandboxRC(t), []string{"T", "D", "--label"})
	if err == nil || !strings.Contains(err.Error(), "usage: graph-engine create-ticket") || !strings.Contains(err.Error(), "--label requires a value") {
		t.Errorf("create-ticket: expected a usage error, got %v", err)
	}
	err = cmdRefineTicket(eng, []string{tk.ID, "D", "--label"})
	if err == nil || !strings.Contains(err.Error(), "usage: graph-engine refine-ticket") || !strings.Contains(err.Error(), "--label requires a value") {
		t.Errorf("refine-ticket: expected a usage error, got %v", err)
	}
	if after := countProjectTickets(t, repo, projectID); after != before {
		t.Errorf("no ticket may be created: %d -> %d", before, after)
	}
	got, _ := repo.GetTicket(tk.ID)
	if got.Description != "元の説明" || got.Status != domain.TicketTODO || got.UpdatedAt != tk.UpdatedAt {
		t.Errorf("the ticket must be unchanged: %+v", got)
	}
}

func TestCmdGetTicketAndListTickets_IncludeLabels(t *testing.T) {
	repo, projectID := cliLabelSetup(t)
	eng := engine.New(repo)
	a1, err := eng.CreateTicketWithOptions(projectID, "A-1", "", engine.CreateTicketOptions{LabelNames: []string{"バグ", "UI"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	a2, err := eng.CreateTicket(projectID, "A-2", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	out := captureStdout(t, func() {
		if err := cmdGetTicket(repo, []string{a1.ID}); err != nil {
			t.Fatalf("cmdGetTicket: %v", err)
		}
	})
	var detail struct {
		Labels []map[string]any `json:"labels"`
	}
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if len(detail.Labels) != 2 {
		t.Fatalf("labels must have 2 elements: %s", out)
	}
	for _, l := range detail.Labels {
		for _, key := range []string{"id", "project_id", "name", "color", "created_at", "updated_at"} {
			if _, ok := l[key]; !ok {
				t.Errorf("label %v lacks %q", l, key)
			}
		}
	}

	out = captureStdout(t, func() {
		if err := cmdGetTicket(repo, []string{a2.ID}); err != nil {
			t.Fatalf("cmdGetTicket: %v", err)
		}
	})
	if !strings.Contains(out, `"labels": []`) {
		t.Errorf("a ticket without labels must print \"labels\": []\n%s", out)
	}

	// A rename shows up in get-ticket.
	labels, _ := repo.ListLabelsByProject(projectID)
	for _, l := range labels {
		if l.Name == "バグ" {
			newName := "不具合"
			if _, err := repo.UpdateLabel(l.ID, store.LabelPatch{Name: &newName}); err != nil {
				t.Fatalf("UpdateLabel: %v", err)
			}
		}
	}
	out = captureStdout(t, func() {
		if err := cmdGetTicket(repo, []string{a1.ID}); err != nil {
			t.Fatalf("cmdGetTicket: %v", err)
		}
	})
	if !strings.Contains(out, "不具合") || strings.Contains(out, "バグ") {
		t.Errorf("get-ticket must show the renamed label:\n%s", out)
	}

	out = captureStdout(t, func() {
		if err := cmdListTickets(repo); err != nil {
			t.Fatalf("cmdListTickets: %v", err)
		}
	})
	var list []cliTicketJSON
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	found := false
	for _, tk := range list {
		if tk.ID == a1.ID {
			found = cliLabelNames(tk.Labels) == "UI,不具合"
		}
	}
	if !found {
		t.Errorf("list-tickets must include A-1's labels:\n%s", out)
	}
}

func TestPrintUsage_DocumentsLabelFlagAndNoLabelCommands(t *testing.T) {
	out := captureStdout(t, printUsage)
	for _, want := range []string{
		"create-ticket <title> [description|-] [--project <id>] [--priority <HIGH|MEDIUM|LOW>] [--label <name>]...",
		"refine-ticket <ticketId> [description|-] [--priority <HIGH|MEDIUM|LOW>] [--label <name>]...",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("usage should contain %q", want)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "create-label") || strings.HasPrefix(trimmed, "list-labels") ||
			strings.HasPrefix(trimmed, "rename-label") || strings.HasPrefix(trimmed, "delete-label") ||
			strings.HasPrefix(trimmed, "update-label") {
			t.Errorf("there must be no label master command, found %q", trimmed)
		}
	}
}
