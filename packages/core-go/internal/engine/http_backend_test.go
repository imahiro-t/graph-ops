package engine

import (
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
	"github.com/graph-ops/core-go/internal/store/httpdatasourcetest"
)

// runRepresentativeFlow drives the engine's representative flow against
// repo -- create and refine a ticket, seed its graph, complete plan and
// plan_review, expand the graph (CreateNode/CreateEdge), complete the next
// node with an html artifact -- and returns a backend-independent trace of
// what each step observed, for comparison between backends.
func runRepresentativeFlow(t *testing.T, repo store.GraphRepository) []string {
	t.Helper()
	var trace []string
	step := func(format string, args ...any) { trace = append(trace, fmt.Sprintf(format, args...)) }

	proj, err := repo.CreateProject("Flow", "FLOW")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := repo.SetCurrentProjectID(proj.ID); err != nil {
		t.Fatalf("SetCurrentProjectID: %v", err)
	}
	e := New(repo)
	cat := baseCatalog(t)

	ticket, err := e.CreateTicket(proj.ID, "title", "desc")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	step("created %s status=%s priority=%s labels=%d", ticket.ID, ticket.Status, ticket.Priority, len(ticket.Labels))
	refined, err := e.RefineTicket(ticket.ID, "refined description", NoPriorityChange())
	if err != nil {
		t.Fatalf("RefineTicket: %v", err)
	}
	step("refined status=%s refined_at_set=%v", refined.Status, refined.RefinedAt != nil)

	executable := func(label string) []domain.GraphNode {
		exec, err := e.GetExecutableNodes(ticket.ID, cat)
		if err != nil {
			t.Fatalf("GetExecutableNodes (%s): %v", label, err)
		}
		var parts []string
		for _, n := range exec {
			parts = append(parts, n.ID+":"+string(n.Type))
		}
		step("%s executable=[%s]", label, strings.Join(parts, ","))
		return exec
	}

	exec := executable("seed")
	planText := "the plan"
	if _, err := e.CompleteNode(exec[0].ID, true, []domain.Artifact{{Name: "plan", Type: domain.ArtifactText, Content: &planText}}); err != nil {
		t.Fatalf("CompleteNode(plan): %v", err)
	}
	exec = executable("after plan")
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(plan_review): %v", err)
	}
	if err := e.ExpandGraph(ticket.ID, cat, nil); err != nil {
		t.Fatalf("ExpandGraph: %v", err)
	}
	detail, err := repo.GetTicketDetail(ticket.ID)
	if err != nil || detail == nil {
		t.Fatalf("GetTicketDetail: %v", err)
	}
	step("expanded status=%s nodes=%d edges=%d artifacts=%d graph_expanded=%v",
		detail.Status, len(detail.Nodes), len(detail.Edges), len(detail.Artifacts), detail.GraphExpandedAt != nil)
	if len(detail.Artifacts) != 1 || detail.Artifacts[0].Content == nil || *detail.Artifacts[0].Content != planText {
		t.Fatalf("expected the plan artifact to round-trip, got %+v", detail.Artifacts)
	}

	exec = executable("after expand")
	if len(exec) == 0 {
		// The default graph stops at the plan approval gate, which a human
		// approves (CompleteNode on the gate) rather than GetExecutableNodes
		// handing it out.
		for _, n := range detail.Nodes {
			if n.Type == domain.NodeTypeApprovalGate && n.Status == domain.NodeTODO {
				res, err := e.CompleteNode(n.ID, true, nil)
				if err != nil {
					t.Fatalf("CompleteNode(approval %s): %v", n.ID, err)
				}
				step("approved %s next=%s", n.ID, res.NextStatus)
				break
			}
		}
		exec = executable("after approval")
	}
	if len(exec) == 0 {
		t.Fatal("no executable node after expansion and approval")
	}
	next := exec[0]
	html := "<html><body>ok</body></html>"
	res, err := e.CompleteNode(next.ID, true, []domain.Artifact{{Name: "report", Type: domain.ArtifactHTML, Content: &html}})
	if err != nil {
		t.Fatalf("CompleteNode(%s): %v", next.ID, err)
	}
	step("completed %s next=%s", next.ID, res.NextStatus)
	node, err := repo.GetNode(next.ID)
	if err != nil || node == nil {
		t.Fatalf("GetNode(%s): %v", next.ID, err)
	}
	arts, err := repo.ListArtifactsByNode(next.ID)
	if err != nil || len(arts) != 1 || arts[0].Content == nil || *arts[0].Content != html {
		t.Fatalf("ListArtifactsByNode(%s) = %+v (%v)", next.ID, arts, err)
	}
	listed, err := repo.ListArtifactsByTicket(ticket.ID)
	if err != nil {
		t.Fatalf("ListArtifactsByTicket: %v", err)
	}
	for _, a := range listed {
		step("listed artifact %s type=%s has_content=%v content_inlined=%v", a.Name, a.Type, a.HasContent, a.Content != nil)
	}
	step("node %s status=%s", node.ID, node.Status)
	executable("after first expanded node")

	final, err := repo.GetTicket(ticket.ID)
	if err != nil || final == nil {
		t.Fatalf("GetTicket: %v", err)
	}
	step("final status=%s", final.Status)
	if cur, err := repo.GetCurrentProjectID(); err != nil || cur != proj.ID {
		t.Fatalf("GetCurrentProjectID = %q (%v), want %q", cur, err, proj.ID)
	}
	return trace
}

// TestEngineFlow_HTTPBackendMatchesSQLite runs the representative engine
// flow against the HTTP data source backend (store.Open with Backend
// "http", talking to the in-memory reference plugin) and against SQLite, and
// requires both to observe exactly the same thing at every step. It lives in
// package engine (not store) because engine imports store; the reference
// plugin lives in the non-test package internal/store/httpdatasourcetest for
// the same reason (DFLT-00088 plan review, condition 1).
func TestEngineFlow_HTTPBackendMatchesSQLite(t *testing.T) {
	plugin := httpdatasourcetest.New("engine-flow-token")
	srv := httptest.NewServer(plugin)
	defer srv.Close()
	httpRepo, err := store.Open(store.Config{Backend: "http", HTTPURL: srv.URL, HTTPToken: "engine-flow-token"})
	if err != nil {
		t.Fatalf("store.Open(http): %v", err)
	}
	if _, ok := httpRepo.(*store.HTTPRepository); !ok {
		t.Fatalf("expected *store.HTTPRepository, got %T", httpRepo)
	}
	sqliteRepo, err := store.Open(store.Config{Backend: "sqlite", SQLitePath: filepath.Join(t.TempDir(), "flow.db")})
	if err != nil {
		t.Fatalf("store.Open(sqlite): %v", err)
	}

	httpTrace := runRepresentativeFlow(t, httpRepo)
	sqliteTrace := runRepresentativeFlow(t, sqliteRepo)
	if !reflect.DeepEqual(httpTrace, sqliteTrace) {
		t.Fatalf("the HTTP backend diverged from SQLite:\nhttp:\n  %s\nsqlite:\n  %s",
			strings.Join(httpTrace, "\n  "), strings.Join(sqliteTrace, "\n  "))
	}
	if len(plugin.Requests()) == 0 {
		t.Fatal("the plugin recorded no requests")
	}
}
