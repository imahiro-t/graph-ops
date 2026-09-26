package store

import (
	"database/sql"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00189: created_at is an RFC3339Nano string (trailing fractional zeros
// dropped), so `ORDER BY created_at` is string order and disagrees with time
// order once the fractional digits differ. The listings must come back in
// time order anyway, including for rows already in the DB with mixed
// precision -- reproduced here by rewriting created_at with raw SQL.

// Three instants in time order whose string order is exactly reversed:
// "…43.10910002Z" < "…43.1091Z" ('0' < 'Z') < "…43Z" ('.' < 'Z').
var mixedPrecisionTimes = [3]string{
	"2026-09-26T10:00:43Z",
	"2026-09-26T10:00:43.1091Z",
	"2026-09-26T10:00:43.10910002Z",
}

// timeOrderRepo is what both SQL backends provide.
type timeOrderRepo interface {
	GraphRepository
	TicketChildLister
	TicketGraphLister
}

func setCreatedAt(t *testing.T, db *sql.DB, table string, ids [3]string) {
	t.Helper()
	for i, id := range ids {
		if _, err := db.Exec(`UPDATE `+table+` SET created_at = ? WHERE id = ?`, mixedPrecisionTimes[i], id); err != nil {
			t.Fatalf("rewriting %s.created_at of %s: %v", table, id, err)
		}
	}
}

// pickIDs returns the IDs of xs that are in want, in xs's order (a listing
// may also hold rows the test did not create, e.g. a default project).
func pickIDs[T any](xs []T, id func(T) string, want [3]string) []string {
	in := map[string]bool{want[0]: true, want[1]: true, want[2]: true}
	var out []string
	for _, x := range xs {
		if in[id(x)] {
			out = append(out, id(x))
		}
	}
	return out
}

func assertIDOrder(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: got %v, want %v", what, got, want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: got %v, want %v (time order)", what, got, want)
			return
		}
	}
}

func assertListingsInTimeOrder(t *testing.T, repo timeOrderRepo, db *sql.DB) {
	t.Helper()

	// Projects.
	var projIDs [3]string
	for i, prefix := range []string{"TOA", "TOB", "TOC"} {
		p, err := repo.CreateProject("Time order "+prefix, prefix)
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		projIDs[i] = p.ID
	}
	setCreatedAt(t, db, "projects", projIDs)
	projects, err := repo.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	assertIDOrder(t, "ListProjects", pickIDs(projects, func(p domain.Project) string { return p.ID }, projIDs), projIDs[0], projIDs[1], projIDs[2])

	// Tickets: a parent and three children.
	projectID := projIDs[0]
	parent, err := repo.CreateTicket(projectID, domain.Ticket{Title: "parent", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket(parent): %v", err)
	}
	var ticketIDs [3]string
	for i := range ticketIDs {
		c, err := repo.CreateTicket(projectID, domain.Ticket{Title: "child", Status: domain.TicketTODO, ParentTicketID: strPtr(parent.ID)})
		if err != nil {
			t.Fatalf("CreateTicket(child): %v", err)
		}
		ticketIDs[i] = c.ID
	}
	setCreatedAt(t, db, "tickets", ticketIDs)
	ticketID := func(tk domain.Ticket) string { return tk.ID }
	all, err := repo.ListTickets()
	if err != nil {
		t.Fatalf("ListTickets: %v", err)
	}
	assertIDOrder(t, "ListTickets (newest first)", pickIDs(all, ticketID, ticketIDs), ticketIDs[2], ticketIDs[1], ticketIDs[0])
	byProject, err := repo.ListTicketsByProject(projectID)
	if err != nil {
		t.Fatalf("ListTicketsByProject: %v", err)
	}
	assertIDOrder(t, "ListTicketsByProject (newest first)", pickIDs(byProject, ticketID, ticketIDs), ticketIDs[2], ticketIDs[1], ticketIDs[0])
	children, err := repo.ListChildTickets(parent.ID)
	if err != nil {
		t.Fatalf("ListChildTickets: %v", err)
	}
	assertIDOrder(t, "ListChildTickets", pickIDs(children, ticketID, ticketIDs), ticketIDs[0], ticketIDs[1], ticketIDs[2])
	// The same through the function (TicketChildLister path).
	children, err = ListChildTickets(repo, parent)
	if err != nil {
		t.Fatalf("ListChildTickets(func): %v", err)
	}
	assertIDOrder(t, "ListChildTickets (func)", pickIDs(children, ticketID, ticketIDs), ticketIDs[0], ticketIDs[1], ticketIDs[2])

	// Nodes, edges and artifacts of one ticket.
	graphTicket := ticketIDs[0]
	var nodeIDs [3]string
	for i := range nodeIDs {
		n, err := repo.CreateNode(domain.GraphNode{TicketID: graphTicket, Name: "n", Type: domain.NodeTypePlan, Status: domain.NodeTODO, MaxIterations: 3})
		if err != nil {
			t.Fatalf("CreateNode: %v", err)
		}
		nodeIDs[i] = n.ID
	}
	edgeIDs := [3]string{"to-e1", "to-e2", "to-e3"}
	for i, id := range edgeIDs {
		if _, err := repo.CreateEdge(domain.GraphEdge{ID: id, TicketID: graphTicket, FromNodeID: nodeIDs[i], ToNodeID: nodeIDs[(i+1)%3]}); err != nil {
			t.Fatalf("CreateEdge: %v", err)
		}
	}
	artIDs := [3]string{"to-a1", "to-a2", "to-a3"}
	for _, id := range artIDs {
		content := "x"
		if _, err := repo.CreateArtifact(domain.Artifact{ID: id, TicketID: graphTicket, NodeID: nodeIDs[0], Name: id, Type: domain.ArtifactText, Content: &content}); err != nil {
			t.Fatalf("CreateArtifact: %v", err)
		}
	}
	setCreatedAt(t, db, "nodes", nodeIDs)
	setCreatedAt(t, db, "edges", edgeIDs)
	setCreatedAt(t, db, "artifacts", artIDs)

	nodeID := func(n domain.GraphNode) string { return n.ID }
	edgeID := func(e domain.GraphEdge) string { return e.ID }
	artID := func(a domain.Artifact) string { return a.ID }

	nodes, err := repo.ListNodesByTicket(graphTicket)
	if err != nil {
		t.Fatalf("ListNodesByTicket: %v", err)
	}
	assertIDOrder(t, "ListNodesByTicket", pickIDs(nodes, nodeID, nodeIDs), nodeIDs[0], nodeIDs[1], nodeIDs[2])
	edges, err := repo.ListEdgesByTicket(graphTicket)
	if err != nil {
		t.Fatalf("ListEdgesByTicket: %v", err)
	}
	assertIDOrder(t, "ListEdgesByTicket", pickIDs(edges, edgeID, edgeIDs), edgeIDs[0], edgeIDs[1], edgeIDs[2])
	arts, err := repo.ListArtifactsByTicket(graphTicket)
	if err != nil {
		t.Fatalf("ListArtifactsByTicket: %v", err)
	}
	assertIDOrder(t, "ListArtifactsByTicket", pickIDs(arts, artID, artIDs), artIDs[0], artIDs[1], artIDs[2])
	arts, err = repo.ListArtifactsByNode(nodeIDs[0])
	if err != nil {
		t.Fatalf("ListArtifactsByNode: %v", err)
	}
	assertIDOrder(t, "ListArtifactsByNode", pickIDs(arts, artID, artIDs), artIDs[0], artIDs[1], artIDs[2])
	detail, err := repo.GetTicketDetail(graphTicket)
	if err != nil || detail == nil {
		t.Fatalf("GetTicketDetail: %+v, %v", detail, err)
	}
	assertIDOrder(t, "GetTicketDetail nodes", pickIDs(detail.Nodes, nodeID, nodeIDs), nodeIDs[0], nodeIDs[1], nodeIDs[2])
	assertIDOrder(t, "GetTicketDetail edges", pickIDs(detail.Edges, edgeID, edgeIDs), edgeIDs[0], edgeIDs[1], edgeIDs[2])
	assertIDOrder(t, "GetTicketDetail artifacts", pickIDs(detail.Artifacts, artID, artIDs), artIDs[0], artIDs[1], artIDs[2])

	gNodes, gEdges, err := repo.ListTicketGraphs([]string{graphTicket, ticketIDs[1]})
	if err != nil {
		t.Fatalf("ListTicketGraphs: %v", err)
	}
	assertIDOrder(t, "ListTicketGraphs nodes", pickIDs(gNodes[graphTicket], nodeID, nodeIDs), nodeIDs[0], nodeIDs[1], nodeIDs[2])
	assertIDOrder(t, "ListTicketGraphs edges", pickIDs(gEdges[graphTicket], edgeID, edgeIDs), edgeIDs[0], edgeIDs[1], edgeIDs[2])

	// The stored strings are returned as they are (no reformatting).
	for i, n := range nodes {
		if n.CreatedAt != mixedPrecisionTimes[i] {
			t.Errorf("node %s created_at = %q, want the stored %q unchanged", n.ID, n.CreatedAt, mixedPrecisionTimes[i])
		}
	}
}

func TestSQLiteRepository_ListingsAreInTimeOrderWithMixedPrecision(t *testing.T) {
	repo := newTestRepo(t)
	assertListingsInTimeOrder(t, repo, repo.db)
}

func TestMySQLRepository_ListingsAreInTimeOrderWithMixedPrecision(t *testing.T) {
	repo := newTestMySQLRepo(t) // skips unless GRAPH_TEST_MYSQL_HOST is set
	assertListingsInTimeOrder(t, repo, repo.db)
}

// Rows at the same instant keep the order SQL returned them in (the sort is
// stable and adds no tiebreaker of its own), even when spelled with a
// different number of fractional digits.
func TestSortByCreatedAt_EqualInstantsKeepIncomingOrder(t *testing.T) {
	nodes := []domain.GraphNode{
		{ID: "b", CreatedAt: "2026-09-26T10:00:43.1Z"},
		{ID: "a", CreatedAt: "2026-09-26T10:00:43.100Z"},
		{ID: "c", CreatedAt: "2026-09-26T10:00:43Z"},
	}
	sortByCreatedAt(nodes, nodeCreatedAt, false)
	assertIDOrder(t, "oldest first", []string{nodes[0].ID, nodes[1].ID, nodes[2].ID}, "c", "b", "a")
	sortByCreatedAt(nodes, nodeCreatedAt, true)
	assertIDOrder(t, "newest first", []string{nodes[0].ID, nodes[1].ID, nodes[2].ID}, "b", "a", "c")
}

func TestSortTicketsByCreation_ComparesAsTimes(t *testing.T) {
	tickets := []domain.Ticket{
		{ID: "T-00003", CreatedAt: "2026-09-26T10:00:43.10910002Z"},
		{ID: "T-00002", CreatedAt: "2026-09-26T10:00:43.1091Z"},
		{ID: "T-00005", CreatedAt: "2026-09-26T10:00:44.1Z"},
		{ID: "T-00004", CreatedAt: "2026-09-26T10:00:44.100Z"}, // same instant as T-00005
	}
	sortTicketsByCreation(tickets)
	got := []string{}
	for _, tk := range tickets {
		got = append(got, tk.ID)
	}
	assertIDOrder(t, "sortTicketsByCreation", got, "T-00002", "T-00003", "T-00004", "T-00005")
}
