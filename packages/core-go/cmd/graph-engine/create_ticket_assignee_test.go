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
// removed. DFLT-00091 re-added update-ticket for title/description/priority
// only; the assignee is still not settable from the CLI.

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

// DFLT-00091 brought update-ticket back, but only for title/description/
// priority: it must still offer no way to set the assignee.
func TestPrintUsage_NoAssigneeArg(t *testing.T) {
	out := captureStdout(t, printUsage)
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "update-ticket <ticketId>") && strings.Contains(line, "--assignee") {
			t.Errorf("update-ticket's usage line must not offer --assignee: %q", line)
		}
	}
	if strings.Contains(out, "[assignee]") || strings.Contains(out, "[--assignee") {
		t.Errorf("usage must not mention an [assignee] argument:\n%s", out)
	}
	if !strings.Contains(out, "create-ticket <title> [description|-] [--project <id>]") {
		t.Errorf("usage should list the new create-ticket form:\n%s", out)
	}
}

// Subprocess mode for the tests below: errors from run() end the process via
// os.Exit (main's exit-code handling), so the real exit code is observed by
// re-running this test binary as the CLI itself.
const cliSubprocessEnv = "GRAPH_ENGINE_TEST_RUN_MAIN"

// runMainIfSubprocess turns the current test process into the CLI when it
// was started by runCLISubprocess. Call it first in every test that
// runCLISubprocess re-runs.
func runMainIfSubprocess() {
	if args := os.Getenv(cliSubprocessEnv); args != "" {
		// No-op unless the parent set a start barrier (see
		// concurrent_cli_test.go); only the concurrency regression test
		// needs several CLI processes to reach the DB at the same moment.
		waitForCLISubprocessStartBarrier()
		os.Args = append([]string{"graph-engine"}, strings.Split(args, "\x1f")...)
		main()
		os.Exit(0)
	}
}

// newSubprocessSQLiteRepo creates a SQLite DB in a temp dir for
// runCLISubprocess to point the CLI at, returning the repo (to set up and
// inspect data from the test itself), the dir and the DB path.
func newSubprocessSQLiteRepo(t *testing.T) (store.GraphRepository, string, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "graph.db")
	repo, err := store.NewSQLiteRepository(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	if err := repo.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return repo, dir, dbPath
}

// newCLISubprocessCmd builds the command that runs the CLI (this test
// binary, re-entering testName and thus runMainIfSubprocess) with args
// against the SQLite DB at dbPath. Split out of runCLISubprocess so the
// concurrency regression test can start several of these at once with the
// same environment instead of spelling it out a second time.
func newCLISubprocessCmd(testName, dir, dbPath string, args ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$")
	cmd.Env = append(os.Environ(),
		cliSubprocessEnv+"="+strings.Join(args, "\x1f"),
		"GRAPH_DB_BACKEND=sqlite",
		"GRAPH_DB_PATH="+dbPath,
		"GRAPH_ARTIFACTS_DIR="+filepath.Join(dir, "artifacts"),
		"HOME="+dir,
	)
	return cmd
}

// cliSubprocessExitCode turns the error from a finished CLI subprocess into
// an exit code, failing the test on anything that is not the process itself
// exiting non-zero.
func cliSubprocessExitCode(t *testing.T, err error, out []byte) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("running the CLI subprocess: %v\n%s", err, out)
	}
	return exitErr.ExitCode()
}

// runCLISubprocess runs the CLI (this test binary, re-entering testName and
// thus runMainIfSubprocess) with args against the SQLite DB at dbPath, and
// returns its combined output and exit code.
func runCLISubprocess(t *testing.T, testName, dir, dbPath string, args ...string) (string, int) {
	t.Helper()
	out, err := newCLISubprocessCmd(testName, dir, dbPath, args...).CombinedOutput()
	return string(out), cliSubprocessExitCode(t, err, out)
}

// DFLT-00024 removed update-ticket because it existed only to edit the
// assignee. DFLT-00091 re-added update-ticket for title/description/priority
// only, so the guarantee this test protects is unchanged: the CLI offers no
// way to change the assignee. "--assignee" is now an unknown flag -- a
// usage error that exits non-zero and leaves the ticket (assignee included)
// untouched.
func TestUpdateTicketRejectsAssignee(t *testing.T) {
	runMainIfSubprocess()

	repo, dir, dbPath := newSubprocessSQLiteRepo(t)
	proj, err := repo.CreateProject("P", "TEST")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	ticket, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	out, code := runCLISubprocess(t, "TestUpdateTicketRejectsAssignee", dir, dbPath,
		"update-ticket", ticket.ID, "--assignee", "山田")
	if code == 0 {
		t.Fatalf("update-ticket --assignee should exit non-zero, got 0\n%s", out)
	}
	if !strings.Contains(out, "usage: graph-engine update-ticket <ticketId>") || !strings.Contains(out, `"--assignee"`) {
		t.Errorf("update-ticket --assignee should fail with update-ticket's usage error naming the flag, got:\n%s", out)
	}
	if strings.Contains(out, "Commands:") {
		t.Errorf("update-ticket is a known command now; the general usage listing should not be printed, got:\n%s", out)
	}

	after, err := repo.GetTicket(ticket.ID)
	if err != nil || after == nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if !reflect.DeepEqual(ticket, *after) {
		t.Errorf("ticket changed:\nbefore=%+v\nafter=%+v", ticket, *after)
	}
}
