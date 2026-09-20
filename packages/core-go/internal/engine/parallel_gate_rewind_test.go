package engine

import (
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// --- DFLT-00119: the shape process-ticket actually runs -- four review_gates
// hanging off one `impl` in parallel, each with loop_back_to: impl -- against
// the two things that used to go wrong there.
//
// Harm 1, a gate's verdict outliving the code it judged: one gate rejecting
// reset `impl` but left its siblings DONE, so the rewritten implementation
// went past gates that had never seen it. In DFLT-00100 the ticket's most
// consequential change reached release approval without ever passing code
// review.
//
// Harm 2, a ticket blocking before its first rework: N gates rejecting over
// one defect each spent an iteration of `impl`, so with the default budget of
// 3 and four gates a ticket could block on its first round -- and could not be
// recovered from the CLI, because reopen-nodes refused the loop target for
// being TODO rather than DONE (DFLT-00103).
//
// Cases (a)-(g) below are the ticket's completion criterion 5, in order. ---

// defaultWorkflowAtParallelGates drives a fresh ticket through the *default*
// workflow -- ExpandGraph with no patch, i.e. exactly the shape
// internal/config/defaults/workflow.yaml defines -- to the moment the four
// review gates are all claimed IN REVIEW off a DONE `impl`, with gherkin_test
// and test_review still TODO behind them. That is the state a rejection
// actually arrives in. The gates come back in the order GetExecutableNodes
// returned them.
func defaultWorkflowAtParallelGates(t *testing.T, e *GraphEngine, projectID string) (ticketID string, cat config.Catalog, gates []domain.GraphNode) {
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
	planApproval := nodeByConfigID(t, e.repo, ticketID, "plan_approval")
	if _, err := e.CompleteNode(planApproval.ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(plan_approval): %v", err)
	}
	completeAllExecutable(t, e, ticketID, cat, 1) // gherkin_spec
	completeAllExecutable(t, e, ticketID, cat, 1) // gherkin_review
	completeAllExecutable(t, e, ticketID, cat, 1) // impl

	gates, err = e.GetExecutableNodes(ticketID, cat)
	if err != nil {
		t.Fatalf("GetExecutableNodes: %v", err)
	}
	if len(gates) != 4 {
		t.Fatalf("expected the four review gates to be claimed in parallel, got %v", execConfigIDs(gates))
	}
	for _, g := range gates {
		if g.Status != domain.NodeInReview {
			t.Fatalf("expected %s to be claimed IN REVIEW, got %s", *g.ConfigID, g.Status)
		}
	}
	return ticketID, cat, gates
}

// reworkImplAndClaimGates completes the rewound `impl` and takes the four
// gates back out of get-executable, returning them claimed IN REVIEW again --
// one full round of rework.
func reworkImplAndClaimGates(t *testing.T, e *GraphEngine, ticketID string, cat config.Catalog) []domain.GraphNode {
	t.Helper()
	exec, err := e.GetExecutableNodes(ticketID, cat)
	if err != nil {
		t.Fatalf("GetExecutableNodes: %v", err)
	}
	if len(exec) != 1 || *exec[0].ConfigID != "impl" {
		t.Fatalf("expected impl alone to be executable after the loop-back, got %v", execConfigIDs(exec))
	}
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(impl rework): %v", err)
	}
	gates, err := e.GetExecutableNodes(ticketID, cat)
	if err != nil {
		t.Fatalf("GetExecutableNodes: %v", err)
	}
	if len(gates) != 4 {
		t.Fatalf("expected all four gates to be re-offered after the rework, got %v", execConfigIDs(gates))
	}
	return gates
}

// TestParallelGates_OneRejectionRewindsEveryGateAndTest is cases (a) and (b):
// one gate rejecting sends `impl` back along with its three siblings,
// `gherkin_test` and `test_review` -- everything in the loop target's forward
// closure -- and only `impl`'s iteration_count moves.
//
// This is the harm-1 regression. Before DFLT-00119 the three siblings and,
// when the failing node was a gate, gherkin_test/test_review kept their DONE
// status, so the next implementation was never put in front of them.
func TestParallelGates_OneRejectionRewindsEveryGateAndTest(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	// Start from the state where gherkin_test has run and test_review is
	// judging it, so the rewind has something to undo past the gates too.
	ticketID, _ := defaultWorkflowAtTestReview(t, e, projectID)
	codeReview := nodeByConfigID(t, repo, ticketID, "code_review")
	setNodeStatus(t, repo, codeReview.ID, domain.NodeInReview)

	res, err := e.CompleteNode(codeReview.ID, false, nil)
	if err != nil {
		t.Fatalf("CompleteNode(code_review, fail): %v", err)
	}
	if res.NextStatus != "AWAITING FIX" || !res.LoopedBack {
		t.Fatalf("expected {AWAITING FIX true}, got %+v", res)
	}

	// (a) the loop target and everything downstream of it.
	assertNodeStatus(t, repo, ticketID, "impl", domain.NodeTODO, 1)
	for _, cfg := range []string{"qa_review", "security_review", "non_functional_review", "gherkin_test", "test_review"} {
		// (b) their own iteration counts stay where they were: the loop
		// target's count is the loop's counter, not each node's.
		assertNodeStatus(t, repo, ticketID, cfg, domain.NodeTODO, 0)
	}
	// The gate that rejected is AWAITING FIX, not TODO, so "sent it back" is
	// distinguishable from "never ran" (DFLT-00042).
	assertNodeStatus(t, repo, ticketID, "code_review", domain.NodeAwaitingFix, 0)

	// A loop-back is a retry, not a failure.
	ticket, _ := repo.GetTicket(ticketID)
	if ticket.Blocked {
		t.Error("expected the ticket to stay unblocked after a loop-back")
	}
}

// TestParallelGates_ReworkReoffersAllFourGates is case (c): once the rewound
// `impl` is done again, every gate is handed out again -- including the three
// that had already passed on the previous implementation.
func TestParallelGates_ReworkReoffersAllFourGates(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, cat, gates := defaultWorkflowAtParallelGates(t, e, projectID)

	if _, err := e.CompleteNode(gates[0].ID, false, nil); err != nil {
		t.Fatalf("CompleteNode(%s, fail): %v", *gates[0].ConfigID, err)
	}

	again := reworkImplAndClaimGates(t, e, ticketID, cat)
	got := map[string]bool{}
	for _, g := range again {
		got[*g.ConfigID] = true
	}
	for _, cfg := range []string{"code_review", "qa_review", "security_review", "non_functional_review"} {
		if !got[cfg] {
			t.Errorf("expected %s to be re-offered after the rework, got %v", cfg, execConfigIDs(again))
		}
	}
	// The gate that rejected comes back from AWAITING FIX, claimed like the
	// rest rather than needing unstick-node, and the round cost exactly one
	// of impl's iterations.
	assertNodeStatus(t, repo, ticketID, "impl", domain.NodeDone, 1)
}

// TestParallelGates_WholeRoundSpendsOneIteration is case (d): all four gates
// rejecting over the same round costs `impl` a single iteration, and the
// ticket does not block.
//
// This is the harm-2 regression. The first rejection is what opens the round;
// it also rewinds the other three gates to TODO, so their own CompleteNode
// calls -- the verdicts their subagents were still producing against the old
// implementation -- are refused outright and never reach the loop-back branch
// at all. Both halves are pinned here, because it is the pair that makes the
// count come out at 1.
func TestParallelGates_WholeRoundSpendsOneIteration(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _, gates := defaultWorkflowAtParallelGates(t, e, projectID)

	if _, err := e.CompleteNode(gates[0].ID, false, nil); err != nil {
		t.Fatalf("CompleteNode(%s, fail): %v", *gates[0].ConfigID, err)
	}
	for _, g := range gates[1:] {
		_, err := e.CompleteNode(g.ID, false, nil)
		assertInvalidNodeState(t, err)
	}

	assertNodeStatus(t, repo, ticketID, "impl", domain.NodeTODO, 1)
	ticket, _ := repo.GetTicket(ticketID)
	if ticket.Blocked {
		t.Error("expected four rejections in one round not to block the ticket")
	}
}

// TestParallelGates_BudgetSurvivesFourGates is case (e): with
// max_iterations 3 and four parallel gates, `impl` can genuinely be redone
// three times, and only the fourth round blocks. Before DFLT-00119 the first
// round alone spent four iterations and blocked the ticket outright.
func TestParallelGates_BudgetSurvivesFourGates(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, cat, gates := defaultWorkflowAtParallelGates(t, e, projectID)
	impl := nodeByConfigID(t, repo, ticketID, "impl")
	if impl.MaxIterations != 3 {
		t.Fatalf("precondition: expected the default max_iterations of 3, got %d", impl.MaxIterations)
	}

	for round := 1; round <= 3; round++ {
		res, err := e.CompleteNode(gates[0].ID, false, nil)
		if err != nil {
			t.Fatalf("round %d: CompleteNode(%s, fail): %v", round, *gates[0].ConfigID, err)
		}
		if res.NextStatus != "AWAITING FIX" || !res.LoopedBack {
			t.Fatalf("round %d: expected {AWAITING FIX true}, got %+v", round, res)
		}
		assertNodeStatus(t, repo, ticketID, "impl", domain.NodeTODO, round)
		if ticket, _ := repo.GetTicket(ticketID); ticket.Blocked {
			t.Fatalf("round %d: the ticket blocked before its budget was spent", round)
		}
		gates = reworkImplAndClaimGates(t, e, ticketID, cat)
	}

	// Fourth round: the budget is gone.
	res, err := e.CompleteNode(gates[0].ID, false, nil)
	if err != nil {
		t.Fatalf("round 4: CompleteNode(%s, fail): %v", *gates[0].ConfigID, err)
	}
	if res.NextStatus != "BLOCKED" || res.LoopedBack {
		t.Fatalf("round 4: expected {BLOCKED false}, got %+v", res)
	}
	if ticket, _ := repo.GetTicket(ticketID); !ticket.Blocked {
		t.Error("round 4: expected the ticket to block once the budget was spent")
	}
	// Blocking writes nothing: impl keeps the DONE status and the count it
	// had, and the gate that rejected keeps its claim.
	assertNodeStatus(t, repo, ticketID, "impl", domain.NodeDone, 3)
	if got, _ := repo.GetNode(gates[0].ID); got.Status != domain.NodeInReview {
		t.Errorf("expected the rejecting gate to stay IN REVIEW when the loop-back never happened, got %s", got.Status)
	}
}

// TestCompleteNode_LateVerdictFromRewoundGateIsRefused is case (f): a gate
// that was IN REVIEW when the rewind swept it up is put back to TODO, and the
// verdict its subagent delivers afterwards -- against the implementation that
// has since been replaced -- is refused with nothing written at all, artifacts
// included.
//
// Recording it is exactly harm 1 in a different guise: a pass would mark the
// gate DONE for code it never saw. The refusal is checkCompletable's
// automatic-node-at-TODO rule (DFLT-00102), reused rather than duplicated so
// the set of completable states stays defined in one place.
func TestCompleteNode_LateVerdictFromRewoundGateIsRefused(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _, gates := defaultWorkflowAtParallelGates(t, e, projectID)
	failing, late := gates[0], gates[1]

	if _, err := e.CompleteNode(failing.ID, false, nil); err != nil {
		t.Fatalf("CompleteNode(%s, fail): %v", *failing.ConfigID, err)
	}
	if got, _ := repo.GetNode(late.ID); got.Status != domain.NodeTODO {
		t.Fatalf("expected the claimed sibling gate to be rewound to TODO, got %s", got.Status)
	}

	verdict := "passed on the implementation that has since been redone"
	_, err := e.CompleteNode(late.ID, true, []domain.Artifact{
		{Name: "review", Type: domain.ArtifactText, Content: &verdict},
	})
	apiErr := assertInvalidNodeState(t, err)
	// The message has to let process-ticket tell this apart from a genuine
	// misuse: it is routine after a rejection, and the answer is to go back to
	// get-executable rather than to report a failure.
	for _, want := range []string{"loop-back", "get-executable"} {
		if !strings.Contains(apiErr.Message, want) {
			t.Errorf("refusal %q does not mention %q", apiErr.Message, want)
		}
	}

	got, _ := repo.GetNode(late.ID)
	if got.Status != domain.NodeTODO {
		t.Errorf("the refused verdict moved the gate to %s", got.Status)
	}
	if got.IterationCount != 0 {
		t.Errorf("the refused verdict bumped the gate's iteration_count to %d", got.IterationCount)
	}
	arts, err := repo.ListArtifactsByNode(late.ID)
	if err != nil {
		t.Fatalf("ListArtifactsByNode: %v", err)
	}
	if len(arts) != 0 {
		t.Errorf("the refused verdict wrote %d artifact(s): %+v", len(arts), arts)
	}
	assertNodeStatus(t, repo, ticketID, "impl", domain.NodeTODO, 1)
}

// TestReopenNodes_RecoversBlockedTicketWithTodoLoopTarget is case (g): a
// ticket blocked with its loop target already at TODO is recoverable with
// grant-iterations followed by reopen-nodes, and resetting a node that is
// already TODO costs no iteration.
//
// This is the state DFLT-00103 was stuck in, and the one reopen-nodes used to
// refuse outright ("not DONE or REJECTED") -- leaving the Web UI's status
// editor as the only way out. It cannot arise from the engine as it now
// stands, so it is written directly: what is under test is the recovery, not
// the route in.
func TestReopenNodes_RecoversBlockedTicketWithTodoLoopTarget(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, cat, gates := defaultWorkflowAtParallelGates(t, e, projectID)
	impl := nodeByConfigID(t, repo, ticketID, "impl")

	if _, err := e.CompleteNode(gates[0].ID, false, nil); err != nil {
		t.Fatalf("CompleteNode(%s, fail): %v", *gates[0].ConfigID, err)
	}
	// Spend the rest of the budget and block the ticket without moving impl
	// off TODO -- the shape the old double-counting produced.
	atLimit := impl.MaxIterations
	if _, err := repo.UpdateNode(impl.ID, store.NodePatch{IterationCount: &atLimit}); err != nil {
		t.Fatalf("UpdateNode(impl iteration_count): %v", err)
	}
	if err := e.blockTicket(ticketID); err != nil {
		t.Fatalf("blockTicket: %v", err)
	}
	if exec, _ := e.GetExecutableNodes(ticketID, cat); len(exec) != 0 {
		t.Fatalf("precondition: a blocked ticket must hand out nothing, got %v", execConfigIDs(exec))
	}

	// Step 1: raise the ceiling. Step 2: reopen. Nothing else.
	if _, err := e.GrantIterations(ticketID, []string{impl.ID}, 1); err != nil {
		t.Fatalf("GrantIterations: %v", err)
	}
	if _, err := e.ReopenNodes(ticketID, []string{impl.ID}); err != nil {
		t.Fatalf("expected reopen-nodes to accept a loop target at TODO: %v", err)
	}

	ticket, _ := repo.GetTicket(ticketID)
	if ticket.Blocked {
		t.Error("expected reopen-nodes to clear the blocked flag")
	}
	// A node that never ran again costs no attempt: impl is still at the
	// count it had, against the raised ceiling.
	assertNodeStatus(t, repo, ticketID, "impl", domain.NodeTODO, atLimit)
	if got, _ := repo.GetNode(impl.ID); got.MaxIterations != atLimit+1 {
		t.Errorf("expected max_iterations %d after the grant, got %d", atLimit+1, got.MaxIterations)
	}
	exec, _ := e.GetExecutableNodes(ticketID, cat)
	if len(exec) != 1 || *exec[0].ConfigID != "impl" {
		t.Fatalf("expected the recovered ticket to offer impl again, got %v", execConfigIDs(exec))
	}
}

// TestReopenNodes_SweepsPastTodoNodesToReachDoneWork pins the other half of
// widening `reopenable`: the predicate does not only decide what goes into the
// reset set, it decides where the forward-closure walk stops. A TODO node used
// to end the branch there, so DONE work sitting behind one kept a stale status
// while its input was redone. Now the walk passes through it and collects that
// work -- and bills it an iteration, since it is a result actually being
// thrown away.
func TestReopenNodes_SweepsPastTodoNodesToReachDoneWork(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _, gates := defaultWorkflowAtParallelGates(t, e, projectID)

	if _, err := e.CompleteNode(gates[0].ID, false, nil); err != nil {
		t.Fatalf("CompleteNode(%s, fail): %v", *gates[0].ConfigID, err)
	}
	// impl and every gate are TODO now. Put a DONE node behind them --
	// gherkin_test, which depends on all four gates -- to stand for work that
	// finished on a branch the rejection did not travel.
	gherkinTest := nodeByConfigID(t, repo, ticketID, "gherkin_test")
	setNodeStatus(t, repo, gherkinTest.ID, domain.NodeDone)
	if err := e.blockTicket(ticketID); err != nil {
		t.Fatalf("blockTicket: %v", err)
	}

	if _, err := e.ReopenNodes(ticketID, []string{nodeByConfigID(t, repo, ticketID, "impl").ID}); err != nil {
		t.Fatalf("ReopenNodes: %v", err)
	}

	// Reached through four gates the walk used to stop at, reset, and charged
	// an iteration for the result it is giving up.
	assertNodeStatus(t, repo, ticketID, "gherkin_test", domain.NodeTODO, 1)
	// The nodes the walk passed through are reset without being charged: the
	// loop target keeps the single iteration the loop-back gave it, and the
	// gates -- three at TODO, one at AWAITING FIX -- keep theirs at zero.
	assertNodeStatus(t, repo, ticketID, "impl", domain.NodeTODO, 1)
	for _, cfg := range []string{"code_review", "qa_review", "security_review", "non_functional_review"} {
		assertNodeStatus(t, repo, ticketID, cfg, domain.NodeTODO, 0)
	}
}

// TestCompleteNode_SecondRejectionInAnAlreadyRewoundRoundAddsNoIteration
// exercises the counting rule directly, for the state a refused late verdict
// cannot produce: a gate claimed IN REVIEW again while its loop target is
// still TODO. The gate is in the target's forward closure, so the round it is
// judging is the one already on the counter and its rejection adds nothing.
//
// The everyday path to "one round, one iteration" is the refusal in case (d);
// this rule is the layer under it, and the one that holds when a graph or a
// Web UI action produces the state another way.
func TestCompleteNode_SecondRejectionInAnAlreadyRewoundRoundAddsNoIteration(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _, gates := defaultWorkflowAtParallelGates(t, e, projectID)

	if _, err := e.CompleteNode(gates[0].ID, false, nil); err != nil {
		t.Fatalf("CompleteNode(%s, fail): %v", *gates[0].ConfigID, err)
	}
	assertNodeStatus(t, repo, ticketID, "impl", domain.NodeTODO, 1)

	second := gates[1]
	setNodeStatus(t, repo, second.ID, domain.NodeInReview)
	res, err := e.CompleteNode(second.ID, false, nil)
	if err != nil {
		t.Fatalf("CompleteNode(%s, fail): %v", *second.ConfigID, err)
	}
	if res.NextStatus != "AWAITING FIX" || !res.LoopedBack {
		t.Fatalf("expected {AWAITING FIX true}, got %+v", res)
	}

	assertNodeStatus(t, repo, ticketID, "impl", domain.NodeTODO, 1)
	assertNodeStatus(t, repo, ticketID, *second.ConfigID, domain.NodeAwaitingFix, 0)
	if ticket, _ := repo.GetTicket(ticketID); ticket.Blocked {
		t.Error("a rejection that counts nothing must not block the ticket")
	}
}

// TestCompleteNode_SidewaysLoopBackStillSpendsAnIteration is the counting
// rule's other half, and the reason it is not simply "the target is TODO".
// When the failing node is NOT in the loop target's forward closure, the
// rewind does not touch it, so it can be handed out and fail again without the
// target ever running -- its prerequisites have nothing to do with the target.
// Were those failures free, that loop would never reach max_iterations. Each
// one therefore counts, exactly as it did before DFLT-00119.
func TestCompleteNode_SidewaysLoopBackStillSpendsAnIteration(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")
	completeAllExecutable(t, e, ticket.ID, cat, 1) // plan
	completeAllExecutable(t, e, ticket.ID, cat, 1) // plan_review

	// side_task is a parallel branch off plan_review; test_review loops back
	// to it although no success edge leads from it to test_review.
	patch := &Patch{ExtraNodes: []ExtraNode{
		{ID: "side_task", Type: "documentation", Name: "Side Task", DependsOn: []string{"plan_review"}},
		{ID: "impl", Type: "implementation", Name: "Implementation", DependsOn: []string{"plan_review"}},
		{ID: "test_review", Type: "review", Name: "Test Result Review", DependsOn: []string{"impl"}, LoopBackTo: "side_task"},
	}}
	if err := e.ExpandGraph(ticket.ID, cat, patch); err != nil {
		t.Fatalf("ExpandGraph: %v", err)
	}
	completeAllExecutable(t, e, ticket.ID, cat, 2) // side_task and impl, in parallel

	sideTask := nodeByConfigID(t, repo, ticket.ID, "side_task")
	testReview := nodeByConfigID(t, repo, ticket.ID, "test_review")
	for round := 1; round <= sideTask.MaxIterations; round++ {
		setNodeStatus(t, repo, testReview.ID, domain.NodeInReview)
		res, err := e.CompleteNode(testReview.ID, false, nil)
		if err != nil {
			t.Fatalf("round %d: CompleteNode(test_review, fail): %v", round, err)
		}
		if res.NextStatus != "AWAITING FIX" || !res.LoopedBack {
			t.Fatalf("round %d: expected {AWAITING FIX true}, got %+v", round, res)
		}
		// side_task stays TODO from the first rejection onwards, and still
		// gets charged: otherwise this loop would never end.
		assertNodeStatus(t, repo, ticket.ID, "side_task", domain.NodeTODO, round)
	}

	setNodeStatus(t, repo, testReview.ID, domain.NodeInReview)
	res, err := e.CompleteNode(testReview.ID, false, nil)
	if err != nil {
		t.Fatalf("CompleteNode(test_review, fail) past the budget: %v", err)
	}
	if res.NextStatus != "BLOCKED" || res.LoopedBack {
		t.Fatalf("expected the sideways loop to block once its budget was spent, got %+v", res)
	}
}
