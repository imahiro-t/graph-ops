package engine

import (
	"errors"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00136: GetExecutableNodes claims nodes one ClaimNode at a time and
// then syncs the ticket's status. A failure after the first claim -- a later
// ClaimNode, or the sync's UpdateTicket -- used to return an error without
// the nodes already claimed, leaving them IN PROGRESS/IN REVIEW with nobody
// running them until someone noticed and ran unstick-node. These tests inject
// those failures and pin the compensation: every node claimed by the failed
// call goes back to its pre-claim status, and the error says which nodes, if
// any, could not be put back.

var errInjected = errors.New("injected failure")

// claimFaultRepo wraps a real repository and fails chosen calls.
type claimFaultRepo struct {
	store.GraphRepository

	// failForwardClaimAt makes the Nth forward claim (to IN PROGRESS or
	// IN REVIEW) fail without reaching the inner repository; 0 disables it.
	failForwardClaimAt int
	// failReleaseFor makes a release (a ClaimNode back to a claimable
	// status) of these node IDs fail without reaching the inner repository.
	failReleaseFor map[string]bool
	// failUpdateTicket makes UpdateTicket -- the one write syncTicketStatus
	// makes -- fail.
	failUpdateTicket bool
	// beforeFirstRelease runs once, just before the first release reaches
	// the repository: a stand-in for another process moving a node on while
	// this call is failing.
	beforeFirstRelease func(r *claimFaultRepo)

	forwardClaims  int
	forwardClaimed []string
	released       []string
	releaseStarted bool
}

func isForwardClaim(s domain.NodeStatus) bool {
	return s == domain.NodeInProgress || s == domain.NodeInReview
}

func (r *claimFaultRepo) ClaimNode(id string, newStatus domain.NodeStatus, excluded []domain.NodeStatus) (*domain.GraphNode, error) {
	if isForwardClaim(newStatus) {
		r.forwardClaims++
		if r.failForwardClaimAt != 0 && r.forwardClaims == r.failForwardClaimAt {
			return nil, errInjected
		}
		n, err := r.GraphRepository.ClaimNode(id, newStatus, excluded)
		if err == nil && n != nil {
			r.forwardClaimed = append(r.forwardClaimed, id)
		}
		return n, err
	}
	if !r.releaseStarted {
		r.releaseStarted = true
		if r.beforeFirstRelease != nil {
			r.beforeFirstRelease(r)
		}
	}
	if r.failReleaseFor[id] {
		return nil, errors.New("injected release failure")
	}
	r.released = append(r.released, id)
	return r.GraphRepository.ClaimNode(id, newStatus, excluded)
}

func (r *claimFaultRepo) UpdateTicket(id string, patch store.TicketPatch) (domain.Ticket, error) {
	if r.failUpdateTicket {
		return domain.Ticket{}, errInjected
	}
	return r.GraphRepository.UpdateTicket(id, patch)
}

// clearFaults turns every injected failure off, for the retry.
func (r *claimFaultRepo) clearFaults() {
	r.failForwardClaimAt = 0
	r.failReleaseFor = nil
	r.failUpdateTicket = false
	r.beforeFirstRelease = nil
}

// rollbackFixture is a ticket with three independent claimable nodes -- two
// implementation nodes at TODO and a review at AWAITING FIX -- so that one
// GetExecutableNodes call claims all three, to both IN PROGRESS and
// IN REVIEW, and the ticket's derived status moves from its stored TODO to
// IN PROGRESS (so the sync really does call UpdateTicket).
type rollbackFixture struct {
	engine   *GraphEngine
	faults   *claimFaultRepo
	inner    store.GraphRepository
	ticketID string
	before   map[string]domain.NodeStatus
}

func newRollbackFixture(t *testing.T) *rollbackFixture {
	t.Helper()
	_, inner, projectID := newTestEngine(t)
	ticket, err := inner.CreateTicket(projectID, domain.Ticket{
		Title: "claims are returned or released", Status: domain.TicketTODO, AutoExecutable: true,
	})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	before := map[string]domain.NodeStatus{}
	for _, spec := range []struct {
		name   string
		typ    domain.NodeType
		status domain.NodeStatus
	}{
		{"impl-a", domain.NodeTypeImplementation, domain.NodeTODO},
		{"impl-b", domain.NodeTypeImplementation, domain.NodeTODO},
		{"review", domain.NodeTypeReview, domain.NodeAwaitingFix},
	} {
		n, err := inner.CreateNode(domain.GraphNode{
			TicketID: ticket.ID, Name: spec.name, Type: spec.typ, Status: spec.status, MaxIterations: 3,
		})
		if err != nil {
			t.Fatalf("CreateNode(%s): %v", spec.name, err)
		}
		before[n.ID] = spec.status
	}
	faults := &claimFaultRepo{GraphRepository: inner}
	return &rollbackFixture{engine: New(faults), faults: faults, inner: inner, ticketID: ticket.ID, before: before}
}

func (f *rollbackFixture) nodeStatuses(t *testing.T) map[string]domain.NodeStatus {
	t.Helper()
	detail, err := f.inner.GetTicketDetail(f.ticketID)
	if err != nil || detail == nil {
		t.Fatalf("GetTicketDetail: %v", err)
	}
	out := map[string]domain.NodeStatus{}
	for _, n := range detail.Nodes {
		out[n.ID] = n.Status
	}
	return out
}

func (f *rollbackFixture) ticketStatus(t *testing.T) domain.TicketStatus {
	t.Helper()
	ticket, err := f.inner.GetTicket(f.ticketID)
	if err != nil || ticket == nil {
		t.Fatalf("GetTicket: %v", err)
	}
	return ticket.Status
}

// assertStatuses checks every node is back at its pre-call status, except the
// IDs in keep, whose expected status is given there instead.
func (f *rollbackFixture) assertStatuses(t *testing.T, keep map[string]domain.NodeStatus) {
	t.Helper()
	got := f.nodeStatuses(t)
	for id, want := range f.before {
		if k, ok := keep[id]; ok {
			want = k
		}
		if got[id] != want {
			t.Errorf("node %s status = %q, want %q", id, got[id], want)
		}
	}
}

func (f *rollbackFixture) callAndExpectError(t *testing.T) error {
	t.Helper()
	nodes, err := f.engine.GetExecutableNodes(f.ticketID, baseCatalog(t))
	if err == nil {
		t.Fatalf("GetExecutableNodes succeeded (returned %d nodes), want the injected failure", len(nodes))
	}
	if len(nodes) != 0 {
		t.Errorf("GetExecutableNodes returned %d nodes alongside its error, want none", len(nodes))
	}
	if !errors.Is(err, errInjected) {
		t.Errorf("error %q does not wrap the injected failure", err)
	}
	return err
}

// retrySucceeds is the "the caller can simply retry" half of the contract:
// with the faults gone, the very same call hands out every node and the
// ticket moves to IN PROGRESS.
func (f *rollbackFixture) retrySucceeds(t *testing.T) {
	t.Helper()
	f.faults.clearFaults()
	nodes, err := f.engine.GetExecutableNodes(f.ticketID, baseCatalog(t))
	if err != nil {
		t.Fatalf("retrying GetExecutableNodes: %v", err)
	}
	if len(nodes) != len(f.before) {
		t.Errorf("the retry handed out %d nodes, want all %d", len(nodes), len(f.before))
	}
	for _, n := range nodes {
		if !isForwardClaim(n.Status) {
			t.Errorf("node %s handed out at %q, want IN PROGRESS/IN REVIEW", n.ID, n.Status)
		}
	}
	if st := f.ticketStatus(t); st != domain.TicketInProgress {
		t.Errorf("ticket status after the retry = %q, want IN PROGRESS", st)
	}
}

func TestGetExecutableNodesReleasesEarlierClaimsWhenALaterClaimFails(t *testing.T) {
	f := newRollbackFixture(t)
	f.faults.failForwardClaimAt = 2

	err := f.callAndExpectError(t)
	if len(f.faults.forwardClaimed) != 1 {
		t.Fatalf("setup: %d nodes were claimed before the failure, want 1", len(f.faults.forwardClaimed))
	}
	if got, want := f.faults.released, f.faults.forwardClaimed; len(got) != 1 || got[0] != want[0] {
		t.Errorf("released %v, want exactly the node claimed before the failure %v", got, want)
	}
	// The node whose claim failed is named, and was not "released" (it
	// was never claimed by this call as far as the engine can tell).
	var failedID string
	for id := range f.before {
		if id != f.faults.forwardClaimed[0] && strings.Contains(err.Error(), "claiming node "+id) {
			failedID = id
		}
	}
	if failedID == "" {
		t.Errorf("error %q does not name the node whose claim failed", err)
	}
	f.assertStatuses(t, nil)
	if st := f.ticketStatus(t); st != domain.TicketTODO {
		t.Errorf("ticket status = %q, want TODO untouched", st)
	}
	f.retrySucceeds(t)
}

// The first claim failing is the pre-existing behaviour: nothing was claimed,
// so there is nothing to release.
func TestGetExecutableNodesFirstClaimFailureReleasesNothing(t *testing.T) {
	f := newRollbackFixture(t)
	f.faults.failForwardClaimAt = 1

	f.callAndExpectError(t)
	if len(f.faults.released) != 0 {
		t.Errorf("released %v after the very first claim failed, want nothing", f.faults.released)
	}
	f.assertStatuses(t, nil)
	f.retrySucceeds(t)
}

func TestGetExecutableNodesReleasesAllClaimsWhenTheStatusSyncFails(t *testing.T) {
	f := newRollbackFixture(t)
	f.faults.failUpdateTicket = true

	f.callAndExpectError(t)
	if len(f.faults.forwardClaimed) != len(f.before) {
		t.Fatalf("setup: %d nodes were claimed before the sync, want all %d", len(f.faults.forwardClaimed), len(f.before))
	}
	if len(f.faults.released) != len(f.before) {
		t.Errorf("released %v, want all %d claimed nodes", f.faults.released, len(f.before))
	}
	// AWAITING FIX goes back to AWAITING FIX, not to TODO.
	f.assertStatuses(t, nil)
	if st := f.ticketStatus(t); st != domain.TicketTODO {
		t.Errorf("ticket status = %q, want TODO untouched", st)
	}
	f.retrySucceeds(t)
}

func TestGetExecutableNodesNamesNodesItCouldNotRelease(t *testing.T) {
	f := newRollbackFixture(t)
	f.faults.failUpdateTicket = true
	// Fail the release of the review node, the one claimed IN REVIEW.
	var stuckID string
	for id, st := range f.before {
		if st == domain.NodeAwaitingFix {
			stuckID = id
		}
	}
	f.faults.failReleaseFor = map[string]bool{stuckID: true}

	err := f.callAndExpectError(t)
	msg := err.Error()
	if !strings.Contains(msg, "failed to release claimed node(s) "+stuckID) {
		t.Errorf("error %q does not name the node that could not be released (%s)", msg, stuckID)
	}
	if !strings.Contains(msg, errInjected.Error()) {
		t.Errorf("error %q lost the original failure's text", msg)
	}
	if !strings.Contains(msg, "unstick-node") {
		t.Errorf("error %q does not point at unstick-node", msg)
	}
	// The other nodes were still released despite the one failure.
	for id := range f.before {
		if id != stuckID && strings.Contains(msg, "node(s) "+id) {
			t.Errorf("error %q names %s, which was released", msg, id)
		}
	}
	f.assertStatuses(t, map[string]domain.NodeStatus{stuckID: domain.NodeInReview})
}

func TestGetExecutableNodesReleaseLeavesANodeSomebodyElseMovedOn(t *testing.T) {
	f := newRollbackFixture(t)
	f.faults.failUpdateTicket = true
	var movedID string
	f.faults.beforeFirstRelease = func(r *claimFaultRepo) {
		// Another process completes one of the claimed nodes while this
		// call is failing.
		movedID = r.forwardClaimed[0]
		done := domain.NodeDone
		if _, err := r.GraphRepository.UpdateNode(movedID, store.NodePatch{Status: &done}); err != nil {
			t.Fatalf("moving node %s to DONE: %v", movedID, err)
		}
	}

	err := f.callAndExpectError(t)
	if strings.Contains(err.Error(), "failed to release") {
		t.Errorf("a node somebody else moved on counted as a failed release: %v", err)
	}
	f.assertStatuses(t, map[string]domain.NodeStatus{movedID: domain.NodeDone})
}
