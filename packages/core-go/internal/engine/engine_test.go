package engine

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// newTestEngine returns a ready-to-use engine/repo pair plus a project ID
// every test can pass to CreateTicket -- CreateTicket now requires one (see
// store.GraphRepository.CreateTicket), even though none of these tests care
// about project scoping itself.
func newTestEngine(t *testing.T) (*GraphEngine, store.GraphRepository, string) {
	t.Helper()
	repo, err := store.NewSQLiteRepository(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	if err := repo.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	proj, err := repo.CreateProject("Test Project", "TEST")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return New(repo), repo, proj.ID
}

// TestAllNonLoopPrereqsDone exercises allNonLoopPrereqsDone directly (no DB
// needed) against a small fixed node/edge set: A -> B (success), C -> B
// (iteration_loop). B is "reached" only once A is DONE -- C's status never
// matters, since its edge is a loop-back, not a forward prerequisite.
func TestAllNonLoopPrereqsDone(t *testing.T) {
	byID := map[string]domain.GraphNode{
		"a": {ID: "a", Status: domain.NodeTODO},
		"b": {ID: "b", Status: domain.NodeTODO},
		"c": {ID: "c", Status: domain.NodeTODO},
	}
	edges := []domain.GraphEdge{
		{FromNodeID: "a", ToNodeID: "b", Condition: domain.EdgeSuccess},
		{FromNodeID: "c", ToNodeID: "b", Condition: domain.EdgeLoop},
	}

	if allNonLoopPrereqsDone("b", byID, edges) {
		t.Errorf("expected b to be unreached while a is not DONE")
	}

	byID["a"] = domain.GraphNode{ID: "a", Status: domain.NodeDone}
	if !allNonLoopPrereqsDone("b", byID, edges) {
		t.Errorf("expected b to be reached once a is DONE, regardless of c's status")
	}

	// A node with no incoming edges at all (e.g. a seed node) is vacuously
	// reached.
	if !allNonLoopPrereqsDone("a", byID, edges) {
		t.Errorf("expected a node with no incoming edges to be reached")
	}
}

func TestCreateTicket_BuildsNoGraph(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticket, err := e.CreateTicket(projectID, "title", "desc")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if ticket.Status != domain.TicketTODO {
		t.Errorf("expected status TODO, got %s", ticket.Status)
	}
	nodes, err := repo.ListNodesByTicket(ticket.ID)
	if err != nil {
		t.Fatalf("ListNodesByTicket: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected no nodes at creation, got %d", len(nodes))
	}
}

func TestRefineTicket_ReplacesDescriptionAndBuildsNoGraph(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticket, _ := e.CreateTicket(projectID, "title", "original description")

	updated, err := e.RefineTicket(ticket.ID, "Completion criteria: X works. Why: because of Y.", NoPriorityChange())
	if err != nil {
		t.Fatalf("RefineTicket: %v", err)
	}
	if updated.Status != domain.TicketRefined {
		t.Errorf("expected status REFINED, got %s", updated.Status)
	}
	if contains(updated.Description, "original description") {
		t.Errorf("expected the refined description to replace the original text, got %q", updated.Description)
	}
	if !contains(updated.Description, "Completion criteria") {
		t.Errorf("expected the refined description to contain the new text, got %q", updated.Description)
	}

	nodes, _ := repo.ListNodesByTicket(ticket.ID)
	if len(nodes) != 0 {
		t.Errorf("refine must not create any graph nodes, got %d", len(nodes))
	}
}

func TestRefineTicket_EmptyDescriptionLeavesExistingTextUntouched(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	ticket, _ := e.CreateTicket(projectID, "title", "original description")

	updated, err := e.RefineTicket(ticket.ID, "", NoPriorityChange())
	if err != nil {
		t.Fatalf("RefineTicket: %v", err)
	}
	if updated.Status != domain.TicketRefined {
		t.Errorf("expected status REFINED, got %s", updated.Status)
	}
	if updated.Description != "original description" {
		t.Errorf("expected the original description to be left untouched, got %q", updated.Description)
	}
}

func TestGetExecutableNodes_SeedsGraphOnFirstCall(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	exec, err := e.GetExecutableNodes(ticket.ID, cat)
	if err != nil {
		t.Fatalf("GetExecutableNodes: %v", err)
	}
	if len(exec) != 1 || exec[0].Type != domain.NodeTypePlan {
		t.Fatalf("expected exactly the plan node to be executable, got %+v", exec)
	}

	nodes, _ := repo.ListNodesByTicket(ticket.ID)
	if len(nodes) != 2 {
		t.Fatalf("expected exactly the 2 seed nodes (plan, plan_review), got %d", len(nodes))
	}
	for _, n := range nodes {
		if n.ConfigID == nil || *n.ConfigID == "" {
			t.Errorf("seed node %+v missing config_id", n)
		}
	}
}

func TestGetExecutableNodes_SeedingIsIdempotent(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	e.GetExecutableNodes(ticket.ID, cat)
	e.GetExecutableNodes(ticket.ID, cat)
	e.GetExecutableNodes(ticket.ID, cat)

	nodes, _ := repo.ListNodesByTicket(ticket.ID)
	if len(nodes) != 2 {
		t.Fatalf("expected seeding to happen exactly once, got %d nodes", len(nodes))
	}
}

func TestCompleteNode_DoesNotAutoExpand(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	planNode := exec[0]
	res, err := e.CompleteNode(planNode.ID, true, nil)
	if err != nil {
		t.Fatalf("CompleteNode(plan): %v", err)
	}
	if res.NextStatus != "DONE" {
		t.Fatalf("unexpected result completing plan: %+v", res)
	}

	nodes, _ := repo.ListNodesByTicket(ticket.ID)
	if len(nodes) != 2 {
		t.Fatalf("graph should not expand until plan_review also passes, got %d nodes", len(nodes))
	}

	exec, err = e.GetExecutableNodes(ticket.ID, cat)
	if err != nil {
		t.Fatalf("GetExecutableNodes after plan done: %v", err)
	}
	if len(exec) != 1 || exec[0].Type != domain.NodeTypeReview {
		t.Fatalf("expected plan_review to become executable, got %+v", exec)
	}
	planReview := exec[0]

	res, err = e.CompleteNode(planReview.ID, true, nil)
	if err != nil {
		t.Fatalf("CompleteNode(plan_review): %v", err)
	}
	if res.NextStatus != "DONE" {
		t.Fatalf("unexpected result completing plan_review: %+v", res)
	}

	// This is the behavior change this test guards: completing plan_review
	// must NOT auto-expand the graph anymore. Expansion is now an explicit,
	// skill-driven ExpandGraph call.
	nodes, _ = repo.ListNodesByTicket(ticket.ID)
	if len(nodes) != 2 {
		t.Fatalf("CompleteNode must no longer auto-expand the graph, got %d nodes", len(nodes))
	}
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	if len(exec) != 0 {
		t.Fatalf("expected no executable nodes until ExpandGraph is called, got %+v", exec)
	}
}

func TestCompleteNode_FailingSeedNodeLoopsBack(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	planNode := exec[0]
	e.CompleteNode(planNode.ID, true, nil) // plan -> DONE

	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	planReview := exec[0]

	res, err := e.CompleteNode(planReview.ID, false, nil)
	if err != nil {
		t.Fatalf("CompleteNode(plan_review, fail): %v", err)
	}
	if res.NextStatus != "AWAITING FIX" || !res.LoopedBack {
		t.Fatalf("expected loop-back to plan, got %+v", res)
	}

	nodes, _ := repo.ListNodesByTicket(ticket.ID)
	if len(nodes) != 2 {
		t.Fatalf("a failed review must not expand the graph, got %d nodes", len(nodes))
	}
	if got := nodeByConfigID(t, repo, ticket.ID, "plan_review"); got.Status != domain.NodeAwaitingFix {
		t.Errorf("expected plan_review to be AWAITING FIX after looping back, got %s", got.Status)
	}
	if got := nodeByConfigID(t, repo, ticket.ID, "plan"); got.Status != domain.NodeTODO || got.IterationCount != 1 {
		t.Errorf("expected plan to be TODO with iteration_count 1, got %s / %d", got.Status, got.IterationCount)
	}
}

func TestCompleteNode_BlocksAfterExceedingMaxIterations(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	e.CompleteNode(exec[0].ID, true, nil) // plan -> DONE

	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	planReview := exec[0]

	// plan_review loops back to plan (max_iterations=3): 3 failures loop,
	// the 4th (iteration_count 3->4 > 3) blocks the ticket.
	//
	// Each retry is driven back through get-executable the way a real run
	// is. A loop-back leaves plan at TODO and plan_review at AWAITING FIX,
	// and since DFLT-00102 neither can be completed again until it has been
	// claimed -- CompleteNode refuses a node nobody handed out.
	var res CompleteNodeResult
	for i := 0; i < 4; i++ {
		var err error
		res, err = e.CompleteNode(planReview.ID, false, nil)
		if err != nil {
			t.Fatalf("failing plan_review (attempt %d): %v", i+1, err)
		}
		if i == 3 {
			break
		}
		exec, _ = e.GetExecutableNodes(ticket.ID, cat) // plan, back at TODO
		if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil {
			t.Fatalf("redoing plan (attempt %d): %v", i+1, err)
		}
		exec, _ = e.GetExecutableNodes(ticket.ID, cat) // plan_review, reclaimed
		planReview = exec[0]
	}
	if res.NextStatus != "BLOCKED" {
		t.Fatalf("expected BLOCKED after exceeding max_iterations, got %+v", res)
	}

	got, _ := repo.GetTicket(ticket.ID)
	if !got.Blocked {
		t.Errorf("expected ticket.Blocked=true, got %+v", got)
	}
}

func TestExpandGraph_NoPatchFallsBackToDefaultFullTemplate(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")
	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	e.CompleteNode(exec[0].ID, true, nil) // plan
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	e.CompleteNode(exec[0].ID, true, nil) // plan_review

	if err := e.ExpandGraph(ticket.ID, cat, nil); err != nil {
		t.Fatalf("ExpandGraph: %v", err)
	}

	nodes, _ := repo.ListNodesByTicket(ticket.ID)
	if len(nodes) != 16 {
		t.Fatalf("expected the default full template (16 nodes, including plan_approval/release_approval -- DFLT-00016), got %d", len(nodes))
	}
	// plan_approval (an approval_gate) sits between plan_review and
	// gherkin_spec now, so nothing is auto-executable until a human approves
	// it -- see the approval-then-executable test below for that step.
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	if len(exec) != 0 {
		t.Fatalf("expected no executable nodes until plan_approval is approved, got %+v", exec)
	}
	planApproval := nodeByConfigID(t, repo, ticket.ID, "plan_approval")
	if planApproval.Type != domain.NodeTypeApprovalGate || !planApproval.IsManual {
		t.Fatalf("expected plan_approval to be a manual approval_gate node, got %+v", planApproval)
	}
	if _, err := e.CompleteNode(planApproval.ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(plan_approval): %v", err)
	}
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	if len(exec) != 1 || exec[0].Type != domain.NodeTypeGherkinSpec {
		t.Fatalf("expected gherkin_spec next once plan_approval is approved, got %+v", exec)
	}
}

func TestExpandGraph_InvestigationOnlyPatch(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "Investigate and summarize passkeys", "")
	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	e.CompleteNode(exec[0].ID, true, nil) // plan
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	e.CompleteNode(exec[0].ID, true, nil) // plan_review

	patch := &Patch{ExtraNodes: []ExtraNode{
		{ID: "investigation", Type: "investigation", Name: "Conduct investigation", DependsOn: []string{"plan_review"}},
		{ID: "investigation_review", Type: "review_gate", GateRef: "investigation_review",
			DependsOn: []string{"investigation"}, LoopBackTo: "investigation"},
		{ID: "release", Type: "release", Name: "Release approval & deployment (manual)", IsManual: true,
			DependsOn: []string{"investigation_review"}},
	}}
	if err := e.ExpandGraph(ticket.ID, cat, patch); err != nil {
		t.Fatalf("ExpandGraph with investigation patch: %v", err)
	}

	nodes, _ := repo.ListNodesByTicket(ticket.ID)
	// 2 seed + 3 patch nodes = 5. Crucially, none of the catalog's default
	// implementation/Gherkin nodes should appear.
	if len(nodes) != 5 {
		t.Fatalf("expected exactly 5 nodes (seed + investigation cluster), got %d: %+v", len(nodes), nodes)
	}
	for _, n := range nodes {
		if n.Type == domain.NodeTypeImplementation || n.Type == domain.NodeTypeGherkinSpec || n.Type == domain.NodeTypeGherkinTest {
			t.Errorf("investigation-only expansion must not include implementation/Gherkin nodes, found %+v", n)
		}
	}

	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	if len(exec) != 1 || exec[0].Type != domain.NodeTypeInvestigation {
		t.Fatalf("expected the investigation node to be next, got %+v", exec)
	}
}

func TestExpandGraph_RejectsBeforeSeedExists(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	if err := e.ExpandGraph(ticket.ID, cat, nil); err == nil {
		t.Fatal("expected an error calling ExpandGraph before the seed exists")
	}
}

func TestExpandGraph_RejectsBeforeSeedDone(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")
	e.GetExecutableNodes(ticket.ID, cat) // seeds plan+plan_review, neither done yet

	if err := e.ExpandGraph(ticket.ID, cat, nil); err == nil {
		t.Fatal("expected an error calling ExpandGraph before the seed is DONE")
	}
}

func TestExpandGraph_RejectsDoubleExpansion(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")
	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	e.CompleteNode(exec[0].ID, true, nil)
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	e.CompleteNode(exec[0].ID, true, nil)

	if err := e.ExpandGraph(ticket.ID, cat, nil); err != nil {
		t.Fatalf("first ExpandGraph: %v", err)
	}
	if err := e.ExpandGraph(ticket.ID, cat, nil); err == nil {
		t.Fatal("expected an error on a second ExpandGraph call")
	}
}

// investigationGraph builds a ticket through the seed (plan/plan_review) and
// expands it with the same small investigation-only patch
// TestExpandGraph_InvestigationOnlyPatch uses: plan (plan) -> plan_review
// (review) -> investigation (investigation) -> investigation_review
// (review_gate) -> release (release, is_manual). It's used below to exercise
// GetExecutableNodes/syncTicketStatus against a non-review node, a `review`
// node, a `review_gate` node, and a manual node all in one small graph.
func investigationGraph(t *testing.T, e *GraphEngine, projectID string) (ticketID string, cat config.Catalog) {
	t.Helper()
	cat = baseCatalog(t)
	ticket, err := e.CreateTicket(projectID, "Investigate and summarize passkeys", "")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil { // plan
		t.Fatalf("CompleteNode(plan): %v", err)
	}
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil { // plan_review
		t.Fatalf("CompleteNode(plan_review): %v", err)
	}

	patch := &Patch{ExtraNodes: []ExtraNode{
		{ID: "investigation", Type: "investigation", Name: "Conduct investigation", DependsOn: []string{"plan_review"}},
		{ID: "investigation_review", Type: "review_gate", GateRef: "investigation_review",
			DependsOn: []string{"investigation"}, LoopBackTo: "investigation"},
		{ID: "release", Type: "release", Name: "Release approval & deployment (manual)", IsManual: true,
			DependsOn: []string{"investigation_review"}},
	}}
	if err := e.ExpandGraph(ticket.ID, cat, patch); err != nil {
		t.Fatalf("ExpandGraph: %v", err)
	}
	return ticket.ID, cat
}

func nodeByConfigID(t *testing.T, repo store.GraphRepository, ticketID, configID string) domain.GraphNode {
	t.Helper()
	nodes, err := repo.ListNodesByTicket(ticketID)
	if err != nil {
		t.Fatalf("ListNodesByTicket: %v", err)
	}
	for _, n := range nodes {
		if n.ConfigID != nil && *n.ConfigID == configID {
			return n
		}
	}
	t.Fatalf("no node with config_id %q found in %+v", configID, nodes)
	return domain.GraphNode{}
}

// --- DFLT-00012: approval_gate is a human-only gate, distinct from
// review_gate's automated pass/fail judgment. Approving must behave like any
// other passing node; rejecting must always block the whole ticket, even if
// (contrary to how a workflow author should define it) an iteration_loop
// edge exists out of the node. ---

// approvalGateGraph builds a ticket through the seed (plan/plan_review) and
// expands it with an approval_gate node (deliberately NOT setting is_manual
// in the patch, to exercise dag.go's forced IsManual=true) followed by a
// plain node so there is something to observe becoming executable once the
// gate is approved. withLoopBack additionally wires an iteration_loop edge
// from the gate back to "plan", to exercise that a reject still blocks
// rather than looping.
func approvalGateGraph(t *testing.T, e *GraphEngine, projectID string, withLoopBack bool) (ticketID string, cat config.Catalog) {
	t.Helper()
	cat = baseCatalog(t)
	ticket, err := e.CreateTicket(projectID, "Approval gate test", "")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil { // plan
		t.Fatalf("CompleteNode(plan): %v", err)
	}
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil { // plan_review
		t.Fatalf("CompleteNode(plan_review): %v", err)
	}

	gateNode := ExtraNode{ID: "approval", Type: "approval_gate", Name: "Approval", DependsOn: []string{"plan_review"}}
	if withLoopBack {
		gateNode.LoopBackTo = "plan"
	}
	patch := &Patch{ExtraNodes: []ExtraNode{
		gateNode,
		{ID: "after_approval", Type: "investigation", Name: "Post-approval work", DependsOn: []string{"approval"}},
	}}
	if err := e.ExpandGraph(ticket.ID, cat, patch); err != nil {
		t.Fatalf("ExpandGraph with approval_gate patch: %v", err)
	}
	return ticket.ID, cat
}

func TestExpandGraph_ApprovalGateForcesIsManualEvenWhenPatchOmitsIt(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _ := approvalGateGraph(t, e, projectID, false)

	gate := nodeByConfigID(t, repo, ticketID, "approval")
	if gate.Type != domain.NodeTypeApprovalGate {
		t.Fatalf("expected type approval_gate, got %s", gate.Type)
	}
	if !gate.IsManual {
		t.Errorf("expected approval_gate node to be persisted with is_manual=true even though the patch never set it, got %+v", gate)
	}
}

func TestGetExecutableNodes_ApprovalGateNeverAutoClaimed(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	ticketID, cat := approvalGateGraph(t, e, projectID, false)

	exec, err := e.GetExecutableNodes(ticketID, cat)
	if err != nil {
		t.Fatalf("GetExecutableNodes: %v", err)
	}
	if len(exec) != 0 {
		t.Fatalf("approval_gate must never be auto-claimed and nothing follows it until it is, got %+v", exec)
	}
}

func TestCompleteNode_ApprovalGateApprovalAdvancesGraphWithoutBlocking(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, cat := approvalGateGraph(t, e, projectID, false)
	gate := nodeByConfigID(t, repo, ticketID, "approval")

	res, err := e.CompleteNode(gate.ID, true, nil)
	if err != nil {
		t.Fatalf("CompleteNode(approval, passed): %v", err)
	}
	if res.NextStatus != "DONE" || res.LoopedBack {
		t.Fatalf("expected {DONE false}, got %+v", res)
	}

	got, _ := repo.GetTicket(ticketID)
	if got.Blocked {
		t.Errorf("approving an approval_gate must not block the ticket, got %+v", got)
	}

	exec, err := e.GetExecutableNodes(ticketID, cat)
	if err != nil {
		t.Fatalf("GetExecutableNodes after approval: %v", err)
	}
	if len(exec) != 1 || exec[0].Type != domain.NodeTypeInvestigation {
		t.Fatalf("expected the successor node to become executable after approval, got %+v", exec)
	}
}

func TestCompleteNode_ApprovalGateRejectionMarksNodeRejectedAndBlocksTicket(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, cat := approvalGateGraph(t, e, projectID, false)
	gate := nodeByConfigID(t, repo, ticketID, "approval")

	reason := "rejection_reason"
	res, err := e.CompleteNode(gate.ID, false, []domain.Artifact{
		{Name: reason, Type: domain.ArtifactText, Content: strPtr("does not match requirements")},
	})
	if err != nil {
		t.Fatalf("CompleteNode(approval, rejected): %v", err)
	}
	// DFLT-00016: rejecting an approval_gate now reports NextStatus
	// "REJECTED" (previously "BLOCKED") and marks the node itself REJECTED
	// rather than leaving it at TODO, so a never-judged gate and a
	// rejected-and-awaiting-triage gate are distinguishable.
	if res.NextStatus != "REJECTED" || res.LoopedBack {
		t.Fatalf("expected {REJECTED false}, got %+v", res)
	}

	gateAfter := nodeByConfigID(t, repo, ticketID, "approval")
	if gateAfter.Status != domain.NodeRejected {
		t.Errorf("expected the approval_gate node itself to become REJECTED, got %+v", gateAfter)
	}

	got, _ := repo.GetTicket(ticketID)
	if !got.Blocked {
		t.Errorf("expected ticket.Blocked=true after rejecting an approval_gate, got %+v", got)
	}

	arts, err := repo.ListArtifactsByNode(gate.ID)
	if err != nil {
		t.Fatalf("ListArtifactsByNode: %v", err)
	}
	var foundReason bool
	for _, a := range arts {
		if a.Name == reason && a.Content != nil && *a.Content == "does not match requirements" {
			foundReason = true
		}
	}
	if !foundReason {
		t.Errorf("expected a %q artifact to be persisted on the rejected gate, got %+v", reason, arts)
	}

	exec, err := e.GetExecutableNodes(ticketID, cat)
	if err != nil {
		t.Fatalf("GetExecutableNodes after rejection: %v", err)
	}
	if len(exec) != 0 {
		t.Fatalf("expected no executable nodes once the ticket is blocked, got %+v", exec)
	}
}

func TestCompleteNode_ApprovalGateRejectionIgnoresLoopBackEdge(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _ := approvalGateGraph(t, e, projectID, true) // approval -> loop_back_to plan
	gate := nodeByConfigID(t, repo, ticketID, "approval")
	planBefore := nodeByConfigID(t, repo, ticketID, "plan")

	res, err := e.CompleteNode(gate.ID, false, nil)
	if err != nil {
		t.Fatalf("CompleteNode(approval, rejected): %v", err)
	}
	if res.NextStatus != "REJECTED" || res.LoopedBack {
		t.Fatalf("expected rejection to block rather than loop back even though a loop_back_to edge exists, got %+v", res)
	}

	got, _ := repo.GetTicket(ticketID)
	if !got.Blocked {
		t.Errorf("expected ticket.Blocked=true, got %+v", got)
	}

	planAfter := nodeByConfigID(t, repo, ticketID, "plan")
	if planAfter.IterationCount != planBefore.IterationCount || planAfter.Status != domain.NodeDone {
		t.Errorf("plan (the loop_back_to target) must be untouched by the rejection, before=%+v after=%+v", planBefore, planAfter)
	}
}

// strPtr is a tiny helper for building domain.Artifact.Content (a *string)
// inline in table-driven/literal test data.
func strPtr(s string) *string { return &s }

// --- DFLT-00016: ReopenNodes is the mechanical primitive behind rejection
// triage -- process-ticket decides which nodes a rejection reason
// implicates, ReopenNodes only resets them (plus their forward closure) and
// unblocks the ticket. ---

func TestReopenNodes_ResetsRootAndForwardClosureAndUnblocks(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	// approvalGateGraph: plan (DONE) -> plan_review (DONE) -> approval
	// (approval_gate) -> after_approval (investigation, never claimed).
	ticketID, _ := approvalGateGraph(t, e, projectID, false)
	gate := nodeByConfigID(t, repo, ticketID, "approval")
	if _, err := e.CompleteNode(gate.ID, false, nil); err != nil {
		t.Fatalf("CompleteNode(approval, rejected): %v", err)
	}

	plan := nodeByConfigID(t, repo, ticketID, "plan")
	updated, err := e.ReopenNodes(ticketID, []string{plan.ID})
	if err != nil {
		t.Fatalf("ReopenNodes: %v", err)
	}
	if updated.Blocked {
		t.Errorf("expected ticket.Blocked=false after ReopenNodes, got %+v", updated.Ticket)
	}

	byConfigID := make(map[string]domain.GraphNode, len(updated.Nodes))
	for _, n := range updated.Nodes {
		if n.ConfigID != nil {
			byConfigID[*n.ConfigID] = n
		}
	}

	for _, configID := range []string{"plan", "plan_review", "approval"} {
		n, ok := byConfigID[configID]
		if !ok {
			t.Fatalf("node %q missing from reopened ticket", configID)
		}
		if n.Status != domain.NodeTODO {
			t.Errorf("expected %q to be reset to TODO, got %+v", configID, n)
		}
		if n.IterationCount != 1 {
			t.Errorf("expected %q iteration_count to be bumped to 1, got %+v", configID, n)
		}
	}

	// after_approval was never claimed (TODO, iteration_count 0) when the
	// gate was rejected, so it has nothing stale to reset and must be left
	// untouched by the forward closure.
	after := byConfigID["after_approval"]
	if after.IterationCount != 0 {
		t.Errorf("expected after_approval (never DONE) to be untouched by the forward closure, got %+v", after)
	}
}

func TestReopenNodes_MultipleRootsAreUnioned(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _ := approvalGateGraph(t, e, projectID, false)
	gate := nodeByConfigID(t, repo, ticketID, "approval")
	if _, err := e.CompleteNode(gate.ID, false, nil); err != nil {
		t.Fatalf("CompleteNode(approval, rejected): %v", err)
	}

	plan := nodeByConfigID(t, repo, ticketID, "plan")
	planReview := nodeByConfigID(t, repo, ticketID, "plan_review")
	// Naming both "plan" and "plan_review" as roots must not double-count
	// plan_review (it would otherwise also be reached via forward closure
	// from "plan") or error.
	updated, err := e.ReopenNodes(ticketID, []string{plan.ID, planReview.ID})
	if err != nil {
		t.Fatalf("ReopenNodes: %v", err)
	}
	if updated.Blocked {
		t.Errorf("expected ticket.Blocked=false, got %+v", updated.Ticket)
	}
	for _, n := range updated.Nodes {
		if n.ConfigID != nil && (*n.ConfigID == "plan" || *n.ConfigID == "plan_review" || *n.ConfigID == "approval") {
			if n.Status != domain.NodeTODO || n.IterationCount != 1 {
				t.Errorf("expected %q to be reset once (TODO, iteration_count=1), got %+v", *n.ConfigID, n)
			}
		}
	}
}

func TestReopenNodes_ExceedingMaxIterationsWritesNothing(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _ := approvalGateGraph(t, e, projectID, false)
	gate := nodeByConfigID(t, repo, ticketID, "approval")
	if _, err := e.CompleteNode(gate.ID, false, nil); err != nil {
		t.Fatalf("CompleteNode(approval, rejected): %v", err)
	}

	plan := nodeByConfigID(t, repo, ticketID, "plan")
	maxed := plan.MaxIterations
	if _, err := repo.UpdateNode(plan.ID, store.NodePatch{IterationCount: &maxed}); err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}

	if _, err := e.ReopenNodes(ticketID, []string{plan.ID}); err == nil {
		t.Fatal("expected an error when reopening would exceed max_iterations")
	}

	// Nothing must have been written: plan stays at its maxed-out iteration
	// count and TODO/DONE status is untouched, plan_review/approval are
	// untouched, and the ticket is still blocked (no partial application).
	planAfter := nodeByConfigID(t, repo, ticketID, "plan")
	if planAfter.IterationCount != maxed || planAfter.Status != domain.NodeDone {
		t.Errorf("expected plan to be untouched by the failed reopen, got %+v", planAfter)
	}
	gateAfter := nodeByConfigID(t, repo, ticketID, "approval")
	if gateAfter.Status != domain.NodeRejected {
		t.Errorf("expected approval gate to remain REJECTED after the failed reopen, got %+v", gateAfter)
	}
	got, _ := repo.GetTicket(ticketID)
	if !got.Blocked {
		t.Errorf("expected ticket to remain blocked after the failed reopen, got %+v", got)
	}
}

func TestReopenNodes_RejectsWhenTicketNotBlocked(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _ := approvalGateGraph(t, e, projectID, false)
	gate := nodeByConfigID(t, repo, ticketID, "approval")
	// Approve (not reject) the gate: the ticket is never blocked.
	if _, err := e.CompleteNode(gate.ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(approval, approved): %v", err)
	}
	plan := nodeByConfigID(t, repo, ticketID, "plan")

	if _, err := e.ReopenNodes(ticketID, []string{plan.ID}); err == nil {
		t.Fatal("expected an error reopening nodes on a ticket that is not blocked")
	}
}

func TestReopenNodes_RejectsNodeNotBelongingToTicket(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _ := approvalGateGraph(t, e, projectID, false)
	gate := nodeByConfigID(t, repo, ticketID, "approval")
	if _, err := e.CompleteNode(gate.ID, false, nil); err != nil {
		t.Fatalf("CompleteNode(approval, rejected): %v", err)
	}

	otherTicketID, _ := approvalGateGraph(t, e, projectID, false)
	otherPlan := nodeByConfigID(t, repo, otherTicketID, "plan")

	if _, err := e.ReopenNodes(ticketID, []string{otherPlan.ID}); err == nil {
		t.Fatal("expected an error reopening a node id that belongs to a different ticket")
	}
}

// TestReopenNodes_RejectsNodeBeingWorked pins the one status range
// reopen-nodes still refuses. DFLT-00119 widened it to accept TODO and
// AWAITING FIX (a blocked loop target is usually at TODO, and refusing it left
// such a ticket unrecoverable from the CLI), but a node something may still be
// working is left to unstick-node: deciding a claim is stale is a judgment
// call that has to be asked for explicitly.
func TestReopenNodes_RejectsNodeBeingWorked(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _ := approvalGateGraph(t, e, projectID, false)
	gate := nodeByConfigID(t, repo, ticketID, "approval")
	if _, err := e.CompleteNode(gate.ID, false, nil); err != nil {
		t.Fatalf("CompleteNode(approval, rejected): %v", err)
	}
	after := nodeByConfigID(t, repo, ticketID, "after_approval")
	setNodeStatus(t, repo, after.ID, domain.NodeInProgress)

	_, err := e.ReopenNodes(ticketID, []string{after.ID})
	if err == nil {
		t.Fatal("expected an error reopening a node that is IN PROGRESS")
	}
	if !strings.Contains(err.Error(), "unstick-node") {
		t.Errorf("expected the refusal to point at unstick-node, got %v", err)
	}

	// The same node at TODO is accepted, and costs no iteration.
	setNodeStatus(t, repo, after.ID, domain.NodeTODO)
	if _, err := e.ReopenNodes(ticketID, []string{after.ID}); err != nil {
		t.Fatalf("expected a TODO node to be reopenable: %v", err)
	}
	got, _ := repo.GetNode(after.ID)
	if got.Status != domain.NodeTODO || got.IterationCount != 0 {
		t.Errorf("expected the TODO node to stay TODO at iteration_count 0, got %s / %d", got.Status, got.IterationCount)
	}
}

// --- UnstickNode: recovers a node GetExecutableNodes claimed (IN
// PROGRESS/IN REVIEW) but that no worker ever actually completed, a state
// ReopenNodes cannot reach because the ticket is never Blocked in this
// scenario (see skiplevel_repro_test.go's
// TestStaleClaim_UnreachableViaGetExecutableNodes for the full field
// scenario this recovers from). ---

func TestUnstickNode_ResetsClaimedNodeToTODOWithoutTouchingIteration(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	exec, _ := e.GetExecutableNodes(ticket.ID, cat) // claims plan
	plan := exec[0]
	if plan.Status != domain.NodeInProgress {
		t.Fatalf("expected plan to be claimed (IN PROGRESS), got %s", plan.Status)
	}

	unstuck, err := e.UnstickNode(plan.ID)
	if err != nil {
		t.Fatalf("UnstickNode: %v", err)
	}
	if unstuck.Status != domain.NodeTODO {
		t.Fatalf("expected TODO after UnstickNode, got %s", unstuck.Status)
	}
	if unstuck.IterationCount != 0 {
		t.Fatalf("UnstickNode must not bump iteration_count, got %d", unstuck.IterationCount)
	}

	// The node is claimable again.
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	if len(exec) != 1 || exec[0].ID != plan.ID {
		t.Fatalf("expected plan to be re-offered after UnstickNode, got %+v", exec)
	}
}

func TestUnstickNode_RejectsNodeNotFound(t *testing.T) {
	e, _, _ := newTestEngine(t)
	if _, err := e.UnstickNode("does-not-exist"); err == nil {
		t.Fatal("expected an error unsticking a node id that does not exist")
	}
}

func TestUnstickNode_RejectsNodeNotClaimed(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")
	e.GetExecutableNodes(ticket.ID, cat) // seeds plan+plan_review, plan claimed
	plan := nodeByConfigID(t, repo, ticket.ID, "plan")
	planReview := nodeByConfigID(t, repo, ticket.ID, "plan_review")

	if _, err := e.UnstickNode(planReview.ID); err == nil {
		t.Fatal("expected an error unsticking a node that is TODO (never claimed)")
	}

	if _, err := e.CompleteNode(plan.ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(plan): %v", err)
	}
	if _, err := e.UnstickNode(plan.ID); err == nil {
		t.Fatal("expected an error unsticking a node that is already DONE")
	}
}

// --- DFLT-00009: nodes must move through IN PROGRESS/IN REVIEW, not just
// TODO -> DONE. ---

func TestGetExecutableNodes_ClaimsNonReviewNodeAsInProgress(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	exec, err := e.GetExecutableNodes(ticket.ID, cat)
	if err != nil {
		t.Fatalf("GetExecutableNodes: %v", err)
	}
	if len(exec) != 1 || exec[0].Type != domain.NodeTypePlan {
		t.Fatalf("expected exactly the plan node, got %+v", exec)
	}
	if exec[0].Status != domain.NodeInProgress {
		t.Errorf("expected the returned plan node's status to be IN PROGRESS, got %s", exec[0].Status)
	}

	stored, err := repo.GetNode(exec[0].ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if stored.Status != domain.NodeInProgress {
		t.Errorf("expected the DB-persisted plan node status to be IN PROGRESS, got %s", stored.Status)
	}
}

func TestGetExecutableNodes_ClaimsReviewNodeAsInReview(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	exec, _ := e.GetExecutableNodes(ticket.ID, cat) // plan
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(plan): %v", err)
	}

	// plan_review (type: review) is next.
	exec, err := e.GetExecutableNodes(ticket.ID, cat)
	if err != nil {
		t.Fatalf("GetExecutableNodes: %v", err)
	}
	if len(exec) != 1 || exec[0].Type != domain.NodeTypeReview {
		t.Fatalf("expected the plan_review (review) node, got %+v", exec)
	}
	if exec[0].Status != domain.NodeInReview {
		t.Errorf("expected review node status IN REVIEW, got %s", exec[0].Status)
	}
	stored, _ := repo.GetNode(exec[0].ID)
	if stored.Status != domain.NodeInReview {
		t.Errorf("expected DB-persisted review status IN REVIEW, got %s", stored.Status)
	}
}

func TestGetExecutableNodes_ClaimsReviewGateNodeAsInReview(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	// investigationGraph already drives the seed (plan/plan_review) to DONE
	// and expands the graph, so the next executable node is "investigation".
	ticketID, cat := investigationGraph(t, e, projectID)

	exec, _ := e.GetExecutableNodes(ticketID, cat) // investigation
	if len(exec) != 1 || exec[0].Type != domain.NodeTypeInvestigation {
		t.Fatalf("expected the investigation node, got %+v", exec)
	}
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(investigation): %v", err)
	}

	// investigation_review (type: review_gate) should be claimed as IN REVIEW.
	exec, err := e.GetExecutableNodes(ticketID, cat)
	if err != nil {
		t.Fatalf("GetExecutableNodes: %v", err)
	}
	if len(exec) != 1 || exec[0].Type != domain.NodeTypeReviewGate {
		t.Fatalf("expected the investigation_review (review_gate) node, got %+v", exec)
	}
	if exec[0].Status != domain.NodeInReview {
		t.Errorf("expected review_gate node status IN REVIEW, got %s", exec[0].Status)
	}
	stored, _ := repo.GetNode(exec[0].ID)
	if stored.Status != domain.NodeInReview {
		t.Errorf("expected DB-persisted review_gate status IN REVIEW, got %s", stored.Status)
	}
}

func TestGetExecutableNodes_DoesNotReDispatchAlreadyClaimedNodes(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	first, _ := e.GetExecutableNodes(ticket.ID, cat)
	if len(first) != 1 {
		t.Fatalf("expected exactly one executable node, got %+v", first)
	}
	planID := first[0].ID

	// A second call, before the plan node is completed, must not hand out the
	// same node again (double-dispatch), and must not touch its status.
	second, err := e.GetExecutableNodes(ticket.ID, cat)
	if err != nil {
		t.Fatalf("GetExecutableNodes (second call): %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("expected no nodes on the second call while plan is still IN PROGRESS, got %+v", second)
	}
	stored, _ := repo.GetNode(planID)
	if stored.Status != domain.NodeInProgress {
		t.Errorf("expected plan node to remain IN PROGRESS, got %s", stored.Status)
	}
}

func TestGetExecutableNodes_PrereqNotDoneNodeIsNotClaimed(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	// Seeds plan+plan_review; plan_review's prereq (plan) is not DONE yet.
	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	if len(exec) != 1 || exec[0].Type != domain.NodeTypePlan {
		t.Fatalf("expected only the plan node to be claimed, got %+v", exec)
	}

	planReview := nodeByConfigID(t, repo, ticket.ID, "plan_review")
	if planReview.Status != domain.NodeTODO {
		t.Errorf("expected plan_review to remain TODO while its prereq is incomplete, got %s", planReview.Status)
	}
}

func TestGetExecutableNodes_ManualNodeIsNeverClaimed(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	// investigationGraph already drives the seed (plan/plan_review) to DONE.
	ticketID, cat := investigationGraph(t, e, projectID)

	exec, _ := e.GetExecutableNodes(ticketID, cat) // investigation
	e.CompleteNode(exec[0].ID, true, nil)
	exec, _ = e.GetExecutableNodes(ticketID, cat) // investigation_review
	e.CompleteNode(exec[0].ID, true, nil)

	// release is now the only node with all prereqs DONE, but is_manual=true.
	exec, err := e.GetExecutableNodes(ticketID, cat)
	if err != nil {
		t.Fatalf("GetExecutableNodes: %v", err)
	}
	if len(exec) != 0 {
		t.Fatalf("expected the manual release node not to be claimed, got %+v", exec)
	}
	release := nodeByConfigID(t, repo, ticketID, "release")
	if release.Status != domain.NodeTODO {
		t.Errorf("expected the manual release node to remain TODO, got %s", release.Status)
	}
}

func TestGetExecutableNodes_BlockedOrNotAutoExecutable_NoStatusChange(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)

	for _, patch := range []store.TicketPatch{
		{Blocked: boolPtr(true)},
		{AutoExecutable: boolPtr(false)},
	} {
		ticket, _ := e.CreateTicket(projectID, "title", "")
		// Seed the graph first (plan/plan_review at TODO), then flip the flag.
		e.GetExecutableNodes(ticket.ID, cat)
		if err := forceAllNodesTodo(repo, ticket.ID); err != nil {
			t.Fatalf("forceAllNodesTodo: %v", err)
		}
		if _, err := repo.UpdateTicket(ticket.ID, patch); err != nil {
			t.Fatalf("UpdateTicket: %v", err)
		}

		exec, err := e.GetExecutableNodes(ticket.ID, cat)
		if err != nil {
			t.Fatalf("GetExecutableNodes: %v", err)
		}
		if len(exec) != 0 {
			t.Fatalf("expected no executable nodes, got %+v", exec)
		}
		nodes, _ := repo.ListNodesByTicket(ticket.ID)
		for _, n := range nodes {
			if n.Status != domain.NodeTODO {
				t.Errorf("expected node %+v to remain TODO when blocked/non-auto-executable, got %s", n, n.Status)
			}
		}
	}
}

func TestCompleteNode_MarksClaimedNodeDone(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	if exec[0].Status != domain.NodeInProgress {
		t.Fatalf("expected plan node to be claimed IN PROGRESS first, got %s", exec[0].Status)
	}

	res, err := e.CompleteNode(exec[0].ID, true, nil)
	if err != nil {
		t.Fatalf("CompleteNode: %v", err)
	}
	if res.NextStatus != "DONE" || res.LoopedBack {
		t.Fatalf("expected {DONE false}, got %+v", res)
	}
}

func TestSyncTicketStatus_InProgressWhileNonReviewNodeInProgress(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	e.GetExecutableNodes(ticket.ID, cat) // claims plan -> IN PROGRESS

	got, _ := repo.GetTicket(ticket.ID)
	if got.Status != domain.TicketInProgress {
		t.Errorf("expected ticket status IN PROGRESS, got %s", got.Status)
	}
}

func TestSyncTicketStatus_InProgressWhileReviewNodeInReview(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	// investigationGraph already drives the seed (plan/plan_review) to DONE.
	ticketID, cat := investigationGraph(t, e, projectID)

	exec, _ := e.GetExecutableNodes(ticketID, cat) // claims investigation -> IN PROGRESS
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(investigation): %v", err)
	}
	e.GetExecutableNodes(ticketID, cat) // claims investigation_review -> IN REVIEW

	// DFLT-00046: a review/review_gate node's own IN REVIEW status is an
	// automated step running, not a human waiting -- the ticket must stay IN
	// PROGRESS. IN REVIEW is reserved for a pending approval_gate (see
	// TestSyncTicketStatus_InReviewWhilePendingApprovalGate below).
	got, _ := repo.GetTicket(ticketID)
	if got.Status != domain.TicketInProgress {
		t.Errorf("expected ticket status IN PROGRESS while a review_gate node runs (not IN REVIEW), got %s", got.Status)
	}
}

func TestSyncTicketStatus_InReleaseWhileReleaseNodeInProgress(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _ := investigationGraph(t, e, projectID)

	release := nodeByConfigID(t, repo, ticketID, "release")
	inProgress := domain.NodeInProgress
	// Manual nodes are moved to IN PROGRESS by the manual-start path (PATCH
	// /api/nodes/{id}), not GetExecutableNodes (which skips is_manual nodes
	// entirely) -- simulate that here.
	if _, err := repo.UpdateNode(release.ID, store.NodePatch{Status: &inProgress}); err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}
	if err := e.syncTicketStatus(ticketID); err != nil {
		t.Fatalf("syncTicketStatus: %v", err)
	}

	got, _ := repo.GetTicket(ticketID)
	if got.Status != domain.TicketInRelease {
		t.Errorf("expected ticket status IN RELEASE, got %s", got.Status)
	}
}

func TestSyncTicketStatus_DoneWhenAllNodesDone(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, cat := investigationGraph(t, e, projectID)

	for {
		exec, err := e.GetExecutableNodes(ticketID, cat)
		if err != nil {
			t.Fatalf("GetExecutableNodes: %v", err)
		}
		if len(exec) == 0 {
			break
		}
		for _, n := range exec {
			if _, err := e.CompleteNode(n.ID, true, nil); err != nil {
				t.Fatalf("CompleteNode: %v", err)
			}
		}
	}

	// release is manual; finish it the way the manual-completion path would.
	release := nodeByConfigID(t, repo, ticketID, "release")
	if _, err := e.CompleteNode(release.ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(release): %v", err)
	}

	got, _ := repo.GetTicket(ticketID)
	if got.Status != domain.TicketDone {
		t.Errorf("expected ticket status DONE, got %s", got.Status)
	}
}

// --- DFLT-00046: syncTicketStatus no longer equates "every existing node is
// DONE" with "the ticket is DONE" while only the seed (plan/plan_review) has
// ever existed, and reserves IN REVIEW for a reached, still-pending human
// approval_gate rather than any node sitting at IN REVIEW. ---

func TestSyncTicketStatus_SeedOnlyDoneStaysInProgressNotDone(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil { // plan
		t.Fatalf("CompleteNode(plan): %v", err)
	}
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil { // plan_review
		t.Fatalf("CompleteNode(plan_review): %v", err)
	}

	got, _ := repo.GetTicket(ticket.ID)
	if got.GraphExpandedAt != nil {
		t.Errorf("expected GraphExpandedAt to stay unset before ExpandGraph runs, got %+v", got.GraphExpandedAt)
	}
	if got.Status != domain.TicketInProgress {
		t.Errorf("expected ticket status IN PROGRESS with only the seed done and the graph not yet expanded (not DONE), got %s", got.Status)
	}
}

func TestSyncTicketStatus_InReviewWhilePendingApprovalGate(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _ := approvalGateGraph(t, e, projectID, false)

	// "approval" is reached (its only prerequisite, plan_review, is DONE) and
	// still TODO -- a human is waiting on it.
	got, _ := repo.GetTicket(ticketID)
	if got.Status != domain.TicketInReview {
		t.Errorf("expected ticket status IN REVIEW while a reached approval_gate is TODO, got %s", got.Status)
	}
}

func TestSyncTicketStatus_UnreachedApprovalGateDoesNotTriggerInReview(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil { // plan
		t.Fatalf("CompleteNode(plan): %v", err)
	}
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil { // plan_review
		t.Fatalf("CompleteNode(plan_review): %v", err)
	}

	// "approval" depends on "investigation", which is still TODO (never
	// claimed/completed) -- so even though the approval_gate row exists at
	// TODO, it has not been reached yet.
	patch := &Patch{ExtraNodes: []ExtraNode{
		{ID: "investigation", Type: "investigation", Name: "Investigate", DependsOn: []string{"plan_review"}},
		{ID: "approval", Type: "approval_gate", Name: "Approval", DependsOn: []string{"investigation"}},
	}}
	if err := e.ExpandGraph(ticket.ID, cat, patch); err != nil {
		t.Fatalf("ExpandGraph: %v", err)
	}

	got, _ := repo.GetTicket(ticket.ID)
	if got.Status == domain.TicketInReview {
		t.Errorf("expected an unreached approval_gate not to put the ticket into IN REVIEW, got %s", got.Status)
	}
	if got.Status != domain.TicketInProgress {
		t.Errorf("expected ticket status IN PROGRESS, got %s", got.Status)
	}
}

func TestSyncTicketStatus_ApprovingGateReturnsTicketToInProgress(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _ := approvalGateGraph(t, e, projectID, false)
	gate := nodeByConfigID(t, repo, ticketID, "approval")

	if got, _ := repo.GetTicket(ticketID); got.Status != domain.TicketInReview {
		t.Fatalf("expected IN REVIEW before approving, got %s", got.Status)
	}

	if _, err := e.CompleteNode(gate.ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(approval, passed): %v", err)
	}

	got, _ := repo.GetTicket(ticketID)
	if got.Status != domain.TicketInProgress {
		t.Errorf("expected ticket status IN PROGRESS after approving the gate, got %s", got.Status)
	}
}

func TestSyncTicketStatus_RejectedApprovalGateStaysInProgress(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _ := approvalGateGraph(t, e, projectID, false)
	gate := nodeByConfigID(t, repo, ticketID, "approval")

	if _, err := e.CompleteNode(gate.ID, false, nil); err != nil {
		t.Fatalf("CompleteNode(approval, rejected): %v", err)
	}

	got, _ := repo.GetTicket(ticketID)
	if got.Status != domain.TicketInProgress {
		t.Errorf("expected ticket status IN PROGRESS (not IN REVIEW) while the approval_gate is REJECTED, got %s", got.Status)
	}
	if !got.Blocked {
		t.Errorf("expected ticket.Blocked=true, got %+v", got)
	}

	// A later resync (e.g. from UnstickNode/ReopenNodes touching an unrelated
	// node) must not drift back into IN REVIEW either, as long as the
	// rejected gate itself is still REJECTED (not reset to TODO).
	if err := e.syncTicketStatus(ticketID); err != nil {
		t.Fatalf("syncTicketStatus: %v", err)
	}
	got, _ = repo.GetTicket(ticketID)
	if got.Status != domain.TicketInProgress {
		t.Errorf("expected ticket status to remain IN PROGRESS after a later resync, got %s", got.Status)
	}
}

func boolPtr(b bool) *bool { return &b }

// forceAllNodesTodo is used by the blocked/non-auto-executable test above to
// reset the seed nodes GetExecutableNodes just claimed back to TODO, so the
// test observes only the effect of blocking, not of the earlier claim.
func forceAllNodesTodo(repo store.GraphRepository, ticketID string) error {
	nodes, err := repo.ListNodesByTicket(ticketID)
	if err != nil {
		return err
	}
	todo := domain.NodeTODO
	for _, n := range nodes {
		if n.Status != domain.NodeTODO {
			if _, err := repo.UpdateNode(n.ID, store.NodePatch{Status: &todo}); err != nil {
				return err
			}
		}
	}
	return nil
}

// --- CloseTicket / ReopenTicket (DFLT-00043) ---

func TestCloseTicket_ClosesFromAnyStatusAndSavesReason(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	// Seed the graph and claim the plan node, so it's IN PROGRESS: closing
	// must work even with a node actively in progress, and must not touch
	// that node's status.
	exec, err := e.GetExecutableNodes(ticket.ID, cat)
	if err != nil {
		t.Fatalf("GetExecutableNodes: %v", err)
	}
	if len(exec) != 1 {
		t.Fatalf("expected one executable node, got %+v", exec)
	}

	closed, err := e.CloseTicket(ticket.ID, "対応不要になったため")
	if err != nil {
		t.Fatalf("CloseTicket: %v", err)
	}
	if closed.Status != domain.TicketClosed {
		t.Errorf("expected status CLOSED, got %s", closed.Status)
	}
	if closed.ClosedReason == nil || *closed.ClosedReason != "対応不要になったため" {
		t.Errorf("expected closed_reason to be saved, got %+v", closed.ClosedReason)
	}
}

func TestCloseTicket_OverwritesPreviousReason(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	if _, err := e.CloseTicket(ticket.ID, "first reason"); err != nil {
		t.Fatalf("CloseTicket (first): %v", err)
	}
	closed, err := e.CloseTicket(ticket.ID, "")
	if err != nil {
		t.Fatalf("CloseTicket (second, empty reason): %v", err)
	}
	if closed.ClosedReason == nil || *closed.ClosedReason != "" {
		t.Errorf("expected the second close to overwrite the reason with empty text, got %+v", closed.ClosedReason)
	}
}

// TestSyncTicketStatus_ClosedTicketUnaffectedByCompleteNode covers a ticket
// closed out from under a node that was already claimed.
//
// It used to check only that the ticket stayed CLOSED -- CompleteNode
// succeeded and syncTicketStatus declined to resurrect the ticket. Since
// DFLT-00102 the completion itself is refused: a closed ticket takes no
// further work, so the node keeps the status it had rather than quietly
// reaching DONE under a ticket nobody is working on any more. The old
// expectation (the ticket stays CLOSED) is checked too, and now holds because
// nothing was written at all.
func TestSyncTicketStatus_ClosedTicketUnaffectedByCompleteNode(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	planNode := exec[0]

	if _, err := e.CloseTicket(ticket.ID, "closing while plan is in progress"); err != nil {
		t.Fatalf("CloseTicket: %v", err)
	}

	_, err := e.CompleteNode(planNode.ID, true, nil)
	assertInvalidNodeState(t, err)

	got, err := repo.GetTicket(ticket.ID)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if got.Status != domain.TicketClosed {
		t.Errorf("expected ticket to remain CLOSED after complete-node, got %s", got.Status)
	}
	node, err := repo.GetNode(planNode.ID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if node.Status != domain.NodeInProgress {
		t.Errorf("expected the node to stay IN PROGRESS, got %s", node.Status)
	}
}

func TestGetExecutableNodes_ClosedTicketReturnsNoneAndSeedsNothing(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	if _, err := e.CloseTicket(ticket.ID, ""); err != nil {
		t.Fatalf("CloseTicket: %v", err)
	}

	exec, err := e.GetExecutableNodes(ticket.ID, cat)
	if err != nil {
		t.Fatalf("GetExecutableNodes: %v", err)
	}
	if len(exec) != 0 {
		t.Fatalf("expected no executable nodes for a CLOSED ticket, got %+v", exec)
	}
	nodes, _ := repo.ListNodesByTicket(ticket.ID)
	if len(nodes) != 0 {
		t.Fatalf("expected a CLOSED ticket to never get seed nodes, got %d", len(nodes))
	}
}

func TestRefineTicket_RejectsClosedTicket(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticket, _ := e.CreateTicket(projectID, "title", "original")

	if _, err := e.CloseTicket(ticket.ID, ""); err != nil {
		t.Fatalf("CloseTicket: %v", err)
	}
	if _, err := e.RefineTicket(ticket.ID, "new description", NoPriorityChange()); err == nil {
		t.Fatalf("expected RefineTicket to reject a CLOSED ticket")
	}
	got, _ := repo.GetTicket(ticket.ID)
	if got.Status != domain.TicketClosed {
		t.Errorf("expected status to remain CLOSED, got %s", got.Status)
	}
	if got.Description != "original" {
		t.Errorf("expected description to remain untouched, got %q", got.Description)
	}
}

func TestReopenTicket_RejectsWhenNotClosed(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	if _, err := e.ReopenTicket(ticket.ID); err == nil {
		t.Fatalf("expected ReopenTicket to reject a ticket that is not CLOSED")
	}
}

func TestReopenTicket_DerivesStatusFromNodes(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	// Claim the plan node (IN PROGRESS) and close with it still in that
	// state.
	e.GetExecutableNodes(ticket.ID, cat)
	if _, err := e.CloseTicket(ticket.ID, ""); err != nil {
		t.Fatalf("CloseTicket: %v", err)
	}

	reopened, err := e.ReopenTicket(ticket.ID)
	if err != nil {
		t.Fatalf("ReopenTicket: %v", err)
	}
	if reopened.Status != domain.TicketInProgress {
		t.Errorf("expected status derived from nodes (IN PROGRESS), got %s", reopened.Status)
	}
}

func TestReopenTicket_NoNodesFallsBackToRefinedOrTODO(t *testing.T) {
	e, _, projectID := newTestEngine(t)

	// Case 1: never refined -> TODO.
	unrefined, _ := e.CreateTicket(projectID, "title", "")
	if _, err := e.CloseTicket(unrefined.ID, ""); err != nil {
		t.Fatalf("CloseTicket: %v", err)
	}
	reopened, err := e.ReopenTicket(unrefined.ID)
	if err != nil {
		t.Fatalf("ReopenTicket: %v", err)
	}
	if reopened.Status != domain.TicketTODO {
		t.Errorf("expected TODO for a never-refined, node-less ticket, got %s", reopened.Status)
	}

	// Case 2: refined -> REFINED.
	refined, _ := e.CreateTicket(projectID, "title2", "")
	if _, err := e.RefineTicket(refined.ID, "refined description", NoPriorityChange()); err != nil {
		t.Fatalf("RefineTicket: %v", err)
	}
	if _, err := e.CloseTicket(refined.ID, ""); err != nil {
		t.Fatalf("CloseTicket: %v", err)
	}
	reopened2, err := e.ReopenTicket(refined.ID)
	if err != nil {
		t.Fatalf("ReopenTicket: %v", err)
	}
	if reopened2.Status != domain.TicketRefined {
		t.Errorf("expected REFINED for a refined, node-less ticket, got %s", reopened2.Status)
	}
}

// TestReopenTicket_SeedOnlyDoneFallsBackToInProgressNotDone applies
// deriveTicketStatus's GraphExpandedAt gate (DFLT-00046) to the ReopenTicket
// path too: a ticket closed while only its seed (plan/plan_review) is DONE
// must reopen into IN PROGRESS, not DONE.
func TestReopenTicket_SeedOnlyDoneFallsBackToInProgressNotDone(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")

	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil { // plan
		t.Fatalf("CompleteNode(plan): %v", err)
	}
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil { // plan_review
		t.Fatalf("CompleteNode(plan_review): %v", err)
	}

	if _, err := e.CloseTicket(ticket.ID, ""); err != nil {
		t.Fatalf("CloseTicket: %v", err)
	}

	reopened, err := e.ReopenTicket(ticket.ID)
	if err != nil {
		t.Fatalf("ReopenTicket: %v", err)
	}
	if reopened.Status != domain.TicketInProgress {
		t.Errorf("expected seed-only-done to reopen into IN PROGRESS (not DONE), got %s", reopened.Status)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i+len(substr) <= len(s); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}
