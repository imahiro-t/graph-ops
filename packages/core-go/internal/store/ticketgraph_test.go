package store

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00112: ListTicketGraphs, the bulk read behind GET /api/tickets.
//
// Both SQL backends share one test body (graphListerRepo) so the MySQL
// implementation can't drift from the SQLite one -- the `condition` column
// has to be quoted differently on MySQL, which is exactly the kind of
// difference a SQLite-only test would miss. The MySQL half skips unless a
// server is configured (see mysqlTestConfig / dev/mysql/test.sh).

// graphListerRepo is what both backends satisfy: the full repository plus
// the optional bulk read.
type graphListerRepo interface {
	GraphRepository
	TicketGraphLister
}

func runTicketGraphTests(t *testing.T, newRepo func(t *testing.T) graphListerRepo) {
	t.Helper()
	t.Run("GroupsByTicketAndIgnoresOthers", func(t *testing.T) {
		testTicketGraphsGroupsByTicket(t, newRepo(t))
	})
	t.Run("EmptyInput", func(t *testing.T) {
		testTicketGraphsEmptyInput(t, newRepo(t))
	})
	t.Run("MatchesPerTicketOrder", func(t *testing.T) {
		testTicketGraphsMatchPerTicketOrder(t, newRepo(t))
	})
	t.Run("ChunkBoundary", func(t *testing.T) {
		testTicketGraphsChunkBoundary(t, newRepo(t))
	})
}

// seedGraph gives ticketID nodeCount nodes (chained by edges) and returns
// the nodes it created.
func seedGraph(t *testing.T, repo GraphRepository, ticketID string, nodeCount int) []domain.GraphNode {
	t.Helper()
	nodes := make([]domain.GraphNode, 0, nodeCount)
	for i := 0; i < nodeCount; i++ {
		n, err := repo.CreateNode(domain.GraphNode{
			TicketID: ticketID,
			Name:     fmt.Sprintf("node %d", i),
			Type:     domain.NodeTypePlan,
			Status:   domain.NodeTODO,
		})
		if err != nil {
			t.Fatalf("CreateNode: %v", err)
		}
		nodes = append(nodes, n)
	}
	for i := 1; i < len(nodes); i++ {
		if _, err := repo.CreateEdge(domain.GraphEdge{
			ID:         fmt.Sprintf("edge-%s-%d", ticketID, i),
			TicketID:   ticketID,
			FromNodeID: nodes[i-1].ID,
			ToNodeID:   nodes[i].ID,
			Condition:  domain.EdgeAlways,
		}); err != nil {
			t.Fatalf("CreateEdge: %v", err)
		}
	}
	return nodes
}

func testTicketGraphsGroupsByTicket(t *testing.T, repo graphListerRepo) {
	projA, err := repo.CreateProject("A", "AAAA")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	projB, err := repo.CreateProject("B", "BBBB")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	newTicket := func(projectID, title string) domain.Ticket {
		tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: title, Status: domain.TicketTODO})
		if err != nil {
			t.Fatalf("CreateTicket: %v", err)
		}
		return tk
	}
	t1 := newTicket(projA.ID, "t1")
	t2 := newTicket(projA.ID, "t2")
	// A ticket with no graph at all, and one belonging to another project:
	// neither may contribute rows to the asked-for tickets.
	empty := newTicket(projA.ID, "empty")
	otherProject := newTicket(projB.ID, "other project")

	seedGraph(t, repo, t1.ID, 3)
	seedGraph(t, repo, t2.ID, 2)
	seedGraph(t, repo, otherProject.ID, 4)

	// The same ID twice: callers hand over whatever the list query returned,
	// and a duplicate must not duplicate rows.
	nodes, edges, err := repo.ListTicketGraphs([]string{t1.ID, t2.ID, empty.ID, t1.ID})
	if err != nil {
		t.Fatalf("ListTicketGraphs: %v", err)
	}

	if got := len(nodes[t1.ID]); got != 3 {
		t.Errorf("ticket %s: got %d nodes, want 3", t1.ID, got)
	}
	if got := len(edges[t1.ID]); got != 2 {
		t.Errorf("ticket %s: got %d edges, want 2", t1.ID, got)
	}
	if got := len(nodes[t2.ID]); got != 2 {
		t.Errorf("ticket %s: got %d nodes, want 2", t2.ID, got)
	}
	if got := len(edges[t2.ID]); got != 1 {
		t.Errorf("ticket %s: got %d edges, want 1", t2.ID, got)
	}
	// A ticket without nodes is simply absent; the HTTP layer normalizes
	// that to [].
	if _, present := nodes[empty.ID]; present {
		t.Errorf("ticket %s has no nodes but appears in the node map", empty.ID)
	}
	if _, present := nodes[otherProject.ID]; present {
		t.Errorf("a ticket that was not asked for (%s) leaked into the result", otherProject.ID)
	}
	if _, present := edges[otherProject.ID]; present {
		t.Errorf("a ticket that was not asked for (%s) leaked into the edge result", otherProject.ID)
	}
	for _, n := range nodes[t1.ID] {
		if n.TicketID != t1.ID {
			t.Errorf("node %s is grouped under %s but belongs to %s", n.ID, t1.ID, n.TicketID)
		}
	}
}

func testTicketGraphsEmptyInput(t *testing.T, repo graphListerRepo) {
	nodes, edges, err := repo.ListTicketGraphs(nil)
	if err != nil {
		t.Fatalf("ListTicketGraphs(nil): %v", err)
	}
	if len(nodes) != 0 || len(edges) != 0 {
		t.Errorf("ListTicketGraphs(nil) = %v, %v; want two empty maps", nodes, edges)
	}
	if nodes, edges, err = repo.ListTicketGraphs([]string{}); err != nil {
		t.Fatalf("ListTicketGraphs([]): %v", err)
	}
	if len(nodes) != 0 || len(edges) != 0 {
		t.Errorf("ListTicketGraphs([]) = %v, %v; want two empty maps", nodes, edges)
	}
}

// The bulk read must return exactly what the per-ticket reads do, in the
// same order: the Web UI renders the node chips in the order it receives
// them, and that order must not change just because the list endpoint now
// loads them differently.
func testTicketGraphsMatchPerTicketOrder(t *testing.T, repo graphListerRepo) {
	proj, err := repo.CreateProject("P", "PPPP")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	tk, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	seedGraph(t, repo, tk.ID, 5)

	wantNodes, err := repo.ListNodesByTicket(tk.ID)
	if err != nil {
		t.Fatalf("ListNodesByTicket: %v", err)
	}
	wantEdges, err := repo.ListEdgesByTicket(tk.ID)
	if err != nil {
		t.Fatalf("ListEdgesByTicket: %v", err)
	}

	nodes, edges, err := repo.ListTicketGraphs([]string{tk.ID})
	if err != nil {
		t.Fatalf("ListTicketGraphs: %v", err)
	}
	if !reflect.DeepEqual(nodes[tk.ID], wantNodes) {
		t.Errorf("bulk nodes differ from ListNodesByTicket:\n got %+v\nwant %+v", nodes[tk.ID], wantNodes)
	}
	if !reflect.DeepEqual(edges[tk.ID], wantEdges) {
		t.Errorf("bulk edges differ from ListEdgesByTicket:\n got %+v\nwant %+v", edges[tk.ID], wantEdges)
	}
}

// More ticket IDs than fit in one IN (...) clause: the read splits into
// several statements (SQLite caps bound parameters per statement), and every
// ticket must still come back. Only the first and last ticket of the set get
// a node, so the assertion covers both chunks without creating hundreds of
// rows.
func testTicketGraphsChunkBoundary(t *testing.T, repo graphListerRepo) {
	proj, err := repo.CreateProject("P", "PPPP")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	ids := make([]string, 0, ticketGraphIDChunk+1)
	for i := 0; i < ticketGraphIDChunk+1; i++ {
		// Only the tickets that need nodes are really created; the rest are
		// synthetic IDs, which the query must tolerate (a ticket may be
		// deleted between the list query and this one).
		if i == 0 || i == ticketGraphIDChunk {
			tk, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: fmt.Sprintf("t%d", i), Status: domain.TicketTODO})
			if err != nil {
				t.Fatalf("CreateTicket: %v", err)
			}
			seedGraph(t, repo, tk.ID, 2)
			ids = append(ids, tk.ID)
			continue
		}
		ids = append(ids, fmt.Sprintf("GONE-%05d", i))
	}

	nodes, edges, err := repo.ListTicketGraphs(ids)
	if err != nil {
		t.Fatalf("ListTicketGraphs over %d ids: %v", len(ids), err)
	}
	for _, id := range []string{ids[0], ids[ticketGraphIDChunk]} {
		if got := len(nodes[id]); got != 2 {
			t.Errorf("ticket %s: got %d nodes, want 2", id, got)
		}
		if got := len(edges[id]); got != 1 {
			t.Errorf("ticket %s: got %d edges, want 1", id, got)
		}
	}
	if len(nodes) != 2 {
		t.Errorf("got graphs for %d tickets, want 2", len(nodes))
	}
}

func TestSQLiteRepository_ListTicketGraphs(t *testing.T) {
	runTicketGraphTests(t, func(t *testing.T) graphListerRepo { return newTestRepo(t) })
}

func TestMySQLRepository_ListTicketGraphs(t *testing.T) {
	runTicketGraphTests(t, func(t *testing.T) graphListerRepo { return newTestMySQLRepo(t) })
}

func TestChunkIDs(t *testing.T) {
	if got := chunkIDs(nil, 2); got != nil {
		t.Errorf("chunkIDs(nil) = %v, want nil", got)
	}
	got := chunkIDs([]string{"a", "b", "c", "d", "e"}, 2)
	want := [][]string{{"a", "b"}, {"c", "d"}, {"e"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("chunkIDs = %v, want %v", got, want)
	}
}
