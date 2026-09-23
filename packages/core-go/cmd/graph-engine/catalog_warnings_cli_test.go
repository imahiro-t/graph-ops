package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// --- DFLT-00140: a review gate that still sets the retired per-gate
// max_iterations is ignored, and every CLI command that loads the workflow
// catalog says so on stderr (configWarnWriter) -- never on stdout, whose
// JSON skills parse, and without changing the exit status. The team tier is
// not editable from the Web UI, so this is where a leftover value there is
// noticed. ---

const legacyGateDoc = "version: 1\nreview_gates:\n  qa_review:\n    max_iterations: 3\n"

// catalogWarningEnv is one isolated user/team tier pair plus a repo.
type catalogWarningEnv struct {
	rc   runtimeConfig
	repo store.GraphRepository
	eng  *engine.GraphEngine
	proj string
}

// newCatalogWarningEnv builds an env whose tier (if any) carries the legacy
// per-gate value.
func newCatalogWarningEnv(t *testing.T, legacyTier string) catalogWarningEnv {
	t.Helper()
	userDir, teamDir := t.TempDir(), t.TempDir()
	switch legacyTier {
	case "user":
		writeFile(t, filepath.Join(userDir, "config.yaml"), legacyGateDoc)
	case "team":
		writeFile(t, filepath.Join(teamDir, "workflow.yaml"), legacyGateDoc)
	}
	repo, projectID := newTestRepoWithProject(t)
	return catalogWarningEnv{
		rc:   runtimeConfig{WorkDir: t.TempDir(), UserExtensionsDir: userDir, TeamExtensionsDir: teamDir},
		repo: repo,
		eng:  engine.New(repo),
		proj: projectID,
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// seedDoneTicket creates a ticket whose plan/plan_review seed is DONE, ready
// for expand-graph. It loads the catalog directly, so it emits no CLI
// warnings of its own.
func (env catalogWarningEnv) seedDoneTicket(t *testing.T) string {
	t.Helper()
	cat, err := config.LoadWithRoots(env.rc.UserExtensionsDir, env.rc.TeamExtensionsDir, "")
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := env.eng.CreateTicket(env.proj, "title", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		exec, err := env.eng.GetExecutableNodes(ticket.ID, cat)
		if err != nil || len(exec) != 1 {
			t.Fatalf("seed step %d: %v %v", i, exec, err)
		}
		if _, err := env.eng.CompleteNode(exec[0].ID, true, nil); err != nil {
			t.Fatal(err)
		}
	}
	return ticket.ID
}

// runCatalogCommand runs one of the three catalog-loading commands and
// returns a normalized form of its stdout (ids and timestamps stripped, so a
// run in another env compares equal), the warning lines, and its error.
func runCatalogCommand(t *testing.T, env catalogWarningEnv, command string) (normalized string, warnings []string, cmdErr error) {
	t.Helper()
	collect := captureConfigWarnings(t)
	var out string
	switch command {
	case "get-executable":
		ticket, err := env.eng.CreateTicket(env.proj, "title", "")
		if err != nil {
			t.Fatal(err)
		}
		out = captureStdout(t, func() { cmdErr = cmdGetExecutable(env.eng, env.repo, env.rc, []string{ticket.ID}) })
		var nodes []domain.GraphNode
		if err := json.Unmarshal([]byte(out), &nodes); err != nil {
			t.Fatalf("get-executable stdout is not JSON: %v\n%s", err, out)
		}
		normalized = summarizeNodes(nodes)
	case "expand-graph":
		ticketID := env.seedDoneTicket(t)
		out = captureStdout(t, func() { cmdErr = cmdExpandGraph(env.eng, env.repo, env.rc, []string{ticketID}) })
		var detail domain.TicketDetail
		if err := json.Unmarshal([]byte(out), &detail); err != nil {
			t.Fatalf("expand-graph stdout is not JSON: %v\n%s", err, out)
		}
		normalized = summarizeNodes(detail.Nodes)
	case "get-workflow-catalog":
		out = captureStdout(t, func() { cmdErr = cmdGetWorkflowCatalog(env.rc, nil) })
		var generic map[string]any
		if err := json.Unmarshal([]byte(out), &generic); err != nil {
			t.Fatalf("get-workflow-catalog stdout is not JSON: %v\n%s", err, out)
		}
		normalized = out
	default:
		t.Fatalf("unknown command %s", command)
	}
	return normalized, collect(), cmdErr
}

func summarizeNodes(nodes []domain.GraphNode) string {
	lines := make([]string, 0, len(nodes))
	for _, n := range nodes {
		lines = append(lines, fmt.Sprintf("%s|%s|%s|%d|%d", n.Name, n.Type, n.Status, n.IterationCount, n.MaxIterations))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestCatalogCommands_WarnAboutLegacyPerGateMaxIterationsOnStderrOnly(t *testing.T) {
	for _, tier := range []string{"user", "team"} {
		for _, command := range []string{"get-executable", "expand-graph", "get-workflow-catalog"} {
			t.Run(tier+"/"+command, func(t *testing.T) {
				baseline, baseWarnings, baseErr := runCatalogCommand(t, newCatalogWarningEnv(t, ""), command)
				if len(baseWarnings) != 0 {
					t.Fatalf("baseline run warned: %q", baseWarnings)
				}

				got, warnings, err := runCatalogCommand(t, newCatalogWarningEnv(t, tier), command)
				if (err == nil) != (baseErr == nil) {
					t.Fatalf("error changed with the legacy value: %v (baseline %v)", err, baseErr)
				}
				if err != nil {
					t.Fatalf("%s: %v", command, err)
				}
				if len(warnings) != 1 {
					t.Fatalf("warnings = %q, want exactly one", warnings)
				}
				if !strings.HasPrefix(warnings[0], "graph-ops: warning: ") || !strings.Contains(warnings[0], "qa_review") {
					t.Errorf("warning %q lacks the prefix or the gate id", warnings[0])
				}
				if got != baseline {
					t.Errorf("stdout differs from a run without the legacy value:\n--- got\n%s\n--- baseline\n%s", got, baseline)
				}
			})
		}
	}
}

func TestGetExecutable_NoLegacyValueNoWarning(t *testing.T) {
	_, warnings, err := runCatalogCommand(t, newCatalogWarningEnv(t, ""), "get-executable")
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %q, want none", warnings)
	}
}

// get-workflow-catalog reports the resolved workflow-wide limit, team tier
// included.
func TestGetWorkflowCatalog_ReportsMaxIterations(t *testing.T) {
	env := newCatalogWarningEnv(t, "")
	writeFile(t, filepath.Join(env.rc.UserExtensionsDir, "config.yaml"), "version: 1\nmax_iterations: 4\n")
	writeFile(t, filepath.Join(env.rc.TeamExtensionsDir, "workflow.yaml"), "version: 1\nmax_iterations: 5\n")
	out := captureStdout(t, func() {
		if err := cmdGetWorkflowCatalog(env.rc, nil); err != nil {
			t.Fatal(err)
		}
	})
	var resp struct {
		MaxIterations int `json:"max_iterations"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.MaxIterations != 5 {
		t.Errorf("max_iterations = %d, want the team tier's 5", resp.MaxIterations)
	}
}

// expand-graph warns about an inline patch gate's max_iterations and ignores
// it: the node takes the workflow-wide limit.
func TestExpandGraph_InlineGateMaxIterationsWarns(t *testing.T) {
	env := newCatalogWarningEnv(t, "")
	ticketID := env.seedDoneTicket(t)
	patchPath := filepath.Join(t.TempDir(), "patch.json")
	writeFile(t, patchPath, `{"extra_nodes":[
		{"id":"impl","type":"implementation","name":"Impl","depends_on":["plan_review"]},
		{"id":"custom_gate","type":"review_gate","gate":{"name":"Custom","criteria":"c","max_iterations":7},"depends_on":["impl"],"loop_back_to":"impl"}
	]}`)
	collect := captureConfigWarnings(t)
	out := captureStdout(t, func() {
		if err := cmdExpandGraph(env.eng, env.repo, env.rc, []string{ticketID, "--patch", patchPath}); err != nil {
			t.Fatal(err)
		}
	})
	warnings := collect()
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], "graph-ops: warning: ") || !strings.Contains(warnings[0], "custom_gate") {
		t.Errorf("warnings = %q, want one about custom_gate", warnings)
	}
	var detail domain.TicketDetail
	if err := json.Unmarshal([]byte(out), &detail); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	for _, n := range detail.Nodes {
		if n.MaxIterations != 3 {
			t.Errorf("%s: max_iterations = %d, want 3", n.Name, n.MaxIterations)
		}
	}
}

// get-review-criteria loads the whole ticket so the round comes from the
// loop target.
func TestCmdGetReviewCriteria_PrintsRoundAndTier(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatal(err)
	}
	impl, err := repo.CreateNode(domain.GraphNode{TicketID: ticket.ID, Name: "impl", Type: domain.NodeTypeImplementation, Status: domain.NodeDone, MaxIterations: 4, IterationCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	criteria := "Gate criteria text."
	gate, err := repo.CreateNode(domain.GraphNode{TicketID: ticket.ID, Name: "code_review", Type: domain.NodeTypeReviewGate, Status: domain.NodeInReview, MaxIterations: 4, Criteria: &criteria})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateEdge(domain.GraphEdge{
		ID: "edge-" + engine.NewArtifactID(), TicketID: ticket.ID,
		FromNodeID: gate.ID, ToNodeID: impl.ID, Condition: domain.EdgeLoop,
	}); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := cmdGetReviewCriteria(repo, []string{gate.ID}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.HasPrefix(out, criteria) {
		t.Errorf("output must start with the gate criteria:\n%s", out)
	}
	if !strings.Contains(out, "Review round: 3 / 4 — tier: Important") {
		t.Errorf("round/tier line missing:\n%s", out)
	}
	if !strings.Contains(out, "This is not the first round") {
		t.Errorf("round 3 must ask for the previous review:\n%s", out)
	}
}
