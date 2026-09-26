package engine

import (
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// TestHasPendingApproval pins the single Go definition of "awaiting
// approval" (DFLT-00144): a reached (every non-loop prerequisite DONE)
// approval_gate still at TODO. deriveTicketStatus's IN REVIEW and GET
// /api/projects/pending-approvals both rely on it.
func TestHasPendingApproval(t *testing.T) {
	node := func(id string, typ domain.NodeType, status domain.NodeStatus) domain.GraphNode {
		return domain.GraphNode{ID: id, Type: typ, Status: status}
	}
	edge := func(from, to string, cond domain.EdgeCondition) domain.GraphEdge {
		return domain.GraphEdge{FromNodeID: from, ToNodeID: to, Condition: cond}
	}

	cases := []struct {
		name  string
		nodes []domain.GraphNode
		edges []domain.GraphEdge
		want  bool
	}{
		{
			name: "reached TODO approval_gate",
			nodes: []domain.GraphNode{
				node("a", domain.NodeTypePlan, domain.NodeDone),
				node("b", domain.NodeTypeReview, domain.NodeDone),
				node("gate", domain.NodeTypeApprovalGate, domain.NodeTODO),
			},
			edges: []domain.GraphEdge{
				edge("a", "gate", domain.EdgeSuccess),
				edge("b", "gate", domain.EdgeSuccess),
			},
			want: true,
		},
		{
			name: "unreached TODO approval_gate (one prerequisite not DONE)",
			nodes: []domain.GraphNode{
				node("a", domain.NodeTypePlan, domain.NodeDone),
				node("b", domain.NodeTypeReview, domain.NodeInProgress),
				node("gate", domain.NodeTypeApprovalGate, domain.NodeTODO),
			},
			edges: []domain.GraphEdge{
				edge("a", "gate", domain.EdgeSuccess),
				edge("b", "gate", domain.EdgeSuccess),
			},
			want: false,
		},
		{
			name: "reached REJECTED approval_gate",
			nodes: []domain.GraphNode{
				node("a", domain.NodeTypePlan, domain.NodeDone),
				node("gate", domain.NodeTypeApprovalGate, domain.NodeRejected),
			},
			edges: []domain.GraphEdge{edge("a", "gate", domain.EdgeSuccess)},
			want:  false,
		},
		{
			name: "reached DONE approval_gate",
			nodes: []domain.GraphNode{
				node("a", domain.NodeTypePlan, domain.NodeDone),
				node("gate", domain.NodeTypeApprovalGate, domain.NodeDone),
			},
			edges: []domain.GraphEdge{edge("a", "gate", domain.EdgeSuccess)},
			want:  false,
		},
		{
			name: "only a release node is reached and TODO; the approval_gate is unreached",
			nodes: []domain.GraphNode{
				node("a", domain.NodeTypePlan, domain.NodeDone),
				node("rel", domain.NodeTypeRelease, domain.NodeTODO),
				node("gate", domain.NodeTypeApprovalGate, domain.NodeTODO),
			},
			edges: []domain.GraphEdge{
				edge("a", "rel", domain.EdgeSuccess),
				edge("rel", "gate", domain.EdgeSuccess),
			},
			want: false,
		},
		{
			name: "the only unfinished prerequisite comes through an iteration_loop edge",
			nodes: []domain.GraphNode{
				node("a", domain.NodeTypePlan, domain.NodeDone),
				node("later", domain.NodeTypeReview, domain.NodeTODO),
				node("gate", domain.NodeTypeApprovalGate, domain.NodeTODO),
			},
			edges: []domain.GraphEdge{
				edge("a", "gate", domain.EdgeSuccess),
				edge("later", "gate", domain.EdgeLoop),
			},
			want: true,
		},
		{
			name: "the gate's prerequisite node is missing from the graph",
			nodes: []domain.GraphNode{
				node("gate", domain.NodeTypeApprovalGate, domain.NodeTODO),
			},
			edges: []domain.GraphEdge{edge("ghost", "gate", domain.EdgeSuccess)},
			want:  false,
		},
		{
			name: "no nodes at all",
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasPendingApproval(tc.nodes, tc.edges); got != tc.want {
				t.Errorf("HasPendingApproval = %v, want %v", got, tc.want)
			}
		})
	}
}
