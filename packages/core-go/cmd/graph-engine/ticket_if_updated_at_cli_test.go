package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00330: update-ticket and refine-ticket take --if-updated-at, which
// makes the write conditional on the ticket's updated_at as get-ticket
// printed it.

func mustTicket(t *testing.T, repo store.GraphRepository, id string) domain.Ticket {
	t.Helper()
	tk, err := repo.GetTicket(id)
	if err != nil || tk == nil {
		t.Fatalf("GetTicket(%s) = %v, %v", id, tk, err)
	}
	return *tk
}

func expectTicketChanged(t *testing.T, err error) {
	t.Helper()
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeTicketChanged {
		t.Fatalf("err = %v, want TICKET_CHANGED", err)
	}
	if !strings.HasPrefix(err.Error(), "TICKET_CHANGED: ") {
		t.Fatalf("the CLI error %q does not name TICKET_CHANGED", err.Error())
	}
}

func newCLITicket(t *testing.T, repo store.GraphRepository, projectID string) domain.Ticket {
	t.Helper()
	tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: "旧タイトル", Description: "旧説明", Status: domain.TicketTODO, Priority: domain.TicketPriorityMedium})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	return mustTicket(t, repo, tk.ID)
}

func TestCmdUpdateTicket_IfUpdatedAt(t *testing.T) {
	forEachBackend(t, func(t *testing.T, repo store.GraphRepository, projectID string) {
		t.Run("a matching value updates", func(t *testing.T) {
			tk := newCLITicket(t, repo, projectID)
			captureStdout(t, func() {
				if err := cmdUpdateTicket(repo, []string{tk.ID, "--title", "新タイトル", "--if-updated-at", tk.UpdatedAt}); err != nil {
					t.Fatalf("update-ticket: %v", err)
				}
			})
			if got := mustTicket(t, repo, tk.ID); got.Title != "新タイトル" {
				t.Fatalf("title = %q", got.Title)
			}
		})

		t.Run("a stale value fails with TICKET_CHANGED and changes nothing", func(t *testing.T) {
			tk := newCLITicket(t, repo, projectID)
			other := "他のメンバーのタイトル"
			if _, err := repo.UpdateTicket(tk.ID, store.TicketPatch{Title: &other}); err != nil {
				t.Fatal(err)
			}
			before := mustTicket(t, repo, tk.ID)
			err := cmdUpdateTicket(repo, []string{tk.ID, "--title", "新タイトル", "--if-updated-at", tk.UpdatedAt})
			expectTicketChanged(t, err)
			after := mustTicket(t, repo, tk.ID)
			if after.Title != other || after.Description != before.Description || after.UpdatedAt != before.UpdatedAt || len(after.Labels) != len(before.Labels) {
				t.Fatalf("a refused update-ticket changed the ticket: %+v", after)
			}
		})

		t.Run("omitted overwrites unconditionally", func(t *testing.T) {
			tk := newCLITicket(t, repo, projectID)
			other := "他のメンバーのタイトル"
			if _, err := repo.UpdateTicket(tk.ID, store.TicketPatch{Title: &other}); err != nil {
				t.Fatal(err)
			}
			captureStdout(t, func() {
				if err := cmdUpdateTicket(repo, []string{tk.ID, "--title", "上書き"}); err != nil {
					t.Fatalf("update-ticket: %v", err)
				}
			})
			if got := mustTicket(t, repo, tk.ID); got.Title != "上書き" {
				t.Fatalf("title = %q", got.Title)
			}
		})
	})
}

func TestCmdUpdateTicket_IfUpdatedAtUsage(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	tk := newCLITicket(t, repo, projectID)
	cases := map[string][]string{
		"alone":       {tk.ID, "--if-updated-at", tk.UpdatedAt},
		"no value":    {tk.ID, "--title", "新タイトル", "--if-updated-at"},
		"flag value":  {tk.ID, "--if-updated-at", "--title", "新タイトル"},
		"given twice": {tk.ID, "--title", "新タイトル", "--if-updated-at", tk.UpdatedAt, "--if-updated-at", tk.UpdatedAt},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			err := cmdUpdateTicket(repo, args)
			if err == nil || !strings.HasPrefix(err.Error(), "usage: graph-engine update-ticket") {
				t.Fatalf("err = %v, want a usage error", err)
			}
			if name == "alone" && !strings.Contains(err.Error(), "specify at least one of") {
				t.Fatalf("err = %v, want the no-field error", err)
			}
			if got := mustTicket(t, repo, tk.ID); got.Title != tk.Title || got.UpdatedAt != tk.UpdatedAt {
				t.Fatalf("a usage error changed the ticket: %+v", got)
			}
		})
	}
}

func TestCmdRefineTicket_IfUpdatedAt(t *testing.T) {
	forEachBackend(t, func(t *testing.T, repo store.GraphRepository, projectID string) {
		eng := engine.New(repo)
		mustCreateLabel(t, repo, projectID, "改善")

		t.Run("a matching value refines", func(t *testing.T) {
			tk := newCLITicket(t, repo, projectID)
			captureStdout(t, func() {
				if err := cmdRefineTicket(eng, []string{tk.ID, "新しい説明", "--if-updated-at", tk.UpdatedAt}); err != nil {
					t.Fatalf("refine-ticket: %v", err)
				}
			})
			got := mustTicket(t, repo, tk.ID)
			if got.Description != "新しい説明" || got.Status != domain.TicketRefined || got.RefinedAt == nil {
				t.Fatalf("not refined: %+v", got)
			}
		})

		t.Run("a stale value fails early and writes nothing", func(t *testing.T) {
			tk := newCLITicket(t, repo, projectID)
			other := "他のメンバーの説明"
			if _, err := repo.UpdateTicket(tk.ID, store.TicketPatch{Description: &other}); err != nil {
				t.Fatal(err)
			}
			before := mustTicket(t, repo, tk.ID)
			err := cmdRefineTicket(eng, []string{tk.ID, "新しい説明", "--priority", "HIGH", "--label", "改善", "--if-updated-at", tk.UpdatedAt})
			expectTicketChanged(t, err)
			after := mustTicket(t, repo, tk.ID)
			if after.Description != other || after.Status != domain.TicketTODO || after.RefinedAt != nil ||
				after.Priority != before.Priority || len(after.Labels) != 0 || after.UpdatedAt != before.UpdatedAt {
				t.Fatalf("a refused refine-ticket changed the ticket: %+v", after)
			}
		})

		t.Run("omitted overwrites unconditionally", func(t *testing.T) {
			tk := newCLITicket(t, repo, projectID)
			other := "他のメンバーの説明"
			if _, err := repo.UpdateTicket(tk.ID, store.TicketPatch{Description: &other}); err != nil {
				t.Fatal(err)
			}
			captureStdout(t, func() {
				if err := cmdRefineTicket(eng, []string{tk.ID, "新しい説明"}); err != nil {
					t.Fatalf("refine-ticket: %v", err)
				}
			})
			if got := mustTicket(t, repo, tk.ID); got.Description != "新しい説明" || got.Status != domain.TicketRefined {
				t.Fatalf("not refined: %+v", got)
			}
		})

		t.Run("CLOSED is reported before the comparison", func(t *testing.T) {
			tk := newCLITicket(t, repo, projectID)
			closed := domain.TicketClosed
			if _, err := repo.UpdateTicket(tk.ID, store.TicketPatch{Status: &closed}); err != nil {
				t.Fatal(err)
			}
			err := cmdRefineTicket(eng, []string{tk.ID, "新しい説明", "--if-updated-at", "2000-01-01T00:00:00Z"})
			if err == nil || !strings.Contains(err.Error(), "CLOSED") {
				t.Fatalf("err = %v, want the CLOSED error", err)
			}
		})
	})
}

func TestCmdRefineTicket_IfUpdatedAtUsage(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	mustCreateLabel(t, repo, projectID, "改善")
	eng := engine.New(repo)
	tk := newCLITicket(t, repo, projectID)
	cases := map[string][]string{
		"no value at the end":        {tk.ID, "新しい説明", "--if-updated-at"},
		"no value before --priority": {tk.ID, "--if-updated-at", "--priority", "HIGH", "新しい説明"},
		"no value before --label":    {tk.ID, "新しい説明", "--if-updated-at", "--label", "改善"},
		"given twice":                {tk.ID, "新しい説明", "--if-updated-at", tk.UpdatedAt, "--if-updated-at", tk.UpdatedAt},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			err := cmdRefineTicket(eng, args)
			if err == nil || !strings.HasPrefix(err.Error(), "usage: graph-engine refine-ticket") || !strings.Contains(err.Error(), "--if-updated-at") {
				t.Fatalf("err = %v, want an --if-updated-at usage error", err)
			}
			got := mustTicket(t, repo, tk.ID)
			if got.Description != tk.Description || got.Priority != tk.Priority || len(got.Labels) != 0 ||
				got.UpdatedAt != tk.UpdatedAt || got.Status != domain.TicketTODO || got.RefinedAt != nil {
				t.Fatalf("a usage error changed the ticket: %+v", got)
			}
		})
	}
}

// TestHelp_IfUpdatedAt: graph-engine help documents --if-updated-at for both
// commands, what to pass and that omitting it overwrites. (A command given
// --help prints this same text.)
func TestHelp_IfUpdatedAt(t *testing.T) {
	out := captureStdout(t, printUsage)
	for _, cmd := range []string{"refine-ticket <ticketId>", "update-ticket <ticketId>"} {
		i := strings.Index(out, "  "+cmd)
		if i < 0 {
			t.Fatalf("help has no %s entry", cmd)
		}
		entry := out[i:]
		// The entry runs until the next command line (two spaces and a
		// lowercase letter at the start of a line).
		end := len(entry)
		for k := 1; k < len(entry)-3; k++ {
			if entry[k] == '\n' && entry[k+1] == ' ' && entry[k+2] == ' ' && entry[k+3] >= 'a' && entry[k+3] <= 'z' {
				end = k
				break
			}
		}
		entry = entry[:end]
		for _, want := range []string{"--if-updated-at", `"updated_at" get-ticket`, "overwrites unconditionally", "TICKET_CHANGED"} {
			if !strings.Contains(strings.Join(strings.Fields(entry), " "), want) {
				t.Errorf("%s help lacks %q:\n%s", cmd, want, entry)
			}
		}
	}
}
