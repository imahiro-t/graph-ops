package engine

import (
	"testing"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// --- DFLT-00101 / BUG-02: a loop-back rewinds not just its target but every
// DONE node between the target and the failing node. Before this, the default
// workflow's `test_review` -> `impl` loop-back left the four review gates and
// `gherkin_test` DONE, so an implementation that was never re-reviewed or
// re-tested could reach `report`/`release_approval`, and `test_review` itself
// was handed straight back by the next get-executable call -- re-judging the
// previous run's test results before the rework existed. ---

// defaultWorkflowAtTestReview drives a fresh ticket through the *default*
// workflow -- ExpandGraph with no patch, i.e. exactly the shape
// internal/config/defaults/workflow.yaml defines, gates and all -- up to the
// point where every node from `impl` through `gherkin_test` is DONE and
// `test_review` has been claimed IN REVIEW. `report` onwards is still TODO.
// Using the real template rather than a hand-written patch is the point: BUG-02
// is a bug about what the shipped workflow does.
func defaultWorkflowAtTestReview(t *testing.T, e *GraphEngine, projectID string) (ticketID string, cat config.Catalog) {
	t.Helper()
	cat = baseCatalog(t)
	ticket, err := e.CreateTicket(projectID, "title", "")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	ticketID = ticket.ID

	completeAllExecutable(t, e, ticketID, cat, 1) // plan
	completeAllExecutable(t, e, ticketID, cat, 1) // plan_review
	if err := e.ExpandGraph(ticketID, cat, nil); err != nil {
		t.Fatalf("ExpandGraph: %v", err)
	}

	// plan_approval and release_approval are manual, so get-executable never
	// offers them -- the human decision is stood in for directly.
	planApproval := nodeByConfigID(t, e.repo, ticketID, "plan_approval")
	if _, err := e.CompleteNode(planApproval.ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(plan_approval): %v", err)
	}

	completeAllExecutable(t, e, ticketID, cat, 1) // gherkin_spec
	completeAllExecutable(t, e, ticketID, cat, 1) // gherkin_review
	completeAllExecutable(t, e, ticketID, cat, 1) // impl
	completeAllExecutable(t, e, ticketID, cat, 4) // the four review gates, in parallel
	completeAllExecutable(t, e, ticketID, cat, 1) // gherkin_test

	exec, err := e.GetExecutableNodes(ticketID, cat)
	if err != nil {
		t.Fatalf("GetExecutableNodes: %v", err)
	}
	if len(exec) != 1 || *exec[0].ConfigID != "test_review" || exec[0].Status != domain.NodeInReview {
		t.Fatalf("expected test_review alone to be claimed IN REVIEW, got %+v", exec)
	}
	return ticketID, cat
}

// completeAllExecutable claims the currently executable nodes, asserts there
// are wantN of them, and passes every one.
func completeAllExecutable(t *testing.T, e *GraphEngine, ticketID string, cat config.Catalog, wantN int) []domain.GraphNode {
	t.Helper()
	exec, err := e.GetExecutableNodes(ticketID, cat)
	if err != nil {
		t.Fatalf("GetExecutableNodes: %v", err)
	}
	if len(exec) != wantN {
		t.Fatalf("expected %d executable node(s), got %d: %+v", wantN, len(exec), exec)
	}
	for _, n := range exec {
		if _, err := e.CompleteNode(n.ID, true, nil); err != nil {
			t.Fatalf("CompleteNode(%s): %v", n.Name, err)
		}
	}
	return exec
}

// assertNodeStatus checks one node (addressed by config id) against an
// expected status and iteration count.
func assertNodeStatus(t *testing.T, repo store.GraphRepository, ticketID, configID string, wantStatus domain.NodeStatus, wantIterations int) {
	t.Helper()
	n := nodeByConfigID(t, repo, ticketID, configID)
	if n.Status != wantStatus {
		t.Errorf("%s: expected status %s, got %s", configID, wantStatus, n.Status)
	}
	if n.IterationCount != wantIterations {
		t.Errorf("%s: expected iteration_count %d, got %d", configID, wantIterations, n.IterationCount)
	}
}

func execConfigIDs(nodes []domain.GraphNode) []string {
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n.ConfigID == nil {
			ids = append(ids, n.ID)
			continue
		}
		ids = append(ids, *n.ConfigID)
	}
	return ids
}

// TestCompleteNode_DefaultWorkflowLoopBackRewindsReviewsAndTests is the
// completion-criterion-3 regression: it drives the real default workflow to a
// failing `test_review` and pins (a) the target's reset, (b) the rewind of
// the four gates and gherkin_test between them, (c) that nothing but `impl` is
// executable straight afterwards, and (d) the order everything comes back in
// as the rework proceeds.
func TestCompleteNode_DefaultWorkflowLoopBackRewindsReviewsAndTests(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, cat := defaultWorkflowAtTestReview(t, e, projectID)
	testReview := nodeByConfigID(t, repo, ticketID, "test_review")

	res, err := e.CompleteNode(testReview.ID, false, nil)
	if err != nil {
		t.Fatalf("CompleteNode(test_review, fail): %v", err)
	}
	if res.NextStatus != "AWAITING FIX" || !res.LoopedBack {
		t.Fatalf("expected {AWAITING FIX true}, got %+v", res)
	}

	// (a) the loop target: TODO, and the only node whose iteration_count moves.
	assertNodeStatus(t, repo, ticketID, "impl", domain.NodeTODO, 1)
	// (b) everything on the success path between impl and test_review, with
	// iteration counts left where they were.
	for _, cfg := range []string{"code_review", "qa_review", "security_review", "non_functional_review", "gherkin_test"} {
		assertNodeStatus(t, repo, ticketID, cfg, domain.NodeTODO, 0)
	}
	// The failing reviewer itself stays AWAITING FIX (DFLT-00042), not TODO.
	assertNodeStatus(t, repo, ticketID, "test_review", domain.NodeAwaitingFix, 0)
	// Upstream of the target, and downstream of the failing node, are both
	// left alone: the former never produced the output being redone, the
	// latter never ran against it.
	for _, cfg := range []string{"plan", "plan_review", "plan_approval", "gherkin_spec", "gherkin_review"} {
		assertNodeStatus(t, repo, ticketID, cfg, domain.NodeDone, 0)
	}
	for _, cfg := range []string{"report", "report_review", "release_approval", "release"} {
		assertNodeStatus(t, repo, ticketID, cfg, domain.NodeTODO, 0)
	}
	// A loop-back is a retry, not a failure: the ticket keeps running.
	ticket, _ := repo.GetTicket(ticketID)
	if ticket.Blocked {
		t.Errorf("expected the ticket to stay unblocked after a loop-back")
	}
	if ticket.Status != domain.TicketInProgress {
		t.Errorf("expected the ticket to stay IN PROGRESS after a loop-back, got %s", ticket.Status)
	}

	// (c) impl alone -- in particular NOT test_review, which used to come
	// back in this very call and re-judge the previous run's test results.
	exec, _ := e.GetExecutableNodes(ticketID, cat)
	if len(exec) != 1 || *exec[0].ConfigID != "impl" {
		t.Fatalf("expected impl alone to be executable after the loop-back, got %v", execConfigIDs(exec))
	}

	// (d) the rework re-runs the reviews, then the tests, then the reviewer
	// that sent it back.
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(impl rework): %v", err)
	}
	gates := completeAllExecutable(t, e, ticketID, cat, 4)
	gateCfgs := map[string]bool{}
	for _, g := range gates {
		gateCfgs[*g.ConfigID] = true
	}
	for _, cfg := range []string{"code_review", "qa_review", "security_review", "non_functional_review"} {
		if !gateCfgs[cfg] {
			t.Errorf("expected %s to be re-offered after the rework, got %v", cfg, execConfigIDs(gates))
		}
	}
	exec, _ = e.GetExecutableNodes(ticketID, cat)
	if len(exec) != 1 || *exec[0].ConfigID != "gherkin_test" {
		t.Fatalf("expected gherkin_test alone once the gates pass again, got %v", execConfigIDs(exec))
	}
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(gherkin_test rerun): %v", err)
	}
	exec, _ = e.GetExecutableNodes(ticketID, cat)
	if len(exec) != 1 || *exec[0].ConfigID != "test_review" {
		t.Fatalf("expected test_review alone once its input has been re-run, got %v", execConfigIDs(exec))
	}
	if exec[0].Status != domain.NodeInReview {
		t.Errorf("expected the re-offered test_review to be claimed IN REVIEW (from AWAITING FIX), got %s", exec[0].Status)
	}
}

// TestCompleteNode_LoopBackLeavesSiblingGatesAlone pins the deliberate limit
// of the rewind (see loopBackRewindSet): with the default workflow's four
// gates all looping back to `impl`, one gate failing does not invalidate the
// other three's verdicts, because they are not on a success path to the
// failing gate. Their judgments of the pre-rework implementation survive --
// the intended scope of this ticket, not an oversight.
func TestCompleteNode_LoopBackLeavesSiblingGatesAlone(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _ := defaultWorkflowAtTestReview(t, e, projectID)
	codeReview := nodeByConfigID(t, repo, ticketID, "code_review")
	// The gate is DONE by now; a gate that is actually judging is IN REVIEW,
	// so put it back into that state rather than failing a DONE node.
	setNodeStatus(t, repo, codeReview.ID, domain.NodeInReview)

	res, err := e.CompleteNode(codeReview.ID, false, nil)
	if err != nil {
		t.Fatalf("CompleteNode(code_review, fail): %v", err)
	}
	if res.NextStatus != "AWAITING FIX" || !res.LoopedBack {
		t.Fatalf("expected {AWAITING FIX true}, got %+v", res)
	}

	assertNodeStatus(t, repo, ticketID, "impl", domain.NodeTODO, 1)
	assertNodeStatus(t, repo, ticketID, "code_review", domain.NodeAwaitingFix, 0)
	for _, cfg := range []string{"qa_review", "security_review", "non_functional_review", "gherkin_test"} {
		assertNodeStatus(t, repo, ticketID, cfg, domain.NodeDone, 0)
	}
	assertNodeStatus(t, repo, ticketID, "test_review", domain.NodeInReview, 0)
}

// TestCompleteNode_LoopBackRewindsDoneNodesOnly pins the "DONE only" filter:
// a node on the path that is currently being worked (IN PROGRESS/IN REVIEW) is
// left as it is, because resetting it would race whatever holds that claim --
// the engine never decides on its own that a claim is stale (see UnstickNode).
// The state set up here (a gate still IN REVIEW while gherkin_test, which
// depends on it, is already DONE) cannot arise from the default workflow on
// its own; it is written directly to exercise the filter. Its consequence is
// that the untouched claim keeps blocking its own successors afterwards, and
// unstick-node is the way out -- exactly the recovery GrantIterations' doc
// comment describes.
func TestCompleteNode_LoopBackRewindsDoneNodesOnly(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, cat := defaultWorkflowAtTestReview(t, e, projectID)
	codeReview := nodeByConfigID(t, repo, ticketID, "code_review")
	setNodeStatus(t, repo, codeReview.ID, domain.NodeInReview)
	testReview := nodeByConfigID(t, repo, ticketID, "test_review")

	if _, err := e.CompleteNode(testReview.ID, false, nil); err != nil {
		t.Fatalf("CompleteNode(test_review, fail): %v", err)
	}

	assertNodeStatus(t, repo, ticketID, "impl", domain.NodeTODO, 1)
	assertNodeStatus(t, repo, ticketID, "code_review", domain.NodeInReview, 0)
	for _, cfg := range []string{"qa_review", "security_review", "non_functional_review", "gherkin_test"} {
		assertNodeStatus(t, repo, ticketID, cfg, domain.NodeTODO, 0)
	}

	exec, _ := e.GetExecutableNodes(ticketID, cat)
	if len(exec) != 1 || *exec[0].ConfigID != "impl" {
		t.Fatalf("expected impl alone to be executable, got %v", execConfigIDs(exec))
	}
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(impl rework): %v", err)
	}
	// The three rewound gates come back; the one still holding a claim does
	// not, and gherkin_test stays behind it until unstick-node frees it.
	exec, _ = e.GetExecutableNodes(ticketID, cat)
	if len(exec) != 3 {
		t.Fatalf("expected the three rewound gates alone, got %v", execConfigIDs(exec))
	}
	if _, err := e.UnstickNode(codeReview.ID); err != nil {
		t.Fatalf("UnstickNode(code_review): %v", err)
	}
	exec, _ = e.GetExecutableNodes(ticketID, cat)
	if len(exec) != 1 || *exec[0].ConfigID != "code_review" {
		t.Fatalf("expected code_review to become executable again after unstick-node, got %v", execConfigIDs(exec))
	}
}

// TestCompleteNode_LoopBackRewindsApprovalGateOnPath pins the approval_gate
// decision (see loopBackRewindSet): a manual gate between the loop target and
// the failing node is rewound like any other DONE node, so the reworked output
// needs a fresh human approval instead of coasting through on the old one. The
// deliberate cost is that the ticket parks on that gate again -- visible here
// as the ticket going back to IN REVIEW (deriveTicketStatus' "a human is
// waiting") once the rework lands.
func TestCompleteNode_LoopBackRewindsApprovalGateOnPath(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")
	completeAllExecutable(t, e, ticket.ID, cat, 1) // plan
	completeAllExecutable(t, e, ticket.ID, cat, 1) // plan_review

	patch := &Patch{ExtraNodes: []ExtraNode{
		{ID: "impl", Type: "implementation", Name: "Implementation", DependsOn: []string{"plan_review"}},
		{ID: "mid_approval", Type: "approval_gate", Name: "Mid Approval", DependsOn: []string{"impl"}},
		{ID: "gherkin_test", Type: "gherkin_test", Name: "Gherkin Test Execution", DependsOn: []string{"mid_approval"}},
		{ID: "test_review", Type: "review", Name: "Test Result Review", DependsOn: []string{"gherkin_test"}, LoopBackTo: "impl"},
	}}
	if err := e.ExpandGraph(ticket.ID, cat, patch); err != nil {
		t.Fatalf("ExpandGraph: %v", err)
	}
	completeAllExecutable(t, e, ticket.ID, cat, 1) // impl
	midApproval := nodeByConfigID(t, repo, ticket.ID, "mid_approval")
	if _, err := e.CompleteNode(midApproval.ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(mid_approval): %v", err)
	}
	completeAllExecutable(t, e, ticket.ID, cat, 1) // gherkin_test
	testReview := nodeByConfigID(t, repo, ticket.ID, "test_review")
	setNodeStatus(t, repo, testReview.ID, domain.NodeInReview)

	if _, err := e.CompleteNode(testReview.ID, false, nil); err != nil {
		t.Fatalf("CompleteNode(test_review, fail): %v", err)
	}

	assertNodeStatus(t, repo, ticket.ID, "impl", domain.NodeTODO, 1)
	assertNodeStatus(t, repo, ticket.ID, "mid_approval", domain.NodeTODO, 0)
	assertNodeStatus(t, repo, ticket.ID, "gherkin_test", domain.NodeTODO, 0)
	assertNodeStatus(t, repo, ticket.ID, "test_review", domain.NodeAwaitingFix, 0)

	// The rewound gate is manual, so it is never handed out for execution --
	// impl is all there is to do.
	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	if len(exec) != 1 || *exec[0].ConfigID != "impl" {
		t.Fatalf("expected impl alone (mid_approval is manual), got %v", execConfigIDs(exec))
	}
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(impl rework): %v", err)
	}
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	if len(exec) != 0 {
		t.Fatalf("expected nothing executable while mid_approval waits on a human, got %v", execConfigIDs(exec))
	}
	got, _ := repo.GetTicket(ticket.ID)
	if got.Status != domain.TicketInReview {
		t.Errorf("expected the ticket to read IN REVIEW (waiting on the re-approval), got %s", got.Status)
	}
}

// TestCompleteNode_LoopBackWithNoSuccessPathToFailedNodeRewindsTargetOnly
// pins the fallback for a loop_back_to that points sideways rather than back
// up the failing node's own lineage: no node is both downstream of the target
// and upstream of the failing node, so the rewind set is empty and the
// behaviour is the pre-DFLT-00101 one (target only). Nothing on the unrelated
// branch is disturbed either.
func TestCompleteNode_LoopBackWithNoSuccessPathToFailedNodeRewindsTargetOnly(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")
	completeAllExecutable(t, e, ticket.ID, cat, 1) // plan
	completeAllExecutable(t, e, ticket.ID, cat, 1) // plan_review

	// side_task is a parallel branch off plan_review: test_review loops back
	// to it, but no success edge leads from it to test_review.
	patch := &Patch{ExtraNodes: []ExtraNode{
		{ID: "side_task", Type: "documentation", Name: "Side Task", DependsOn: []string{"plan_review"}},
		{ID: "impl", Type: "implementation", Name: "Implementation", DependsOn: []string{"plan_review"}},
		{ID: "gherkin_test", Type: "gherkin_test", Name: "Gherkin Test Execution", DependsOn: []string{"impl"}},
		{ID: "test_review", Type: "review", Name: "Test Result Review", DependsOn: []string{"gherkin_test"}, LoopBackTo: "side_task"},
	}}
	if err := e.ExpandGraph(ticket.ID, cat, patch); err != nil {
		t.Fatalf("ExpandGraph: %v", err)
	}
	completeAllExecutable(t, e, ticket.ID, cat, 2) // side_task and impl, in parallel
	completeAllExecutable(t, e, ticket.ID, cat, 1) // gherkin_test
	testReview := nodeByConfigID(t, repo, ticket.ID, "test_review")
	setNodeStatus(t, repo, testReview.ID, domain.NodeInReview)

	res, err := e.CompleteNode(testReview.ID, false, nil)
	if err != nil {
		t.Fatalf("CompleteNode(test_review, fail): %v", err)
	}
	if res.NextStatus != "AWAITING FIX" || !res.LoopedBack {
		t.Fatalf("expected {AWAITING FIX true}, got %+v", res)
	}
	assertNodeStatus(t, repo, ticket.ID, "side_task", domain.NodeTODO, 1)
	assertNodeStatus(t, repo, ticket.ID, "impl", domain.NodeDone, 0)
	assertNodeStatus(t, repo, ticket.ID, "gherkin_test", domain.NodeDone, 0)
	assertNodeStatus(t, repo, ticket.ID, "test_review", domain.NodeAwaitingFix, 0)
}

// TestCompleteNode_LoopBackAtIterationLimitRewindsNothing is the
// completion-criterion-2 regression: the iteration-budget check runs before
// any write, so a loop-back that blocks the ticket must not leave the
// intermediate nodes half-rewound. Every status and iteration count is
// asserted unchanged, including the failing reviewer's (it stays IN REVIEW,
// not AWAITING FIX -- DFLT-00042's own regression, kept here for the
// multi-node graph).
func TestCompleteNode_LoopBackAtIterationLimitRewindsNothing(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, cat := defaultWorkflowAtTestReview(t, e, projectID)
	impl := nodeByConfigID(t, repo, ticketID, "impl")
	// Drive the loop target's counter to its ceiling directly rather than
	// looping max_iterations times: what is under test is the branch taken
	// once the budget is gone, not the counting.
	atLimit := impl.MaxIterations
	if _, err := repo.UpdateNode(impl.ID, store.NodePatch{IterationCount: &atLimit}); err != nil {
		t.Fatalf("UpdateNode(impl iteration_count): %v", err)
	}
	testReview := nodeByConfigID(t, repo, ticketID, "test_review")

	res, err := e.CompleteNode(testReview.ID, false, nil)
	if err != nil {
		t.Fatalf("CompleteNode(test_review, fail): %v", err)
	}
	if res.NextStatus != "BLOCKED" || res.LoopedBack {
		t.Fatalf("expected {BLOCKED false}, got %+v", res)
	}
	ticket, _ := repo.GetTicket(ticketID)
	if !ticket.Blocked {
		t.Errorf("expected the ticket to be blocked")
	}
	assertNodeStatus(t, repo, ticketID, "impl", domain.NodeDone, atLimit)
	for _, cfg := range []string{"code_review", "qa_review", "security_review", "non_functional_review", "gherkin_test"} {
		assertNodeStatus(t, repo, ticketID, cfg, domain.NodeDone, 0)
	}
	assertNodeStatus(t, repo, ticketID, "test_review", domain.NodeInReview, 0)

	// A blocked ticket hands out nothing, so the half-rewound state this
	// guards against would be invisible until recovery -- which is precisely
	// why it must not exist.
	exec, _ := e.GetExecutableNodes(ticketID, cat)
	if len(exec) != 0 {
		t.Fatalf("expected nothing executable on a blocked ticket, got %v", execConfigIDs(exec))
	}
}

// TestLoopBackRewindSet exercises the helper directly against a fixed
// node/edge set (no DB), covering the two ways a naive closure goes wrong:
// `down` is reachable from the target but does not reach the failing node, and
// `sibling` reaches the failing node but not from the target.
func TestLoopBackRewindSet(t *testing.T) {
	byID := map[string]domain.GraphNode{
		"target":  {ID: "target", Status: domain.NodeDone},
		"mid":     {ID: "mid", Status: domain.NodeDone},
		"claimed": {ID: "claimed", Status: domain.NodeInReview},
		"failed":  {ID: "failed", Status: domain.NodeInReview},
		"down":    {ID: "down", Status: domain.NodeDone},
		"sibling": {ID: "sibling", Status: domain.NodeDone},
	}
	edges := []domain.GraphEdge{
		{FromNodeID: "target", ToNodeID: "mid", Condition: domain.EdgeSuccess},
		{FromNodeID: "target", ToNodeID: "claimed", Condition: domain.EdgeSuccess},
		{FromNodeID: "mid", ToNodeID: "failed", Condition: domain.EdgeSuccess},
		{FromNodeID: "claimed", ToNodeID: "failed", Condition: domain.EdgeSuccess},
		{FromNodeID: "sibling", ToNodeID: "failed", Condition: domain.EdgeSuccess},
		{FromNodeID: "failed", ToNodeID: "down", Condition: domain.EdgeSuccess},
		{FromNodeID: "failed", ToNodeID: "target", Condition: domain.EdgeLoop},
	}

	got := loopBackRewindSet("target", "failed", byID, edges)
	if len(got) != 1 || got[0] != "mid" {
		t.Errorf("expected [mid] (claimed is not DONE, down is past the failure, sibling is off the target's path), got %v", got)
	}

	// An unreachable failing node yields an empty set, i.e. the loop-back
	// falls back to resetting its target alone.
	if got := loopBackRewindSet("down", "failed", byID, edges); len(got) != 0 {
		t.Errorf("expected no rewind when the failing node is not downstream of the target, got %v", got)
	}
}
