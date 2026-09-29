package main

import (
	"strings"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/identity"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00329: complete-node records who decided a manual node -- this
// process's own myName -- and, with --session in an autopilot run's
// session, that the autopilot decided.
func TestCmdCompleteNode_RecordsTheDecider(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	home := t.TempDir()
	if _, _, err := runtimeconfig.UpdateHome(home, func(c *runtimeconfig.FileConfig) error {
		c.MyName = "CLI Member"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketInProgress})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(name string, typ domain.NodeType) string {
		n, err := repo.CreateNode(domain.GraphNode{TicketID: ticket.ID, Name: name, Type: typ, Status: domain.NodeTODO, MaxIterations: 3, IsManual: true})
		if err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	gate := mk("approval", domain.NodeTypeApprovalGate)
	release := mk("release", domain.NodeTypeRelease)
	runSession := identity.NewSessionID()
	now := time.Now().UTC().Format(domain.SessionTimestampLayout)
	if err := repo.(store.ProcessingSessionStore).SaveProcessingSession(domain.ProcessingSession{
		ID: runSession, ProjectID: projectID, TicketID: ticket.ID, ActorName: "CLI Member", MachineID: "m", RunID: "run-1", StartedAt: now, Heartbeat: now,
	}); err != nil {
		t.Fatal(err)
	}
	rc := runtimeConfig{HomeDir: home}

	captureStdout(t, func() {
		if err := cmdCompleteNode(eng, repo, rc, []string{gate, "false", "--reason", "needs work"}); err != nil {
			t.Fatal(err)
		}
		if err := cmdCompleteNode(eng, repo, rc, []string{release, "true", "--session", runSession}); err != nil {
			t.Fatal(err)
		}
	})
	g, _ := repo.GetNode(gate)
	r, _ := repo.GetNode(release)
	if g.Status != domain.NodeRejected || g.DecidedByName == nil || *g.DecidedByName != "CLI Member" || g.DecidedByAutopilot == nil || *g.DecidedByAutopilot {
		t.Fatalf("gate = %s by %v (autopilot %v), want REJECTED by CLI Member, not the autopilot", g.Status, g.DecidedByName, g.DecidedByAutopilot)
	}
	if r.Status != domain.NodeDone || r.DecidedByAutopilot == nil || !*r.DecidedByAutopilot {
		t.Fatalf("release = %s (autopilot %v), want DONE by the autopilot", r.Status, r.DecidedByAutopilot)
	}

	// Deciding the rejected gate again is refused (it is REJECTED now).
	err = cmdCompleteNode(eng, repo, rc, []string{gate, "true"})
	if err == nil || !strings.Contains(err.Error(), "REJECTED") {
		t.Fatalf("second decision = %v, want a refusal", err)
	}
}

// A claimed node completed without --claim draws a warning that says what
// --claim is for.
func TestCmdCompleteNode_WarnsWithoutClaim(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	eng := engine.New(repo)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketInProgress})
	if err != nil {
		t.Fatal(err)
	}
	n, err := repo.CreateNode(domain.GraphNode{TicketID: ticket.ID, Name: "impl", Type: domain.NodeTypeImplementation, Status: domain.NodeTODO, MaxIterations: 3})
	if err != nil {
		t.Fatal(err)
	}
	if c, err := repo.ClaimNode(n.ID, domain.NodeInProgress, []domain.NodeStatus{domain.NodeInProgress, domain.NodeInReview}, &domain.NodeClaim{Name: "W", Token: identity.NewSessionID(), ClaimedAt: "2026-09-30T00:00:00Z"}); err != nil || c == nil {
		t.Fatal(err)
	}
	var stderr string
	captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			if err := cmdCompleteNode(eng, repo, runtimeConfig{HomeDir: t.TempDir()}, []string{n.ID, "true"}); err != nil {
				t.Fatal(err)
			}
		})
	})
	if !strings.Contains(stderr, "--claim") {
		t.Fatalf("stderr = %q, want a warning about --claim", stderr)
	}
}
