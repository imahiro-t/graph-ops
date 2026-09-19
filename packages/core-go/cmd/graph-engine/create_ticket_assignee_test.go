package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00024: create-ticket lost its third positional (the free-text
// assignee) and update-ticket, which existed only to edit that assignee, was
// removed.

const createTicketUsage = "usage: graph-engine create-ticket <title> [description|-] [--project <id>]"

func TestCmdCreateTicket_TitleAndDescriptionOnly(t *testing.T) {
	cases := []struct {
		name      string
		args      func(projectID string) []string
		wantTitle string
		wantDesc  string
	}{
		{"title only", func(string) []string { return []string{"t1"} }, "t1", ""},
		{"title and description", func(string) []string { return []string{"t1", "d1"} }, "t1", "d1"},
		{"--project last", func(p string) []string { return []string{"t1", "d1", "--project", p} }, "t1", "d1"},
		{"--project first", func(p string) []string { return []string{"--project", p, "t1", "d1"} }, "t1", "d1"},
		{"--project between", func(p string) []string { return []string{"t1", "--project", p, "d1"} }, "t1", "d1"},
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
			if m["title"] != tc.wantTitle || m["description"] != tc.wantDesc || m["project_id"] != projectID {
				t.Errorf("unexpected ticket: %v", m)
			}
			if _, ok := m["assignee"]; ok {
				t.Errorf("stdout JSON must not contain an assignee key: %v", m)
			}
		})
	}
}

func TestCmdCreateTicket_ExtraPositionalIsUsageError(t *testing.T) {
	cases := map[string]func(projectID string) []string{
		"third positional":           func(string) []string { return []string{"t1", "d1", "山田"} },
		"empty third positional":     func(string) []string { return []string{"t1", "d1", ""} },
		"third positional + project": func(p string) []string { return []string{"t1", "d1", "山田", "--project", p} },
		"four positionals":           func(string) []string { return []string{"t1", "d1", "山田", "余分"} },
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			repo, projectID := newTestRepoWithProject(t)
			eng := engine.New(repo)

			err := cmdCreateTicket(eng, repo, runtimeConfig{}, args(projectID))
			if err == nil || !strings.Contains(err.Error(), createTicketUsage) {
				t.Fatalf("expected the create-ticket usage error, got %v", err)
			}
			if tickets, _ := repo.ListTicketsByProject(projectID); len(tickets) != 0 {
				t.Errorf("no ticket should be created, got %d", len(tickets))
			}
		})
	}
}

func TestCmdCreateTicket_NoPositionalIsUsageError(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	err := cmdCreateTicket(engine.New(repo), repo, runtimeConfig{}, []string{"--project", projectID})
	if err == nil || !strings.Contains(err.Error(), createTicketUsage) {
		t.Fatalf("expected the create-ticket usage error, got %v", err)
	}
}

func TestPrintUsage_NoUpdateTicketOrAssigneeArg(t *testing.T) {
	out := captureStdout(t, printUsage)
	if strings.Contains(out, "update-ticket") {
		t.Errorf("usage must not list update-ticket:\n%s", out)
	}
	if strings.Contains(out, "[assignee]") {
		t.Errorf("usage must not mention an [assignee] argument:\n%s", out)
	}
	if !strings.Contains(out, "create-ticket <title> [description|-] [--project <id>]") {
		t.Errorf("usage should list the new create-ticket form:\n%s", out)
	}
}

// Subprocess mode for TestUpdateTicketCommandIsUnknown: the unknown-command
// branch of run() calls os.Exit(1), so it is exercised by re-running this test
// binary as the CLI itself.
const cliSubprocessEnv = "GRAPH_ENGINE_TEST_RUN_MAIN"

func TestUpdateTicketCommandIsUnknown(t *testing.T) {
	if args := os.Getenv(cliSubprocessEnv); args != "" {
		os.Args = append([]string{"graph-engine"}, strings.Split(args, "\x1f")...)
		main()
		os.Exit(0)
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "graph.db")
	repo, err := store.NewSQLiteRepository(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	if err := repo.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	proj, err := repo.CreateProject("P", "TEST")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	ticket, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestUpdateTicketCommandIsUnknown$")
	cmd.Env = append(os.Environ(),
		cliSubprocessEnv+"="+strings.Join([]string{"update-ticket", ticket.ID, "--assignee", "山田"}, "\x1f"),
		"GRAPH_DB_PATH="+dbPath,
		"GRAPH_ARTIFACTS_DIR="+filepath.Join(dir, "artifacts"),
		"HOME="+dir,
	)
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
		t.Fatalf("update-ticket should exit non-zero, got err=%v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Commands:") {
		t.Errorf("update-ticket should print the usage listing, got:\n%s", out)
	}

	after, err := repo.GetTicket(ticket.ID)
	if err != nil || after == nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if !reflect.DeepEqual(ticket, *after) {
		t.Errorf("ticket changed:\nbefore=%+v\nafter=%+v", ticket, *after)
	}
}
