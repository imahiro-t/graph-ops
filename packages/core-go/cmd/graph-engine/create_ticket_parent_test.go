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

// DFLT-00142: create-ticket --parent and get-ticket's parent/children.

type cliFamilyJSON struct {
	ID             string             `json:"id"`
	ProjectID      string             `json:"project_id"`
	ParentTicketID *string            `json:"parent_ticket_id"`
	Parent         *domain.TicketRef  `json:"parent"`
	Children       []domain.TicketRef `json:"children"`
}

func runCreateTicketWithParent(t *testing.T, eng *engine.GraphEngine, repo store.GraphRepository, rc runtimeConfig, args ...string) (cliFamilyJSON, error) {
	t.Helper()
	var runErr error
	out := captureStdout(t, func() { runErr = cmdCreateTicket(eng, repo, rc, args) })
	if runErr != nil {
		if out != "" {
			t.Errorf("a failed create-ticket must print nothing on stdout, got %q", out)
		}
		return cliFamilyJSON{}, runErr
	}
	var tk cliFamilyJSON
	if err := json.Unmarshal([]byte(out), &tk); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	return tk, nil
}

func runGetTicket(t *testing.T, eng *engine.GraphEngine, id string) cliFamilyJSON {
	t.Helper()
	var runErr error
	out := captureStdout(t, func() { runErr = cmdGetTicket(eng, []string{id}) })
	if runErr != nil {
		t.Fatalf("get-ticket %s: %v", id, runErr)
	}
	var d cliFamilyJSON
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"children"`) || !strings.Contains(out, `"parent"`) || !strings.Contains(out, `"parent_ticket_id"`) {
		t.Fatalf("get-ticket output lacks parent/children keys:\n%s", out)
	}
	return d
}

func TestCmdCreateTicket_ParentAndGetTicketFamily(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	rc := sandboxRC(t)
	parent, err := runCreateTicketWithParent(t, eng, repo, rc, "P", "--project", projectID)
	if err != nil {
		t.Fatal(err)
	}
	c1, err := runCreateTicketWithParent(t, eng, repo, rc, "子1", "--parent", parent.ID, "--project", projectID)
	if err != nil {
		t.Fatalf("create-ticket --parent: %v", err)
	}
	c2, err := runCreateTicketWithParent(t, eng, repo, rc, "--parent", parent.ID, "子2", "説明")
	if err != nil {
		t.Fatalf("create-ticket --parent (no --project): %v", err)
	}
	for _, c := range []cliFamilyJSON{c1, c2} {
		if c.ParentTicketID == nil || *c.ParentTicketID != parent.ID || c.ProjectID != projectID {
			t.Fatalf("child = %+v, want parent %s in %s", c, parent.ID, projectID)
		}
	}

	pd := runGetTicket(t, eng, parent.ID)
	if pd.Parent != nil || pd.ParentTicketID != nil {
		t.Errorf("the parent itself has a parent: %+v", pd)
	}
	if len(pd.Children) != 2 || pd.Children[0].ID != c1.ID || pd.Children[0].Title != "子1" ||
		pd.Children[1].ID != c2.ID || pd.Children[1].Status != domain.TicketTODO {
		t.Fatalf("children = %+v, want [%s %s] with title/status", pd.Children, c1.ID, c2.ID)
	}
	cd := runGetTicket(t, eng, c1.ID)
	if cd.Parent == nil || cd.Parent.ID != parent.ID || cd.Parent.Title != "P" || cd.Parent.Status != domain.TicketTODO {
		t.Fatalf("parent = %+v", cd.Parent)
	}
	if cd.Children == nil || len(cd.Children) != 0 {
		t.Fatalf("children of a leaf = %#v, want []", cd.Children)
	}
}

// With --project omitted the parent's project wins, even when the current
// project (the resolution fallback) is another one.
func TestCmdCreateTicket_ParentDefaultsToParentsProject(t *testing.T) {
	repo, _ := newTestRepoWithProject(t)
	eng := engine.New(repo)
	other, err := repo.CreateProject("Other", "OTH")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := eng.CreateTicket(other.ID, "P", "")
	if err != nil {
		t.Fatal(err)
	}
	child, err := runCreateTicketWithParent(t, eng, repo, sandboxRC(t), "C", "--parent", parent.ID)
	if err != nil {
		t.Fatalf("create-ticket --parent: %v", err)
	}
	if child.ProjectID != other.ID {
		t.Fatalf("child project = %s, want the parent's %s", child.ProjectID, other.ID)
	}
}

func TestCmdCreateTicket_ParentErrors(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	rc := sandboxRC(t)
	other, err := repo.CreateProject("Other", "OTH")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := eng.CreateTicket(projectID, "P", "")
	if err != nil {
		t.Fatal(err)
	}
	before := countProjectTickets(t, repo, projectID) + countProjectTickets(t, repo, other.ID)

	cases := []struct {
		name string
		args []string
		code domain.ErrorCode
	}{
		{"other project", []string{"C", "--project", other.ID, "--parent", parent.ID}, domain.ErrCodeValidation},
		{"missing parent", []string{"C", "--parent", "TEST-99999"}, domain.ErrCodeTicketNotFound},
		{"missing parent with --project", []string{"C", "--parent", "TEST-99999", "--project", projectID}, domain.ErrCodeTicketNotFound},
	}
	for _, tc := range cases {
		_, err := runCreateTicketWithParent(t, eng, repo, rc, tc.args...)
		var apiErr *domain.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != tc.code {
			t.Errorf("%s: err = %v, want %s", tc.name, err, tc.code)
		}
		if err != nil && !strings.Contains(err.Error(), string(tc.code)) {
			t.Errorf("%s: the message %q should name %s for a CLI user", tc.name, err.Error(), tc.code)
		}
	}
	for _, args := range [][]string{{"C", "--parent"}, {"C", "--parent", ""}, {"C", "--parent", parent.ID, "--parent", parent.ID}} {
		if _, err := runCreateTicketWithParent(t, eng, repo, rc, args...); err == nil || !strings.Contains(err.Error(), "--parent") {
			t.Errorf("args %q: err = %v, want a --parent usage error", args, err)
		}
	}
	if after := countProjectTickets(t, repo, projectID) + countProjectTickets(t, repo, other.ID); after != before {
		t.Fatalf("no ticket may be created: %d -> %d", before, after)
	}
}

func TestHelp_MentionsParentFlagAndFamilyOutput(t *testing.T) {
	helpText := captureStdout(t, printUsage)
	for _, want := range []string{"[--parent <ticketId>]", `"children"`, "PARENT_TICKET_UNSUPPORTED"} {
		if !strings.Contains(helpText, want) {
			t.Errorf("help text should mention %s", want)
		}
	}
	if !strings.Contains(createTicketUsageLine, "--parent") {
		t.Errorf("create-ticket usage line lacks --parent: %s", createTicketUsageLine)
	}
}
