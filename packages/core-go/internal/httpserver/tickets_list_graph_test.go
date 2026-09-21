package httpserver

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00112: GET /api/tickets carries each ticket's execution graph
// (nodes/edges) but never its artifacts, so the Web UI's 15s poll is one
// request whose payload doesn't grow with the stored artifact bodies.
//
// These tests read the response as []map[string]any rather than into
// domain.TicketGraph on purpose: what matters is the JSON the browser
// actually receives -- which keys are present, and that nodes/edges are
// arrays rather than null -- and decoding into the Go type would hide
// exactly those two things.

// seedGraphTicket creates a ticket with n nodes, an edge between the first
// two of them, and one text artifact whose body must not show up in the
// list response.
func seedGraphTicket(t *testing.T, repo store.GraphRepository, projectID, title string, nodeCount int, artifactBody string) domain.Ticket {
	t.Helper()
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: title, Description: "d", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	var nodes []domain.GraphNode
	for i := 0; i < nodeCount; i++ {
		n, err := repo.CreateNode(domain.GraphNode{
			ID:       "node-" + engine.NewArtifactID(),
			TicketID: ticket.ID,
			Name:     "node",
			Type:     domain.NodeTypePlan,
			Status:   domain.NodeTODO,
		})
		if err != nil {
			t.Fatalf("CreateNode: %v", err)
		}
		nodes = append(nodes, n)
	}
	if len(nodes) >= 2 {
		if _, err := repo.CreateEdge(domain.GraphEdge{
			ID:         "edge-" + engine.NewArtifactID(),
			TicketID:   ticket.ID,
			FromNodeID: nodes[0].ID,
			ToNodeID:   nodes[1].ID,
			Condition:  domain.EdgeAlways,
		}); err != nil {
			t.Fatalf("CreateEdge: %v", err)
		}
	}
	if len(nodes) > 0 && artifactBody != "" {
		if _, err := repo.CreateArtifact(domain.Artifact{
			ID:       "art-" + engine.NewArtifactID(),
			TicketID: ticket.ID,
			NodeID:   nodes[0].ID,
			Name:     "plan",
			Type:     domain.ArtifactText,
			Content:  &artifactBody,
		}); err != nil {
			t.Fatalf("CreateArtifact: %v", err)
		}
	}
	return ticket
}

// decodeTicketList reads a ticket-list response as raw JSON objects, keyed
// by ticket ID.
func decodeTicketList(t *testing.T, rec *httptest.ResponseRecorder) map[string]map[string]any {
	t.Helper()
	if rec.Code != 200 {
		t.Fatalf("GET /api/tickets: status %d (body: %s)", rec.Code, rec.Body.String())
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode ticket list: %v (body: %s)", err, rec.Body.String())
	}
	out := map[string]map[string]any{}
	for _, item := range list {
		id, _ := item["id"].(string)
		out[id] = item
	}
	return out
}

// jsonArray fails the test unless item[key] is a JSON array (not null, not
// absent) and returns it.
func jsonArray(t *testing.T, item map[string]any, key string) []any {
	t.Helper()
	raw, ok := item[key]
	if !ok {
		t.Fatalf("ticket %v has no %q key", item["id"], key)
	}
	if raw == nil {
		t.Fatalf("ticket %v has %q = null; the Web UI reads .%s.length unconditionally", item["id"], key, key)
	}
	arr, ok := raw.([]any)
	if !ok {
		t.Fatalf("ticket %v has a non-array %q: %T", item["id"], key, raw)
	}
	return arr
}

func TestHandleListTickets_IncludesGraphExcludesArtifacts(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	const secret = "この計画本文はポーリングのレスポンスに乗ってはいけない"
	a := seedGraphTicket(t, repo, projectID, "A", 3, secret)
	b := seedGraphTicket(t, repo, projectID, "B", 2, secret)

	rec := doJSON(t, s, "GET", "/api/tickets", nil)
	body := rec.Body.String()
	list := decodeTicketList(t, rec)

	if len(list) != 2 {
		t.Fatalf("expected 2 tickets, got %d (body: %s)", len(list), body)
	}
	for id, want := range map[string]int{a.ID: 3, b.ID: 2} {
		item, ok := list[id]
		if !ok {
			t.Fatalf("ticket %s missing from the list response", id)
		}
		if got := len(jsonArray(t, item, "nodes")); got != want {
			t.Errorf("ticket %s: got %d nodes, want %d", id, got, want)
		}
		if got := len(jsonArray(t, item, "edges")); got != 1 {
			t.Errorf("ticket %s: got %d edges, want 1", id, got)
		}
		if _, present := item["artifacts"]; present {
			t.Errorf("ticket %s: the list response carries an \"artifacts\" key; artifacts belong to GET /api/tickets/{id} only", id)
		}
	}
	// Completion criterion 2: no artifact body in a polling response at all,
	// not merely no "artifacts" key.
	if strings.Contains(body, secret) {
		t.Errorf("the list response contains a text artifact's body")
	}

	// The detail endpoint is unchanged: it still carries artifacts, bodies
	// included, for the one ticket the user expanded.
	rec = doJSON(t, s, "GET", "/api/tickets/"+a.ID, nil)
	if rec.Code != 200 {
		t.Fatalf("GET /api/tickets/%s: status %d", a.ID, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), secret) {
		t.Errorf("GET /api/tickets/{id} no longer returns the artifact body")
	}
}

// A ticket whose graph has not been built yet must serialize as [] rather
// than null -- App.tsx/TicketItem.tsx call ticket.nodes.length and
// ticket.edges.filter(...) without a guard, so a null would crash the UI.
func TestHandleListTickets_EmptyGraphIsArrayNotNull(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket := seedGraphTicket(t, repo, projectID, "no nodes yet", 0, "")

	list := decodeTicketList(t, doJSON(t, s, "GET", "/api/tickets", nil))
	item, ok := list[ticket.ID]
	if !ok {
		t.Fatalf("ticket %s missing from the list response", ticket.ID)
	}
	if got := len(jsonArray(t, item, "nodes")); got != 0 {
		t.Errorf("got %d nodes, want 0", got)
	}
	if got := len(jsonArray(t, item, "edges")); got != 0 {
		t.Errorf("got %d edges, want 0", got)
	}
}

// ?all=true (the cross-project escape hatch) goes through the same
// post-processing as the project-scoped default, so it returns the same
// shape -- including the tickets of a project that is not the current one.
func TestHandleListTickets_AllTrueHasSameShape(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	const secret = "他プロジェクトの成果物本文"
	mine := seedGraphTicket(t, repo, projectID, "current project", 2, secret)

	other, err := repo.CreateProject("Other Project", "OTHR")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := runtimeconfig.SetProjectPath(s.cfg.HomeDir, other.ID, t.TempDir()); err != nil {
		t.Fatalf("SetProjectPath: %v", err)
	}
	theirs := seedGraphTicket(t, repo, other.ID, "other project", 3, secret)

	scoped := decodeTicketList(t, doJSON(t, s, "GET", "/api/tickets", nil))
	if _, present := scoped[theirs.ID]; present {
		t.Fatalf("the project-scoped listing leaked another project's ticket")
	}

	rec := doJSON(t, s, "GET", "/api/tickets?all=true", nil)
	all := decodeTicketList(t, rec)
	for id, want := range map[string]int{mine.ID: 2, theirs.ID: 3} {
		item, ok := all[id]
		if !ok {
			t.Fatalf("ticket %s missing from the ?all=true response", id)
		}
		if got := len(jsonArray(t, item, "nodes")); got != want {
			t.Errorf("ticket %s: got %d nodes, want %d", id, got, want)
		}
		if got := len(jsonArray(t, item, "edges")); got != 1 {
			t.Errorf("ticket %s: got %d edges, want 1", id, got)
		}
		if _, present := item["artifacts"]; present {
			t.Errorf("ticket %s: the ?all=true response carries an \"artifacts\" key", id)
		}
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Errorf("the ?all=true response contains a text artifact's body")
	}
}

// repoWithoutBulkGraphs is a GraphRepository that does not implement
// store.TicketGraphLister -- the shape of the HTTP data source backend,
// whose remote protocol has no bulk graph read. Embedding the interface
// (rather than a concrete repository) is what drops the extra method: the
// wrapper only has GraphRepository's own methods.
//
// Every call the fallback could make is counted, because against the HTTP
// data source each one is a separate remote round trip: what the fallback
// must cost is exactly one downstream read per listed ticket, not two.
type repoWithoutBulkGraphs struct {
	store.GraphRepository
	nodeCalls   int
	edgeCalls   int
	detailCalls int
	// detailNotFound makes GetTicketDetail report every ticket as missing,
	// standing in for a ticket deleted between the listing and the read.
	detailNotFound bool
}

func (r *repoWithoutBulkGraphs) ListNodesByTicket(ticketID string) ([]domain.GraphNode, error) {
	r.nodeCalls++
	return r.GraphRepository.ListNodesByTicket(ticketID)
}

func (r *repoWithoutBulkGraphs) ListEdgesByTicket(ticketID string) ([]domain.GraphEdge, error) {
	r.edgeCalls++
	return r.GraphRepository.ListEdgesByTicket(ticketID)
}

func (r *repoWithoutBulkGraphs) GetTicketDetail(ticketID string) (*domain.TicketDetail, error) {
	r.detailCalls++
	if r.detailNotFound {
		return nil, nil
	}
	return r.GraphRepository.GetTicketDetail(ticketID)
}

func TestHandleListTickets_FallsBackWhenRepoHasNoBulkRead(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	a := seedGraphTicket(t, repo, projectID, "A", 3, "本文")
	b := seedGraphTicket(t, repo, projectID, "B", 0, "")

	fallback := &repoWithoutBulkGraphs{GraphRepository: repo}
	if _, ok := any(fallback).(store.TicketGraphLister); ok {
		t.Fatal("the fallback fixture implements TicketGraphLister; it must not")
	}
	s.repo = fallback

	list := decodeTicketList(t, doJSON(t, s, "GET", "/api/tickets", nil))
	if got := len(jsonArray(t, list[a.ID], "nodes")); got != 3 {
		t.Errorf("ticket %s: got %d nodes, want 3", a.ID, got)
	}
	if got := len(jsonArray(t, list[b.ID], "nodes")); got != 0 {
		t.Errorf("ticket %s: got %d nodes, want 0", b.ID, got)
	}
	// Edges are asserted separately from nodes because ticketGraphs hands the
	// two over on two separate lines: a test that only counts nodes still
	// passes when the edge assignment is gone. Dropping it is not a quiet gap
	// in the UI either -- TicketItem.tsx's isNodeReached is
	// ticket.edges.every(...), and every() is true for an empty array, so an
	// empty edges list makes every TODO approval gate blink at once, the bug
	// DFLT-00016 fixed. seedGraphTicket wires node 0 -> node 1, so ticket A
	// has exactly one edge and the node-less ticket B has none.
	if got := len(jsonArray(t, list[a.ID], "edges")); got != 1 {
		t.Errorf("ticket %s: got %d edges, want 1", a.ID, got)
	}
	if got := len(jsonArray(t, list[b.ID], "edges")); got != 0 {
		t.Errorf("ticket %s: got %d edges, want 0", b.ID, got)
	}
	if _, present := list[a.ID]["artifacts"]; present {
		t.Errorf("the fallback path carries an \"artifacts\" key")
	}

	// The call count is the point of this test. Against the HTTP data
	// source every downstream call is a remote round trip, so the fallback
	// must spend exactly one per listed ticket -- GET /tickets/{id}/detail,
	// which brings nodes and edges back together. The
	// ListNodesByTicket/ListEdgesByTicket pair would be two sequential
	// round trips per ticket (2N+1 for the whole poll instead of N+1),
	// doubling the load on the data source that this ticket set out to
	// reduce.
	if fallback.detailCalls != 2 {
		t.Errorf("fallback made %d GetTicketDetail calls, want 2 (one per listed ticket)", fallback.detailCalls)
	}
	if fallback.nodeCalls != 0 || fallback.edgeCalls != 0 {
		t.Errorf("fallback made %d ListNodesByTicket / %d ListEdgesByTicket calls, want 0 / 0: "+
			"per-ticket node+edge reads are two remote round trips where one detail read suffices",
			fallback.nodeCalls, fallback.edgeCalls)
	}
}

// A ticket that disappears between the listing and the fallback's graph read
// must not fail the whole poll: it comes back with an empty graph, which is
// what the per-ticket node/edge reads returned for an unknown ticket as
// well.
func TestHandleListTickets_FallbackTolerationOfVanishedTicket(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	a := seedGraphTicket(t, repo, projectID, "A", 3, "本文")

	fallback := &repoWithoutBulkGraphs{GraphRepository: repo, detailNotFound: true}
	s.repo = fallback

	list := decodeTicketList(t, doJSON(t, s, "GET", "/api/tickets", nil))
	item, ok := list[a.ID]
	if !ok {
		t.Fatalf("ticket %s missing from the list response", a.ID)
	}
	if got := len(jsonArray(t, item, "nodes")); got != 0 {
		t.Errorf("got %d nodes, want 0", got)
	}
	if got := len(jsonArray(t, item, "edges")); got != 0 {
		t.Errorf("got %d edges, want 0", got)
	}
}
