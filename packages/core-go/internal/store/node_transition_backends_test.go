// package store_test for the same reason as node_claims_backends_test.go:
// it shares that file's backends (SQLite, the HTTP reference plugin at 1.2,
// and MySQL when dev/mysql/test.sh provides one).
package store_test

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
	"github.com/graph-ops/core-go/internal/store/httpdatasourcetest"
)

func applier(t *testing.T, repo store.GraphRepository) store.NodeTransitionApplier {
	t.Helper()
	a, ok := repo.(store.NodeTransitionApplier)
	if !ok {
		t.Fatalf("%T does not implement NodeTransitionApplier", repo)
	}
	return a
}

func nodeStatusPtr(s domain.NodeStatus) *domain.NodeStatus { return &s }

func conflictReason(err error) string {
	var c *store.NodeTransitionConflictError
	if !errors.As(err, &c) {
		return ""
	}
	return c.Reason
}

// TestNodeTransition_Semantics is the store contract of ApplyNodeTransition
// (DFLT-00329), on every backend.
func TestNodeTransition_Semantics(t *testing.T) {
	for _, b := range claimBackends() {
		t.Run(b.name, func(t *testing.T) {
			repo := b.open(t)
			a := applier(t, repo)
			get := func(id string) domain.GraphNode {
				t.Helper()
				n, err := repo.GetNode(id)
				if err != nil || n == nil {
					t.Fatalf("GetNode(%s) = %v, %v", id, n, err)
				}
				return *n
			}
			claim := func(id, token string) {
				t.Helper()
				c := &domain.NodeClaim{Name: "W", Token: token, ClaimedAt: time.Now().UTC().Format(time.RFC3339Nano)}
				if n, err := repo.ClaimNode(id, domain.NodeInProgress, []domain.NodeStatus{domain.NodeInProgress, domain.NodeInReview}, c); err != nil || n == nil {
					t.Fatalf("ClaimNode(%s) = %v, %v", id, n, err)
				}
			}
			reason := "no"
			art := func(g claimGraph, node string) domain.Artifact {
				return domain.Artifact{ID: "art-" + node + "-" + time.Now().Format("150405.000000000"), TicketID: g.ticketID, NodeID: node, Name: "rejection_reason", Type: domain.ArtifactText, Content: &reason}
			}

			t.Run("a required step that does not hold writes nothing", func(t *testing.T) {
				g := newClaimGraph(t, repo)
				claim(g.gate1, "tok-1")
				before := get(g.gate1)
				blocked := true
				_, err := a.ApplyNodeTransition(g.ticketID, store.NodeTransition{
					RequireTicketOpen: true, SetBlocked: &blocked,
					Artifacts: []domain.Artifact{art(g, g.approval)},
					Steps: []store.NodeStep{
						{NodeID: g.impl, SetStatus: nodeStatusPtr(domain.NodeTODO), IncrementIteration: true},
						{NodeID: g.gate1, Required: true, IfStatusIn: []domain.NodeStatus{domain.NodeInProgress}, CheckClaimToken: true, IfClaimToken: sp("tok-other"), SetStatus: nodeStatusPtr(domain.NodeDone)},
					},
				})
				if !errors.Is(err, store.ErrNodeTransitionConflict) || conflictReason(err) == "" {
					t.Fatalf("err = %v, want a node transition conflict", err)
				}
				if b.name != "http-1.2" && conflictReason(err) != store.ConflictClaimToken {
					t.Errorf("reason = %q, want claim_token", conflictReason(err))
				}
				after := get(g.gate1)
				impl := get(g.impl)
				arts, _ := repo.ListArtifactsByTicket(g.ticketID)
				tk, _ := repo.GetTicket(g.ticketID)
				if after.Status != domain.NodeInProgress || after.UpdatedAt != before.UpdatedAt || impl.Status != domain.NodeTODO || impl.IterationCount != 0 || len(arts) != 0 || tk.Blocked {
					t.Fatalf("a refused transition wrote: gate1 %s, impl %s it=%d, %d artifacts, blocked=%v", after.Status, impl.Status, impl.IterationCount, len(arts), tk.Blocked)
				}
			})

			t.Run("a step that does not hold is skipped when not required", func(t *testing.T) {
				g := newClaimGraph(t, repo)
				res, err := a.ApplyNodeTransition(g.ticketID, store.NodeTransition{Steps: []store.NodeStep{
					{NodeID: g.impl, IfStatusNotIn: []domain.NodeStatus{domain.NodeTODO}, SetStatus: nodeStatusPtr(domain.NodeTODO), IncrementIteration: true},
					{NodeID: g.gate1, SetStatus: nodeStatusPtr(domain.NodeAwaitingFix)},
				}})
				if err != nil {
					t.Fatal(err)
				}
				if res.Applied[0] || !res.Applied[1] {
					t.Fatalf("applied = %v, want [false true]", res.Applied)
				}
				if n := get(g.impl); n.IterationCount != 0 {
					t.Fatalf("the skipped step bumped the count to %d", n.IterationCount)
				}
				if n := res.Node(g.gate1); n == nil || n.Status != domain.NodeAwaitingFix {
					t.Fatalf("result node = %+v", n)
				}
			})

			t.Run("a check-only step leaves the row untouched", func(t *testing.T) {
				g := newClaimGraph(t, repo)
				claim(g.gate1, "tok-check")
				before := get(g.gate1)
				time.Sleep(2 * time.Millisecond)
				blocked := true
				if _, err := a.ApplyNodeTransition(g.ticketID, store.NodeTransition{SetBlocked: &blocked, Steps: []store.NodeStep{
					{NodeID: g.gate1, Required: true, IfStatusIn: []domain.NodeStatus{domain.NodeInProgress}, CheckClaimToken: true, IfClaimToken: sp("tok-check")},
				}}); err != nil {
					t.Fatal(err)
				}
				after := get(g.gate1)
				if after.UpdatedAt != before.UpdatedAt || after.ClaimToken == nil || *after.ClaimToken != "tok-check" || after.Status != domain.NodeInProgress {
					t.Fatalf("the check-only step changed the node: %+v -> %+v", before, after)
				}
				if tk, _ := repo.GetTicket(g.ticketID); !tk.Blocked {
					t.Fatal("the ticket write of the same transition was lost")
				}
			})

			t.Run("a decision without a status is refused", func(t *testing.T) {
				g := newClaimGraph(t, repo)
				_, err := a.ApplyNodeTransition(g.ticketID, store.NodeTransition{Steps: []store.NodeStep{
					{NodeID: g.approval, Required: true, Decision: &domain.NodeDecision{Name: "A"}},
				}})
				var apiErr *domain.APIError
				if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeValidation {
					t.Fatalf("err = %v, want VALIDATION_ERROR", err)
				}
			})

			t.Run("increments are added to the stored value", func(t *testing.T) {
				g := newClaimGraph(t, repo)
				two := 2
				if _, err := repo.UpdateNode(g.impl, store.NodePatch{IterationCount: &two}); err != nil {
					t.Fatal(err)
				}
				res, err := a.ApplyNodeTransition(g.ticketID, store.NodeTransition{Steps: []store.NodeStep{
					{NodeID: g.impl, Required: true, IncrementIteration: true, AddMaxIterations: 4},
				}})
				if err != nil {
					t.Fatal(err)
				}
				if n := res.Node(g.impl); n == nil || n.IterationCount != 3 || n.MaxIterations != 7 {
					t.Fatalf("impl = %+v, want it=3 max=7", n)
				}
			})

			t.Run("a CLOSED ticket conflicts", func(t *testing.T) {
				g := newClaimGraph(t, repo)
				closed := domain.TicketClosed
				if _, err := repo.UpdateTicket(g.ticketID, store.TicketPatch{Status: &closed}); err != nil {
					t.Fatal(err)
				}
				_, err := a.ApplyNodeTransition(g.ticketID, store.NodeTransition{RequireTicketOpen: true, Steps: []store.NodeStep{
					{NodeID: g.approval, Required: true, SetStatus: nodeStatusPtr(domain.NodeDone)},
				}})
				if !errors.Is(err, store.ErrNodeTransitionConflict) {
					t.Fatalf("err = %v, want a conflict", err)
				}
				if b.name != "http-1.2" && conflictReason(err) != store.ConflictTicketClosed {
					t.Errorf("reason = %q, want ticket_closed", conflictReason(err))
				}
			})

			t.Run("the decision is written with the status and cleared by every other status write", func(t *testing.T) {
				g := newClaimGraph(t, repo)
				d := &domain.NodeDecision{Name: "Alice", NameIsFallback: true, DecidedAt: "2026-09-30T01:02:03Z", Autopilot: true}
				if _, err := a.ApplyNodeTransition(g.ticketID, store.NodeTransition{Steps: []store.NodeStep{
					{NodeID: g.approval, Required: true, SetStatus: nodeStatusPtr(domain.NodeRejected), Decision: d},
				}}); err != nil {
					t.Fatal(err)
				}
				n := get(g.approval)
				if deref(n.DecidedByName) != "Alice" || n.DecidedByNameIsFallback == nil || !*n.DecidedByNameIsFallback ||
					deref(n.DecidedAt) != "2026-09-30T01:02:03Z" || n.DecidedByAutopilot == nil || !*n.DecidedByAutopilot {
					t.Fatalf("decision not stored: %+v", n)
				}
				// Every status write that is not a decision clears it: a
				// plain UpdateNode, a transition step without a decision,
				// and a claim.
				for _, write := range []func(){
					func() {
						if _, err := repo.UpdateNode(g.approval, store.NodePatch{Status: nodeStatusPtr(domain.NodeTODO)}); err != nil {
							t.Fatal(err)
						}
					},
					func() {
						if _, err := a.ApplyNodeTransition(g.ticketID, store.NodeTransition{Steps: []store.NodeStep{{NodeID: g.approval, SetStatus: nodeStatusPtr(domain.NodeTODO)}}}); err != nil {
							t.Fatal(err)
						}
					},
					func() { claim(g.approval, "tok-a") },
				} {
					if _, err := a.ApplyNodeTransition(g.ticketID, store.NodeTransition{Steps: []store.NodeStep{
						{NodeID: g.approval, SetStatus: nodeStatusPtr(domain.NodeDone), Decision: d},
					}}); err != nil {
						t.Fatal(err)
					}
					write()
					n := get(g.approval)
					if n.DecidedByName != nil || n.DecidedByNameIsFallback != nil || n.DecidedAt != nil || n.DecidedByAutopilot != nil {
						t.Fatalf("a status write left the decision on the node: %+v", n)
					}
				}
				// A write that does not touch the status leaves it.
				if _, err := a.ApplyNodeTransition(g.ticketID, store.NodeTransition{Steps: []store.NodeStep{
					{NodeID: g.approval, SetStatus: nodeStatusPtr(domain.NodeDone), Decision: d},
				}}); err != nil {
					t.Fatal(err)
				}
				assignee := sp("bob")
				if _, err := repo.UpdateNode(g.approval, store.NodePatch{Assignee: &assignee}); err != nil {
					t.Fatal(err)
				}
				if n := get(g.approval); deref(n.DecidedByName) != "Alice" {
					t.Fatal("a write that did not touch the status cleared the decision")
				}
			})

			t.Run("TicketPatch.IfStatus", func(t *testing.T) {
				g := newClaimGraph(t, repo)
				todo, inProgress := domain.TicketTODO, domain.TicketInProgress
				if _, err := repo.UpdateTicket(g.ticketID, store.TicketPatch{Status: &inProgress, IfStatus: &todo}); err != nil {
					t.Fatalf("a matching IfStatus: %v", err)
				}
				_, err := repo.UpdateTicket(g.ticketID, store.TicketPatch{Status: &todo, IfStatus: &todo})
				var apiErr *domain.APIError
				if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeTicketStatusChanged {
					t.Fatalf("err = %v, want TICKET_STATUS_CHANGED", err)
				}
				if tk, _ := repo.GetTicket(g.ticketID); tk.Status != domain.TicketInProgress {
					t.Fatalf("a refused IfStatus wrote the status: %s", tk.Status)
				}
			})
		})
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// TestNodeTransition_HTTP12Wire: a 1.2 plugin receives one POST
// /tickets/{id}/node-transition (and if_status on a conditional ticket
// PATCH); an older one receives neither, and the decision fields are
// neither sent nor read.
func TestNodeTransition_HTTP12Wire(t *testing.T) {
	for _, version := range []string{"1.2", "1.1"} {
		t.Run(version, func(t *testing.T) {
			plugin := httpdatasourcetest.New("")
			plugin.Version = version
			srv := httptest.NewServer(plugin)
			t.Cleanup(srv.Close)
			repo, err := store.Open(store.Config{Backend: "http", HTTPURL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			g := newClaimGraph(t, repo)
			plugin.ResetRequests()
			_, err = applier(t, repo).ApplyNodeTransition(g.ticketID, store.NodeTransition{Steps: []store.NodeStep{
				{NodeID: g.approval, Required: true, SetStatus: nodeStatusPtr(domain.NodeDone), Decision: &domain.NodeDecision{Name: "A"}},
			}})
			todo, inProgress := domain.TicketTODO, domain.TicketInProgress
			_, patchErr := repo.UpdateTicket(g.ticketID, store.TicketPatch{Status: &inProgress, IfStatus: &todo})
			if patchErr != nil {
				t.Fatalf("UpdateTicket with IfStatus: %v", patchErr)
			}
			var sawTransition, sawIfStatus bool
			for _, r := range plugin.Requests() {
				sawTransition = sawTransition || strings.HasSuffix(r.Path, "/node-transition")
				sawIfStatus = sawIfStatus || strings.Contains(string(r.Body), "if_status")
			}
			if version == "1.2" {
				if err != nil || !sawTransition || !sawIfStatus {
					t.Fatalf("1.2: err=%v transition=%v if_status=%v", err, sawTransition, sawIfStatus)
				}
				if n, _ := repo.GetNode(g.approval); deref(n.DecidedByName) != "A" {
					t.Fatal("1.2: the decision did not round-trip")
				}
				return
			}
			if !errors.Is(err, store.ErrNodeTransitionUnsupported) || sawTransition || sawIfStatus {
				t.Fatalf("1.1: err=%v transition=%v if_status=%v", err, sawTransition, sawIfStatus)
			}
		})
	}
}

func sp(s string) *string { return &s }
