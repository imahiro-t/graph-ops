package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

// Store-level tests for CreateGraphBatch (DFLT-00328), against both SQL
// backends: the check, the all-or-nothing write, and the order the rows list
// back in. The concurrent tests, which drive the engine, are in
// graph_batch_concurrency_test.go (package store_test).

func batchNode(configID string) domain.GraphNode {
	c := configID
	return domain.GraphNode{Name: configID, Type: domain.NodeTypeImplementation, Status: domain.NodeTODO, ConfigID: &c}
}

func (b backend) newTicket(t *testing.T) domain.Ticket {
	t.Helper()
	ticket, err := b.repo.CreateTicket(b.proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	return ticket
}

func (b backend) batcher(t *testing.T) GraphBatchCreator {
	t.Helper()
	c, ok := b.repo.(GraphBatchCreator)
	if !ok {
		t.Fatalf("%T does not implement GraphBatchCreator", b.repo)
	}
	return c
}

func TestGraphBatchTimestamps_FixedDigitsKeepStringOrder(t *testing.T) {
	// A base on a tenth of a second is where RFC3339Nano would write ".1Z"
	// and then ".100001Z", which sort the wrong way round as strings.
	base := time.Date(2026, 9, 30, 1, 2, 3, 100_000_000, time.UTC)
	got := graphBatchTimestamps(base, 3)
	want := []string{"2026-09-30T01:02:03.100000000Z", "2026-09-30T01:02:03.100001000Z", "2026-09-30T01:02:03.100002000Z"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("graphBatchTimestamps = %v, want %v", got, want)
		}
	}
	for i := 1; i < len(got); i++ {
		if !(got[i-1] < got[i]) {
			t.Fatalf("%q does not sort before %q as a string", got[i-1], got[i])
		}
	}
}

// TestCreateGraphBatch_RowsListInCreationOrder is the plan review's
// carry-over: rows of one batch are an instant apart, and must list back in
// the order they were sent -- nodes by their minted IDs, edges as sent --
// with created_at values of one fixed width that are increasing as strings.
func TestCreateGraphBatch_RowsListInCreationOrder(t *testing.T) {
	eachSQLBackend(t, func(t *testing.T, b backend) {
		ticket := b.newTicket(t)
		const n = 25
		var batch GraphBatch
		for i := 0; i < n; i++ {
			batch.Nodes = append(batch.Nodes, batchNode(fmt.Sprintf("n%02d", i)))
		}
		for i := 1; i < n; i++ {
			batch.Edges = append(batch.Edges, GraphBatchEdge{
				ID:   fmt.Sprintf("edge-%02d", i),
				From: GraphBatchNodeRef{ConfigID: fmt.Sprintf("n%02d", i-1)},
				To:   GraphBatchNodeRef{ConfigID: fmt.Sprintf("n%02d", i)}, Condition: domain.EdgeSuccess,
			})
		}
		if err := b.batcher(t).CreateGraphBatch(ticket.ID, batch); err != nil {
			t.Fatalf("CreateGraphBatch: %v", err)
		}
		nodes, err := b.repo.ListNodesByTicket(ticket.ID)
		if err != nil || len(nodes) != n {
			t.Fatalf("ListNodesByTicket = %d nodes (%v), want %d", len(nodes), err, n)
		}
		for i, node := range nodes {
			if want := fmt.Sprintf("%s-%02d", ticket.ID, i+1); node.ID != want || *node.ConfigID != fmt.Sprintf("n%02d", i) {
				t.Fatalf("node %d = %s (%s), want %s (n%02d)", i, node.ID, *node.ConfigID, want, i)
			}
			if len(node.CreatedAt) != len(nodes[0].CreatedAt) {
				t.Fatalf("node created_at %q and %q differ in width", nodes[0].CreatedAt, node.CreatedAt)
			}
			if i > 0 && !(nodes[i-1].CreatedAt < node.CreatedAt) {
				t.Fatalf("node created_at %q then %q: not increasing as strings", nodes[i-1].CreatedAt, node.CreatedAt)
			}
		}
		edges, err := b.repo.ListEdgesByTicket(ticket.ID)
		if err != nil || len(edges) != n-1 {
			t.Fatalf("ListEdgesByTicket = %d edges (%v), want %d", len(edges), err, n-1)
		}
		for j, e := range edges {
			if want := fmt.Sprintf("edge-%02d", j+1); e.ID != want {
				t.Fatalf("edge %d = %s, want %s", j, e.ID, want)
			}
			if e.FromNodeID != nodes[j].ID || e.ToNodeID != nodes[j+1].ID {
				t.Fatalf("edge %s = %s -> %s, want %s -> %s", e.ID, e.FromNodeID, e.ToNodeID, nodes[j].ID, nodes[j+1].ID)
			}
			if !(nodes[n-1].CreatedAt < e.CreatedAt) || (j > 0 && !(edges[j-1].CreatedAt < e.CreatedAt)) {
				t.Fatalf("edge created_at %q is out of order", e.CreatedAt)
			}
		}
	})
}

func TestCreateGraphBatch_RefusesWhenTheGraphChanged(t *testing.T) {
	eachSQLBackend(t, func(t *testing.T, b backend) {
		ticket := b.newTicket(t)
		c := b.batcher(t)
		seed := GraphBatch{Nodes: []domain.GraphNode{batchNode("plan")}}
		if err := c.CreateGraphBatch(ticket.ID, seed); err != nil {
			t.Fatalf("first seed: %v", err)
		}
		// A second seed planned against "no nodes yet".
		if err := c.CreateGraphBatch(ticket.ID, seed); !errors.Is(err, ErrGraphChanged) {
			t.Fatalf("second seed = %v, want ErrGraphChanged", err)
		}
		stamp := "2026-09-30T00:00:00Z"
		expand := GraphBatch{ExpectedNodeCount: 1, Nodes: []domain.GraphNode{batchNode("impl")}, GraphExpandedAt: &stamp}
		if err := c.CreateGraphBatch(ticket.ID, expand); err != nil {
			t.Fatalf("first expansion: %v", err)
		}
		// Planned against the seed: the count no longer matches.
		if err := c.CreateGraphBatch(ticket.ID, expand); !errors.Is(err, ErrGraphChanged) {
			t.Fatalf("second expansion = %v, want ErrGraphChanged", err)
		}
		// Even with a matching count, graph_expanded_at is already set.
		expand.ExpectedNodeCount = 2
		if err := c.CreateGraphBatch(ticket.ID, expand); !errors.Is(err, ErrGraphChanged) {
			t.Fatalf("expansion with graph_expanded_at already set = %v, want ErrGraphChanged", err)
		}
		nodes, _ := b.repo.ListNodesByTicket(ticket.ID)
		if len(nodes) != 2 {
			t.Fatalf("nodes = %d, want 2", len(nodes))
		}
		got, _ := b.repo.GetTicket(ticket.ID)
		if got.GraphExpandedAt == nil || *got.GraphExpandedAt != stamp {
			t.Fatalf("graph_expanded_at = %v, want %s", got.GraphExpandedAt, stamp)
		}
		if err := c.CreateGraphBatch("NOPE-99999", seed); !isAPIErrorCode(err, domain.ErrCodeTicketNotFound) {
			t.Fatalf("missing ticket = %v, want TICKET_NOT_FOUND", err)
		}
	})
}

// TestCreateGraphBatch_FailureWritesNothing fails a batch after its nodes
// have been inserted (its last edge names a config_id the batch does not
// have) and checks that nothing of it is left: no nodes, no edges, the node
// sequence unmoved and graph_expanded_at unset.
func TestCreateGraphBatch_FailureWritesNothing(t *testing.T) {
	eachSQLBackend(t, func(t *testing.T, b backend) {
		ticket := b.newTicket(t)
		stamp := "2026-09-30T00:00:00Z"
		bad := GraphBatch{
			Nodes: []domain.GraphNode{batchNode("a"), batchNode("b")},
			Edges: []GraphBatchEdge{
				{ID: "edge-ok", From: GraphBatchNodeRef{ConfigID: "a"}, To: GraphBatchNodeRef{ConfigID: "b"}, Condition: domain.EdgeSuccess},
				{ID: "edge-bad", From: GraphBatchNodeRef{ConfigID: "b"}, To: GraphBatchNodeRef{ConfigID: "missing"}, Condition: domain.EdgeSuccess},
			},
			GraphExpandedAt: &stamp,
		}
		err := b.batcher(t).CreateGraphBatch(ticket.ID, bad)
		if !isAPIErrorCode(err, domain.ErrCodeValidation) {
			t.Fatalf("CreateGraphBatch = %v, want VALIDATION_ERROR", err)
		}
		for _, other := range []GraphBatchEdge{
			{ID: "e", From: GraphBatchNodeRef{NodeID: "OTHER-01"}, To: GraphBatchNodeRef{ConfigID: "a"}},
			{ID: "e", From: GraphBatchNodeRef{}, To: GraphBatchNodeRef{ConfigID: "a"}},
			{ID: "", From: GraphBatchNodeRef{ConfigID: "a"}, To: GraphBatchNodeRef{ConfigID: "b"}},
		} {
			bad.Edges = []GraphBatchEdge{other}
			if err := b.batcher(t).CreateGraphBatch(ticket.ID, bad); !isAPIErrorCode(err, domain.ErrCodeValidation) {
				t.Fatalf("edge %+v: CreateGraphBatch = %v, want VALIDATION_ERROR", other, err)
			}
		}
		nodes, _ := b.repo.ListNodesByTicket(ticket.ID)
		edges, _ := b.repo.ListEdgesByTicket(ticket.ID)
		if len(nodes) != 0 || len(edges) != 0 {
			t.Fatalf("a failed batch left %d nodes and %d edges", len(nodes), len(edges))
		}
		got, _ := b.repo.GetTicket(ticket.ID)
		if got.GraphExpandedAt != nil {
			t.Fatalf("a failed batch set graph_expanded_at to %s", *got.GraphExpandedAt)
		}
		// The sequence did not move: the next node is still -01.
		n, err := b.repo.CreateNode(domain.GraphNode{TicketID: ticket.ID, Name: "n", Type: domain.NodeTypePlan, Status: domain.NodeTODO})
		if err != nil || !strings.HasSuffix(n.ID, "-01") {
			t.Fatalf("CreateNode after the failed batch = %s (%v), want ...-01", n.ID, err)
		}
	})
}

func isAPIErrorCode(err error, code domain.ErrorCode) bool {
	var apiErr *domain.APIError
	return errors.As(err, &apiErr) && apiErr.Code == code
}
