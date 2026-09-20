package engine

import (
	"testing"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// --- DFLT-00042: a review/review_gate node that fails and loops back along
// its iteration_loop edge is itself marked AWAITING FIX (not reset to TODO),
// so "sent back, waiting on the target's rework" is distinguishable from
// "never run" by status alone. ---

// seedPlanDoneAndClaimPlanReview drives a fresh ticket's seed to the point
// where plan is DONE and plan_review has been claimed (IN REVIEW), returning
// the ticket id and catalog.
func seedPlanDoneAndClaimPlanReview(t *testing.T, e *GraphEngine, projectID string) (ticketID string, cat config.Catalog) {
	t.Helper()
	cat = baseCatalog(t)
	ticket, err := e.CreateTicket(projectID, "title", "")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	if len(exec) != 1 || exec[0].Type != domain.NodeTypePlan {
		t.Fatalf("expected the plan node, got %+v", exec)
	}
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(plan): %v", err)
	}
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	if len(exec) != 1 || exec[0].Type != domain.NodeTypeReview || exec[0].Status != domain.NodeInReview {
		t.Fatalf("expected plan_review to be claimed IN REVIEW, got %+v", exec)
	}
	return ticket.ID, cat
}

// investigationGraphWithReviewGateClaimed drives investigationGraph to the
// point where investigation is DONE and investigation_review (review_gate)
// has been claimed (IN REVIEW).
func investigationGraphWithReviewGateClaimed(t *testing.T, e *GraphEngine, projectID string) (ticketID string, cat config.Catalog) {
	t.Helper()
	ticketID, cat = investigationGraph(t, e, projectID)
	exec, _ := e.GetExecutableNodes(ticketID, cat)
	if len(exec) != 1 || exec[0].Type != domain.NodeTypeInvestigation {
		t.Fatalf("expected the investigation node, got %+v", exec)
	}
	if _, err := e.CompleteNode(exec[0].ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(investigation): %v", err)
	}
	exec, _ = e.GetExecutableNodes(ticketID, cat)
	if len(exec) != 1 || exec[0].Type != domain.NodeTypeReviewGate || exec[0].Status != domain.NodeInReview {
		t.Fatalf("expected investigation_review to be claimed IN REVIEW, got %+v", exec)
	}
	return ticketID, cat
}

func executableIDs(nodes []domain.GraphNode) map[string]domain.GraphNode {
	m := make(map[string]domain.GraphNode, len(nodes))
	for _, n := range nodes {
		m[n.ID] = n
	}
	return m
}

func setNodeStatus(t *testing.T, repo store.GraphRepository, nodeID string, status domain.NodeStatus) {
	t.Helper()
	if _, err := repo.UpdateNode(nodeID, store.NodePatch{Status: &status}); err != nil {
		t.Fatalf("UpdateNode(%s -> %s): %v", nodeID, status, err)
	}
}

type loopBackCase struct {
	name       string
	nodeType   domain.NodeType
	reviewerID string // config id
	targetID   string // config id
	setup      func(t *testing.T, e *GraphEngine, projectID string) (string, config.Catalog)
}

var loopBackCases = []loopBackCase{
	{name: "review", nodeType: domain.NodeTypeReview, reviewerID: "plan_review", targetID: "plan", setup: seedPlanDoneAndClaimPlanReview},
	{name: "review_gate", nodeType: domain.NodeTypeReviewGate, reviewerID: "investigation_review", targetID: "investigation", setup: investigationGraphWithReviewGateClaimed},
}

func TestCompleteNode_LoopBackMarksReviewerAwaitingFix(t *testing.T) {
	for _, tc := range loopBackCases {
		t.Run(tc.name, func(t *testing.T) {
			e, repo, projectID := newTestEngine(t)
			ticketID, _ := tc.setup(t, e, projectID)
			reviewer := nodeByConfigID(t, repo, ticketID, tc.reviewerID)
			if reviewer.Type != tc.nodeType {
				t.Fatalf("expected %s to be a %s node, got %s", tc.reviewerID, tc.nodeType, reviewer.Type)
			}
			if target := nodeByConfigID(t, repo, ticketID, tc.targetID); target.IterationCount != 0 {
				t.Fatalf("expected %s iteration_count 0 before the loop-back, got %d", tc.targetID, target.IterationCount)
			}

			res, err := e.CompleteNode(reviewer.ID, false, nil)
			if err != nil {
				t.Fatalf("CompleteNode(%s, fail): %v", tc.reviewerID, err)
			}
			if res.NextStatus != "AWAITING FIX" || !res.LoopedBack {
				t.Fatalf("expected {AWAITING FIX true}, got %+v", res)
			}
			if got := nodeByConfigID(t, repo, ticketID, tc.reviewerID); got.Status != domain.NodeAwaitingFix {
				t.Errorf("expected %s to be AWAITING FIX, got %s", tc.reviewerID, got.Status)
			}
			target := nodeByConfigID(t, repo, ticketID, tc.targetID)
			if target.Status != domain.NodeTODO {
				t.Errorf("expected %s to be reset to TODO, got %s", tc.targetID, target.Status)
			}
			if target.IterationCount != 1 {
				t.Errorf("expected %s iteration_count 1, got %d", tc.targetID, target.IterationCount)
			}
			ticket, _ := repo.GetTicket(ticketID)
			if ticket.Blocked {
				t.Errorf("a loop-back within max_iterations must not block the ticket")
			}
		})
	}
}

// TestGetExecutableNodes_AwaitingFixReviewerClaimedOnceTargetDone covers the
// whole re-dispatch cycle: the AWAITING FIX reviewer is not claimed while its
// target is being reworked, is claimed as IN REVIEW once the target is DONE,
// becomes AWAITING FIX again on a second failure (iteration_count 2), and on
// a pass goes to DONE.
func TestGetExecutableNodes_AwaitingFixReviewerClaimedOnceTargetDone(t *testing.T) {
	for _, tc := range loopBackCases {
		t.Run(tc.name, func(t *testing.T) {
			e, repo, projectID := newTestEngine(t)
			ticketID, cat := tc.setup(t, e, projectID)
			reviewer := nodeByConfigID(t, repo, ticketID, tc.reviewerID)
			target := nodeByConfigID(t, repo, ticketID, tc.targetID)

			for iteration := 1; iteration <= 2; iteration++ {
				res, err := e.CompleteNode(reviewer.ID, false, nil)
				if err != nil {
					t.Fatalf("iteration %d: CompleteNode(fail): %v", iteration, err)
				}
				if res.NextStatus != "AWAITING FIX" || !res.LoopedBack {
					t.Fatalf("iteration %d: expected {AWAITING FIX true}, got %+v", iteration, res)
				}
				if got, _ := repo.GetNode(target.ID); got.Status != domain.NodeTODO || got.IterationCount != iteration {
					t.Fatalf("iteration %d: expected target TODO with iteration_count %d, got %s / %d", iteration, iteration, got.Status, got.IterationCount)
				}

				// While the target is being reworked, only the target is claimed.
				exec, err := e.GetExecutableNodes(ticketID, cat)
				if err != nil {
					t.Fatalf("GetExecutableNodes: %v", err)
				}
				ids := executableIDs(exec)
				if _, ok := ids[target.ID]; !ok || len(exec) != 1 {
					t.Fatalf("iteration %d: expected only %s to be claimed, got %+v", iteration, tc.targetID, exec)
				}
				if got, _ := repo.GetNode(reviewer.ID); got.Status != domain.NodeAwaitingFix {
					t.Fatalf("iteration %d: expected %s to stay AWAITING FIX while its target is reworked, got %s", iteration, tc.reviewerID, got.Status)
				}

				if _, err := e.CompleteNode(target.ID, true, nil); err != nil {
					t.Fatalf("CompleteNode(target): %v", err)
				}

				// Target DONE again: the AWAITING FIX reviewer is claimed as IN REVIEW.
				exec, err = e.GetExecutableNodes(ticketID, cat)
				if err != nil {
					t.Fatalf("GetExecutableNodes: %v", err)
				}
				claimed, ok := executableIDs(exec)[reviewer.ID]
				if !ok {
					t.Fatalf("iteration %d: expected %s to be re-claimed once its target is DONE, got %+v", iteration, tc.reviewerID, exec)
				}
				if claimed.Status != domain.NodeInReview {
					t.Fatalf("iteration %d: expected the re-claimed reviewer to be IN REVIEW, got %s", iteration, claimed.Status)
				}
			}

			res, err := e.CompleteNode(reviewer.ID, true, nil)
			if err != nil {
				t.Fatalf("CompleteNode(pass): %v", err)
			}
			if res.NextStatus != "DONE" || res.LoopedBack {
				t.Fatalf("expected {DONE false}, got %+v", res)
			}
			if got, _ := repo.GetNode(reviewer.ID); got.Status != domain.NodeDone {
				t.Errorf("expected the reviewer to be DONE after passing, got %s", got.Status)
			}
		})
	}
}

// TestGetExecutableNodes_AwaitingFixSuccessorAdvancesAfterPass checks that a
// review_gate re-claimed from AWAITING FIX that then passes lets its success
// successor become executable.
func TestGetExecutableNodes_AwaitingFixSuccessorAdvancesAfterPass(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, cat := investigationGraphWithReviewGateClaimed(t, e, projectID)
	reviewer := nodeByConfigID(t, repo, ticketID, "investigation_review")
	target := nodeByConfigID(t, repo, ticketID, "investigation")

	if _, err := e.CompleteNode(reviewer.ID, false, nil); err != nil {
		t.Fatalf("CompleteNode(fail): %v", err)
	}
	e.GetExecutableNodes(ticketID, cat) // claims investigation
	if _, err := e.CompleteNode(target.ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(investigation): %v", err)
	}
	e.GetExecutableNodes(ticketID, cat) // claims investigation_review
	if _, err := e.CompleteNode(reviewer.ID, true, nil); err != nil {
		t.Fatalf("CompleteNode(pass): %v", err)
	}

	// release (the successor) is manual so it's never auto-claimed; its
	// prerequisite being DONE is what "advances" means here.
	if got, _ := repo.GetNode(reviewer.ID); got.Status != domain.NodeDone {
		t.Fatalf("expected investigation_review DONE, got %s", got.Status)
	}
	exec, _ := e.GetExecutableNodes(ticketID, cat)
	if len(exec) != 0 {
		t.Fatalf("expected nothing auto-claimable (only the manual release remains), got %+v", exec)
	}
	release := nodeByConfigID(t, repo, ticketID, "release")
	if release.Status != domain.NodeTODO {
		t.Errorf("expected release to remain TODO, got %s", release.Status)
	}
}

// implCodeReviewGraph expands the seed with impl -> code_review (review_gate,
// loop_back_to impl).
func implCodeReviewGraph(t *testing.T, e *GraphEngine, projectID string) (ticketID string, cat config.Catalog) {
	t.Helper()
	cat = baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")
	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	e.CompleteNode(exec[0].ID, true, nil) // plan
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	e.CompleteNode(exec[0].ID, true, nil) // plan_review
	patch := &Patch{ExtraNodes: []ExtraNode{
		{ID: "impl", Type: "implementation", Name: "Implementation", DependsOn: []string{"plan_review"}},
		{ID: "code_review", Type: "review_gate", GateRef: "code_review", DependsOn: []string{"impl"}, LoopBackTo: "impl"},
	}}
	if err := e.ExpandGraph(ticket.ID, cat, patch); err != nil {
		t.Fatalf("ExpandGraph: %v", err)
	}
	return ticket.ID, cat
}

func TestGetExecutableNodes_AwaitingFixNotClaimedWhilePrereqInProgress(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, cat := implCodeReviewGraph(t, e, projectID)
	impl := nodeByConfigID(t, repo, ticketID, "impl")
	codeReview := nodeByConfigID(t, repo, ticketID, "code_review")
	setNodeStatus(t, repo, impl.ID, domain.NodeInProgress)
	setNodeStatus(t, repo, codeReview.ID, domain.NodeAwaitingFix)

	exec, err := e.GetExecutableNodes(ticketID, cat)
	if err != nil {
		t.Fatalf("GetExecutableNodes: %v", err)
	}
	if _, ok := executableIDs(exec)[codeReview.ID]; ok {
		t.Fatalf("expected code_review not to be claimed while impl is IN PROGRESS, got %+v", exec)
	}
	if got, _ := repo.GetNode(codeReview.ID); got.Status != domain.NodeAwaitingFix {
		t.Errorf("expected code_review to stay AWAITING FIX, got %s", got.Status)
	}
}

// TestGetExecutableNodes_LegacyTodoReviewerStillReclaimed covers completion
// criterion 7: a reviewer looped back by the pre-DFLT-00042 engine (left at
// TODO, target TODO with iteration_count 1) is not migrated, and re-dispatch
// still proceeds as before.
func TestGetExecutableNodes_LegacyTodoReviewerStillReclaimed(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, cat := seedPlanDoneAndClaimPlanReview(t, e, projectID)
	plan := nodeByConfigID(t, repo, ticketID, "plan")
	planReview := nodeByConfigID(t, repo, ticketID, "plan_review")
	todo := domain.NodeTODO
	one := 1
	if _, err := repo.UpdateNode(plan.ID, store.NodePatch{Status: &todo, IterationCount: &one}); err != nil {
		t.Fatalf("UpdateNode(plan): %v", err)
	}
	setNodeStatus(t, repo, planReview.ID, domain.NodeTODO)

	exec, _ := e.GetExecutableNodes(ticketID, cat)
	if len(exec) != 1 || exec[0].ID != plan.ID {
		t.Fatalf("expected only plan to be claimed, got %+v", exec)
	}
	e.CompleteNode(plan.ID, true, nil)
	exec, _ = e.GetExecutableNodes(ticketID, cat)
	if len(exec) != 1 || exec[0].ID != planReview.ID || exec[0].Status != domain.NodeInReview {
		t.Fatalf("expected the legacy TODO plan_review to be claimed IN REVIEW, got %+v", exec)
	}
}

// TestCompleteNode_ExceedingMaxIterationsLeavesStatusesUnchanged is the
// completion-criterion-4 regression: the blocking failure (target already at
// max_iterations) must not mark the claimed reviewer AWAITING FIX.
//
// This is the minimal graph (plan -> plan_review, nothing in between), so
// DFLT-00101's intermediate-node rewind cannot apply here and the
// expectations below are unchanged by it. The multi-node counterpart -- a
// blocked loop-back leaving the nodes between target and reviewer untouched
// too -- is TestCompleteNode_LoopBackAtIterationLimitRewindsNothing.
func TestCompleteNode_ExceedingMaxIterationsLeavesStatusesUnchanged(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, cat := seedPlanDoneAndClaimPlanReview(t, e, projectID)
	plan := nodeByConfigID(t, repo, ticketID, "plan")
	planReview := nodeByConfigID(t, repo, ticketID, "plan_review")

	// max_iterations is 3: three loop-backs, each followed by a real re-run.
	for i := 1; i <= plan.MaxIterations; i++ {
		res, _ := e.CompleteNode(planReview.ID, false, nil)
		if res.NextStatus != "AWAITING FIX" {
			t.Fatalf("loop-back %d: expected AWAITING FIX, got %+v", i, res)
		}
		e.GetExecutableNodes(ticketID, cat) // claims plan
		e.CompleteNode(plan.ID, true, nil)
		exec, _ := e.GetExecutableNodes(ticketID, cat) // claims plan_review
		if len(exec) != 1 || exec[0].ID != planReview.ID {
			t.Fatalf("loop-back %d: expected plan_review to be re-claimed, got %+v", i, exec)
		}
	}
	if got, _ := repo.GetNode(plan.ID); got.IterationCount != plan.MaxIterations || got.Status != domain.NodeDone {
		t.Fatalf("precondition: expected plan DONE at iteration_count %d, got %s / %d", plan.MaxIterations, got.Status, got.IterationCount)
	}

	res, err := e.CompleteNode(planReview.ID, false, nil)
	if err != nil {
		t.Fatalf("CompleteNode(fail): %v", err)
	}
	if res.NextStatus != "BLOCKED" || res.LoopedBack {
		t.Fatalf("expected {BLOCKED false}, got %+v", res)
	}
	ticket, _ := repo.GetTicket(ticketID)
	if !ticket.Blocked {
		t.Errorf("expected the ticket to be blocked")
	}
	if got, _ := repo.GetNode(planReview.ID); got.Status != domain.NodeInReview {
		t.Errorf("expected plan_review to stay IN REVIEW (not AWAITING FIX), got %s", got.Status)
	}
	if got, _ := repo.GetNode(plan.ID); got.Status != domain.NodeDone || got.IterationCount != plan.MaxIterations {
		t.Errorf("expected plan to stay DONE at iteration_count %d, got %s / %d", plan.MaxIterations, got.Status, got.IterationCount)
	}
}

func TestCompleteNode_NoLoopEdgeBlocksWithoutAwaitingFix(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := baseCatalog(t)
	ticket, _ := e.CreateTicket(projectID, "title", "")
	exec, _ := e.GetExecutableNodes(ticket.ID, cat)
	e.CompleteNode(exec[0].ID, true, nil) // plan
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	e.CompleteNode(exec[0].ID, true, nil) // plan_review
	patch := &Patch{ExtraNodes: []ExtraNode{
		{ID: "extra_review", Type: "review", Name: "Extra Review", DependsOn: []string{"plan_review"}},
	}}
	if err := e.ExpandGraph(ticket.ID, cat, patch); err != nil {
		t.Fatalf("ExpandGraph: %v", err)
	}
	exec, _ = e.GetExecutableNodes(ticket.ID, cat)
	if len(exec) != 1 || exec[0].Status != domain.NodeInReview {
		t.Fatalf("expected extra_review to be claimed IN REVIEW, got %+v", exec)
	}
	extra := exec[0]

	res, err := e.CompleteNode(extra.ID, false, nil)
	if err != nil {
		t.Fatalf("CompleteNode(fail): %v", err)
	}
	if res.NextStatus != "BLOCKED" || res.LoopedBack {
		t.Fatalf("expected {BLOCKED false}, got %+v", res)
	}
	got, _ := repo.GetTicket(ticket.ID)
	if !got.Blocked {
		t.Errorf("expected the ticket to be blocked")
	}
	if n, _ := repo.GetNode(extra.ID); n.Status != domain.NodeInReview {
		t.Errorf("expected extra_review to stay IN REVIEW (not AWAITING FIX), got %s", n.Status)
	}
}

// TestSyncTicketStatus_AwaitingFixTreatedLikeTodo covers completion criterion
// 5. syncTicketStatus leaves the ticket untouched when no branch matches, so
// every case first pins the ticket status to a known value (TODO).
func TestSyncTicketStatus_AwaitingFixTreatedLikeTodo(t *testing.T) {
	cases := []struct {
		name       string
		targetCfg  string
		others     func(cfg string) domain.NodeStatus
		target     domain.NodeStatus
		wantTicket domain.TicketStatus
	}{
		{"others DONE, target TODO", "investigation_review", func(string) domain.NodeStatus { return domain.NodeDone }, domain.NodeTODO, domain.TicketInProgress},
		{"others DONE, target AWAITING FIX", "investigation_review", func(string) domain.NodeStatus { return domain.NodeDone }, domain.NodeAwaitingFix, domain.TicketInProgress},
		{"others TODO, target TODO", "investigation_review", func(string) domain.NodeStatus { return domain.NodeTODO }, domain.NodeTODO, domain.TicketTODO},
		{"others TODO, target AWAITING FIX", "investigation_review", func(string) domain.NodeStatus { return domain.NodeTODO }, domain.NodeAwaitingFix, domain.TicketTODO},
		// Not mistaken for IN REVIEW: plan DONE, the rest TODO, plan_review varied.
		// A `review` node's own IN REVIEW status no longer puts the ticket
		// itself into IN REVIEW (DFLT-00046 reserves that for a pending
		// approval_gate) -- plan_review IN REVIEW falls into the IN PROGRESS
		// catch-all just like AWAITING FIX/TODO do.
		{"plan DONE, plan_review AWAITING FIX", "plan_review", planDoneRestTodo, domain.NodeAwaitingFix, domain.TicketInProgress},
		{"plan DONE, plan_review TODO", "plan_review", planDoneRestTodo, domain.NodeTODO, domain.TicketInProgress},
		{"plan DONE, plan_review IN REVIEW", "plan_review", planDoneRestTodo, domain.NodeInReview, domain.TicketInProgress},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, repo, projectID := newTestEngine(t)
			ticketID, _ := investigationGraph(t, e, projectID)
			nodes, _ := repo.ListNodesByTicket(ticketID)
			for _, n := range nodes {
				status := tc.others(*n.ConfigID)
				if *n.ConfigID == tc.targetCfg {
					status = tc.target
				}
				setNodeStatus(t, repo, n.ID, status)
			}
			known := domain.TicketTODO
			if _, err := repo.UpdateTicket(ticketID, store.TicketPatch{Status: &known}); err != nil {
				t.Fatalf("UpdateTicket: %v", err)
			}

			if err := e.syncTicketStatus(ticketID); err != nil {
				t.Fatalf("syncTicketStatus: %v", err)
			}
			got, _ := repo.GetTicket(ticketID)
			if got.Status != tc.wantTicket {
				t.Errorf("expected ticket status %s, got %s", tc.wantTicket, got.Status)
			}
			if tc.target == domain.NodeAwaitingFix && got.Status == domain.TicketDone {
				t.Errorf("an AWAITING FIX node must keep the ticket from being DONE")
			}
		})
	}
}

func planDoneRestTodo(cfg string) domain.NodeStatus {
	if cfg == "plan" {
		return domain.NodeDone
	}
	return domain.NodeTODO
}
