package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00082: wait-node lets a process-ticket session parked at an
// approval_gate resume when the gate is judged elsewhere (e.g. the Web UI).

func withFastPoll(t *testing.T) {
	t.Helper()
	orig := waitNodePollInterval
	waitNodePollInterval = 5 * time.Millisecond
	t.Cleanup(func() { waitNodePollInterval = orig })
}

func runWaitNode(t *testing.T, repo store.GraphRepository, args ...string) (waitNodeResult, string, error) {
	t.Helper()
	var cmdErr error
	out := captureStdout(t, func() {
		cmdErr = cmdWaitNode(repo, args)
	})
	var res waitNodeResult
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("unmarshal stdout %q: %v", out, err)
		}
	}
	return res, out, cmdErr
}

func mustCreateNodeOnTicket(t *testing.T, repo store.GraphRepository, ticketID string, nodeType domain.NodeType, status domain.NodeStatus) string {
	t.Helper()
	// is_manual mirrors what ExpandGraph does for the manual node types --
	// see mustCreateTicketAndNode for why the fixture has to set it.
	node, err := repo.CreateNode(domain.GraphNode{
		TicketID: ticketID, Name: "n", Type: nodeType, Status: status, MaxIterations: 3,
		IsManual: nodeType == domain.NodeTypeApprovalGate || nodeType == domain.NodeTypeRelease,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	return node.ID
}

func TestCmdWaitNode_AlreadyDoneReturnsImmediately(t *testing.T) {
	withFastPoll(t)
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	_, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeApprovalGate)
	if _, err := eng.CompleteNode(nodeID, true, nil); err != nil {
		t.Fatalf("CompleteNode: %v", err)
	}

	res, out, err := runWaitNode(t, repo, nodeID)
	if err != nil {
		t.Fatalf("cmdWaitNode: %v", err)
	}
	if res.Result != "changed" || len(res.Nodes) != 1 || res.Nodes[0].ID != nodeID || res.Nodes[0].Status != domain.NodeDone {
		t.Fatalf("unexpected output: %s", out)
	}
	if strings.Contains(out, "rejection_reason") {
		t.Errorf("rejection_reason must be omitted for DONE, got %s", out)
	}
}

func TestCmdWaitNode_DetectsChangeWhileWaiting(t *testing.T) {
	withFastPoll(t)
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	_, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeApprovalGate)

	done := make(chan error, 1)
	go func() {
		time.Sleep(30 * time.Millisecond)
		_, err := eng.CompleteNode(nodeID, true, nil)
		done <- err
	}()

	res, out, err := runWaitNode(t, repo, nodeID, "--timeout", "5s")
	if err != nil {
		t.Fatalf("cmdWaitNode: %v", err)
	}
	if cerr := <-done; cerr != nil {
		t.Fatalf("CompleteNode: %v", cerr)
	}
	if res.Result != "changed" || len(res.Nodes) != 1 || res.Nodes[0].Status != domain.NodeDone {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestCmdWaitNode_RejectedIncludesLatestReason(t *testing.T) {
	withFastPoll(t)
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	gateID := mustCreateNodeOnTicket(t, repo, ticket.ID, domain.NodeTypeApprovalGate, domain.NodeTODO)

	reject := func(reason string) {
		t.Helper()
		var cerr error
		captureStdout(t, func() {
			cerr = cmdCompleteNode(eng, repo, []string{gateID, "false", "--reason", reason})
		})
		if cerr != nil {
			t.Fatalf("cmdCompleteNode: %v", cerr)
		}
	}

	reject("first reason")
	res, out, err := runWaitNode(t, repo, gateID)
	if err != nil {
		t.Fatalf("cmdWaitNode: %v", err)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].Status != domain.NodeRejected || res.Nodes[0].RejectionReason != "first reason" {
		t.Fatalf("unexpected output after first rejection: %s", out)
	}

	if _, err := eng.ReopenNodes(ticket.ID, []string{gateID}); err != nil {
		t.Fatalf("ReopenNodes: %v", err)
	}
	// Keep the two rejection_reason artifacts' created_at apart, so this
	// test does not depend on how equal timestamps are ordered (that case is
	// covered by TestLatestRejectionReason_OrdersByParsedTime).
	time.Sleep(5 * time.Millisecond)
	reject("second reason")

	res, out, err = runWaitNode(t, repo, gateID)
	if err != nil {
		t.Fatalf("cmdWaitNode: %v", err)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].RejectionReason != "second reason" {
		t.Fatalf("expected the newest reason, got %s", out)
	}
}

func TestCmdWaitNode_MultipleNodesReportsOnlyChanged(t *testing.T) {
	withFastPoll(t)
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	a := mustCreateNodeOnTicket(t, repo, ticket.ID, domain.NodeTypeApprovalGate, domain.NodeTODO)
	b := mustCreateNodeOnTicket(t, repo, ticket.ID, domain.NodeTypeApprovalGate, domain.NodeTODO)
	if _, err := eng.CompleteNode(b, true, nil); err != nil {
		t.Fatalf("CompleteNode: %v", err)
	}

	// Duplicate ids are collapsed.
	res, out, err := runWaitNode(t, repo, a, b, a)
	if err != nil {
		t.Fatalf("cmdWaitNode: %v", err)
	}
	if res.Result != "changed" || len(res.Nodes) != 1 || res.Nodes[0].ID != b {
		t.Fatalf("expected only %s, got %s", b, out)
	}
}

func TestCmdWaitNode_TimeoutPrintsAndExitsTwo(t *testing.T) {
	withFastPoll(t)
	repo, projectID := newTestRepoWithProject(t)
	_, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeApprovalGate)

	start := time.Now()
	res, out, err := runWaitNode(t, repo, "--timeout", "50ms", nodeID)
	if time.Since(start) < 50*time.Millisecond {
		t.Errorf("returned before the timeout elapsed")
	}
	var ec exitCodeError
	if !errors.As(err, &ec) || ec.code != 2 {
		t.Fatalf("expected exitCodeError{2}, got %v", err)
	}
	if res.Result != "timeout" || res.Nodes == nil || len(res.Nodes) != 0 {
		t.Fatalf("unexpected output: %s", out)
	}
	if !strings.Contains(out, `"nodes": []`) {
		t.Errorf("nodes must serialize as an empty array, got %s", out)
	}
}

func TestCmdWaitNode_UnknownNodeErrorsWithoutOutput(t *testing.T) {
	withFastPoll(t)
	repo, projectID := newTestRepoWithProject(t)
	_, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeApprovalGate)

	_, out, err := runWaitNode(t, repo, nodeID, "NOPE-99999-01")
	if err == nil || !strings.Contains(err.Error(), "NOPE-99999-01") {
		t.Fatalf("expected not-found error, got %v", err)
	}
	var ec exitCodeError
	if errors.As(err, &ec) {
		t.Errorf("not-found must be a plain error (exit 1), got %v", err)
	}
	if out != "" {
		t.Errorf("expected no stdout, got %q", out)
	}
}

func TestCmdWaitNode_UsageErrors(t *testing.T) {
	withFastPoll(t)
	repo, projectID := newTestRepoWithProject(t)
	_, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeApprovalGate)

	cases := map[string][]string{
		"no args":            nil,
		"only timeout":       {"--timeout", "1s"},
		"missing value":      {nodeID, "--timeout"},
		"unparseable":        {nodeID, "--timeout", "abc"},
		"zero":               {nodeID, "--timeout", "0"},
		"negative":           {nodeID, "--timeout", "-1s"},
		"unknown flag":       {nodeID, "--poll", "1s"},
		"unknown short flag": {nodeID, "-x"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			_, out, err := runWaitNode(t, repo, args...)
			if err == nil || !strings.Contains(err.Error(), "usage: graph-engine wait-node") {
				t.Fatalf("expected usage error, got %v", err)
			}
			if out != "" {
				t.Errorf("expected no stdout, got %q", out)
			}
		})
	}
}

func TestExitCodeFor(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantCode  int
		wantPrint bool
	}{
		{"nil", nil, 0, false},
		{"plain error", errors.New("boom"), 1, true},
		{"timeout", exitCodeError{code: 2}, 2, false},
		{"wrapped timeout", errors.Join(errors.New("ctx"), exitCodeError{code: 2}), 2, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, printErr := exitCodeFor(tc.err)
			if code != tc.wantCode || printErr != tc.wantPrint {
				t.Errorf("exitCodeFor(%v) = (%d, %v), want (%d, %v)", tc.err, code, printErr, tc.wantCode, tc.wantPrint)
			}
		})
	}
}

// artifactListRepo overrides ListArtifactsByNode only; any other method
// called on it panics via the nil embedded interface.
type artifactListRepo struct {
	store.GraphRepository
	artifacts []domain.Artifact
}

func (r artifactListRepo) ListArtifactsByNode(string) ([]domain.Artifact, error) {
	return r.artifacts, nil
}

func TestLatestRejectionReason_OrdersByParsedTime(t *testing.T) {
	str := func(s string) *string { return &s }
	tests := []struct {
		name      string
		artifacts []domain.Artifact
		want      string
	}{
		{"none", nil, ""},
		{"ignores other names", []domain.Artifact{
			{Name: "notes", Content: str("x"), CreatedAt: "2026-09-17T00:00:01Z"},
		}, ""},
		{
			// As text "…05.1Z" > "…05.12Z", but .1s is earlier than .12s.
			"trailing zeros trimmed by RFC3339Nano",
			[]domain.Artifact{
				{Name: "rejection_reason", Content: str("older"), CreatedAt: "2026-09-17T00:00:05.1Z"},
				{Name: "rejection_reason", Content: str("newer"), CreatedAt: "2026-09-17T00:00:05.12Z"},
			},
			"newer",
		},
		{"store order reversed", []domain.Artifact{
			{Name: "rejection_reason", Content: str("newer"), CreatedAt: "2026-09-17T00:00:06Z"},
			{Name: "rejection_reason", Content: str("older"), CreatedAt: "2026-09-17T00:00:05Z"},
		}, "newer"},
		{"equal timestamps -> later row wins", []domain.Artifact{
			{Name: "rejection_reason", Content: str("first"), CreatedAt: "2026-09-17T00:00:05Z"},
			{Name: "rejection_reason", Content: str("second"), CreatedAt: "2026-09-17T00:00:05Z"},
		}, "second"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := latestRejectionReason(artifactListRepo{artifacts: tc.artifacts}, "N")
			if err != nil {
				t.Fatalf("latestRejectionReason: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
