package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
)

// TestCmdCompleteNode_ReviewLoopBackPrintsAwaitingFix (DFLT-00042): the CLI
// prints engine.CompleteNode's result as JSON, so a failing review with an
// iteration_loop edge prints nextStatus "AWAITING FIX" / loopedBack true.
func TestCmdCompleteNode_ReviewLoopBackPrintsAwaitingFix(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	plan, err := repo.CreateNode(domain.GraphNode{
		TicketID: ticket.ID, Name: "plan", Type: domain.NodeTypePlan, Status: domain.NodeDone, MaxIterations: 3,
	})
	if err != nil {
		t.Fatalf("CreateNode(plan): %v", err)
	}
	review, err := repo.CreateNode(domain.GraphNode{
		TicketID: ticket.ID, Name: "plan_review", Type: domain.NodeTypeReview, Status: domain.NodeInReview, MaxIterations: 3,
	})
	if err != nil {
		t.Fatalf("CreateNode(plan_review): %v", err)
	}
	if _, err := repo.CreateEdge(domain.GraphEdge{
		ID: "edge-" + engine.NewArtifactID(), TicketID: ticket.ID,
		FromNodeID: review.ID, ToNodeID: plan.ID, Condition: domain.EdgeLoop,
	}); err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}

	var cmdErr error
	out := captureStdout(t, func() {
		cmdErr = cmdCompleteNode(eng, repo, []string{review.ID, "false"})
	})
	if cmdErr != nil {
		t.Fatalf("cmdCompleteNode: %v", cmdErr)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal stdout %q: %v", out, err)
	}
	if result["nextStatus"] != "AWAITING FIX" || result["loopedBack"] != true {
		t.Errorf("expected nextStatus AWAITING FIX / loopedBack true, got %s", out)
	}
	if got, _ := repo.GetNode(review.ID); got.Status != domain.NodeAwaitingFix {
		t.Errorf("plan_review status = %s, want AWAITING FIX", got.Status)
	}
}

// --- DFLT-00016: cmdCompleteNode's --reason flag and the reopen-nodes
// command are CLI-only plumbing around engine.CompleteNode/ReopenNodes,
// whose own behavior is exercised in depth by internal/engine's tests. These
// tests cover the CLI-level argument handling and wiring only. ---

func TestCmdCompleteNode_ReasonSavesRejectionReasonArtifactOnApprovalGate(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	_, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeApprovalGate)

	if err := cmdCompleteNode(eng, repo, []string{nodeID, "false", "--reason", "does not match requirements"}); err != nil {
		t.Fatalf("cmdCompleteNode: %v", err)
	}

	node, err := repo.GetNode(nodeID)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if node.Status != domain.NodeRejected {
		t.Fatalf("expected node to be REJECTED, got %+v", node)
	}

	arts, err := repo.ListArtifactsByNode(nodeID)
	if err != nil {
		t.Fatalf("ListArtifactsByNode: %v", err)
	}
	var found bool
	for _, a := range arts {
		if a.Name == "rejection_reason" && a.Type == domain.ArtifactText && a.Content != nil && *a.Content == "does not match requirements" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a rejection_reason text artifact, got %+v", arts)
	}
}

func TestCmdCompleteNode_ReasonRejectedWhenApprovingRatherThanRejecting(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	_, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeApprovalGate)

	err := cmdCompleteNode(eng, repo, []string{nodeID, "true", "--reason", "should not be allowed"})
	if err == nil {
		t.Fatal("expected an error using --reason with passed=true")
	}

	node, getErr := repo.GetNode(nodeID)
	if getErr != nil {
		t.Fatalf("GetNode: %v", getErr)
	}
	if node.Status != domain.NodeTODO {
		t.Errorf("expected the node to be left untouched (TODO), got %+v", node)
	}
	arts, _ := repo.ListArtifactsByNode(nodeID)
	if len(arts) != 0 {
		t.Errorf("expected no artifact to be written on a rejected misuse, got %+v", arts)
	}
}

func TestCmdCompleteNode_ReasonRejectedOnNonApprovalGateNode(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	_, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeReview)

	err := cmdCompleteNode(eng, repo, []string{nodeID, "false", "--reason", "there are issues"})
	if err == nil {
		t.Fatal("expected an error using --reason against a non-approval_gate node")
	}

	node, getErr := repo.GetNode(nodeID)
	if getErr != nil {
		t.Fatalf("GetNode: %v", getErr)
	}
	if node.Status != domain.NodeTODO {
		t.Errorf("expected the node to be left untouched (TODO), got %+v", node)
	}
}

func TestCmdCompleteNode_UsageErrors(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	_, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeApprovalGate)

	if err := cmdCompleteNode(eng, repo, nil); err == nil {
		t.Error("expected an error with no arguments")
	}
	if err := cmdCompleteNode(eng, repo, []string{nodeID, "false", "--reason"}); err == nil {
		t.Error("expected an error when --reason has no value")
	}
	if err := cmdCompleteNode(eng, repo, []string{nodeID, "false", "--unknown-flag"}); err == nil {
		t.Error("expected an error for an unrecognized argument")
	}
}

func TestCmdReopenNodes_UsageErrors(t *testing.T) {
	repo, _ := newTestRepoWithProject(t)
	eng := engine.New(repo)

	if err := cmdReopenNodes(eng, nil); err == nil {
		t.Error("expected an error with no arguments")
	}
	if err := cmdReopenNodes(eng, []string{"TICK-00001"}); err == nil {
		t.Error("expected an error with only a ticket id and no node ids")
	}
	if err := cmdReopenNodes(eng, []string{"TICK-00001", "  ,  ,"}); err == nil {
		t.Error("expected an error when the node id list is empty after trimming")
	}
}

func TestCmdReopenNodes_SplitsCommaSeparatedIDsAndPropagatesEngineError(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	ticketID, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeImplementation)

	// The ticket was never blocked, so engine.ReopenNodes must reject this --
	// this also proves cmdReopenNodes actually parsed and forwarded the
	// comma-separated id list rather than, say, passing the raw string
	// through as one id.
	err := cmdReopenNodes(eng, []string{ticketID, nodeID + "," + nodeID})
	if err == nil {
		t.Fatal("expected engine.ReopenNodes' error (ticket not blocked) to propagate")
	}
	if !strings.Contains(err.Error(), "not blocked") {
		t.Errorf("expected a 'not blocked' error, got %v", err)
	}
}
