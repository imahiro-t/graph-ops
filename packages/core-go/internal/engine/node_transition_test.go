package engine

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00329: completing a node (and reopen-nodes, grant-iterations,
// unstick-node) is described once as a store.NodeTransition and then written
// either atomically (a repository with the NodeTransitionApplier add-on) or
// one call at a time (any other repository -- an HTTP data source older than
// protocol 1.2). newTestEngine's SQLite repository has the add-on, so the
// existing engine tests all run the atomic path; the tests here run the
// representative cases through both paths and require the same outcome, so
// the sequential path -- the pre-DFLT-00329 behaviour -- cannot quietly rot.

// sequentialRepo hides the NodeTransitionApplier add-on: it embeds the
// store.GraphRepository *interface*, whose method set has no
// ApplyNodeTransition, so the engine's type assertion fails and every
// transition takes applyNodeTransitionSequential.
type sequentialRepo struct {
	store.GraphRepository
}

// forEachTransitionPath runs fn once on the atomic path and once on the
// sequential one, each against a fresh SQLite database.
func forEachTransitionPath(t *testing.T, fn func(t *testing.T, e *GraphEngine, repo store.GraphRepository, projectID string)) {
	t.Helper()
	for _, path := range []string{"atomic", "sequential"} {
		t.Run(path, func(t *testing.T) {
			_, repo, projectID := newTestEngine(t)
			if path == "sequential" {
				repo = &sequentialRepo{GraphRepository: repo}
				if _, ok := repo.(store.NodeTransitionApplier); ok {
					t.Fatal("sequentialRepo still exposes NodeTransitionApplier")
				}
			} else if _, ok := repo.(store.NodeTransitionApplier); !ok {
				t.Fatal("the test repository does not implement NodeTransitionApplier")
			}
			fn(t, New(repo), repo, projectID)
		})
	}
}

// transitionFixture is a hand-built graph:
//
//	impl --success--> gate1 --success--> approval --success--> release
//	impl --success--> gate2 --success--> approval
//	gate1, gate2 --iteration_loop--> impl
//	side --iteration_loop--> impl   (side is not downstream of impl: sideways)
//	lone                             (a review with no loop edge at all)
//
// impl and gate2 are DONE, gate1, side and lone are IN PROGRESS with a
// claim, approval and release (manual) are TODO. The graph counts as
// expanded, and the ticket is IN PROGRESS.
type transitionFixture struct {
	ticketID                                          string
	impl, gate1, gate2, approval, release, side, lone string
	tokens                                            map[string]string
}

func newTransitionFixture(t *testing.T, repo store.GraphRepository, projectID string) transitionFixture {
	t.Helper()
	expanded := time.Now().UTC().Format(time.RFC3339Nano)
	tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: "transition", Status: domain.TicketInProgress, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if _, err := repo.UpdateTicket(tk.ID, store.TicketPatch{GraphExpandedAt: &expanded}); err != nil {
		t.Fatalf("UpdateTicket: %v", err)
	}
	f := transitionFixture{ticketID: tk.ID, tokens: map[string]string{}}
	mk := func(name string, typ domain.NodeType, status domain.NodeStatus, manual bool) string {
		n, err := repo.CreateNode(domain.GraphNode{TicketID: tk.ID, Name: name, Type: typ, Status: status, MaxIterations: 3, IsManual: manual})
		if err != nil {
			t.Fatalf("CreateNode(%s): %v", name, err)
		}
		return n.ID
	}
	f.impl = mk("impl", domain.NodeTypeImplementation, domain.NodeDone, false)
	f.gate1 = mk("gate1", domain.NodeTypeReviewGate, domain.NodeTODO, false)
	f.gate2 = mk("gate2", domain.NodeTypeReviewGate, domain.NodeDone, false)
	f.approval = mk("approval", domain.NodeTypeApprovalGate, domain.NodeTODO, true)
	f.release = mk("release", domain.NodeTypeRelease, domain.NodeTODO, true)
	f.side = mk("side", domain.NodeTypeReview, domain.NodeTODO, false)
	f.lone = mk("lone", domain.NodeTypeReview, domain.NodeTODO, false)
	edge := func(from, to string, c domain.EdgeCondition) {
		if _, err := repo.CreateEdge(domain.GraphEdge{ID: newEdgeID(), TicketID: tk.ID, FromNodeID: from, ToNodeID: to, Condition: c}); err != nil {
			t.Fatalf("CreateEdge: %v", err)
		}
	}
	edge(f.impl, f.gate1, domain.EdgeSuccess)
	edge(f.impl, f.gate2, domain.EdgeSuccess)
	edge(f.gate1, f.impl, domain.EdgeLoop)
	edge(f.gate2, f.impl, domain.EdgeLoop)
	edge(f.gate1, f.approval, domain.EdgeSuccess)
	edge(f.gate2, f.approval, domain.EdgeSuccess)
	edge(f.approval, f.release, domain.EdgeSuccess)
	edge(f.side, f.impl, domain.EdgeLoop)
	for _, id := range []string{f.gate1, f.side, f.lone} {
		f.claim(t, repo, id)
	}
	return f
}

// claim moves id to IN PROGRESS with a fresh claim and remembers its token.
func (f transitionFixture) claim(t *testing.T, repo store.GraphRepository, id string) string {
	t.Helper()
	claim := &domain.NodeClaim{Name: "Worker", Token: fmt.Sprintf("tok-%s-%d", id, time.Now().UnixNano()), ClaimedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	n, err := repo.ClaimNode(id, domain.NodeInProgress, []domain.NodeStatus{domain.NodeInProgress, domain.NodeInReview}, claim)
	if err != nil || n == nil {
		t.Fatalf("ClaimNode(%s) = %v, %v", id, n, err)
	}
	f.tokens[id] = claim.Token
	return claim.Token
}

// trace is everything a transition may have changed, backend-neutral, with
// the decision columns left out (only the atomic path records them).
func (f transitionFixture) trace(t *testing.T, repo store.GraphRepository) string {
	t.Helper()
	d, err := repo.GetTicketDetail(f.ticketID)
	if err != nil || d == nil {
		t.Fatalf("GetTicketDetail: %v", err)
	}
	names := map[string]string{}
	var lines []string
	for _, n := range d.Nodes {
		names[n.ID] = n.Name
		lines = append(lines, fmt.Sprintf("%s %s it=%d max=%d claimed=%v", n.Name, n.Status, n.IterationCount, n.MaxIterations, n.ClaimToken != nil))
	}
	sort.Strings(lines)
	var arts []string
	for _, a := range d.Artifacts {
		arts = append(arts, names[a.NodeID]+"/"+a.Name)
	}
	sort.Strings(arts)
	return fmt.Sprintf("ticket %s blocked=%v\n%s\nartifacts=%v", d.Status, d.Blocked, strings.Join(lines, "\n"), arts)
}

func textArtifact(name, content string) []domain.Artifact {
	return []domain.Artifact{{Name: name, Type: domain.ArtifactText, Content: &content}}
}

func isInvalidNodeState(err error) bool {
	var apiErr *domain.APIError
	return errors.As(err, &apiErr) && apiErr.Code == domain.ErrCodeInvalidNodeState
}

// TestTransitionPaths_SameOutcome is the table the sequential path is held
// to: every representative case must leave the same graph behind on both
// paths, and each is also checked against what it should be.
func TestTransitionPaths_SameOutcome(t *testing.T) {
	alice := &Decider{Name: "Alice"}
	cases := []struct {
		name  string
		setup func(t *testing.T, f transitionFixture, repo store.GraphRepository)
		run   func(t *testing.T, e *GraphEngine, f transitionFixture) (string, error)
		check func(t *testing.T, trace string)
	}{
		{
			name: "pass",
			run: func(t *testing.T, e *GraphEngine, f transitionFixture) (string, error) {
				r, err := e.CompleteNodeWith(f.gate1, true, textArtifact("review", "ok"), CompleteNodeOptions{ClaimToken: f.tokens[f.gate1]})
				return r.NextStatus, err
			},
			check: func(t *testing.T, trace string) {
				mustContain(t, trace, "gate1 DONE it=0 max=3 claimed=false", "gate1/review")
			},
		},
		{
			name: "approval gate rejection",
			run: func(t *testing.T, e *GraphEngine, f transitionFixture) (string, error) {
				r, err := e.CompleteNodeWith(f.approval, false, textArtifact("rejection_reason", "no"), CompleteNodeOptions{Decider: alice})
				return r.NextStatus, err
			},
			check: func(t *testing.T, trace string) {
				mustContain(t, trace, "blocked=true", "approval REJECTED", "approval/rejection_reason")
			},
		},
		{
			name: "vertical loop-back",
			run: func(t *testing.T, e *GraphEngine, f transitionFixture) (string, error) {
				r, err := e.CompleteNodeWith(f.gate1, false, textArtifact("review", "ng"), CompleteNodeOptions{ClaimToken: f.tokens[f.gate1]})
				return r.NextStatus, err
			},
			check: func(t *testing.T, trace string) {
				mustContain(t, trace, "blocked=false", "impl TODO it=1", "gate1 AWAITING FIX", "gate2 TODO it=0")
			},
		},
		{
			name: "second verdict of the same round counts nothing",
			setup: func(t *testing.T, f transitionFixture, repo store.GraphRepository) {
				// gate2 is still working when gate1's loop-back rewinds it:
				// rewound to TODO, then handed out again.
				f.claim(t, repo, f.gate2)
				e := New(repo)
				if _, err := e.CompleteNodeWith(f.gate1, false, nil, CompleteNodeOptions{}); err != nil {
					t.Fatalf("first loop-back: %v", err)
				}
				f.claim(t, repo, f.gate2)
			},
			run: func(t *testing.T, e *GraphEngine, f transitionFixture) (string, error) {
				r, err := e.CompleteNodeWith(f.gate2, false, nil, CompleteNodeOptions{ClaimToken: f.tokens[f.gate2]})
				return r.NextStatus, err
			},
			check: func(t *testing.T, trace string) {
				// impl's count moves once for the round; the rewind still
				// sweeps gate1 (AWAITING FIX, not TODO) back to TODO.
				mustContain(t, trace, "impl TODO it=1", "gate1 TODO", "gate2 AWAITING FIX")
			},
		},
		{
			name: "sideways loop-back",
			run: func(t *testing.T, e *GraphEngine, f transitionFixture) (string, error) {
				r, err := e.CompleteNodeWith(f.side, false, nil, CompleteNodeOptions{ClaimToken: f.tokens[f.side]})
				return r.NextStatus, err
			},
			check: func(t *testing.T, trace string) {
				mustContain(t, trace, "impl TODO it=1", "side AWAITING FIX", "gate1 TODO", "gate2 TODO")
			},
		},
		{
			name: "round limit blocks and leaves the node claimed",
			setup: func(t *testing.T, f transitionFixture, repo store.GraphRepository) {
				two := 2
				if _, err := repo.UpdateNode(f.impl, store.NodePatch{IterationCount: &two}); err != nil {
					t.Fatal(err)
				}
			},
			run: func(t *testing.T, e *GraphEngine, f transitionFixture) (string, error) {
				r, err := e.CompleteNodeWith(f.gate1, false, textArtifact("review", "ng"), CompleteNodeOptions{ClaimToken: f.tokens[f.gate1]})
				return r.NextStatus, err
			},
			check: func(t *testing.T, trace string) {
				mustContain(t, trace, "blocked=true", "impl DONE it=2", "gate1 IN PROGRESS it=0 max=3 claimed=true", "gate1/review")
			},
		},
		{
			name: "no loop edge blocks and leaves the node claimed",
			run: func(t *testing.T, e *GraphEngine, f transitionFixture) (string, error) {
				r, err := e.CompleteNodeWith(f.lone, false, nil, CompleteNodeOptions{ClaimToken: f.tokens[f.lone]})
				return r.NextStatus, err
			},
			check: func(t *testing.T, trace string) {
				mustContain(t, trace, "blocked=true", "lone IN PROGRESS it=0 max=3 claimed=true")
			},
		},
		{
			name: "manual node passes",
			run: func(t *testing.T, e *GraphEngine, f transitionFixture) (string, error) {
				r, err := e.CompleteNodeWith(f.release, true, nil, CompleteNodeOptions{Decider: alice})
				return r.NextStatus, err
			},
			check: func(t *testing.T, trace string) {
				mustContain(t, trace, "release DONE")
			},
		},
		{
			name: "stale claim token is refused",
			setup: func(t *testing.T, f transitionFixture, repo store.GraphRepository) {
				todo := domain.NodeTODO
				if _, err := repo.UpdateNode(f.gate1, store.NodePatch{Status: &todo}); err != nil {
					t.Fatal(err)
				}
				old := f.tokens[f.gate1]
				f.claim(t, repo, f.gate1)
				f.tokens["stale"] = old
			},
			run: func(t *testing.T, e *GraphEngine, f transitionFixture) (string, error) {
				_, err := e.CompleteNodeWith(f.gate1, true, textArtifact("review", "late"), CompleteNodeOptions{ClaimToken: f.tokens["stale"]})
				if !isInvalidNodeState(err) {
					return "", fmt.Errorf("want INVALID_NODE_STATE, got %v", err)
				}
				return "refused", nil
			},
			check: func(t *testing.T, trace string) {
				mustContain(t, trace, "gate1 IN PROGRESS it=0 max=3 claimed=true", "artifacts=[]")
			},
		},
		{
			name: "reopen-nodes",
			setup: func(t *testing.T, f transitionFixture, repo store.GraphRepository) {
				if _, err := New(repo).CompleteNodeWith(f.approval, false, nil, CompleteNodeOptions{}); err != nil {
					t.Fatal(err)
				}
			},
			run: func(t *testing.T, e *GraphEngine, f transitionFixture) (string, error) {
				_, err := e.ReopenNodes(f.ticketID, []string{f.impl, f.approval})
				return "", err
			},
			check: func(t *testing.T, trace string) {
				mustContain(t, trace, "blocked=false", "impl TODO it=1", "approval TODO it=1", "gate2 TODO it=1", "release TODO it=0")
			},
		},
		{
			name: "grant-iterations",
			run: func(t *testing.T, e *GraphEngine, f transitionFixture) (string, error) {
				nodes, err := e.GrantIterations(f.ticketID, []string{f.impl, f.gate1}, 2)
				var parts []string
				for _, n := range nodes {
					parts = append(parts, fmt.Sprintf("%s=%d", n.Name, n.MaxIterations))
				}
				return strings.Join(parts, ","), err
			},
			check: func(t *testing.T, trace string) {
				mustContain(t, trace, "impl DONE it=0 max=5", "gate1 IN PROGRESS it=0 max=5 claimed=true")
			},
		},
		{
			name: "unstick-node",
			run: func(t *testing.T, e *GraphEngine, f transitionFixture) (string, error) {
				res, err := e.UnstickNodeWith(f.gate1, UnstickOptions{})
				return string(res.Node.Status), err
			},
			check: func(t *testing.T, trace string) {
				mustContain(t, trace, "gate1 TODO it=0 max=3 claimed=false")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results := map[string]string{}
			forEachTransitionPath(t, func(t *testing.T, e *GraphEngine, repo store.GraphRepository, projectID string) {
				e.SetLogf(func(string, ...any) {})
				f := newTransitionFixture(t, repo, projectID)
				if tc.setup != nil {
					tc.setup(t, f, repo)
				}
				out, err := tc.run(t, e, f)
				if err != nil {
					t.Fatalf("run: %v", err)
				}
				trace := f.trace(t, repo)
				tc.check(t, trace)
				results[t.Name()[strings.LastIndex(t.Name(), "/")+1:]] = "result=" + out + "\n" + trace
			})
			if results["atomic"] != results["sequential"] {
				t.Fatalf("the two paths diverged:\natomic:\n%s\n\nsequential:\n%s", results["atomic"], results["sequential"])
			}
		})
	}
}

func mustContain(t *testing.T, trace string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(trace, w) {
			t.Errorf("trace lacks %q:\n%s", w, trace)
		}
	}
}

// TestCompleteNodeWith_RecordsTheDecisionOnlyOnTheAtomicPath: the decider is
// written on a manual node together with its status (atomic path), never
// on an automatic node, and the sequential path -- an HTTP data source
// older than 1.2, which cannot keep it -- records none.
func TestCompleteNodeWith_RecordsTheDecisionOnlyOnTheAtomicPath(t *testing.T) {
	forEachTransitionPath(t, func(t *testing.T, e *GraphEngine, repo store.GraphRepository, projectID string) {
		atomic := !strings.HasSuffix(t.Name(), "sequential")
		f := newTransitionFixture(t, repo, projectID)
		e.SetClock(func() time.Time { return time.Date(2026, 9, 30, 1, 2, 3, 0, time.UTC) })
		decider := &Decider{Name: "Alice", NameIsFallback: true}
		if _, err := e.CompleteNodeWith(f.approval, false, nil, CompleteNodeOptions{Decider: decider}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.CompleteNodeWith(f.gate1, true, nil, CompleteNodeOptions{Decider: decider}); err != nil {
			t.Fatal(err)
		}
		approval, _ := repo.GetNode(f.approval)
		gate1, _ := repo.GetNode(f.gate1)
		if gate1.DecidedByName != nil {
			t.Errorf("an automatic node recorded a decider: %q", *gate1.DecidedByName)
		}
		if !atomic {
			if approval.DecidedByName != nil {
				t.Errorf("the sequential path recorded a decider: %q", *approval.DecidedByName)
			}
			return
		}
		if approval.DecidedByName == nil || *approval.DecidedByName != "Alice" ||
			approval.DecidedByNameIsFallback == nil || !*approval.DecidedByNameIsFallback ||
			approval.DecidedAt == nil || *approval.DecidedAt != "2026-09-30T01:02:03Z" ||
			approval.DecidedByAutopilot == nil || *approval.DecidedByAutopilot {
			t.Fatalf("decision = %v %v %v %v", deref(approval.DecidedByName), approval.DecidedByNameIsFallback, deref(approval.DecidedAt), approval.DecidedByAutopilot)
		}
		// Reopening the rejected gate is a status write that is not a
		// decision: it clears the decider.
		if _, err := e.ReopenNodes(f.ticketID, []string{f.approval}); err != nil {
			t.Fatal(err)
		}
		approval, _ = repo.GetNode(f.approval)
		if approval.DecidedByName != nil || approval.DecidedAt != nil || approval.DecidedByAutopilot != nil || approval.DecidedByNameIsFallback != nil {
			t.Fatal("reopen-nodes left the previous decision on the gate")
		}
	})
}

// TestCompleteNodeWith_AutopilotDecision: a decision made with --session in
// a session begun for an autopilot run is recorded as the autopilot's.
func TestCompleteNodeWith_AutopilotDecision(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	f := newTransitionFixture(t, repo, projectID)
	now := time.Now().UTC().Format(domain.SessionTimestampLayout)
	ss := repo.(store.ProcessingSessionStore)
	for _, s := range []domain.ProcessingSession{
		{ID: "sess-run", ProjectID: projectID, TicketID: f.ticketID, ActorName: "A", MachineID: "m", RunID: "run-1", StartedAt: now, Heartbeat: now},
		{ID: "sess-person", ProjectID: projectID, TicketID: f.ticketID, ActorName: "A", MachineID: "m", StartedAt: now, Heartbeat: now},
	} {
		if err := ss.SaveProcessingSession(s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.CompleteNodeWith(f.release, true, nil, CompleteNodeOptions{Decider: &Decider{Name: "A"}, SessionID: "sess-run"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.CompleteNodeWith(f.approval, true, nil, CompleteNodeOptions{Decider: &Decider{Name: "A"}, SessionID: "sess-person"}); err != nil {
		t.Fatal(err)
	}
	release, _ := repo.GetNode(f.release)
	approval, _ := repo.GetNode(f.approval)
	if release.DecidedByAutopilot == nil || !*release.DecidedByAutopilot {
		t.Error("a decision in an autopilot run's session was not recorded as the autopilot's")
	}
	if approval.DecidedByAutopilot == nil || *approval.DecidedByAutopilot {
		t.Error("a decision in a person's session was recorded as the autopilot's")
	}
}

// abaRepo is the SQLite repository with a hook that runs right after the
// first GetNode of hookNode -- i.e. between CompleteNodeWith's read and its
// write. It embeds *store.SQLiteRepository, not the GraphRepository
// interface, so the NodeTransitionApplier add-on stays visible and the
// completion takes the atomic path this test is about.
type abaRepo struct {
	*store.SQLiteRepository
	hookNode string
	hook     func()
}

func (r *abaRepo) GetNode(id string) (*domain.GraphNode, error) {
	n, err := r.SQLiteRepository.GetNode(id)
	if id == r.hookNode && r.hook != nil {
		hook := r.hook
		r.hook = nil
		hook()
	}
	return n, err
}

func newSQLiteForHooks(t *testing.T) (*store.SQLiteRepository, string) {
	t.Helper()
	inner, err := store.NewSQLiteRepository(filepath.Join(t.TempDir(), "hooks.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := inner.Init(); err != nil {
		t.Fatal(err)
	}
	proj, err := inner.CreateProject("Hooks", "HOOK")
	if err != nil {
		t.Fatal(err)
	}
	return inner, proj.ID
}

// TestCompleteNodeWith_ABA is completion criterion 4: a loop-back rewinds a
// node a worker is still judging and get-executable hands it out again; the
// old worker's verdict must not be recorded.
func TestCompleteNodeWith_ABA(t *testing.T) {
	// (a) With --claim: refused on both paths (the sequential path's
	// --claim check catches it too, as it did before DFLT-00329).
	t.Run("with --claim", func(t *testing.T) {
		forEachTransitionPath(t, func(t *testing.T, e *GraphEngine, repo store.GraphRepository, projectID string) {
			f := newTransitionFixture(t, repo, projectID)
			stale := f.tokens[f.gate1]
			// A sibling gate rejects: the loop-back rewinds gate1 ...
			f.claim(t, repo, f.gate2)
			if _, err := e.CompleteNodeWith(f.gate2, false, nil, CompleteNodeOptions{ClaimToken: f.tokens[f.gate2]}); err != nil {
				t.Fatal(err)
			}
			// ... impl is redone, and gate1 is handed out again.
			if _, err := e.CompleteNode(f.impl, true, nil); err == nil {
				t.Fatal("impl at TODO completed without a claim")
			}
			f.claim(t, repo, f.gate1)
			_, err := e.CompleteNodeWith(f.gate1, true, textArtifact("review", "stale verdict"), CompleteNodeOptions{ClaimToken: stale})
			if !isInvalidNodeState(err) {
				t.Fatalf("err = %v, want INVALID_NODE_STATE", err)
			}
			n, _ := repo.GetNode(f.gate1)
			arts, _ := repo.ListArtifactsByNode(f.gate1)
			if n.Status != domain.NodeInProgress || n.ClaimToken == nil || *n.ClaimToken != f.tokens[f.gate1] || len(arts) != 0 {
				t.Fatalf("the stale verdict was recorded: status %s, %d artifacts", n.Status, len(arts))
			}
		})
	})
	// (b) Without --claim, the rewind and the new claim landing between
	// the read and the write: the atomic path compares the claim token it
	// read and refuses. There is deliberately no sequential version: the
	// sequential path cannot tell this case apart (its known limitation --
	// an HTTP data source older than 1.2 -- see applyNodeTransitionSequential).
	t.Run("without --claim, between read and write", func(t *testing.T) {
		inner, projectID := newSQLiteForHooks(t)
		repo := &abaRepo{SQLiteRepository: inner}
		var _ store.NodeTransitionApplier = repo
		e := New(repo)
		f := newTransitionFixture(t, repo, projectID)
		repo.hookNode = f.gate1
		repo.hook = func() {
			todo := domain.NodeTODO
			if _, err := inner.UpdateNode(f.gate1, store.NodePatch{Status: &todo}); err != nil {
				t.Error(err)
			}
			f.claim(t, inner, f.gate1)
		}
		_, err := e.CompleteNodeWith(f.gate1, false, textArtifact("review", "stale verdict"), CompleteNodeOptions{})
		if !isInvalidNodeState(err) {
			t.Fatalf("err = %v, want INVALID_NODE_STATE", err)
		}
		var apiErr *domain.APIError
		errors.As(err, &apiErr)
		if apiErr.Details["reason"] != store.ConflictClaimToken {
			t.Errorf("details = %v, want reason claim_token", apiErr.Details)
		}
		n, _ := inner.GetNode(f.gate1)
		impl, _ := inner.GetNode(f.impl)
		arts, _ := inner.ListArtifactsByNode(f.gate1)
		if n.Status != domain.NodeInProgress || impl.Status != domain.NodeDone || impl.IterationCount != 0 || len(arts) != 0 {
			t.Fatalf("the stale verdict was recorded: gate1 %s, impl %s it=%d, %d artifacts", n.Status, impl.Status, impl.IterationCount, len(arts))
		}
	})
}

// TestCompleteNodeWith_FirstDecisionWins: approve and reject of the same
// approval gate, the second landing between the first's read and write.
// (The goroutine race against SQLite and MySQL is in internal/store's
// node_transition_concurrency_test.go.)
func TestCompleteNodeWith_FirstDecisionWins(t *testing.T) {
	inner, projectID := newSQLiteForHooks(t)
	repo := &abaRepo{SQLiteRepository: inner}
	e := New(repo)
	f := newTransitionFixture(t, repo, projectID)
	repo.hookNode = f.approval
	repo.hook = func() {
		if _, err := New(inner).CompleteNodeWith(f.approval, true, nil, CompleteNodeOptions{Decider: &Decider{Name: "Bob"}}); err != nil {
			t.Error(err)
		}
	}
	_, err := e.CompleteNodeWith(f.approval, false, textArtifact("rejection_reason", "no"), CompleteNodeOptions{Decider: &Decider{Name: "Alice"}})
	if !isInvalidNodeState(err) {
		t.Fatalf("err = %v, want INVALID_NODE_STATE", err)
	}
	if !strings.Contains(err.Error(), "from TODO to DONE") {
		t.Errorf("message does not say what changed: %v", err)
	}
	n, _ := inner.GetNode(f.approval)
	arts, _ := inner.ListArtifactsByNode(f.approval)
	tk, _ := inner.GetTicket(f.ticketID)
	if n.Status != domain.NodeDone || deref(n.DecidedByName) != "Bob" || len(arts) != 0 || tk.Blocked {
		t.Fatalf("the losing rejection was recorded: %s by %s, %d artifacts, blocked=%v", n.Status, deref(n.DecidedByName), len(arts), tk.Blocked)
	}
}

// TestCompleteNodeWith_ClosedInBetween: a ticket closed between the read and
// the write refuses the completion.
func TestCompleteNodeWith_ClosedInBetween(t *testing.T) {
	inner, projectID := newSQLiteForHooks(t)
	repo := &abaRepo{SQLiteRepository: inner}
	e := New(repo)
	f := newTransitionFixture(t, repo, projectID)
	repo.hookNode = f.release
	repo.hook = func() {
		if _, err := New(inner).CloseTicket(f.ticketID, "withdrawn"); err != nil {
			t.Error(err)
		}
	}
	_, err := e.CompleteNode(f.release, true, nil)
	if !isInvalidNodeState(err) || !strings.Contains(err.Error(), "CLOSED") {
		t.Fatalf("err = %v, want INVALID_NODE_STATE naming CLOSED", err)
	}
	n, _ := inner.GetNode(f.release)
	if n.Status != domain.NodeTODO {
		t.Fatalf("release = %s, want TODO", n.Status)
	}
}

// transitionHookRepo runs hook once, just before the first transition reaches
// the SQLite repository -- a stand-in for another command's transition
// landing first.
type transitionHookRepo struct {
	*store.SQLiteRepository
	hook func()
}

func (r *transitionHookRepo) ApplyNodeTransition(ticketID string, t store.NodeTransition) (store.NodeTransitionResult, error) {
	if r.hook != nil {
		hook := r.hook
		r.hook = nil
		hook()
	}
	return r.SQLiteRepository.ApplyNodeTransition(ticketID, t)
}

// TestReopenNodes_ConcurrentReopenSpendsOneIteration: two reopen-nodes of
// the same nodes at once -- the second one's read is taken before the
// first one's write. Before DFLT-00329 both wrote read+1 and the nodes were
// reset twice; now the second is refused.
func TestReopenNodes_ConcurrentReopenSpendsOneIteration(t *testing.T) {
	inner, projectID := newSQLiteForHooks(t)
	repo := &transitionHookRepo{SQLiteRepository: inner}
	e := New(repo)
	f := newTransitionFixture(t, repo, projectID)
	if _, err := e.CompleteNode(f.approval, false, nil); err != nil {
		t.Fatal(err)
	}
	repo.hook = func() {
		if _, err := New(inner).ReopenNodes(f.ticketID, []string{f.approval}); err != nil {
			t.Error(err)
		}
	}
	_, err := e.ReopenNodes(f.ticketID, []string{f.approval})
	if !isInvalidNodeState(err) {
		t.Fatalf("err = %v, want INVALID_NODE_STATE", err)
	}
	n, _ := inner.GetNode(f.approval)
	tk, _ := inner.GetTicket(f.ticketID)
	if n.Status != domain.NodeTODO || n.IterationCount != 1 || tk.Blocked {
		t.Fatalf("approval %s it=%d, blocked=%v; want TODO it=1 unblocked", n.Status, n.IterationCount, tk.Blocked)
	}
}

// TestGrantIterations_ConcurrentGrantsBothCount: two grants at once used to
// write back read+extra each, so one was lost.
func TestGrantIterations_ConcurrentGrantsBothCount(t *testing.T) {
	inner, projectID := newSQLiteForHooks(t)
	repo := &transitionHookRepo{SQLiteRepository: inner}
	e := New(repo)
	f := newTransitionFixture(t, repo, projectID)
	repo.hook = func() {
		if _, err := New(inner).GrantIterations(f.ticketID, []string{f.impl}, 1); err != nil {
			t.Error(err)
		}
	}
	nodes, err := e.GrantIterations(f.ticketID, []string{f.impl}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if nodes[0].MaxIterations != 6 {
		t.Fatalf("max_iterations = %d, want 6 (3 + 1 + 2)", nodes[0].MaxIterations)
	}
}

// TestUnstickNodeWith_CompletedInBetween: a node completed between
// unstick-node's check and its release is not released.
func TestUnstickNodeWith_CompletedInBetween(t *testing.T) {
	inner, projectID := newSQLiteForHooks(t)
	repo := &transitionHookRepo{SQLiteRepository: inner}
	e := New(repo)
	f := newTransitionFixture(t, repo, projectID)
	repo.hook = func() {
		if _, err := New(inner).CompleteNode(f.gate1, true, nil); err != nil {
			t.Error(err)
		}
	}
	_, err := e.UnstickNodeWith(f.gate1, UnstickOptions{})
	if !isInvalidNodeState(err) {
		t.Fatalf("err = %v, want INVALID_NODE_STATE", err)
	}
	n, _ := inner.GetNode(f.gate1)
	if n.Status != domain.NodeDone {
		t.Fatalf("gate1 = %s, want DONE", n.Status)
	}
}

// TestApplyTransition_NonRequiredCheckStepAppliedMatches: a step that writes
// nothing and is not required is only a check, and its Applied has to mean
// the same on both paths -- true when its conditions hold, false when they
// do not -- without failing the transition (code review round 1, item 5).
func TestApplyTransition_NonRequiredCheckStepAppliedMatches(t *testing.T) {
	forEachTransitionPath(t, func(t *testing.T, e *GraphEngine, repo store.GraphRepository, projectID string) {
		f := newTransitionFixture(t, repo, projectID)
		done := domain.NodeDone
		res, err := e.applyTransition(f.ticketID, store.NodeTransition{Steps: []store.NodeStep{
			{NodeID: f.impl, IfStatusIn: []domain.NodeStatus{domain.NodeDone}},
			{NodeID: f.release, IfStatusIn: []domain.NodeStatus{domain.NodeDone}},
			{NodeID: f.gate2, Required: true, IfStatusIn: []domain.NodeStatus{domain.NodeDone}, SetStatus: &done},
		}})
		if err != nil {
			t.Fatalf("applyTransition: %v", err)
		}
		if want := []bool{true, false, true}; fmt.Sprint(res.Applied) != fmt.Sprint(want) {
			t.Fatalf("Applied = %v, want %v", res.Applied, want)
		}
	})
}
