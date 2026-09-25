package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
	"github.com/graph-ops/core-go/internal/store/httpdatasourcetest"
)

// DFLT-00091: delete-ticket and update-ticket, exercised against every
// storage backend (SQLite, MySQL, HTTP data source). The MySQL subtests need
// GRAPH_TEST_MYSQL_HOST and friends -- ./dev/mysql/test.sh sets them and runs
// this package -- and skip without them.

var backendProjectSeq atomic.Int64

// forEachBackend runs fn once per storage backend, each in its own subtest
// with a fresh project to work in.
func forEachBackend(t *testing.T, fn func(t *testing.T, repo store.GraphRepository, projectID string)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) {
		repo, projectID := newTestRepoWithProject(t)
		fn(t, repo, projectID)
	})
	t.Run("mysql", func(t *testing.T) {
		repo, projectID := newMySQLTestRepoWithProject(t)
		fn(t, repo, projectID)
	})
	t.Run("http", func(t *testing.T) {
		repo, projectID := newHTTPTestRepoWithProject(t)
		fn(t, repo, projectID)
	})
}

// newMySQLTestRepoWithProject opens the MySQL database named by the same
// GRAPH_TEST_MYSQL_* variables internal/store's MySQL tests use (that
// package's mysqlTestConfig lives in a _test.go file and is not importable).
// The database is shared with other test packages run by ./dev/mysql/test.sh
// (sequentially, -p 1), so this test only ever touches the project it
// creates, and deletes it -- with its tickets -- when the test ends.
func newMySQLTestRepoWithProject(t *testing.T) (store.GraphRepository, string) {
	t.Helper()
	host := os.Getenv("GRAPH_TEST_MYSQL_HOST")
	if host == "" {
		t.Skip("GRAPH_TEST_MYSQL_HOST not set; skipping the MySQL backend (run ./dev/mysql/test.sh)")
	}
	port := 3306
	if p := os.Getenv("GRAPH_TEST_MYSQL_PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			t.Fatalf("invalid GRAPH_TEST_MYSQL_PORT %q: %v", p, err)
		}
		port = n
	}
	repo, err := store.Open(store.Config{
		Backend:        "mysql",
		MySQLHost:      host,
		MySQLPort:      port,
		MySQLDatabase:  os.Getenv("GRAPH_TEST_MYSQL_DATABASE"),
		MySQLUser:      os.Getenv("GRAPH_TEST_MYSQL_USER"),
		MySQLPassword:  os.Getenv("GRAPH_TEST_MYSQL_PASSWORD"),
		MySQLTLSMode:   os.Getenv("GRAPH_TEST_MYSQL_TLS"),
		MySQLTLSCAFile: os.Getenv("GRAPH_TEST_MYSQL_TLS_CA"),
	})
	if err != nil {
		t.Fatalf("store.Open(mysql): %v", err)
	}
	// Prefix left empty: the store derives one from the name and
	// de-duplicates it against whatever projects the shared DB already has.
	name := fmt.Sprintf("CLI Test %d %d", time.Now().UnixNano(), backendProjectSeq.Add(1))
	proj, err := repo.CreateProject(name, "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	t.Cleanup(func() {
		if err := repo.DeleteProject(proj.ID); err != nil {
			t.Errorf("cleanup DeleteProject(%s): %v", proj.ID, err)
		}
	})
	return repo, proj.ID
}

// newHTTPTestRepoWithProject opens an HTTP data source repository backed by
// the reference plugin (httpdatasourcetest), served over loopback.
func newHTTPTestRepoWithProject(t *testing.T) (store.GraphRepository, string) {
	t.Helper()
	const token = "cli-test-token"
	srv := httptest.NewServer(httpdatasourcetest.New(token))
	t.Cleanup(srv.Close)
	repo, err := store.Open(store.Config{Backend: "http", HTTPURL: srv.URL, HTTPToken: token})
	if err != nil {
		t.Fatalf("store.Open(http): %v", err)
	}
	proj, err := repo.CreateProject("CLI Test", "TEST")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return repo, proj.ID
}

func mustCreateLabel(t *testing.T, repo store.GraphRepository, projectID, name string) domain.Label {
	t.Helper()
	label, err := repo.CreateLabel(projectID, name, string(domain.LabelColorBlue))
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	return label
}

// mustCreateInProgressTicket creates a ticket carrying label, then moves it
// to IN PROGRESS with refined_at set, the way a ticket mid-execution looks.
// It returns the ticket as re-read from the store.
func mustCreateInProgressTicket(t *testing.T, repo store.GraphRepository, projectID string, label domain.Label) domain.Ticket {
	t.Helper()
	created, err := repo.CreateTicket(projectID, domain.Ticket{
		Title:       "元のタイトル",
		Description: "元の説明",
		Status:      domain.TicketTODO,
		Priority:    domain.TicketPriorityMedium,
		Labels:      []domain.Label{{ID: label.ID}},
	})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	status := domain.TicketInProgress
	refinedAt := "2026-01-02T03:04:05Z"
	if _, err := repo.UpdateTicket(created.ID, store.TicketPatch{Status: &status, RefinedAt: &refinedAt}); err != nil {
		t.Fatalf("UpdateTicket(IN PROGRESS): %v", err)
	}
	return mustGetTicket(t, repo, created.ID)
}

func mustGetTicket(t *testing.T, repo store.GraphRepository, id string) domain.Ticket {
	t.Helper()
	ticket, err := repo.GetTicket(id)
	if err != nil {
		t.Fatalf("GetTicket(%s): %v", id, err)
	}
	if ticket == nil {
		t.Fatalf("GetTicket(%s): ticket not found", id)
	}
	return *ticket
}

func assertTicketNotFoundCode(t *testing.T, err error) {
	t.Helper()
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeTicketNotFound {
		t.Fatalf("err = %v (%T), want an APIError with code %s", err, err, domain.ErrCodeTicketNotFound)
	}
}

func labelIDs(labels []domain.Label) []string {
	ids := []string{}
	for _, l := range labels {
		ids = append(ids, l.ID)
	}
	return ids
}

// --- delete-ticket ----------------------------------------------------------

// deleteFixture is the Background of the delete-ticket feature: T1 (IN
// PROGRESS, labeled) with nodes N1 (IN PROGRESS) -> N2, an artifact A1 on N1,
// and an unrelated ticket T2 in the same project.
type deleteFixture struct {
	label  domain.Label
	t1, t2 domain.Ticket
	n1, n2 domain.GraphNode
	a1     domain.Artifact
}

func newDeleteFixture(t *testing.T, repo store.GraphRepository, projectID string) deleteFixture {
	t.Helper()
	var f deleteFixture
	f.label = mustCreateLabel(t, repo, projectID, "機能追加")
	f.t1 = mustCreateInProgressTicket(t, repo, projectID, f.label)
	var err error
	f.n1, err = repo.CreateNode(domain.GraphNode{TicketID: f.t1.ID, Name: "N1", Type: domain.NodeType("implementation"), Status: domain.NodeInProgress})
	if err != nil {
		t.Fatalf("CreateNode(N1): %v", err)
	}
	f.n2, err = repo.CreateNode(domain.GraphNode{TicketID: f.t1.ID, Name: "N2", Type: domain.NodeType("review"), Status: domain.NodeTODO})
	if err != nil {
		t.Fatalf("CreateNode(N2): %v", err)
	}
	if _, err := repo.CreateEdge(domain.GraphEdge{TicketID: f.t1.ID, FromNodeID: f.n1.ID, ToNodeID: f.n2.ID, Condition: domain.EdgeSuccess}); err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	content := "A1 の内容"
	f.a1, err = repo.CreateArtifact(domain.Artifact{TicketID: f.t1.ID, NodeID: f.n1.ID, Name: "A1", Type: domain.ArtifactText, Content: &content})
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	f.t2, err = repo.CreateTicket(projectID, domain.Ticket{Title: "T2", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket(T2): %v", err)
	}
	return f
}

func labelTicketCount(t *testing.T, repo store.GraphRepository, projectID, labelID string) (count int, found bool) {
	t.Helper()
	usages, err := repo.ListLabelsByProject(projectID)
	if err != nil {
		t.Fatalf("ListLabelsByProject: %v", err)
	}
	for _, u := range usages {
		if u.ID == labelID {
			return u.TicketCount, true
		}
	}
	return 0, false
}

// assertFixtureIntact checks that nothing of T1 (nor T2) was deleted.
func assertFixtureIntact(t *testing.T, repo store.GraphRepository, projectID string, f deleteFixture) {
	t.Helper()
	detail, err := repo.GetTicketDetail(f.t1.ID)
	if err != nil || detail == nil {
		t.Fatalf("GetTicketDetail(T1) = %v, %v; T1 should still exist", detail, err)
	}
	if len(detail.Nodes) != 2 || len(detail.Edges) != 1 {
		t.Errorf("T1 should keep its 2 nodes and 1 edge, got %d nodes and %d edges", len(detail.Nodes), len(detail.Edges))
	}
	if a, err := repo.GetArtifact(f.a1.ID); err != nil || a == nil {
		t.Errorf("artifact A1 should still exist: %v, %v", a, err)
	}
	if got := labelIDs(mustGetTicket(t, repo, f.t1.ID).Labels); !reflect.DeepEqual(got, []string{f.label.ID}) {
		t.Errorf("T1's labels = %v, want [%s]", got, f.label.ID)
	}
	mustGetTicket(t, repo, f.t2.ID)
}

func TestCmdDeleteTicket_DeletesTicketAndEverythingItOwns(t *testing.T) {
	forEachBackend(t, func(t *testing.T, repo store.GraphRepository, projectID string) {
		f := newDeleteFixture(t, repo, projectID)
		if count, _ := labelTicketCount(t, repo, projectID, f.label.ID); count != 1 {
			t.Fatalf("precondition: label should be used by 1 ticket, got %d", count)
		}

		var runErr error
		out := captureStdout(t, func() { runErr = cmdDeleteTicket(repo, []string{f.t1.ID, "--yes"}) })
		if runErr != nil {
			t.Fatalf("delete-ticket --yes: %v", runErr)
		}
		var result map[string]any
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatalf("stdout is not JSON only: %v\n%s", err, out)
		}
		if !reflect.DeepEqual(result, map[string]any{"id": f.t1.ID, "deleted": true}) {
			t.Errorf("stdout = %v, want {id: %s, deleted: true}", result, f.t1.ID)
		}

		if detail, err := repo.GetTicketDetail(f.t1.ID); err != nil || detail != nil {
			t.Errorf("GetTicketDetail(T1) = %v, %v; want nil, nil", detail, err)
		}
		var getErr error
		captureStdout(t, func() { getErr = cmdGetTicket(engine.New(repo), []string{f.t1.ID}) })
		if getErr == nil || !strings.Contains(getErr.Error(), "not found") {
			t.Errorf("get-ticket after delete = %v, want a not-found error", getErr)
		}
		for _, n := range []domain.GraphNode{f.n1, f.n2} {
			if got, err := repo.GetNode(n.ID); err != nil || got != nil {
				t.Errorf("GetNode(%s) = %v, %v; want nil, nil", n.ID, got, err)
			}
		}
		if edges, err := repo.ListEdgesByTicket(f.t1.ID); err != nil || len(edges) != 0 {
			t.Errorf("ListEdgesByTicket(T1) = %v, %v; want empty", edges, err)
		}
		if arts, err := repo.ListArtifactsByTicket(f.t1.ID); err != nil || len(arts) != 0 {
			t.Errorf("ListArtifactsByTicket(T1) = %v, %v; want empty", arts, err)
		}
		if a, err := repo.GetArtifact(f.a1.ID); err != nil || a != nil {
			t.Errorf("GetArtifact(A1) = %v, %v; want nil, nil", a, err)
		}
		count, found := labelTicketCount(t, repo, projectID, f.label.ID)
		if !found {
			t.Errorf("the label itself should remain in the project")
		} else if count != 0 {
			t.Errorf("label ticket count = %d, want 0", count)
		}
		mustGetTicket(t, repo, f.t2.ID)
	})
}

func TestCmdDeleteTicket_YesBeforeTicketID(t *testing.T) {
	forEachBackend(t, func(t *testing.T, repo store.GraphRepository, projectID string) {
		f := newDeleteFixture(t, repo, projectID)
		var runErr error
		captureStdout(t, func() { runErr = cmdDeleteTicket(repo, []string{"--yes", f.t1.ID}) })
		if runErr != nil {
			t.Fatalf("delete-ticket --yes <id>: %v", runErr)
		}
		if got, err := repo.GetTicket(f.t1.ID); err != nil || got != nil {
			t.Errorf("GetTicket(T1) = %v, %v; want nil, nil", got, err)
		}
		mustGetTicket(t, repo, f.t2.ID)
	})
}

func TestCmdDeleteTicket_WithoutYesDeletesNothing(t *testing.T) {
	forEachBackend(t, func(t *testing.T, repo store.GraphRepository, projectID string) {
		f := newDeleteFixture(t, repo, projectID)
		var runErr error
		out := captureStdout(t, func() { runErr = cmdDeleteTicket(repo, []string{f.t1.ID}) })
		if runErr == nil {
			t.Fatalf("delete-ticket without --yes should fail")
		}
		msg := runErr.Error()
		for _, want := range []string{"--yes", "cannot be undone", "nodes, edges, artifacts and label attachments"} {
			if !strings.Contains(msg, want) {
				t.Errorf("error %q should mention %q", msg, want)
			}
		}
		if out != "" {
			t.Errorf("nothing should be printed to stdout, got %q", out)
		}
		assertFixtureIntact(t, repo, projectID, f)
	})
}

func TestCmdDeleteTicket_UnknownIDIsTicketNotFound(t *testing.T) {
	forEachBackend(t, func(t *testing.T, repo store.GraphRepository, projectID string) {
		f := newDeleteFixture(t, repo, projectID)
		var runErr error
		captureStdout(t, func() { runErr = cmdDeleteTicket(repo, []string{"NO-SUCH-99999", "--yes"}) })
		assertTicketNotFoundCode(t, runErr)
		assertFixtureIntact(t, repo, projectID, f)
	})
}

func TestCmdDeleteTicket_UsageErrorsDeleteNothing(t *testing.T) {
	forEachBackend(t, func(t *testing.T, repo store.GraphRepository, projectID string) {
		f := newDeleteFixture(t, repo, projectID)
		cases := map[string][]string{
			"no ticket id":           {},
			"--yes only":             {"--yes"},
			"two positionals":        {f.t1.ID, f.t2.ID, "--yes"},
			"unknown flag (--force)": {f.t1.ID, "--yes", "--force"},
		}
		for name, args := range cases {
			var runErr error
			captureStdout(t, func() { runErr = cmdDeleteTicket(repo, args) })
			if runErr == nil || !strings.Contains(runErr.Error(), "usage: graph-engine delete-ticket <ticketId> --yes") {
				t.Errorf("%s: err = %v, want the delete-ticket usage error", name, runErr)
			}
		}
		assertFixtureIntact(t, repo, projectID, f)
	})
}

// --- update-ticket ----------------------------------------------------------

// ticketFields is what update-ticket may or may not change, plus what it
// must never change (status, refined_at, labels).
type ticketFields struct {
	Title, Description string
	Priority           domain.TicketPriority
	Status             domain.TicketStatus
	RefinedAt          string
	LabelIDs           []string
}

func fieldsOf(tk domain.Ticket) ticketFields {
	f := ticketFields{Title: tk.Title, Description: tk.Description, Priority: tk.Priority, Status: tk.Status, LabelIDs: labelIDs(tk.Labels)}
	if tk.RefinedAt != nil {
		f.RefinedAt = *tk.RefinedAt
	}
	return f
}

// runUpdate runs update-ticket and, on success, checks that stdout is the
// updated ticket's JSON (with its labels) and matches what the store holds.
func runUpdate(t *testing.T, repo store.GraphRepository, args ...string) (domain.Ticket, error) {
	t.Helper()
	var runErr error
	out := captureStdout(t, func() { runErr = cmdUpdateTicket(repo, args) })
	if runErr != nil {
		if out != "" {
			t.Errorf("a failed update-ticket should print nothing to stdout, got %q", out)
		}
		return domain.Ticket{}, runErr
	}
	var printed domain.Ticket
	if err := json.Unmarshal([]byte(out), &printed); err != nil {
		t.Fatalf("stdout is not the ticket JSON: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"labels"`) {
		t.Errorf("stdout should include the ticket's labels:\n%s", out)
	}
	stored := mustGetTicket(t, repo, printed.ID)
	if !reflect.DeepEqual(fieldsOf(printed), fieldsOf(stored)) {
		t.Errorf("printed ticket %+v differs from the stored one %+v", fieldsOf(printed), fieldsOf(stored))
	}
	return stored, nil
}

func TestCmdUpdateTicket_ChangesOnlyTheGivenFields(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		stdin  string
		mutate func(f *ticketFields)
	}{
		{"title only", []string{"--title", "新しいタイトル"}, "", func(f *ticketFields) { f.Title = "新しいタイトル" }},
		{"description only", []string{"--description", "新しい説明"}, "", func(f *ticketFields) { f.Description = "新しい説明" }},
		{"priority only", []string{"--priority", "HIGH"}, "", func(f *ticketFields) { f.Priority = domain.TicketPriorityHigh }},
		{"description from stdin", []string{"--description", "-"},
			"# 見出し\n\n- \"引用符\" と 'single' と $HOME と `backquote`\n\n```sh\necho \"$x\"\n```\n",
			func(f *ticketFields) {
				f.Description = "# 見出し\n\n- \"引用符\" と 'single' と $HOME と `backquote`\n\n```sh\necho \"$x\"\n```\n"
			}},
		{"all three, description from stdin", []string{"--title", "新タイトル", "--description", "-", "--priority", "LOW"}, "標準入力からの説明",
			func(f *ticketFields) {
				f.Title, f.Description, f.Priority = "新タイトル", "標準入力からの説明", domain.TicketPriorityLow
			}},
		{"title and priority", []string{"--title", "新タイトル", "--priority", "HIGH"}, "",
			func(f *ticketFields) { f.Title, f.Priority = "新タイトル", domain.TicketPriorityHigh }},
		{"flags in another order", []string{"--priority", "LOW", "--description", "説明2", "--title", "題2"}, "",
			func(f *ticketFields) {
				f.Title, f.Description, f.Priority = "題2", "説明2", domain.TicketPriorityLow
			}},
		{"empty description clears it", []string{"--description", ""}, "", func(f *ticketFields) { f.Description = "" }},
		{"values starting with -", []string{"--title", "-draft", "--description", "- 箇条書き"}, "",
			func(f *ticketFields) { f.Title, f.Description = "-draft", "- 箇条書き" }},
	}
	forEachBackend(t, func(t *testing.T, repo store.GraphRepository, projectID string) {
		label := mustCreateLabel(t, repo, projectID, "機能追加")
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				before := mustCreateInProgressTicket(t, repo, projectID, label)
				if tc.stdin != "" {
					withStdin(t, tc.stdin)
				}
				after, err := runUpdate(t, repo, append([]string{before.ID}, tc.args...)...)
				if err != nil {
					t.Fatalf("update-ticket %v: %v", tc.args, err)
				}
				want := fieldsOf(before)
				tc.mutate(&want)
				if got := fieldsOf(after); !reflect.DeepEqual(got, want) {
					t.Errorf("after update-ticket %v:\n got  %+v\n want %+v", tc.args, got, want)
				}
				if after.Status != domain.TicketInProgress {
					t.Errorf("status = %s, want it to stay IN PROGRESS", after.Status)
				}
			})
		}
	})
}

func TestCmdUpdateTicket_InvalidInputChangesNothing(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		stdin *string
	}{
		{"no field", nil, nil},
		{"priority none", []string{"--priority", "none"}, nil},
		{"priority URGENT", []string{"--priority", "URGENT"}, nil},
		{"priority lower-case", []string{"--priority", "high"}, nil},
		{"empty title", []string{"--title", ""}, nil},
		{"whitespace title", []string{"--title", "   "}, nil},
		{"empty stdin", []string{"--description", "-"}, ptr("")},
		{"whitespace stdin", []string{"--description", "-"}, ptr("  \n\t\n")},
		{"one valid and one invalid field", []string{"--title", "OK", "--priority", "xx"}, nil},
		// Usage errors.
		{"flag without value", []string{"--title"}, nil},
		{"unknown flag --status", []string{"--status", "DONE"}, nil},
		{"unknown flag --assignee", []string{"--assignee", "山田"}, nil},
		{"unknown flag --label", []string{"--label", "機能追加"}, nil},
		{"extra positional", []string{"extra", "--title", "x"}, nil},
		{"duplicated flag", []string{"--title", "a", "--title", "b"}, nil},
	}
	forEachBackend(t, func(t *testing.T, repo store.GraphRepository, projectID string) {
		label := mustCreateLabel(t, repo, projectID, "機能追加")
		before := mustCreateInProgressTicket(t, repo, projectID, label)
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if tc.stdin != nil {
					withStdin(t, *tc.stdin)
				}
				_, err := runUpdate(t, repo, append([]string{before.ID}, tc.args...)...)
				if err == nil {
					t.Fatalf("update-ticket %v should fail", tc.args)
				}
				if !strings.Contains(err.Error(), "usage: graph-engine update-ticket <ticketId>") {
					t.Errorf("err = %v, want it to carry the update-ticket usage", err)
				}
				// Everything, updated_at included, must be exactly as before.
				if after := mustGetTicket(t, repo, before.ID); !reflect.DeepEqual(before, after) {
					t.Errorf("ticket changed:\nbefore=%+v\nafter =%+v", before, after)
				}
			})
		}
	})
}

func ptr(s string) *string { return &s }

func TestCmdUpdateTicket_MissingTicketIDIsUsageError(t *testing.T) {
	repo, _ := newTestRepoWithProject(t)
	for _, args := range [][]string{{}, {"--title", "x"}} {
		_, err := runUpdate(t, repo, args...)
		if err == nil || !strings.Contains(err.Error(), "usage: graph-engine update-ticket <ticketId>") {
			t.Errorf("update-ticket %v: err = %v, want the usage error", args, err)
		}
	}
}

func TestCmdUpdateTicket_UnknownIDIsTicketNotFound(t *testing.T) {
	forEachBackend(t, func(t *testing.T, repo store.GraphRepository, projectID string) {
		label := mustCreateLabel(t, repo, projectID, "機能追加")
		before := mustCreateInProgressTicket(t, repo, projectID, label)
		_, err := runUpdate(t, repo, "NO-SUCH-99999", "--title", "x")
		assertTicketNotFoundCode(t, err)
		if after := mustGetTicket(t, repo, before.ID); !reflect.DeepEqual(before, after) {
			t.Errorf("an unrelated ticket changed:\nbefore=%+v\nafter =%+v", before, after)
		}
	})
}

// --- exit codes (real process) -----------------------------------------------

// The completion criteria ask for a non-zero exit, which only the real
// process (main's exitCodeFor handling) shows, so these run the CLI as a
// subprocess against a temporary SQLite DB.
func TestDeleteUpdateTicket_ExitCodes(t *testing.T) {
	runMainIfSubprocess()

	repo, dir, dbPath := newSubprocessSQLiteRepo(t)
	proj, err := repo.CreateProject("P", "TEST")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	label := mustCreateLabel(t, repo, proj.ID, "機能追加")
	t1 := mustCreateInProgressTicket(t, repo, proj.ID, label)

	for _, args := range [][]string{
		{"delete-ticket", "NO-SUCH-99999", "--yes"},
		{"delete-ticket", t1.ID},
		{"update-ticket", "NO-SUCH-99999", "--title", "x"},
		{"update-ticket", t1.ID},
	} {
		out, code := runCLISubprocess(t, "TestDeleteUpdateTicket_ExitCodes", dir, dbPath, args...)
		if code == 0 {
			t.Errorf("%v: exit code 0, want non-zero\n%s", args, out)
		}
		if !strings.Contains(out, "Error:") {
			t.Errorf("%v: expected an \"Error:\" line, got:\n%s", args, out)
		}
		if after := mustGetTicket(t, repo, t1.ID); !reflect.DeepEqual(t1, after) {
			t.Errorf("%v: ticket changed:\nbefore=%+v\nafter =%+v", args, t1, after)
		}
	}

	// And the success paths exit 0.
	out, code := runCLISubprocess(t, "TestDeleteUpdateTicket_ExitCodes", dir, dbPath, "update-ticket", t1.ID, "--priority", "HIGH")
	if code != 0 {
		t.Fatalf("update-ticket --priority HIGH: exit %d\n%s", code, out)
	}
	if got := mustGetTicket(t, repo, t1.ID); got.Priority != domain.TicketPriorityHigh || got.Status != domain.TicketInProgress {
		t.Errorf("after update-ticket: priority=%s status=%s, want HIGH / IN PROGRESS", got.Priority, got.Status)
	}
	out, code = runCLISubprocess(t, "TestDeleteUpdateTicket_ExitCodes", dir, dbPath, "delete-ticket", t1.ID, "--yes")
	if code != 0 {
		t.Fatalf("delete-ticket --yes: exit %d\n%s", code, out)
	}
	if got, err := repo.GetTicket(t1.ID); err != nil || got != nil {
		t.Errorf("GetTicket after delete-ticket --yes = %v, %v; want nil, nil", got, err)
	}
}

// --- help ---------------------------------------------------------------------

func TestPrintUsage_ListsDeleteAndUpdateTicket(t *testing.T) {
	out := captureStdout(t, printUsage)
	for _, want := range []string{
		"delete-ticket <ticketId> --yes",
		"update-ticket <ticketId> [--title <text>] [--description <text|->] [--priority <HIGH|MEDIUM|LOW>]",
		"--yes is required",
		"Works from ANY status",
		"NOT changed (unlike refine-ticket)",
		`--description "-" reads stdin`,
		"priority without changing the status, use update-ticket",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("usage should contain %q:\n%s", want, out)
		}
	}
}
