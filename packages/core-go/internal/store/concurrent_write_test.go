package store

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

// Tests for DFLT-00102's two store-layer fixes, run against both SQL
// backends:
//
//   - BUG-13: UpdateNode/UpdateProject wrote every column back, so two updates
//     touching different columns each reverted the other's work.
//   - CHK-01: claiming a node was a read followed by a write, so two callers
//     could both see a node free and both take it.
//
// Each has two kinds of test. The deterministic ones pin the guarantee itself
// -- an UPDATE reaches only the columns the patch names; a CAS against an
// excluded status changes no row -- and hold regardless of scheduling. The
// concurrent ones reproduce the original defects, and are the reason to
// believe the guarantees are the ones that actually matter; they were checked
// against the pre-fix code and fail there.

// backend is one SQL repository plus a project to hang fixtures off.
type backend struct {
	name string
	repo GraphRepository
	proj domain.Project
}

// eachSQLBackend runs fn against SQLite and, when a test server is configured,
// MySQL. The HTTP datasource is deliberately absent: it is a client for a
// remote system of record, and GraphRepository.ClaimNode says outright that it
// cannot make the claim atomic.
func eachSQLBackend(t *testing.T, fn func(t *testing.T, b backend)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) {
		repo := newTestRepo(t)
		proj, err := repo.CreateProject("Test Project", "TEST")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		fn(t, backend{name: "sqlite", repo: repo, proj: proj})
	})
	t.Run("mysql", func(t *testing.T) {
		repo := newTestMySQLRepo(t) // skips unless GRAPH_TEST_MYSQL_HOST is set
		proj, err := repo.CreateProject("Test Project", "TEST")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		fn(t, backend{name: "mysql", repo: repo, proj: proj})
	})
}

func (b backend) newNode(t *testing.T, status domain.NodeStatus) domain.GraphNode {
	t.Helper()
	ticket, err := b.repo.CreateTicket(b.proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := b.repo.CreateNode(domain.GraphNode{
		TicketID: ticket.ID, Name: "n", Type: domain.NodeTypeImplementation,
		Status: status, MaxIterations: 3,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	return node
}

func strPtr(s string) *string { return &s }

// --- BUG-13: an update must not touch columns its patch never named ---

// TestUpdateNode_PatchTouchesOnlyItsOwnColumns is the deterministic form of
// the fix. It needs no race: it sets assignee, then sends a patch that names
// only status, and checks the assignee survived. Under the old whole-row
// rewrite the status patch carried the assignee it had read along with it,
// which is only harmless while nothing else is writing.
func TestUpdateNode_PatchTouchesOnlyItsOwnColumns(t *testing.T) {
	eachSQLBackend(t, func(t *testing.T, b backend) {
		node := b.newNode(t, domain.NodeTODO)
		alice := strPtr("alice")
		if _, err := b.repo.UpdateNode(node.ID, NodePatch{Assignee: &alice}); err != nil {
			t.Fatalf("setting assignee: %v", err)
		}

		inProgress := domain.NodeInProgress
		got, err := b.repo.UpdateNode(node.ID, NodePatch{Status: &inProgress})
		if err != nil {
			t.Fatalf("UpdateNode(status): %v", err)
		}
		if got.Assignee == nil || *got.Assignee != "alice" {
			t.Errorf("a status-only patch changed assignee to %v, want alice", got.Assignee)
		}
		if got.Status != domain.NodeInProgress {
			t.Errorf("status = %s, want IN PROGRESS", got.Status)
		}
	})
}

// TestUpdateNode_AssigneeKeepsItsThreeStates: NodePatch.Assignee is a **string
// so "leave alone", "clear" and "set" stay distinguishable. Rewriting these
// updates column by column is exactly where those three could have been
// flattened into two -- and clearing an assignee is a thing the Web UI does.
func TestUpdateNode_AssigneeKeepsItsThreeStates(t *testing.T) {
	eachSQLBackend(t, func(t *testing.T, b backend) {
		for _, tc := range []struct {
			name  string
			patch NodePatch
			want  *string
		}{
			{"unspecified leaves it", NodePatch{}, strPtr("alice")},
			{"explicit nil clears it", NodePatch{Assignee: new(*string)}, nil},
			{"a value sets it", NodePatch{Assignee: func() **string { p := strPtr("bob"); return &p }()}, strPtr("bob")},
		} {
			t.Run(tc.name, func(t *testing.T) {
				node := b.newNode(t, domain.NodeTODO)
				alice := strPtr("alice")
				if _, err := b.repo.UpdateNode(node.ID, NodePatch{Assignee: &alice}); err != nil {
					t.Fatalf("setting assignee: %v", err)
				}

				got, err := b.repo.UpdateNode(node.ID, tc.patch)
				if err != nil {
					t.Fatalf("UpdateNode: %v", err)
				}
				switch {
				case tc.want == nil && got.Assignee != nil:
					t.Errorf("assignee = %q, want it cleared", *got.Assignee)
				case tc.want != nil && (got.Assignee == nil || *got.Assignee != *tc.want):
					t.Errorf("assignee = %v, want %q", got.Assignee, *tc.want)
				}
			})
		}
	})
}

// TestUpdateNode_EmptyPatchStillMovesUpdatedAt pins the one thing an update
// always does, however little the patch asks for. Callers and the Web UI's
// "last updated" column have always been able to rely on it, so the move to
// column-scoped writes must not quietly turn an empty patch into a no-op.
func TestUpdateNode_EmptyPatchStillMovesUpdatedAt(t *testing.T) {
	eachSQLBackend(t, func(t *testing.T, b backend) {
		node := b.newNode(t, domain.NodeTODO)

		got, err := b.repo.UpdateNode(node.ID, NodePatch{})
		if err != nil {
			t.Fatalf("UpdateNode({}): %v", err)
		}
		assertUpdatedAtMoved(t, node.UpdatedAt, got.UpdatedAt)
		if got.Name != node.Name || got.Type != node.Type || got.Status != node.Status ||
			got.IterationCount != node.IterationCount || got.MaxIterations != node.MaxIterations ||
			got.IsManual != node.IsManual {
			t.Errorf("an empty patch changed the row:\n before %+v\n after  %+v", node, got)
		}
	})
}

// assertUpdatedAtMoved fails the test unless after is a later instant than
// before. The values are compared as parsed times, not as strings:
// RFC3339Nano drops trailing zeros from the fraction, so a later time can
// sort before an earlier one when its string extends the earlier one's --
// "...43.1091Z" then "...43.10910002Z" puts '0' against 'Z', and the later
// time compares as the smaller string.
func assertUpdatedAtMoved(t *testing.T, before, after string) {
	t.Helper()
	b := mustParseRFC3339Nano(t, "updated_at before the update", before)
	a := mustParseRFC3339Nano(t, "updated_at after the update", after)
	if !a.After(b) {
		t.Errorf("updated_at = %s, want something after %s", after, before)
	}
}

func mustParseRFC3339Nano(t *testing.T, field, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("%s = %q is not an RFC3339Nano timestamp: %v", field, s, err)
	}
	return ts
}

// TestUpdateNode_MissingNodeStillErrors: the not-found behaviour is part of
// the contract and did not change with the rewrite.
func TestUpdateNode_MissingNodeStillErrors(t *testing.T) {
	eachSQLBackend(t, func(t *testing.T, b backend) {
		status := domain.NodeDone
		if _, err := b.repo.UpdateNode("NO-SUCH-NODE-99", NodePatch{Status: &status}); err == nil {
			t.Fatal("UpdateNode on a missing node succeeded, want an error")
		}
	})
}

// TestUpdateNode_ConcurrentDifferentColumnsBothSurvive is BUG-13 itself: the
// Web UI reassigning a node while complete-node moves its status. Both writes
// are legitimate, they touch different columns, and before the fix whichever
// landed second wrote back the value it had read for the other's column and
// silently undid it.
//
// Repeated, because a single pass often does not interleave. Verified against
// the pre-fix code, where it fails.
func TestUpdateNode_ConcurrentDifferentColumnsBothSurvive(t *testing.T) {
	eachSQLBackend(t, func(t *testing.T, b backend) {
		// The round count is a compromise: high enough that the two writes
		// really interleave -- against the pre-fix code this fails within
		// the first few rounds on either backend -- and low enough not to
		// dominate the MySQL suite's runtime.
		const rounds = 80
		for i := 0; i < rounds; i++ {
			node := b.newNode(t, domain.NodeTODO)

			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			var assignErr, statusErr error
			go func() {
				defer wg.Done()
				<-start
				alice := strPtr("alice")
				_, assignErr = b.repo.UpdateNode(node.ID, NodePatch{Assignee: &alice})
			}()
			go func() {
				defer wg.Done()
				<-start
				inProgress := domain.NodeInProgress
				_, statusErr = b.repo.UpdateNode(node.ID, NodePatch{Status: &inProgress})
			}()
			close(start)
			wg.Wait()

			if assignErr != nil || statusErr != nil {
				t.Fatalf("round %d: assign=%v status=%v", i, assignErr, statusErr)
			}
			got, err := b.repo.GetNode(node.ID)
			if err != nil {
				t.Fatalf("GetNode: %v", err)
			}
			if got.Assignee == nil || *got.Assignee != "alice" {
				t.Fatalf("round %d: the assignee change was lost (assignee=%v, status=%s)", i, got.Assignee, got.Status)
			}
			if got.Status != domain.NodeInProgress {
				t.Fatalf("round %d: the status change was lost (assignee=%v, status=%s)", i, got.Assignee, got.Status)
			}
		}
	})
}

// TestUpdateProject_NilNamePatchDoesNotRollBackARename is UpdateProject's
// narrower version of the same defect. Its statement already named only the
// columns it meant to write, but it wrote `name` unconditionally -- reading
// the current name and writing it straight back -- so a patch that never meant
// to touch the name still undid a rename that landed in between.
func TestUpdateProject_NilNamePatchDoesNotRollBackARename(t *testing.T) {
	eachSQLBackend(t, func(t *testing.T, b backend) {
		// More rounds than the node test above: this race has a narrower
		// window on SQLite, where SetMaxOpenConns(1) serializes the
		// statements and only the gap between the read and the write is
		// left to hit. At 200 it reproduces on both backends against the
		// pre-fix code; at 120 the SQLite half went green by luck.
		const rounds = 200
		for i := 0; i < rounds; i++ {
			proj, err := b.repo.CreateProject("old name", "")
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}

			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			var renameErr, touchErr error
			go func() {
				defer wg.Done()
				<-start
				_, renameErr = b.repo.UpdateProject(proj.ID, ProjectPatch{Name: strPtr("new name")})
			}()
			go func() {
				defer wg.Done()
				<-start
				_, touchErr = b.repo.UpdateProject(proj.ID, ProjectPatch{})
			}()
			close(start)
			wg.Wait()

			if renameErr != nil || touchErr != nil {
				t.Fatalf("round %d: rename=%v touch=%v", i, renameErr, touchErr)
			}
			got, err := b.repo.GetProject(proj.ID)
			if err != nil {
				t.Fatalf("GetProject: %v", err)
			}
			if got.Name != "new name" {
				t.Fatalf("round %d: name = %q; the name-less patch rolled the rename back", i, got.Name)
			}
		}
	})
}

// TestUpdateProject_EmptyPatchStillMovesUpdatedAt is UpdateProject's half of
// the "an empty patch still touches the row" rule.
func TestUpdateProject_EmptyPatchStillMovesUpdatedAt(t *testing.T) {
	eachSQLBackend(t, func(t *testing.T, b backend) {
		proj, err := b.repo.CreateProject("a name", "")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}

		got, err := b.repo.UpdateProject(proj.ID, ProjectPatch{})
		if err != nil {
			t.Fatalf("UpdateProject({}): %v", err)
		}
		assertUpdatedAtMoved(t, proj.UpdatedAt, got.UpdatedAt)
		if got.Name != proj.Name || got.Prefix != proj.Prefix {
			t.Errorf("an empty patch changed the row:\n before %+v\n after  %+v", proj, got)
		}
	})
}

// TestUpdateProject_MissingProjectStillReturnsProjectNotFound keeps the error
// code the rewrite could have dropped.
func TestUpdateProject_MissingProjectStillReturnsProjectNotFound(t *testing.T) {
	eachSQLBackend(t, func(t *testing.T, b backend) {
		_, err := b.repo.UpdateProject("no-such-project", ProjectPatch{Name: strPtr("x")})
		var apiErr *domain.APIError
		if err == nil || !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeProjectNotFound {
			t.Fatalf("UpdateProject on a missing project = %v, want PROJECT_NOT_FOUND", err)
		}
	})
}

// --- CHK-01: the claim has to be one step ---

// claimExclusions is what engine.GetExecutableNodes passes: a node is
// claimable unless it is finished or somebody already holds it.
var claimExclusions = []domain.NodeStatus{domain.NodeDone, domain.NodeInProgress, domain.NodeInReview}

// TestClaimNode_StatusRange pins which statuses a claim may take, and is the
// deterministic counterpart to the race test below: an excluded status simply
// changes no row, whatever the scheduling. The range is unchanged from the
// read-then-write version -- this ticket made the claim atomic, it did not
// widen or narrow what can be claimed.
func TestClaimNode_StatusRange(t *testing.T) {
	eachSQLBackend(t, func(t *testing.T, b backend) {
		for _, tc := range []struct {
			status     domain.NodeStatus
			claimable  bool
			afterClaim domain.NodeStatus
		}{
			{domain.NodeTODO, true, domain.NodeInProgress},
			{domain.NodeAwaitingFix, true, domain.NodeInProgress},
			{domain.NodeRejected, true, domain.NodeInProgress},
			{domain.NodeInProgress, false, domain.NodeInProgress},
			{domain.NodeInReview, false, domain.NodeInReview},
			{domain.NodeDone, false, domain.NodeDone},
		} {
			t.Run(string(tc.status), func(t *testing.T) {
				node := b.newNode(t, tc.status)

				claimed, err := b.repo.ClaimNode(node.ID, domain.NodeInProgress, claimExclusions)
				if err != nil {
					t.Fatalf("ClaimNode: %v", err)
				}
				if tc.claimable != (claimed != nil) {
					t.Fatalf("ClaimNode from %s returned %+v, claimable=%v", tc.status, claimed, tc.claimable)
				}
				if claimed != nil && claimed.Status != domain.NodeInProgress {
					t.Errorf("claimed node status = %s, want IN PROGRESS", claimed.Status)
				}
				got, err := b.repo.GetNode(node.ID)
				if err != nil {
					t.Fatalf("GetNode: %v", err)
				}
				if got.Status != tc.afterClaim {
					t.Errorf("stored status = %s, want %s", got.Status, tc.afterClaim)
				}
			})
		}
	})
}

// TestClaimNode_MissingNodeIsNotAnError: a node that has been deleted is the
// same answer as one somebody else holds -- "not mine" -- and never an error
// the caller has to special-case.
func TestClaimNode_MissingNodeIsNotAnError(t *testing.T) {
	eachSQLBackend(t, func(t *testing.T, b backend) {
		claimed, err := b.repo.ClaimNode("NO-SUCH-NODE-99", domain.NodeInProgress, claimExclusions)
		if err != nil {
			t.Fatalf("ClaimNode on a missing node = %v, want no error", err)
		}
		if claimed != nil {
			t.Fatalf("ClaimNode on a missing node returned %+v, want nil", claimed)
		}
	})
}

// TestClaimNode_ConcurrentClaimsYieldExactlyOneWinner is CHK-01: several
// process-ticket subagents calling get-executable at the same moment must not
// be handed the same node. Before the fix the check and the write were
// separate statements and every racer could pass the check; now the WHERE
// clause carries the check, so the database hands the row to one of them.
//
// Verified against the pre-fix code, where it fails.
func TestClaimNode_ConcurrentClaimsYieldExactlyOneWinner(t *testing.T) {
	eachSQLBackend(t, func(t *testing.T, b backend) {
		const (
			rounds   = 50
			claimers = 4
		)
		for i := 0; i < rounds; i++ {
			node := b.newNode(t, domain.NodeTODO)

			start := make(chan struct{})
			results := make(chan *domain.GraphNode, claimers)
			errs := make(chan error, claimers)
			var wg sync.WaitGroup
			for c := 0; c < claimers; c++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					claimed, err := b.repo.ClaimNode(node.ID, domain.NodeInProgress, claimExclusions)
					if err != nil {
						errs <- err
						return
					}
					results <- claimed
				}()
			}
			close(start)
			wg.Wait()
			close(results)
			close(errs)

			for err := range errs {
				t.Fatalf("round %d: ClaimNode: %v", i, err)
			}
			winners := 0
			for claimed := range results {
				if claimed != nil {
					winners++
				}
			}
			if winners != 1 {
				t.Fatalf("round %d: %d of %d claimers were handed the same node, want exactly 1", i, winners, claimers)
			}
		}
	})
}
