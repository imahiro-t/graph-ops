// package store_test for the same reason as claim_concurrency_mysql_test.go:
// these tests drive the engine, and living in internal/store is what gets
// them run against a real MySQL by dev/mysql/test.sh.
package store_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// transitionBackend is a backend the concurrency tests race against: SQLite
// (one connection, so the racers are serialized statement by statement --
// the atomic transaction is still what decides) and MySQL (really
// concurrent), when dev/mysql/test.sh provides one.
type transitionBackend struct {
	name string
	open func(t *testing.T) store.GraphRepository
}

func transitionBackends() []transitionBackend {
	return []transitionBackend{
		{name: "sqlite", open: func(t *testing.T) store.GraphRepository {
			repo, err := store.Open(store.Config{Backend: "sqlite", SQLitePath: filepath.Join(t.TempDir(), "race.db")})
			if err != nil {
				t.Fatalf("store.Open(sqlite): %v", err)
			}
			return repo
		}},
		{name: "mysql", open: func(t *testing.T) store.GraphRepository { return mysqlRepoForClaimTest(t) }},
	}
}

func newRaceTicket(t *testing.T, repo store.GraphRepository, title string) domain.Ticket {
	t.Helper()
	proj, err := repo.CreateProject("Race "+time.Now().Format("150405.000000000"), "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	tk, err := repo.CreateTicket(proj.ID, domain.Ticket{Title: title, Status: domain.TicketInProgress, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	return tk
}

func errCode(err error) domain.ErrorCode {
	var apiErr *domain.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return ""
}

// readBarrierRepo makes the first `parties` GetNode calls for nodeID wait
// for each other, so every racer has read the node before any of them
// writes -- the "at the same moment" of two members pressing approve and
// reject, made deterministic. (Without it, SQLite's single connection lets
// a racer read after another one's decision committed, which is a later
// decision on the new state rather than a concurrent one: a release a
// "reject" left at TODO may legitimately be approved afterwards.) It
// forwards ApplyNodeTransition, so the engine keeps the atomic path.
type readBarrierRepo struct {
	store.GraphRepository
	applier store.NodeTransitionApplier
	nodeID  string
	mu      sync.Mutex
	left    int
	ready   chan struct{}
}

func newReadBarrierRepo(repo store.GraphRepository, nodeID string, parties int) *readBarrierRepo {
	return &readBarrierRepo{GraphRepository: repo, applier: repo.(store.NodeTransitionApplier), nodeID: nodeID, left: parties, ready: make(chan struct{})}
}

func (r *readBarrierRepo) GetNode(id string) (*domain.GraphNode, error) {
	n, err := r.GraphRepository.GetNode(id)
	if id != r.nodeID {
		return n, err
	}
	r.mu.Lock()
	wait := r.left > 0
	if wait {
		r.left--
		if r.left == 0 {
			close(r.ready)
		}
	}
	r.mu.Unlock()
	if wait {
		<-r.ready
	}
	return n, err
}

func (r *readBarrierRepo) ApplyNodeTransition(ticketID string, t store.NodeTransition) (store.NodeTransitionResult, error) {
	return r.applier.ApplyNodeTransition(ticketID, t)
}

// TestManualDecision_ConcurrentApproveAndReject is completion criterion 2:
// several members press "approve" and "reject" on the same manual node at
// once -- an approval_gate, a release, and a custom is_manual node (whose
// "reject" blocks the ticket without changing the node's status). Exactly
// one call gets through; every other one is INVALID_NODE_STATE and wrote
// nothing, so the node, its artifacts, its decider and the ticket's blocked
// flag all agree with the winner.
func TestManualDecision_ConcurrentApproveAndReject(t *testing.T) {
	kinds := []struct {
		name   string
		typ    domain.NodeType
		reject domain.NodeStatus // the node's status after a winning reject
	}{
		{"approval_gate", domain.NodeTypeApprovalGate, domain.NodeRejected},
		{"release", domain.NodeTypeRelease, domain.NodeTODO},
		{"custom manual", domain.NodeType("sign_off"), domain.NodeTODO},
	}
	const rounds, perSide = 4, 4
	for _, b := range transitionBackends() {
		t.Run(b.name, func(t *testing.T) {
			repo := b.open(t)
			for _, k := range kinds {
				t.Run(k.name, func(t *testing.T) {
					for round := 0; round < rounds; round++ {
						tk := newRaceTicket(t, repo, "decide "+k.name)
						node, err := repo.CreateNode(domain.GraphNode{TicketID: tk.ID, Name: k.name, Type: k.typ, Status: domain.NodeTODO, MaxIterations: 3, IsManual: true})
						if err != nil {
							t.Fatal(err)
						}
						type outcome struct {
							who     string
							approve bool
							err     error
						}
						racing := newReadBarrierRepo(repo, node.ID, 2*perSide)
						start := make(chan struct{})
						results := make(chan outcome, 2*perSide)
						var wg sync.WaitGroup
						for i := 0; i < 2*perSide; i++ {
							approve := i%2 == 0
							who := fmt.Sprintf("member-%d", i)
							wg.Add(1)
							go func() {
								defer wg.Done()
								var arts []domain.Artifact
								if !approve {
									reason := "rejected by " + who
									arts = []domain.Artifact{{Name: "rejection_reason", Type: domain.ArtifactText, Content: &reason}}
								}
								<-start
								_, err := engine.New(racing).CompleteNodeWith(node.ID, approve, arts, engine.CompleteNodeOptions{Decider: &engine.Decider{Name: who}})
								results <- outcome{who, approve, err}
							}()
						}
						close(start)
						wg.Wait()
						close(results)

						var winners []outcome
						for r := range results {
							switch {
							case r.err == nil:
								winners = append(winners, r)
							case errCode(r.err) != domain.ErrCodeInvalidNodeState:
								t.Fatalf("round %d: %s: %v, want success or INVALID_NODE_STATE", round, r.who, r.err)
							}
						}
						if len(winners) != 1 {
							t.Fatalf("round %d: %d calls got through, want exactly 1", round, len(winners))
						}
						w := winners[0]
						got, _ := repo.GetNode(node.ID)
						arts, _ := repo.ListArtifactsByNode(node.ID)
						ticket, _ := repo.GetTicket(tk.ID)
						if w.approve {
							if got.Status != domain.NodeDone || len(arts) != 0 || ticket.Blocked || deref(got.DecidedByName) != w.who {
								t.Fatalf("round %d: approve by %s won, but node %s decided by %q, %d artifacts, blocked=%v", round, w.who, got.Status, deref(got.DecidedByName), len(arts), ticket.Blocked)
							}
							continue
						}
						if got.Status != k.reject || len(arts) != 1 || !strings.Contains(deref(arts[0].Content), w.who) || !ticket.Blocked {
							t.Fatalf("round %d: reject by %s won, but node %s, %d artifacts, blocked=%v", round, w.who, got.Status, len(arts), ticket.Blocked)
						}
						// A blocking reject writes no status, so it records
						// no decider either (the decision columns go only
						// with a status write).
						wantDecider := ""
						if k.reject == domain.NodeRejected {
							wantDecider = w.who
						}
						if deref(got.DecidedByName) != wantDecider {
							t.Fatalf("round %d: decider %q, want %q", round, deref(got.DecidedByName), wantDecider)
						}
					}
				})
			}
		})
	}
}

// firstReadRepo records the first GetNode of nodeID -- for a completion,
// the read its whole decision (and the conditions of its write) is made on.
// It forwards ApplyNodeTransition, so the engine keeps the atomic path.
type firstReadRepo struct {
	store.GraphRepository
	applier store.NodeTransitionApplier
	nodeID  string
	mu      sync.Mutex
	read    *domain.GraphNode
}

func newFirstReadRepo(repo store.GraphRepository, nodeID string) *firstReadRepo {
	return &firstReadRepo{GraphRepository: repo, applier: repo.(store.NodeTransitionApplier), nodeID: nodeID}
}

func (r *firstReadRepo) GetNode(id string) (*domain.GraphNode, error) {
	n, err := r.GraphRepository.GetNode(id)
	if id == r.nodeID && n != nil {
		r.mu.Lock()
		if r.read == nil {
			cp := *n
			r.read = &cp
		}
		r.mu.Unlock()
	}
	return n, err
}

func (r *firstReadRepo) ApplyNodeTransition(ticketID string, t store.NodeTransition) (store.NodeTransitionResult, error) {
	return r.applier.ApplyNodeTransition(ticketID, t)
}

// TestLoopBack_ConcurrentWithRewindAndReclaim (completion criterion 4,
// raced): a review is completed without --claim while a loop-back rewinds
// it and get-executable hands it out again. Whenever the completion is
// refused it wrote nothing. Whenever it got through, its artifact is there
// and the state agrees with the claim it read:
//
//   - it read the first claim (t1): it landed before the rewind, and the
//     node was then rewound and reclaimed on top of it (IN PROGRESS t2);
//   - it read the second claim (t2): the rewind and the reclaim had both
//     happened before it even read, and a completion without --claim
//     cannot tell that claim from its own, so it legitimately completes
//     it (DONE, the token cleared). Only --claim refuses this one; that is
//     why the CLI warns about a completion without it (DFLT-00367).
//
// A completion that read t1 and still ends DONE would be the ABA this
// test exists to catch: its write went through although the node had been
// rewound and reclaimed between its read and its write.
func TestLoopBack_ConcurrentWithRewindAndReclaim(t *testing.T) {
	for _, b := range transitionBackends() {
		t.Run(b.name, func(t *testing.T) {
			repo := b.open(t)
			for round := 0; round < 10; round++ {
				tk := newRaceTicket(t, repo, "aba")
				gate, err := repo.CreateNode(domain.GraphNode{TicketID: tk.ID, Name: "gate", Type: domain.NodeTypeReviewGate, Status: domain.NodeTODO, MaxIterations: 3})
				if err != nil {
					t.Fatal(err)
				}
				claim := func(token string) {
					c := &domain.NodeClaim{Name: "W", Token: token, ClaimedAt: time.Now().UTC().Format(time.RFC3339Nano)}
					if n, err := repo.ClaimNode(gate.ID, domain.NodeInProgress, []domain.NodeStatus{domain.NodeInProgress, domain.NodeInReview}, c); err != nil || n == nil {
						t.Errorf("ClaimNode: %v, %v", n, err)
					}
				}
				claim("t1")
				completer := newFirstReadRepo(repo, gate.ID)
				start := make(chan struct{})
				var wg sync.WaitGroup
				var completeErr error
				wg.Add(2)
				go func() {
					defer wg.Done()
					<-start
					todo := domain.NodeTODO
					if _, err := repo.UpdateNode(gate.ID, store.NodePatch{Status: &todo}); err != nil {
						t.Error(err)
					}
					claim("t2")
				}()
				go func() {
					defer wg.Done()
					verdict := "stale?"
					<-start
					_, completeErr = engine.New(completer).CompleteNodeWith(gate.ID, true, []domain.Artifact{{Name: "review", Type: domain.ArtifactText, Content: &verdict}}, engine.CompleteNodeOptions{})
				}()
				close(start)
				wg.Wait()
				arts, _ := repo.ListArtifactsByNode(gate.ID)
				n, _ := repo.GetNode(gate.ID)
				if completer.read == nil {
					t.Fatalf("round %d: the completion never read the gate", round)
				}
				read := fmt.Sprintf("%s %s", completer.read.Status, deref(completer.read.ClaimToken))
				got := fmt.Sprintf("%s %s", n.Status, deref(n.ClaimToken))
				// The rewind and the reclaim always complete, whichever
				// came first (the reclaim excludes only IN PROGRESS / IN
				// REVIEW), so every outcome but one ends IN PROGRESS t2.
				want := "IN PROGRESS t2"
				if completeErr != nil {
					if errCode(completeErr) != domain.ErrCodeInvalidNodeState {
						t.Fatalf("round %d: %v, want INVALID_NODE_STATE", round, completeErr)
					}
					if len(arts) != 0 {
						t.Fatalf("round %d: a refused completion (read %s) left %d artifacts", round, read, len(arts))
					}
				} else {
					if len(arts) != 1 {
						t.Fatalf("round %d: a completion that got through (read %s) has %d artifacts", round, read, len(arts))
					}
					switch read {
					case "IN PROGRESS t1":
						// Landed before the rewind; rewound and reclaimed on top.
					case "IN PROGRESS t2":
						// Read after the reclaim: completes the new claim.
						want = "DONE "
					default:
						t.Fatalf("round %d: a completion that read %s got through", round, read)
					}
				}
				if got != want {
					t.Fatalf("round %d: completion read %s, err %v; gate %s token %q, want %q", round, read, completeErr, n.Status, deref(n.ClaimToken), want)
				}
			}
		})
	}
}

// TestReopenAndGrant_Concurrent: concurrent grant-iterations all count;
// concurrent reopen-nodes spend one iteration and reset once.
func TestReopenAndGrant_Concurrent(t *testing.T) {
	for _, b := range transitionBackends() {
		t.Run(b.name, func(t *testing.T) {
			repo := b.open(t)
			tk := newRaceTicket(t, repo, "grants")
			node, err := repo.CreateNode(domain.GraphNode{TicketID: tk.ID, Name: "gate", Type: domain.NodeTypeApprovalGate, Status: domain.NodeTODO, MaxIterations: 3, IsManual: true})
			if err != nil {
				t.Fatal(err)
			}
			const n = 6
			run := func(fn func() error) []error {
				start := make(chan struct{})
				errs := make(chan error, n)
				var wg sync.WaitGroup
				for i := 0; i < n; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						errs <- fn()
					}()
				}
				close(start)
				wg.Wait()
				close(errs)
				var out []error
				for err := range errs {
					out = append(out, err)
				}
				return out
			}
			for _, err := range run(func() error {
				_, err := engine.New(repo).GrantIterations(tk.ID, []string{node.ID}, 1)
				return err
			}) {
				if err != nil {
					t.Fatalf("GrantIterations: %v", err)
				}
			}
			if got, _ := repo.GetNode(node.ID); got.MaxIterations != 3+n {
				t.Fatalf("max_iterations = %d after %d concurrent grants of 1, want %d", got.MaxIterations, n, 3+n)
			}

			if _, err := engine.New(repo).CompleteNode(node.ID, false, nil); err != nil {
				t.Fatal(err)
			}
			successes := 0
			for _, err := range run(func() error {
				_, err := engine.New(repo).ReopenNodes(tk.ID, []string{node.ID})
				return err
			}) {
				switch {
				case err == nil:
					successes++
				case errCode(err) == domain.ErrCodeInvalidNodeState, strings.Contains(err.Error(), "is not blocked"), strings.Contains(err.Error(), "not DONE, REJECTED"):
					// Refused on the atomic check, or on the snapshot of a
					// reopen that landed before this one read.
				default:
					t.Fatalf("ReopenNodes: %v", err)
				}
			}
			got, _ := repo.GetNode(node.ID)
			ticket, _ := repo.GetTicket(tk.ID)
			if successes != 1 || got.Status != domain.NodeTODO || got.IterationCount != 1 || ticket.Blocked {
				t.Fatalf("%d reopens got through; gate %s it=%d, blocked=%v; want exactly 1, TODO it=1, unblocked", successes, got.Status, got.IterationCount, ticket.Blocked)
			}
		})
	}
}

// TestNodeTransition_DeadlockWithArtifactInserts_MySQL: add-artifact on one
// node of a ticket racing a loop-back that rewinds that node's siblings. The
// artifact INSERT takes shared locks on the ticket and node rows for its
// foreign keys while the transition holds the ticket row and waits for a
// node row, which InnoDB can resolve only by killing one of them (Error
// 1213). Neither may surface the bare driver error: each call succeeds or
// ends with CONCURRENT_WRITE_CONFLICT having written nothing, every
// artifact that was reported written is there, and the loop-back is either
// wholly applied or not at all.
func TestNodeTransition_DeadlockWithArtifactInserts_MySQL(t *testing.T) {
	repo := mysqlRepoForClaimTest(t)
	const rounds, writers = 8, 6
	for round := 0; round < rounds; round++ {
		tk := newRaceTicket(t, repo, "deadlock")
		mk := func(name string, typ domain.NodeType, status domain.NodeStatus) string {
			n, err := repo.CreateNode(domain.GraphNode{TicketID: tk.ID, Name: name, Type: typ, Status: status, MaxIterations: 5})
			if err != nil {
				t.Fatal(err)
			}
			return n.ID
		}
		impl := mk("impl", domain.NodeTypeImplementation, domain.NodeDone)
		failing := mk("failing", domain.NodeTypeReviewGate, domain.NodeTODO)
		var siblings []string
		for i := 0; i < 4; i++ {
			siblings = append(siblings, mk(fmt.Sprintf("sib%d", i), domain.NodeTypeReviewGate, domain.NodeDone))
		}
		edge := func(from, to string, c domain.EdgeCondition) {
			if _, err := repo.CreateEdge(domain.GraphEdge{ID: fmt.Sprintf("edge-%s-%s-%s", from, to, c), TicketID: tk.ID, FromNodeID: from, ToNodeID: to, Condition: c}); err != nil {
				t.Fatal(err)
			}
		}
		edge(impl, failing, domain.EdgeSuccess)
		edge(failing, impl, domain.EdgeLoop)
		for _, s := range siblings {
			edge(impl, s, domain.EdgeSuccess)
		}
		if n, err := repo.ClaimNode(failing, domain.NodeInProgress, []domain.NodeStatus{domain.NodeInProgress}, &domain.NodeClaim{Name: "W", Token: "tok", ClaimedAt: "2026-09-30T00:00:00Z"}); err != nil || n == nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		var transitionErr error
		artErrs := make([]error, writers)
		artIDs := make([]string, writers)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, transitionErr = engine.New(repo).CompleteNodeWith(failing, false, nil, engine.CompleteNodeOptions{ClaimToken: "tok"})
		}()
		for i := 0; i < writers; i++ {
			i := i
			artIDs[i] = fmt.Sprintf("art-dl-%d-%d-%d", round, i, time.Now().UnixNano())
			wg.Add(1)
			go func() {
				defer wg.Done()
				content := "x"
				<-start
				_, artErrs[i] = repo.CreateArtifact(domain.Artifact{ID: artIDs[i], TicketID: tk.ID, NodeID: siblings[i%len(siblings)], Name: "notes", Type: domain.ArtifactText, Content: &content})
			}()
		}
		close(start)
		wg.Wait()

		checkErr := func(what string, err error) {
			var myErr *mysqldriver.MySQLError
			if errors.As(err, &myErr) {
				t.Fatalf("round %d: %s leaked a bare MySQL error: %v", round, what, err)
			}
			if err != nil && errCode(err) != domain.ErrCodeConcurrentWriteConflict {
				t.Fatalf("round %d: %s: %v", round, what, err)
			}
		}
		checkErr("the loop-back", transitionErr)
		for i, err := range artErrs {
			checkErr("add-artifact", err)
			a, _ := repo.GetArtifact(artIDs[i])
			if (err == nil) != (a != nil) {
				t.Fatalf("round %d: add-artifact %d returned %v but the artifact is present=%v", round, i, err, a != nil)
			}
		}
		nodes, _ := repo.ListNodesByTicket(tk.ID)
		rewound := 0
		for _, n := range nodes {
			for _, s := range siblings {
				if n.ID == s && n.Status == domain.NodeTODO {
					rewound++
				}
			}
		}
		if transitionErr == nil && rewound != len(siblings) || transitionErr != nil && rewound != 0 {
			t.Fatalf("round %d: loop-back err=%v but %d of %d siblings rewound -- partially applied", round, transitionErr, rewound, len(siblings))
		}
	}
}

// TestNodeTransition_DifferentTicketsDoNotBlockEachOther_MySQL: completions
// of different tickets at once neither wait on each other's locks nor
// deadlock (the node rows are locked by primary key, which takes no gap
// lock -- unlike the ticket_id range read the graph batch had to drop,
// DFLT-00328).
func TestNodeTransition_DifferentTicketsDoNotBlockEachOther_MySQL(t *testing.T) {
	repo := mysqlRepoForClaimTest(t)
	const tickets = 12
	var nodeIDs []string
	for i := 0; i < tickets; i++ {
		tk := newRaceTicket(t, repo, "parallel")
		n, err := repo.CreateNode(domain.GraphNode{TicketID: tk.ID, Name: "approval", Type: domain.NodeTypeApprovalGate, Status: domain.NodeTODO, MaxIterations: 3, IsManual: true})
		if err != nil {
			t.Fatal(err)
		}
		nodeIDs = append(nodeIDs, n.ID)
	}
	start := make(chan struct{})
	errs := make(chan error, tickets)
	var wg sync.WaitGroup
	for i, id := range nodeIDs {
		approve := i%2 == 0
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := engine.New(repo).CompleteNodeWith(id, approve, nil, engine.CompleteNodeOptions{Decider: &engine.Decider{Name: "m"}})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a completion of its own ticket failed: %v", err)
		}
	}
}
