package engine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// --- DFLT-00140: one workflow-wide review iteration limit (3/4/5), counted
// in review rounds, and convergence tiers that follow the round. ---

// catalogWithLimit is the default catalog with the workflow-wide limit set to
// n, as if a user or team tier had set max_iterations: n.
func catalogWithLimit(t *testing.T, n int) config.Catalog {
	t.Helper()
	cat := baseCatalog(t)
	cat.MaxIterations = n
	return cat
}

// parallelGatesWithLimit is defaultWorkflowAtParallelGates for a catalog with
// limit n: the four review gates claimed IN REVIEW off a DONE impl.
func parallelGatesWithLimit(t *testing.T, e *GraphEngine, projectID string, n int) (ticketID string, cat config.Catalog, gates []domain.GraphNode) {
	t.Helper()
	cat = catalogWithLimit(t, n)
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
	if _, err := e.CompleteNode(nodeByConfigID(t, e.repo, ticketID, "plan_approval").ID, true, nil); err != nil {
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
		t.Fatalf("expected the four review gates, got %v", execConfigIDs(gates))
	}
	return ticketID, cat, gates
}

func criteriaFor(t *testing.T, repo store.GraphRepository, ticketID, configID string) string {
	t.Helper()
	node := nodeByConfigID(t, repo, ticketID, configID)
	detail, err := repo.GetTicketDetail(ticketID)
	if err != nil || detail == nil {
		t.Fatalf("GetTicketDetail: %v", err)
	}
	return GetReviewCriteria(node, *detail)
}

func setIterationCount(t *testing.T, repo store.GraphRepository, nodeID string, count int) {
	t.Helper()
	if _, err := repo.UpdateNode(nodeID, store.NodePatch{IterationCount: &count}); err != nil {
		t.Fatalf("UpdateNode(iteration_count): %v", err)
	}
}

func TestReviewTierFor_MatchesTheTierTable(t *testing.T) {
	N, I, F := TierNormal, TierImportant, TierFinal
	cases := map[int][]ReviewTier{
		3: {N, I, F, F},       // round 4 = granted past the limit
		4: {N, N, I, F, F},    // round 5 = granted
		5: {N, N, I, I, F, F}, // round 6 = granted
		// Legacy per-node values must not break anything.
		1: {F, F},
		2: {N, F, F},
		6: {N, N, I, I, I, F, F},
	}
	for limit, want := range cases {
		for i, tier := range want {
			round := i + 1
			if got := ReviewTierFor(round, limit); got != tier {
				t.Errorf("ReviewTierFor(round %d, limit %d) = %s, want %s", round, limit, got, tier)
			}
		}
	}
}

// The tier table, end to end through get-review-criteria, for a review_gate
// (code_review) and a review (test_review), both looping back to impl.
func TestGetReviewCriteria_TierTableForReviewAndReviewGate(t *testing.T) {
	want := map[int][]ReviewTier{
		3: {TierNormal, TierImportant, TierFinal},
		4: {TierNormal, TierNormal, TierImportant, TierFinal},
		5: {TierNormal, TierNormal, TierImportant, TierImportant, TierFinal},
	}
	for n, tiers := range want {
		t.Run(fmt.Sprintf("limit %d", n), func(t *testing.T) {
			e, repo, projectID := newTestEngine(t)
			ticketID, _, _ := parallelGatesWithLimit(t, e, projectID, n)
			impl := nodeByConfigID(t, repo, ticketID, "impl")
			for i, tier := range tiers {
				round := i + 1
				setIterationCount(t, repo, impl.ID, round-1)
				for _, cfg := range []string{"code_review", "test_review"} {
					out := criteriaFor(t, repo, ticketID, cfg)
					line := fmt.Sprintf("Review round: %d / %d — tier: %s", round, n, tier)
					if !strings.Contains(out, line) {
						t.Errorf("%s round %d: output lacks %q:\n%s", cfg, round, line, out)
					}
				}
			}
		})
	}
}

// Completion criterion 2: a round granted past the original limit is Final,
// and the round line shows the raised ceiling.
func TestGetReviewCriteria_GrantedRoundsAreFinal(t *testing.T) {
	cases := []struct{ n, k, round int }{
		{3, 1, 4}, {3, 2, 4}, {3, 2, 5}, {5, 1, 6},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("n%d+k%d round%d", tc.n, tc.k, tc.round), func(t *testing.T) {
			e, repo, projectID := newTestEngine(t)
			ticketID, _, _ := parallelGatesWithLimit(t, e, projectID, tc.n)
			impl := nodeByConfigID(t, repo, ticketID, "impl")
			if _, err := e.GrantIterations(ticketID, []string{impl.ID}, tc.k); err != nil {
				t.Fatalf("GrantIterations: %v", err)
			}
			setIterationCount(t, repo, impl.ID, tc.round-1)
			out := criteriaFor(t, repo, ticketID, "code_review")
			line := fmt.Sprintf("Review round: %d / %d — tier: Final", tc.round, tc.n+tc.k)
			if !strings.Contains(out, line) {
				t.Errorf("output lacks %q:\n%s", line, out)
			}
		})
	}
}

// Legacy data: the review node's own max_iterations is the tier limit, and
// odd values still give a sensible tier.
func TestGetReviewCriteria_LegacyLimits(t *testing.T) {
	cases := []struct {
		limit, round int
		tier         ReviewTier
	}{
		{1, 1, TierFinal}, {2, 1, TierNormal}, {2, 2, TierFinal},
		{6, 2, TierNormal}, {6, 3, TierImportant}, {6, 6, TierFinal},
	}
	for _, tc := range cases {
		review := domain.GraphNode{ID: "r", MaxIterations: tc.limit}
		target := domain.GraphNode{ID: "t", MaxIterations: 10, IterationCount: tc.round - 1}
		detail := domain.TicketDetail{
			Nodes: []domain.GraphNode{review, target},
			Edges: []domain.GraphEdge{{FromNodeID: "r", ToNodeID: "t", Condition: domain.EdgeLoop}},
		}
		out := GetReviewCriteria(review, detail)
		if !strings.Contains(out, "tier: "+string(tc.tier)) {
			t.Errorf("limit %d round %d: want tier %s in:\n%s", tc.limit, tc.round, tc.tier, out)
		}
	}
}

// A review with no loop target is always round 1 of its own limit.
func TestGetReviewCriteria_NoLoopTargetIsRoundOne(t *testing.T) {
	review := domain.GraphNode{ID: "r", MaxIterations: 4, IterationCount: 3}
	detail := domain.TicketDetail{
		Nodes: []domain.GraphNode{review},
		// A success edge out of the review is not a loop target.
		Edges: []domain.GraphEdge{{FromNodeID: "r", ToNodeID: "x", Condition: domain.EdgeSuccess}},
	}
	out := GetReviewCriteria(review, detail)
	if !strings.Contains(out, "Review round: 1 / 4 — tier: Normal") {
		t.Errorf("output:\n%s", out)
	}
	if strings.Contains(out, "previous review") {
		t.Errorf("round 1 must not ask for the previous review:\n%s", out)
	}
}

func TestGetReviewCriteria_OutputLayout(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _, _ := parallelGatesWithLimit(t, e, projectID, 3)
	impl := nodeByConfigID(t, repo, ticketID, "impl")

	// review_gate, round 2 of 3: gate criteria first, then the round line,
	// then the Important definition.
	setIterationCount(t, repo, impl.ID, 1)
	gate := nodeByConfigID(t, repo, ticketID, "code_review")
	out := criteriaFor(t, repo, ticketID, "code_review")
	if gate.Criteria == nil || *gate.Criteria == "" || !strings.HasPrefix(out, *gate.Criteria) {
		t.Fatalf("output must start with the gate criteria:\n%s", out)
	}
	roundAt := strings.Index(out, "Review round: 2 / 3 — tier: Important")
	defAt := strings.Index(out, reviewTierDefinitions[TierImportant])
	if roundAt < len(*gate.Criteria) || defAt < roundAt {
		t.Errorf("expected criteria, then round line, then Important definition:\n%s", out)
	}
	if !strings.Contains(out, "correctness bugs, unmet completion criteria, security problems, or regressions") ||
		!strings.Contains(out, "carry-over") {
		t.Errorf("Important definition incomplete:\n%s", out)
	}

	// review (no gate criteria), round 1 of 3: starts with the round line.
	plan := nodeByConfigID(t, repo, ticketID, "plan")
	setIterationCount(t, repo, plan.ID, 0)
	out = criteriaFor(t, repo, ticketID, "plan_review")
	if !strings.HasPrefix(out, "Review round: 1 / 3 — tier: Normal") {
		t.Errorf("review output must start with the round line:\n%s", out)
	}
	if !strings.Contains(out, "minor points included") {
		t.Errorf("Normal definition missing:\n%s", out)
	}

	// Final.
	setIterationCount(t, repo, impl.ID, 2)
	out = criteriaFor(t, repo, ticketID, "code_review")
	if !strings.Contains(out, "tier: Final") || !strings.Contains(out, "critical bugs, security vulnerabilities, data corruption, or unmet completion criteria") {
		t.Errorf("Final tier/definition missing:\n%s", out)
	}
}

// Every tier carries the never-relaxed rules and the carry-over rule; only
// round 2+ asks for the previous review and the diff since.
func TestGetReviewCriteria_FixedRulesAndPreviousRoundInstruction(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _, _ := parallelGatesWithLimit(t, e, projectID, 5)
	impl := nodeByConfigID(t, repo, ticketID, "impl")
	for round := 1; round <= 5; round++ {
		setIterationCount(t, repo, impl.ID, round-1)
		out := criteriaFor(t, repo, ticketID, "code_review")
		for _, want := range []string{
			"newly introduced by the changes made since the previous round is judged as strictly as at the Normal tier",
			"flagged in a previous round that is still not fixed fails the review",
			"under the last heading of the review template",
			"conditional approval",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("round %d: output lacks %q", round, want)
			}
		}
		hasPrev := strings.Contains(out, reviewPreviousRoundText)
		if round == 1 && hasPrev {
			t.Errorf("round 1 must not ask for the previous review")
		}
		if round >= 2 && !hasPrev {
			t.Errorf("round %d must ask for the previous review and the diff since", round)
		}
	}
}

// A round 2+ review can have no earlier review of its own (QA review of
// DFLT-00140): a parallel gate rewound by a sibling before its verdict was
// recorded, or test_review reached only after the gates already sent impl
// back. The round-2+ instruction must then say to judge the whole output at
// the given tier and take the diff base from the loop target's previous
// round, while keeping the never-relaxed rules.
func TestGetReviewCriteria_RoundTwoWithoutOwnPreviousReview(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _, gates := parallelGatesWithLimit(t, e, projectID, 3)
	if res, err := e.CompleteNode(gates[0].ID, false, nil); err != nil || !res.LoopedBack {
		t.Fatalf("first rejection: %+v, %v", res, err)
	}
	wantFallback := []string{
		"If this node has no previous review of its own",
		"judge the whole output under review at this round's tier",
		"take the diff base from the loop target's previous round",
		"A draft this node saved in a round whose verdict was refused is not a previous review",
		"The never-relaxed rules still apply in full to everything that diff introduced.",
	}
	check := func(configID string) {
		t.Helper()
		node := nodeByConfigID(t, repo, ticketID, configID)
		arts, err := repo.ListArtifactsByNode(node.ID)
		if err != nil {
			t.Fatalf("ListArtifactsByNode: %v", err)
		}
		if len(arts) != 0 {
			t.Fatalf("%s: expected no earlier review of its own, got %d artifacts", configID, len(arts))
		}
		out := criteriaFor(t, repo, ticketID, configID)
		if !strings.Contains(out, "Review round: 2 / 3") {
			t.Errorf("%s: expected round 2 / 3:\n%s", configID, out)
		}
		for _, want := range append(wantFallback, "newly introduced by the changes made since the previous round is judged as strictly as at the Normal tier") {
			if !strings.Contains(out, want) {
				t.Errorf("%s: output lacks %q:\n%s", configID, want, out)
			}
		}
	}
	// The sibling gates never recorded a verdict in round 1.
	for _, g := range gates[1:] {
		check(*g.ConfigID)
	}
	// test_review has never run at all, yet its loop target was already redone.
	check("test_review")
	// Round 1 never carries the fallback.
	setIterationCount(t, repo, nodeByConfigID(t, repo, ticketID, "impl").ID, 0)
	if out := criteriaFor(t, repo, ticketID, "test_review"); strings.Contains(out, wantFallback[0]) {
		t.Errorf("round 1 must not carry the no-previous-review fallback:\n%s", out)
	}
}

// Completion criterion 3: with limit N, rounds 1..N-1 loop back and round
// N's failure blocks, writing nothing.
func TestCompleteNode_BlocksInRoundNForEachLimit(t *testing.T) {
	for _, n := range []int{3, 4, 5} {
		t.Run(fmt.Sprintf("limit %d", n), func(t *testing.T) {
			e, repo, projectID := newTestEngine(t)
			ticketID, cat, gates := parallelGatesWithLimit(t, e, projectID, n)
			for round := 1; round < n; round++ {
				res, err := e.CompleteNode(gates[0].ID, false, nil)
				if err != nil {
					t.Fatalf("round %d: %v", round, err)
				}
				if res.NextStatus != "AWAITING FIX" || !res.LoopedBack {
					t.Fatalf("round %d: expected a loop-back, got %+v", round, res)
				}
				assertNodeStatus(t, repo, ticketID, "impl", domain.NodeTODO, round)
				if tk, _ := repo.GetTicket(ticketID); tk.Blocked {
					t.Fatalf("round %d: blocked early", round)
				}
				gates = reworkImplAndClaimGates(t, e, ticketID, cat)
			}
			res, err := e.CompleteNode(gates[0].ID, false, nil)
			if err != nil {
				t.Fatalf("round %d: %v", n, err)
			}
			if res.NextStatus != "BLOCKED" || res.LoopedBack {
				t.Fatalf("round %d: expected {BLOCKED false}, got %+v", n, res)
			}
			if tk, _ := repo.GetTicket(ticketID); !tk.Blocked {
				t.Errorf("expected the ticket to be blocked")
			}
			assertNodeStatus(t, repo, ticketID, "impl", domain.NodeDone, n-1)
			for _, g := range gates {
				if got, _ := repo.GetNode(g.ID); got.Status != domain.NodeInReview {
					t.Errorf("%s: expected IN REVIEW (nothing rewound), got %s", *g.ConfigID, got.Status)
				}
			}
		})
	}
}

// Before round N, a failure loops back (a direct table over the count).
func TestCompleteNode_LoopsBackBeforeRoundN(t *testing.T) {
	cases := []struct{ n, count int }{{3, 0}, {3, 1}, {4, 2}, {5, 3}}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("n%d count%d", tc.n, tc.count), func(t *testing.T) {
			e, repo, projectID := newTestEngine(t)
			ticketID, _, gates := parallelGatesWithLimit(t, e, projectID, tc.n)
			setIterationCount(t, repo, nodeByConfigID(t, repo, ticketID, "impl").ID, tc.count)
			res, err := e.CompleteNode(gates[0].ID, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !res.LoopedBack {
				t.Fatalf("expected a loop-back, got %+v", res)
			}
			assertNodeStatus(t, repo, ticketID, "impl", domain.NodeTODO, tc.count+1)
		})
	}
}

// A review node (plan_review -> plan) follows the same rule.
func TestCompleteNode_ReviewNodeBlocksInRoundN(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _ := seedPlanDoneAndClaimPlanReview(t, e, projectID)
	plan := nodeByConfigID(t, repo, ticketID, "plan")
	setIterationCount(t, repo, plan.ID, 2)
	res, err := e.CompleteNode(nodeByConfigID(t, repo, ticketID, "plan_review").ID, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.NextStatus != "BLOCKED" {
		t.Fatalf("expected BLOCKED, got %+v", res)
	}
	assertNodeStatus(t, repo, ticketID, "plan", domain.NodeDone, 2)
}

// Parallel gates: the first rejection rewinds; the other three verdicts of
// that round are refused and count nothing.
func TestParallelGates_RoundCountsOnceUnderRoundLimit(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _, gates := parallelGatesWithLimit(t, e, projectID, 3)
	res, err := e.CompleteNode(gates[0].ID, false, nil)
	if err != nil || !res.LoopedBack {
		t.Fatalf("first rejection: %+v, %v", res, err)
	}
	assertNodeStatus(t, repo, ticketID, "impl", domain.NodeTODO, 1)
	for _, g := range gates[1:] {
		if got, _ := repo.GetNode(g.ID); got.Status != domain.NodeTODO {
			t.Errorf("%s: expected TODO after the rewind, got %s", *g.ConfigID, got.Status)
		}
		before, _ := repo.ListArtifactsByNode(g.ID)
		content := "x"
		_, err := e.CompleteNode(g.ID, false, []domain.Artifact{{Name: "late", Type: "text", Content: &content}})
		assertInvalidNodeState(t, err)
		after, _ := repo.ListArtifactsByNode(g.ID)
		if len(after) != len(before) {
			t.Errorf("%s: a refused verdict wrote artifacts", *g.ConfigID)
		}
	}
	assertNodeStatus(t, repo, ticketID, "impl", domain.NodeTODO, 1)
	if tk, _ := repo.GetTicket(ticketID); tk.Blocked {
		t.Error("one round of rejections must not block")
	}
}

// A second rejection arriving in an already-rewound round counts nothing and
// never blocks -- including when the count is already N-1.
func TestParallelGates_LateRejectionInRewoundRoundNeverBlocks(t *testing.T) {
	cases := []struct{ n, count int }{{3, 1}, {3, 2}, {4, 3}, {5, 4}}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("n%d count%d", tc.n, tc.count), func(t *testing.T) {
			e, repo, projectID := newTestEngine(t)
			ticketID, _, gates := parallelGatesWithLimit(t, e, projectID, tc.n)
			if _, err := e.CompleteNode(gates[0].ID, false, nil); err != nil {
				t.Fatal(err)
			}
			impl := nodeByConfigID(t, repo, ticketID, "impl")
			setIterationCount(t, repo, impl.ID, tc.count)
			setNodeStatus(t, repo, gates[1].ID, domain.NodeInReview)
			res, err := e.CompleteNode(gates[1].ID, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			if res.NextStatus != string(domain.NodeAwaitingFix) {
				t.Fatalf("expected AWAITING FIX, got %+v", res)
			}
			assertNodeStatus(t, repo, ticketID, "impl", domain.NodeTODO, tc.count)
			if tk, _ := repo.GetTicket(ticketID); tk.Blocked {
				t.Error("a rejection that counts nothing must not block")
			}
		})
	}
}

func TestParallelGates_BlockInRoundN(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _, gates := parallelGatesWithLimit(t, e, projectID, 3)
	setIterationCount(t, repo, nodeByConfigID(t, repo, ticketID, "impl").ID, 2)
	res, err := e.CompleteNode(gates[2].ID, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.NextStatus != "BLOCKED" {
		t.Fatalf("expected BLOCKED, got %+v", res)
	}
	assertNodeStatus(t, repo, ticketID, "impl", domain.NodeDone, 2)
}

// blockInLastRound drives a limit-3 ticket to the round-3 block for real.
func blockInLastRound(t *testing.T, e *GraphEngine, repo store.GraphRepository, projectID string) (ticketID string, cat config.Catalog) {
	t.Helper()
	ticketID, cat, gates := parallelGatesWithLimit(t, e, projectID, 3)
	for round := 1; round < 3; round++ {
		if _, err := e.CompleteNode(gates[0].ID, false, nil); err != nil {
			t.Fatal(err)
		}
		gates = reworkImplAndClaimGates(t, e, ticketID, cat)
	}
	res, err := e.CompleteNode(gates[0].ID, false, nil)
	if err != nil || res.NextStatus != "BLOCKED" {
		t.Fatalf("precondition: expected the round-3 block, got %+v, %v", res, err)
	}
	// The gates stay claimed when a block writes nothing; free them the way
	// the documented recovery does.
	for _, g := range gates {
		if _, err := e.UnstickNode(g.ID); err != nil {
			t.Fatalf("UnstickNode: %v", err)
		}
	}
	return ticketID, cat
}

func TestReopenNodes_RefusesLoopTargetAfterRoundLimitWithoutGrant(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _ := blockInLastRound(t, e, repo, projectID)
	impl := nodeByConfigID(t, repo, ticketID, "impl")
	if _, err := e.ReopenNodes(ticketID, []string{impl.ID}); err == nil {
		t.Fatal("expected reopen-nodes to refuse without a grant")
	} else if !strings.Contains(err.Error(), "max_iterations") {
		t.Errorf("refusal should name max_iterations: %v", err)
	}
	assertNodeStatus(t, repo, ticketID, "impl", domain.NodeDone, 2)
	if tk, _ := repo.GetTicket(ticketID); !tk.Blocked {
		t.Error("a refused reopen must leave the ticket blocked")
	}
}

func TestGrantIterations_GrantedRoundIsFinalAndBlocks(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, cat := blockInLastRound(t, e, repo, projectID)
	impl := nodeByConfigID(t, repo, ticketID, "impl")
	if _, err := e.GrantIterations(ticketID, []string{impl.ID}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ReopenNodes(ticketID, []string{impl.ID}); err != nil {
		t.Fatalf("ReopenNodes after grant: %v", err)
	}
	if got, _ := repo.GetNode(impl.ID); got.MaxIterations != 4 || got.IterationCount != 3 {
		t.Fatalf("impl after grant+reopen = max %d / count %d, want 4 / 3", got.MaxIterations, got.IterationCount)
	}
	gates := reworkImplAndClaimGates(t, e, ticketID, cat)
	if out := criteriaFor(t, repo, ticketID, "code_review"); !strings.Contains(out, "Review round: 4 / 4 — tier: Final") {
		t.Errorf("round 4 must be Final:\n%s", out)
	}
	res, err := e.CompleteNode(gates[0].ID, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.NextStatus != "BLOCKED" || res.LoopedBack {
		t.Fatalf("round 4 failure: expected {BLOCKED false}, got %+v", res)
	}
}

func TestGrantIterations_TwoGrantedRoundsAreBothFinal(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, cat := blockInLastRound(t, e, repo, projectID)
	impl := nodeByConfigID(t, repo, ticketID, "impl")
	if _, err := e.GrantIterations(ticketID, []string{impl.ID}, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ReopenNodes(ticketID, []string{impl.ID}); err != nil {
		t.Fatalf("ReopenNodes after grant: %v", err)
	}
	gates := reworkImplAndClaimGates(t, e, ticketID, cat)
	if out := criteriaFor(t, repo, ticketID, "code_review"); !strings.Contains(out, "Review round: 4 / 5 — tier: Final") {
		t.Errorf("round 4 must be Final:\n%s", out)
	}
	res, err := e.CompleteNode(gates[0].ID, false, nil)
	if err != nil || !res.LoopedBack {
		t.Fatalf("round 4 failure should loop back, got %+v, %v", res, err)
	}
	gates = reworkImplAndClaimGates(t, e, ticketID, cat)
	if out := criteriaFor(t, repo, ticketID, "code_review"); !strings.Contains(out, "Review round: 5 / 5 — tier: Final") {
		t.Errorf("round 5 must be Final:\n%s", out)
	}
	res, err = e.CompleteNode(gates[0].ID, false, nil)
	if err != nil || res.NextStatus != "BLOCKED" {
		t.Fatalf("round 5 failure should block, got %+v, %v", res, err)
	}
}

// The reopen check uses the round meaning (>=) for loop targets only; every
// other node keeps the plain "would exceed" (>) check.
func TestReopenNodes_BudgetCheckByNodeKind(t *testing.T) {
	cases := []struct {
		cfg     string
		loop    bool
		status  domain.NodeStatus
		refused bool
	}{
		{"impl", true, domain.NodeDone, true},
		{"impl", true, domain.NodeRejected, true},
		{"release_approval", false, domain.NodeDone, false},
		{"release_approval", false, domain.NodeRejected, false},
	}
	for _, tc := range cases {
		t.Run(tc.cfg+"/"+string(tc.status), func(t *testing.T) {
			e, repo, projectID := newTestEngine(t)
			ticketID, _, _ := parallelGatesWithLimit(t, e, projectID, 3)
			node := nodeByConfigID(t, repo, ticketID, tc.cfg)
			setNodeStatus(t, repo, node.ID, tc.status)
			setIterationCount(t, repo, node.ID, 2)
			if err := e.blockTicket(ticketID); err != nil {
				t.Fatal(err)
			}
			_, err := e.ReopenNodes(ticketID, []string{node.ID})
			if tc.refused {
				if err == nil {
					t.Fatal("expected a refusal")
				}
				assertNodeStatus(t, repo, ticketID, tc.cfg, tc.status, 2)
				return
			}
			if err != nil {
				t.Fatalf("expected success, got %v", err)
			}
			assertNodeStatus(t, repo, ticketID, tc.cfg, domain.NodeTODO, 3)
		})
	}
}

func TestReopenNodes_NonLoopTargetAtItsMaxIsStillRefused(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticketID, _, _ := parallelGatesWithLimit(t, e, projectID, 3)
	node := nodeByConfigID(t, repo, ticketID, "release_approval")
	setNodeStatus(t, repo, node.ID, domain.NodeDone)
	setIterationCount(t, repo, node.ID, 3)
	if err := e.blockTicket(ticketID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ReopenNodes(ticketID, []string{node.ID}); err == nil {
		t.Fatal("expected a refusal")
	}
	assertNodeStatus(t, repo, ticketID, "release_approval", domain.NodeDone, 3)
}

// Completion criterion 2: the catalog's value is stored on every node when
// the graph is built, and later catalog changes leave existing nodes alone.
func TestGraphCreation_StoresTheLimitOnEveryNode(t *testing.T) {
	for _, n := range []int{3, 4, 5} {
		t.Run(fmt.Sprintf("limit %d", n), func(t *testing.T) {
			e, repo, projectID := newTestEngine(t)
			ticketID, _, _ := parallelGatesWithLimit(t, e, projectID, n)
			nodes, _ := repo.ListNodesByTicket(ticketID)
			if len(nodes) < 10 {
				t.Fatalf("expected the full graph, got %d nodes", len(nodes))
			}
			for _, node := range nodes {
				if node.MaxIterations != n {
					t.Errorf("%s: max_iterations = %d, want %d", node.Name, node.MaxIterations, n)
				}
			}
		})
	}
}

func TestGraphCreation_SeedAndExpansionKeepTheirOwnValues(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat3 := catalogWithLimit(t, 3)
	ticket, _ := e.CreateTicket(projectID, "title", "")
	completeAllExecutable(t, e, ticket.ID, cat3, 1) // plan (seed built at 3)
	completeAllExecutable(t, e, ticket.ID, cat3, 1) // plan_review

	cat4 := catalogWithLimit(t, 4)
	if err := e.ExpandGraph(ticket.ID, cat4, nil); err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []string{"plan", "plan_review"} {
		if got := nodeByConfigID(t, repo, ticket.ID, cfg).MaxIterations; got != 3 {
			t.Errorf("%s: max_iterations = %d, want the seed-time 3", cfg, got)
		}
	}
	for _, cfg := range []string{"impl", "code_review", "test_review", "release"} {
		if got := nodeByConfigID(t, repo, ticket.ID, cfg).MaxIterations; got != 4 {
			t.Errorf("%s: max_iterations = %d, want the expansion-time 4", cfg, got)
		}
	}

	// Changing the catalog afterwards changes nothing already built, and
	// the round/tier still follow the stored 4.
	cat5 := catalogWithLimit(t, 5)
	if _, err := e.GetExecutableNodes(ticket.ID, cat5); err != nil {
		t.Fatal(err)
	}
	if got := nodeByConfigID(t, repo, ticket.ID, "impl").MaxIterations; got != 4 {
		t.Errorf("impl: max_iterations = %d after a catalog change, want 4", got)
	}
	if out := criteriaFor(t, repo, ticket.ID, "code_review"); !strings.Contains(out, "Review round: 1 / 4") {
		t.Errorf("round line should use the stored limit:\n%s", out)
	}
}

// An inline patch gate's max_iterations is ignored: the node takes the
// workflow-wide value, and the helper reports a warning for it.
func TestExpandGraph_InlineGateMaxIterationsIsIgnored(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	cat := catalogWithLimit(t, 3)
	ticket, _ := e.CreateTicket(projectID, "title", "")
	completeAllExecutable(t, e, ticket.ID, cat, 1)
	completeAllExecutable(t, e, ticket.ID, cat, 1)
	seven := 7
	patch := &Patch{ExtraNodes: []ExtraNode{
		{ID: "impl", Type: "implementation", Name: "Impl", DependsOn: []string{"plan_review"}},
		{ID: "custom_gate", Type: "review_gate", Gate: &ExtraGateDef{Name: "Custom", Criteria: "c", LegacyMaxIterations: &seven}, DependsOn: []string{"impl"}, LoopBackTo: "impl"},
	}}
	warnings := InlineGateMaxIterationsWarnings(patch)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "custom_gate") || !strings.Contains(warnings[0], "ignored") {
		t.Errorf("warnings = %q", warnings)
	}
	if err := e.ExpandGraph(ticket.ID, cat, patch); err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []string{"impl", "custom_gate"} {
		if got := nodeByConfigID(t, repo, ticket.ID, cfg).MaxIterations; got != 3 {
			t.Errorf("%s: max_iterations = %d, want 3", cfg, got)
		}
	}
	if w := InlineGateMaxIterationsWarnings(&Patch{ExtraNodes: []ExtraNode{{ID: "g", Gate: &ExtraGateDef{Name: "x"}}}}); len(w) != 0 {
		t.Errorf("no legacy value, yet warnings = %q", w)
	}
}
