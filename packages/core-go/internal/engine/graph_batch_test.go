package engine

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
	"github.com/graph-ops/core-go/internal/store/httpdatasourcetest"
)

// DFLT-00328: the seed and the expansion are written as one
// store.GraphBatch. These tests pin how the engine uses it: never on the
// hot path, losing a race is not an error for the seed and is "already been
// expanded" for the expansion, and a repository that cannot batch still
// gets its graph, one row at a time.

// batchRepo wraps a SQLite repository and lets a test see and steer
// CreateGraphBatch.
type batchRepo struct {
	*store.SQLiteRepository
	batches int
	// before, when set, runs just before a batch reaches the repository --
	// a stand-in for another session creating the graph at that moment.
	before func(r *batchRepo, ticketID string, b store.GraphBatch)
	// unsupported makes every batch answer store.ErrGraphBatchUnsupported,
	// like an HTTP data source older than 1.2.
	unsupported bool
}

func (r *batchRepo) CreateGraphBatch(ticketID string, b store.GraphBatch) error {
	r.batches++
	if r.unsupported {
		return store.ErrGraphBatchUnsupported
	}
	if r.before != nil {
		before := r.before
		r.before = nil
		before(r, ticketID, b)
	}
	return r.SQLiteRepository.CreateGraphBatch(ticketID, b)
}

func newBatchRepoEngine(t *testing.T) (*GraphEngine, *batchRepo, domain.Ticket) {
	t.Helper()
	inner, err := store.NewSQLiteRepository(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	if err := inner.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	proj, err := inner.CreateProject("Test Project", "TEST")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	repo := &batchRepo{SQLiteRepository: inner}
	e := New(repo)
	ticket, err := e.CreateTicket(proj.ID, "t", "")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	return e, repo, ticket
}

func completeSeedNodes(t *testing.T, e *GraphEngine, ticketID string) {
	t.Helper()
	cat := baseCatalog(t)
	for i := 0; i < 2; i++ {
		exec, err := e.GetExecutableNodes(ticketID, cat)
		if err != nil || len(exec) != 1 {
			t.Fatalf("GetExecutableNodes = %v (%v), want one seed node", exec, err)
		}
		if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil {
			t.Fatalf("CompleteNode(%s): %v", exec[0].ID, err)
		}
	}
}

func TestGetExecutableNodes_HotPathTakesNoBatch(t *testing.T) {
	e, repo, ticket := newBatchRepoEngine(t)
	cat := baseCatalog(t)
	if _, err := e.GetExecutableNodes(ticket.ID, cat); err != nil {
		t.Fatalf("first GetExecutableNodes: %v", err)
	}
	if repo.batches != 1 {
		t.Fatalf("the first GetExecutableNodes made %d batches, want 1 (the seed)", repo.batches)
	}
	for i := 0; i < 3; i++ {
		if _, err := e.GetExecutableNodes(ticket.ID, cat); err != nil {
			t.Fatalf("GetExecutableNodes: %v", err)
		}
	}
	if repo.batches != 1 {
		t.Fatalf("GetExecutableNodes on a seeded ticket made a batch (%d in all)", repo.batches)
	}
}

func TestEnsureGraphStarted_LosingTheRaceUsesTheExistingSeed(t *testing.T) {
	e, repo, ticket := newBatchRepoEngine(t)
	repo.before = func(r *batchRepo, ticketID string, b store.GraphBatch) {
		// Another session's seed lands between the check and the batch.
		if err := r.SQLiteRepository.CreateGraphBatch(ticketID, b); err != nil {
			t.Fatalf("the other session's seed: %v", err)
		}
	}
	exec, err := e.GetExecutableNodes(ticket.ID, baseCatalog(t))
	if err != nil {
		t.Fatalf("GetExecutableNodes = %v, want the existing seed to be used", err)
	}
	nodes, _ := repo.ListNodesByTicket(ticket.ID)
	if len(nodes) != 2 {
		t.Fatalf("the ticket has %d nodes, want the one seed of 2", len(nodes))
	}
	if len(exec) != 1 || exec[0].ID != nodes[0].ID {
		t.Fatalf("GetExecutableNodes handed out %v, want the existing plan node %s", exec, nodes[0].ID)
	}
}

func TestExpandGraph_LosingTheRaceIsAlreadyExpanded(t *testing.T) {
	e, repo, ticket := newBatchRepoEngine(t)
	completeSeedNodes(t, e, ticket.ID)
	repo.before = func(r *batchRepo, ticketID string, b store.GraphBatch) {
		if err := r.SQLiteRepository.CreateGraphBatch(ticketID, b); err != nil {
			t.Fatalf("the other session's expansion: %v", err)
		}
	}
	err := e.ExpandGraph(ticket.ID, baseCatalog(t), nil)
	want := "ticket " + ticket.ID + "'s graph has already been expanded"
	if err == nil || err.Error() != want {
		t.Fatalf("ExpandGraph = %v, want exactly %q", err, want)
	}
	if !errors.Is(err, store.ErrGraphChanged) {
		t.Fatalf("ExpandGraph's error %v does not wrap store.ErrGraphChanged", err)
	}
	// The same text as finding the graph expanded before starting.
	if err := e.ExpandGraph(ticket.ID, baseCatalog(t), nil); err == nil || err.Error() != want {
		t.Fatalf("ExpandGraph on an expanded graph = %v, want exactly %q", err, want)
	}
}

func TestExpandGraph_WithoutBatchFallsBackToOneRowAtATime(t *testing.T) {
	batched, repo, ticket := newBatchRepoEngine(t)
	completeSeedNodes(t, batched, ticket.ID)
	if err := batched.ExpandGraph(ticket.ID, baseCatalog(t), nil); err != nil {
		t.Fatalf("ExpandGraph (batched): %v", err)
	}
	wantNodes, _ := repo.ListNodesByTicket(ticket.ID)
	wantEdges, _ := repo.ListEdgesByTicket(ticket.ID)

	e, repo2, ticket2 := newBatchRepoEngine(t)
	repo2.unsupported = true
	completeSeedNodes(t, e, ticket2.ID)
	if err := e.ExpandGraph(ticket2.ID, baseCatalog(t), nil); err != nil {
		t.Fatalf("ExpandGraph (fallback): %v", err)
	}
	nodes, _ := repo2.ListNodesByTicket(ticket2.ID)
	edges, _ := repo2.ListEdgesByTicket(ticket2.ID)
	if len(nodes) != len(wantNodes) || len(edges) != len(wantEdges) {
		t.Fatalf("fallback built %d nodes / %d edges, the batch %d / %d", len(nodes), len(edges), len(wantNodes), len(wantEdges))
	}
	for i := range nodes {
		if *nodes[i].ConfigID != *wantNodes[i].ConfigID || strings.TrimPrefix(nodes[i].ID, ticket2.ID) != strings.TrimPrefix(wantNodes[i].ID, ticket.ID) {
			t.Fatalf("node %d: fallback %s (%s), batch %s (%s)", i, nodes[i].ID, *nodes[i].ConfigID, wantNodes[i].ID, *wantNodes[i].ConfigID)
		}
	}
	got, _ := repo2.GetTicket(ticket2.ID)
	if got.GraphExpandedAt == nil {
		t.Fatal("the fallback did not set graph_expanded_at")
	}
}

// graphBatchRequests returns the POST /tickets/{id}/graph requests the plugin
// received, decoded.
func graphBatchRequests(t *testing.T, plugin *httpdatasourcetest.Plugin) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, r := range plugin.Requests() {
		if r.Method == http.MethodPost && strings.HasSuffix(r.Path, "/graph") {
			var body map[string]any
			if err := json.Unmarshal(r.Body, &body); err != nil {
				t.Fatalf("decoding %s: %v", r.Path, err)
			}
			out = append(out, body)
		}
	}
	return out
}

func newHTTPEngine(t *testing.T, version string) (*GraphEngine, store.GraphRepository, *httpdatasourcetest.Plugin, domain.Ticket) {
	t.Helper()
	plugin := httpdatasourcetest.New("")
	plugin.Version = version
	srv := httptest.NewServer(plugin)
	t.Cleanup(srv.Close)
	repo, err := store.Open(store.Config{Backend: "http", HTTPURL: srv.URL})
	if err != nil {
		t.Fatalf("store.Open(http): %v", err)
	}
	proj, err := repo.CreateProject("P", "HTTP")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	e := New(repo)
	ticket, err := e.CreateTicket(proj.ID, "t", "")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	return e, repo, plugin, ticket
}

func TestGraphBatch_HTTP12SendsOneRequest(t *testing.T) {
	e, repo, plugin, ticket := newHTTPEngine(t, "1.2")
	plugin.ResetRequests()
	completeSeedNodes(t, e, ticket.ID)
	if err := e.ExpandGraph(ticket.ID, baseCatalog(t), nil); err != nil {
		t.Fatalf("ExpandGraph: %v", err)
	}
	for _, r := range plugin.Requests() {
		if r.Method == http.MethodPost && (strings.HasSuffix(r.Path, "/nodes") || strings.HasSuffix(r.Path, "/edges")) {
			t.Fatalf("a 1.2 data source was sent %s %s", r.Method, r.Path)
		}
	}
	reqs := graphBatchRequests(t, plugin)
	if len(reqs) != 2 {
		t.Fatalf("%d graph batch requests, want 2 (seed, expansion)", len(reqs))
	}
	if reqs[0]["expected_node_count"] != float64(0) || reqs[0]["graph_expanded_at"] != nil {
		t.Fatalf("seed batch = %v", reqs[0])
	}
	if reqs[1]["expected_node_count"] != float64(2) || reqs[1]["graph_expanded_at"] == nil {
		t.Fatalf("expansion batch = %v", reqs[1])
	}
	// The edge IDs graph-engine minted are the ones stored.
	sent := map[string]bool{}
	for _, req := range reqs {
		for _, e := range req["edges"].([]any) {
			sent[e.(map[string]any)["id"].(string)] = true
		}
	}
	edges, _ := repo.ListEdgesByTicket(ticket.ID)
	if len(edges) != len(sent) {
		t.Fatalf("%d edges stored, %d sent", len(edges), len(sent))
	}
	for _, e := range edges {
		if !sent[e.ID] || !strings.HasPrefix(e.ID, "edge-") {
			t.Fatalf("stored edge %s is not one graph-engine sent", e.ID)
		}
	}
	got, _ := repo.GetTicket(ticket.ID)
	if got.GraphExpandedAt == nil {
		t.Fatal("graph_expanded_at is not set")
	}
}

func TestGraphBatch_HTTP12GraphChanged(t *testing.T) {
	_, repo, plugin, ticket := newHTTPEngine(t, "1.2")
	cat := baseCatalog(t)
	httpRepo := repo.(*store.HTTPRepository)

	// Seed: another session seeded first -> 409 GRAPH_CHANGED, absorbed.
	plan := "plan"
	if err := httpRepo.CreateGraphBatch(ticket.ID, store.GraphBatch{Nodes: []domain.GraphNode{{Name: "p", Type: domain.NodeTypePlan, Status: domain.NodeTODO, ConfigID: &plan}}}); err != nil {
		t.Fatalf("direct seed: %v", err)
	}
	err := httpRepo.CreateGraphBatch(ticket.ID, store.GraphBatch{Nodes: []domain.GraphNode{{Name: "p", Type: domain.NodeTypePlan, Status: domain.NodeTODO, ConfigID: &plan}}})
	if !errors.Is(err, store.ErrGraphChanged) {
		t.Fatalf("second seed = %v, want ErrGraphChanged", err)
	}
	last := plugin.Requests()[len(plugin.Requests())-1]
	if !strings.HasSuffix(last.Path, "/graph") {
		t.Fatalf("last request %s %s", last.Method, last.Path)
	}

	// Through the engine: an expansion racing another one.
	e2, repo2, _, ticket2 := newHTTPEngine(t, "1.2")
	completeSeedNodes(t, e2, ticket2.ID)
	stamp := "2026-09-30T00:00:00Z"
	if err := repo2.(*store.HTTPRepository).CreateGraphBatch(ticket2.ID, store.GraphBatch{ExpectedNodeCount: 2, GraphExpandedAt: &stamp}); err != nil {
		t.Fatalf("the other session's expansion: %v", err)
	}
	err = e2.ExpandGraph(ticket2.ID, cat, nil)
	if err == nil || err.Error() != "ticket "+ticket2.ID+"'s graph has already been expanded" {
		t.Fatalf("ExpandGraph = %v, want already been expanded", err)
	}
}

func TestGraphBatch_HTTP11FallsBackWithoutSending(t *testing.T) {
	e, repo, plugin, ticket := newHTTPEngine(t, "1.1")
	completeSeedNodes(t, e, ticket.ID)
	if err := e.ExpandGraph(ticket.ID, baseCatalog(t), nil); err != nil {
		t.Fatalf("ExpandGraph: %v", err)
	}
	if reqs := graphBatchRequests(t, plugin); len(reqs) != 0 {
		t.Fatalf("a 1.1 data source was sent %d graph batch requests", len(reqs))
	}
	if err := repo.(*store.HTTPRepository).CreateGraphBatch(ticket.ID, store.GraphBatch{}); !errors.Is(err, store.ErrGraphBatchUnsupported) {
		t.Fatalf("CreateGraphBatch against 1.1 = %v, want ErrGraphBatchUnsupported", err)
	}
	nodes, _ := repo.ListNodesByTicket(ticket.ID)
	if len(nodes) <= 2 {
		t.Fatalf("the fallback created %d nodes", len(nodes))
	}
	got, _ := repo.GetTicket(ticket.ID)
	if got.GraphExpandedAt == nil {
		t.Fatal("graph_expanded_at is not set")
	}
}
