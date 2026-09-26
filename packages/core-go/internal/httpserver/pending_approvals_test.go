package httpserver

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00144: GET /api/projects/pending-approvals counts, per project, the
// tickets with a reached approval_gate still at TODO.

const pendingApprovalsPath = "/api/projects/pending-approvals"

// gateNode describes one node of a seeded graph: its type and status, and the
// indexes (into the same slice) of the nodes it depends on.
type gateNode struct {
	typ       domain.NodeType
	status    domain.NodeStatus
	dependsOn []int
}

// seedTicketWithNodes creates a ticket in projectID whose graph is nodes,
// wiring a success edge from every dependsOn entry to the node.
func seedTicketWithNodes(t *testing.T, repo store.GraphRepository, projectID string, nodes []gateNode) domain.Ticket {
	t.Helper()
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Description: "d", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		created, err := repo.CreateNode(domain.GraphNode{
			ID:       "node-" + engine.NewArtifactID(),
			TicketID: ticket.ID,
			Name:     string(n.typ),
			Type:     n.typ,
			Status:   n.status,
		})
		if err != nil {
			t.Fatalf("CreateNode: %v", err)
		}
		ids[i] = created.ID
	}
	for i, n := range nodes {
		for _, dep := range n.dependsOn {
			if _, err := repo.CreateEdge(domain.GraphEdge{
				ID:         "edge-" + engine.NewArtifactID(),
				TicketID:   ticket.ID,
				FromNodeID: ids[dep],
				ToNodeID:   ids[i],
				Condition:  domain.EdgeSuccess,
			}); err != nil {
				t.Fatalf("CreateEdge: %v", err)
			}
		}
	}
	return ticket
}

// reachedGate is a plan (DONE) -> approval_gate (TODO) graph: awaiting approval.
func reachedGate() []gateNode {
	return []gateNode{
		{typ: domain.NodeTypePlan, status: domain.NodeDone},
		{typ: domain.NodeTypeApprovalGate, status: domain.NodeTODO, dependsOn: []int{0}},
	}
}

// decodePendingApprovals reads the response as raw JSON so a null "counts"
// is distinguishable from an empty object.
func decodePendingApprovals(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rec.Code != 200 {
		t.Fatalf("GET %s: status %d (body: %s)", pendingApprovalsPath, rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	counts, ok := body["counts"].(map[string]any)
	if !ok {
		t.Fatalf("counts is %T (%v), want a JSON object", body["counts"], body["counts"])
	}
	return counts
}

func assertCounts(t *testing.T, got map[string]any, want map[string]int) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("counts = %v, want %v", got, want)
		return
	}
	for id, n := range want {
		v, ok := got[id].(float64)
		if !ok || int(v) != n {
			t.Errorf("counts[%s] = %v, want %d (counts: %v)", id, got[id], n, got)
		}
	}
}

func newSecondProject(t *testing.T, repo store.GraphRepository) string {
	t.Helper()
	p, err := repo.CreateProject("Second Project", "SEC")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return p.ID
}

func TestPendingApprovals_CountsTicketsPerProject(t *testing.T) {
	s, repo, projectA := newTestServer(t)
	projectB := newSecondProject(t, repo)
	seedTicketWithNodes(t, repo, projectA, reachedGate())
	seedTicketWithNodes(t, repo, projectA, reachedGate())
	seedTicketWithNodes(t, repo, projectB, reachedGate())

	got := decodePendingApprovals(t, doJSON(t, s, "GET", pendingApprovalsPath, nil))
	assertCounts(t, got, map[string]int{projectA: 2, projectB: 1})
}

func TestPendingApprovals_TwoGatesInOneTicketCountOnce(t *testing.T) {
	s, repo, projectA := newTestServer(t)
	seedTicketWithNodes(t, repo, projectA, []gateNode{
		{typ: domain.NodeTypePlan, status: domain.NodeDone},
		{typ: domain.NodeTypeApprovalGate, status: domain.NodeTODO, dependsOn: []int{0}},
		{typ: domain.NodeTypeApprovalGate, status: domain.NodeTODO, dependsOn: []int{0}},
	})

	got := decodePendingApprovals(t, doJSON(t, s, "GET", pendingApprovalsPath, nil))
	assertCounts(t, got, map[string]int{projectA: 1})
}

func TestPendingApprovals_IgnoresTicketsNotAwaitingApproval(t *testing.T) {
	cases := []struct {
		name  string
		nodes []gateNode
		close bool
	}{
		{
			name: "unreached gate",
			nodes: []gateNode{
				{typ: domain.NodeTypePlan, status: domain.NodeInProgress},
				{typ: domain.NodeTypeApprovalGate, status: domain.NodeTODO, dependsOn: []int{0}},
			},
		},
		{
			name: "rejected gate",
			nodes: []gateNode{
				{typ: domain.NodeTypePlan, status: domain.NodeDone},
				{typ: domain.NodeTypeApprovalGate, status: domain.NodeRejected, dependsOn: []int{0}},
			},
		},
		{
			name: "only a release node is reached",
			nodes: []gateNode{
				{typ: domain.NodeTypePlan, status: domain.NodeDone},
				{typ: domain.NodeTypeRelease, status: domain.NodeTODO, dependsOn: []int{0}},
			},
		},
		{
			name:  "closed ticket with a reached gate",
			nodes: reachedGate(),
			close: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, projectA := newTestServer(t)
			ticket := seedTicketWithNodes(t, repo, projectA, tc.nodes)
			if tc.close {
				rec := doJSON(t, s, "POST", "/api/tickets/"+ticket.ID+"/close", map[string]any{"reason": "withdrawn"})
				if rec.Code != 200 {
					t.Fatalf("close: status %d (body: %s)", rec.Code, rec.Body.String())
				}
			}
			got := decodePendingApprovals(t, doJSON(t, s, "GET", pendingApprovalsPath, nil))
			if _, ok := got[projectA]; ok {
				t.Errorf("counts has project %s: %v, want no key", projectA, got)
			}
		})
	}
}

func TestPendingApprovals_CountsDoneTicketWithReachedGate(t *testing.T) {
	s, repo, projectA := newTestServer(t)
	ticket := seedTicketWithNodes(t, repo, projectA, reachedGate())
	rec := doJSON(t, s, "PATCH", "/api/tickets/"+ticket.ID, map[string]any{"status": "DONE"})
	if rec.Code != 200 {
		t.Fatalf("PATCH status DONE: status %d (body: %s)", rec.Code, rec.Body.String())
	}
	if got, _ := repo.GetTicket(ticket.ID); got == nil || got.Status != domain.TicketDone {
		t.Fatalf("ticket status = %v, want DONE", got)
	}

	got := decodePendingApprovals(t, doJSON(t, s, "GET", pendingApprovalsPath, nil))
	assertCounts(t, got, map[string]int{projectA: 1})
}

func TestPendingApprovals_EmptyObjectWhenNothingAwaits(t *testing.T) {
	s, repo, projectA := newTestServer(t)
	seedTicketWithNodes(t, repo, projectA, []gateNode{{typ: domain.NodeTypePlan, status: domain.NodeTODO}})

	rec := doJSON(t, s, "GET", pendingApprovalsPath, nil)
	got := decodePendingApprovals(t, rec)
	if len(got) != 0 {
		t.Errorf("counts = %v, want {}", got)
	}
}

func TestPendingApprovals_RouteDoesNotShadowProjectRoutes(t *testing.T) {
	s, _, projectA := newTestServer(t)

	rec := doJSON(t, s, "GET", "/api/projects/"+projectA, nil)
	if rec.Code != 200 {
		t.Fatalf("GET /api/projects/{id}: status %d (body: %s)", rec.Code, rec.Body.String())
	}
	var project map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &project); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	if project["id"] != projectA {
		t.Errorf("GET /api/projects/{id} returned id %v, want %s", project["id"], projectA)
	}

	rec = doJSON(t, s, "GET", "/api/projects", nil)
	if rec.Code != 200 {
		t.Fatalf("GET /api/projects: status %d", rec.Code)
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("GET /api/projects is no longer a JSON array: %v (body: %s)", err, rec.Body.String())
	}
	for _, p := range list {
		for _, key := range []string{"counts", "pending_approvals", "pendingApprovals"} {
			if _, ok := p[key]; ok {
				t.Errorf("GET /api/projects item carries %q; the counts belong to the separate endpoint", key)
			}
		}
	}
}

func TestPendingApprovals_FallsBackWhenRepoHasNoBulkRead(t *testing.T) {
	s, repo, projectA := newTestServer(t)
	seedTicketWithNodes(t, repo, projectA, reachedGate())
	seedTicketWithNodes(t, repo, projectA, []gateNode{
		{typ: domain.NodeTypePlan, status: domain.NodeTODO},
		{typ: domain.NodeTypeApprovalGate, status: domain.NodeTODO, dependsOn: []int{0}},
	})
	fallback := &repoWithoutBulkGraphs{GraphRepository: repo}
	s.repo = fallback

	got := decodePendingApprovals(t, doJSON(t, s, "GET", pendingApprovalsPath, nil))
	assertCounts(t, got, map[string]int{projectA: 1})
	if fallback.detailCalls != 2 {
		t.Errorf("GetTicketDetail called %d times, want 2 (one per open ticket)", fallback.detailCalls)
	}
}

// repoFailingListTickets fails the cross-project ticket listing.
type repoFailingListTickets struct {
	store.GraphRepository
}

func (r *repoFailingListTickets) ListTickets() ([]domain.Ticket, error) {
	return nil, errors.New("boom")
}

func TestPendingApprovals_500WhenListingFails(t *testing.T) {
	s, repo, _ := newTestServer(t)
	s.repo = &repoFailingListTickets{GraphRepository: repo}

	rec := doJSON(t, s, "GET", pendingApprovalsPath, nil)
	if rec.Code != 500 {
		t.Fatalf("status %d, want 500 (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != string(domain.ErrCodeInternal) || body.Error.Message == "" {
		t.Errorf("error payload = %+v, want code %s and a message", body.Error, domain.ErrCodeInternal)
	}
}
