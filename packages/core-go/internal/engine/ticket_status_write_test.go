package engine

import (
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00100: syncTicketStatus used to call UpdateTicket unconditionally, so
// every complete-node and -- worse -- every read-only get-executable rewrote
// the ticket row (and bumped its updated_at) even when the derived status was
// the one already stored. On SQLite that write is a deferred read-then-write
// transaction upgrade, which busy_timeout cannot cover, so it was the one
// remaining source of "database is locked" once the DSN fix landed.
//
// These tests pin the guard: a no-op sync issues no write at all, while a
// sync that really does change the status still writes exactly once.

// ticketWriteCountingRepo counts UpdateTicket calls. Counting the calls (not
// inspecting updated_at) is what makes "no write was issued" observable
// deterministically -- a skipped write and a write that happened to land in
// the same clock tick would otherwise look alike.
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
// syncTicketStatus caller that process-ticket runs concurrently.
func TestCompleteNodeWritesTheTicketRowOnlyWhenItsStatusChanges(t *testing.T) {
	e, repo, projectID := newCountingEngine(t)
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
		repo.updateTicketCalls = 0
		if _, err := e.CompleteNode(nodeID, true, nil); err != nil {
			t.Fatalf("CompleteNode(%s): %v", nodeID, err)
		}
		if repo.updateTicketCalls != 0 {
			t.Errorf("complete-node %d issued %d ticket writes, want 0 -- the derived status is unchanged", i, repo.updateTicketCalls)
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
	repo.updateTicketCalls = 0
	if _, err := e.GetExecutableNodes(ticket.ID, cat); err != nil {
		t.Fatalf("GetExecutableNodes: %v", err)
	}
	if repo.updateTicketCalls != 1 {
		t.Errorf("the IN PROGRESS -> DONE transition issued %d ticket writes, want exactly 1", repo.updateTicketCalls)
	}
	done, err := repo.GetTicket(ticket.ID)
	if err != nil || done == nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if done.Status != domain.TicketDone {
		t.Errorf("ticket status = %q, want DONE once every node is DONE and the graph has been expanded", done.Status)
	}
}
