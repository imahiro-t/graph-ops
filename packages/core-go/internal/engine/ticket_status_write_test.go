package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00100: syncTicketStatus used to call UpdateTicket unconditionally, so
// every complete-node and -- worse -- every read-only get-executable rewrote
// the ticket row (and bumped its updated_at) even when the derived status was
// the one already stored. On SQLite that write was then a deferred
// read-then-write transaction upgrade, which busy_timeout cannot cover, so it
// was the one remaining source of "database is locked" once the DSN fix
// landed. DFLT-00136 has since made SQLite transactions begin IMMEDIATE, so
// the write now waits for the lock instead of failing; the guard remains as
// an optimization (no write, no lock to wait for), not as the lock-error fix.
//
// These tests pin the guard: a no-op sync issues no write at all, while a
// sync that really does change the status still writes exactly once.

// ticketWriteCountingRepo counts UpdateTicket calls. Counting the calls (not
// inspecting updated_at) is what makes "no write was issued" observable
// deterministically -- a skipped write and a write that happened to land in
// the same clock tick would otherwise look alike.
//
// It embeds the store.GraphRepository interface, which hides the
// store.NodeTransitionApplier add-on (DFLT-00329): CompleteNode through it
// takes the sequential path, where each write is its own repository call.
// atomicTicketWriteCountingRepo below is the same counter on the atomic
// path.
type ticketWriteCountingRepo struct {
	store.GraphRepository
	updateTicketCalls int
}

func (r *ticketWriteCountingRepo) UpdateTicket(id string, patch store.TicketPatch) (domain.Ticket, error) {
	r.updateTicketCalls++
	return r.GraphRepository.UpdateTicket(id, patch)
}

// newCountingEngine is newTestEngine with UpdateTicket instrumented.
func newCountingEngine(t *testing.T) (*GraphEngine, *ticketWriteCountingRepo, string) {
	t.Helper()
	_, repo, projectID := newTestEngine(t)
	counting := &ticketWriteCountingRepo{GraphRepository: repo}
	return New(counting), counting, projectID
}

// TestGetExecutableNodesWritesTheTicketRowOnlyWhenItsStatusChanges is the
// measurement the ticket asks for: a read-only command must not become a
// contender for the write lock.
func TestGetExecutableNodesWritesTheTicketRowOnlyWhenItsStatusChanges(t *testing.T) {
	e, repo, projectID := newCountingEngine(t)
	cat := baseCatalog(t)

	// Stored status TODO, derived status IN PROGRESS: the first sync has real
	// work to do.
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{
		Title: "read-only commands must not write", Status: domain.TicketTODO, AutoExecutable: true,
	})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if _, err := repo.CreateNode(domain.GraphNode{
		TicketID: ticket.ID, Name: "impl", Type: domain.NodeTypeImplementation,
		Status: domain.NodeTODO, MaxIterations: 3,
	}); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	repo.updateTicketCalls = 0
	if _, err := e.GetExecutableNodes(ticket.ID, cat); err != nil {
		t.Fatalf("GetExecutableNodes (first): %v", err)
	}
	if repo.updateTicketCalls != 1 {
		t.Errorf("first get-executable issued %d ticket writes, want exactly 1 (TODO -> IN PROGRESS)", repo.updateTicketCalls)
	}

	after, err := repo.GetTicket(ticket.ID)
	if err != nil || after == nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if after.Status != domain.TicketInProgress {
		t.Fatalf("ticket status = %q after the first get-executable, want IN PROGRESS", after.Status)
	}
	settledAt := after.UpdatedAt

	// Every later get-executable derives the same IN PROGRESS and must now
	// leave the row -- and its updated_at -- completely alone.
	for i := 0; i < 3; i++ {
		repo.updateTicketCalls = 0
		if _, err := e.GetExecutableNodes(ticket.ID, cat); err != nil {
			t.Fatalf("GetExecutableNodes (repeat %d): %v", i, err)
		}
		if repo.updateTicketCalls != 0 {
			t.Errorf("repeat %d: get-executable issued %d ticket writes, want 0 -- a read-only command must not take the write lock", i, repo.updateTicketCalls)
		}
	}

	settled, err := repo.GetTicket(ticket.ID)
	if err != nil || settled == nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if settled.UpdatedAt != settledAt {
		t.Errorf("ticket updated_at moved from %s to %s across get-executable calls that changed nothing", settledAt, settled.UpdatedAt)
	}
}

// TestCompleteNodeWritesTheTicketRowOnlyWhenItsStatusChanges covers the other
// syncTicketStatus caller that process-ticket runs concurrently. The
// completions below all pass, so the only ticket write CompleteNode can make
// is syncTicketStatus's: the counts are its writes, on the sequential path
// (see ticketWriteCountingRepo) -- the atomic path is
// TestCompleteNodeWritesTheTicketRowOnlyWhenItsStatusChanges_Atomic.
func TestCompleteNodeWritesTheTicketRowOnlyWhenItsStatusChanges(t *testing.T) {
	e, repo, projectID := newCountingEngine(t)
	checkCompleteNodeTicketWrites(t, e, repo, &repo.updateTicketCalls, projectID)
}

// atomicTicketWriteCountingRepo is ticketWriteCountingRepo with the
// NodeTransitionApplier add-on forwarded, so CompleteNode takes the atomic
// path; a node transition writes the ticket only for SetBlocked, never its
// status, so the counted calls are still syncTicketStatus's.
type atomicTicketWriteCountingRepo struct {
	ticketWriteCountingRepo
	inner store.NodeTransitionApplier
}

func (r *atomicTicketWriteCountingRepo) ApplyNodeTransition(ticketID string, t store.NodeTransition) (store.NodeTransitionResult, error) {
	return r.inner.ApplyNodeTransition(ticketID, t)
}

func TestCompleteNodeWritesTheTicketRowOnlyWhenItsStatusChanges_Atomic(t *testing.T) {
	_, inner, projectID := newTestEngine(t)
	repo := &atomicTicketWriteCountingRepo{ticketWriteCountingRepo: ticketWriteCountingRepo{GraphRepository: inner}, inner: inner.(store.NodeTransitionApplier)}
	var _ store.NodeTransitionApplier = repo
	checkCompleteNodeTicketWrites(t, New(repo), repo, &repo.updateTicketCalls, projectID)
}

func checkCompleteNodeTicketWrites(t *testing.T, e *GraphEngine, repo store.GraphRepository, updateTicketCalls *int, projectID string) {
	t.Helper()
	cat := baseCatalog(t)

	// Already IN PROGRESS, and it stays IN PROGRESS while siblings remain:
	// completing one node derives no change.
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{
		Title: "complete-node must not rewrite an unchanged status", Status: domain.TicketInProgress, AutoExecutable: true,
	})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	nodeIDs := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		node, err := repo.CreateNode(domain.GraphNode{
			TicketID: ticket.ID, Name: "impl", Type: domain.NodeTypeImplementation,
			Status: domain.NodeInProgress, MaxIterations: 3,
		})
		if err != nil {
			t.Fatalf("CreateNode(%d): %v", i, err)
		}
		nodeIDs = append(nodeIDs, node.ID)
	}

	for i, nodeID := range nodeIDs {
		*updateTicketCalls = 0
		if _, err := e.CompleteNode(nodeID, true, nil); err != nil {
			t.Fatalf("CompleteNode(%s): %v", nodeID, err)
		}
		if *updateTicketCalls != 0 {
			t.Errorf("complete-node %d issued %d ticket writes, want 0 -- the derived status is unchanged", i, *updateTicketCalls)
		}
	}

	// The guard must not swallow a real transition: the ticket is still
	// IN PROGRESS only because ExpandGraph never ran (GraphExpandedAt is nil,
	// so deriveTicketStatus refuses to call it DONE). Flip that and the next
	// sync has to write.
	expandedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := repo.UpdateTicket(ticket.ID, store.TicketPatch{GraphExpandedAt: &expandedAt}); err != nil {
		t.Fatalf("marking the graph expanded: %v", err)
	}
	*updateTicketCalls = 0
	if _, err := e.GetExecutableNodes(ticket.ID, cat); err != nil {
		t.Fatalf("GetExecutableNodes: %v", err)
	}
	if *updateTicketCalls != 1 {
		t.Errorf("the IN PROGRESS -> DONE transition issued %d ticket writes, want exactly 1", *updateTicketCalls)
	}
	done, err := repo.GetTicket(ticket.ID)
	if err != nil || done == nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if done.Status != domain.TicketDone {
		t.Errorf("ticket status = %q, want DONE once every node is DONE and the graph has been expanded", done.Status)
	}
}

// closeRacingRepo closes the ticket just before syncTicketStatus's status
// write reaches the store -- another member's close-ticket landing between
// the sync's read and its write.
type closeRacingRepo struct {
	*store.SQLiteRepository
	armed bool
}

func (r *closeRacingRepo) UpdateTicket(id string, patch store.TicketPatch) (domain.Ticket, error) {
	if r.armed && patch.Status != nil && patch.IfStatus != nil {
		r.armed = false
		closed, reason := domain.TicketClosed, "withdrawn"
		if _, err := r.SQLiteRepository.UpdateTicket(id, store.TicketPatch{Status: &closed, ClosedReason: &reason}); err != nil {
			return domain.Ticket{}, err
		}
	}
	return r.SQLiteRepository.UpdateTicket(id, patch)
}

// TestSyncTicketStatusNeverOverwritesAConcurrentClose (DFLT-00329): the
// derived status used to be written unconditionally once the read had shown
// the ticket not CLOSED, reviving a ticket somebody closed in between.
func TestSyncTicketStatusNeverOverwritesAConcurrentClose(t *testing.T) {
	inner, projectID := newSQLiteForHooks(t)
	repo := &closeRacingRepo{SQLiteRepository: inner}
	e := New(repo)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "close race", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateNode(domain.GraphNode{TicketID: ticket.ID, Name: "impl", Type: domain.NodeTypeImplementation, Status: domain.NodeInProgress, MaxIterations: 3}); err != nil {
		t.Fatal(err)
	}
	repo.armed = true
	if err := e.syncTicketStatus(ticket.ID); err != nil {
		t.Fatalf("syncTicketStatus: %v", err)
	}
	if repo.armed {
		t.Fatal("the sync never tried to write the status; the test did not exercise the race")
	}
	got, _ := repo.GetTicket(ticket.ID)
	if got.Status != domain.TicketClosed {
		t.Fatalf("status = %s, want CLOSED to survive the sync", got.Status)
	}
}

// TestReopenTicket_OnlyOnce (DFLT-00329): the reopen is written only while
// the ticket is still CLOSED.
func TestReopenTicket_OnlyOnce(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	ticket, err := e.CreateTicket(projectID, "reopen twice", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.CloseTicket(ticket.ID, "x"); err != nil {
		t.Fatal(err)
	}
	closed := domain.TicketClosed
	todo := domain.TicketTODO
	if _, err := repo.UpdateTicket(ticket.ID, store.TicketPatch{Status: &todo, IfStatus: &closed}); err != nil {
		t.Fatalf("the first conditional reopen: %v", err)
	}
	_, err = repo.UpdateTicket(ticket.ID, store.TicketPatch{Status: &todo, IfStatus: &closed})
	if !isTicketStatusChanged(err) {
		t.Fatalf("err = %v, want TICKET_STATUS_CHANGED", err)
	}
	if _, err := e.ReopenTicket(ticket.ID); err == nil || !strings.Contains(err.Error(), "not CLOSED") {
		t.Fatalf("ReopenTicket of a reopened ticket = %v, want a not-CLOSED error", err)
	}
}
