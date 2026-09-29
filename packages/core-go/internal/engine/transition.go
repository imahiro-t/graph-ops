package engine

import (
	"errors"
	"fmt"
	"sort"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// This file runs a store.NodeTransition (DFLT-00329). The engine decides what
// a completion, a reopen, a grant or an unstick writes, and states it once
// as a NodeTransition -- the writes plus the conditions they were decided on
// (what the read saw). How it is written is decided here, in one of two
// ways:
//
//   - atomically, by a repository with the store.NodeTransitionApplier
//     add-on (SQLite, MySQL, an HTTP data source speaking protocol 1.2):
//     every condition is checked and every write made in one step, or
//     nothing is written;
//   - one call at a time (applyNodeTransitionSequential), by any other
//     repository -- an HTTP data source older than 1.2, and a test
//     repository that embeds store.GraphRepository and so hides the add-on.
//     This is the pre-DFLT-00329 behaviour, in the same order, and it is not
//     atomic: see its doc comment for what it cannot prevent.
//
// Keeping the NodeTransition the single description of the write is what
// keeps the two paths from drifting apart: only the executor differs.

// applyTransition applies t to ticketID's nodes, atomically when the
// repository can, and one write at a time otherwise.
func (e *GraphEngine) applyTransition(ticketID string, t store.NodeTransition) (store.NodeTransitionResult, error) {
	if applier, ok := e.repo.(store.NodeTransitionApplier); ok {
		res, err := applier.ApplyNodeTransition(ticketID, t)
		if !errors.Is(err, store.ErrNodeTransitionUnsupported) {
			return res, err
		}
		e.warnEvery("node-transition-unsupported", 0,
			"the data source cannot apply node transitions in one request (an HTTP data source older than protocol 1.2): completing, reopening and unsticking nodes are not atomic there, and who decided a manual node is not recorded")
	}
	return e.applyNodeTransitionSequential(ticketID, t)
}

// applyNodeTransitionSequential is the one-call-at-a-time executor of a
// NodeTransition, for a repository that cannot apply one atomically. It
// writes in the order CompleteNode always has: the artifacts, then the steps
// in order, then the ticket's blocked flag.
//
// The conditions are checked on fresh reads just before the writes, so a
// change that landed before this call is still caught; one that lands
// between a read and its write is not. In particular, on this path:
//
//   - a completion without --claim cannot tell a node that was rewound and
//     handed out again since the caller read it (the ABA the atomic path
//     closes by comparing the claim token it read);
//   - with --claim there is still a window between the check and the write;
//   - two people approving and rejecting the same approval_gate at once can
//     both get through;
//   - no decision is recorded (a data source older than 1.2 does not keep
//     one).
//
// That is the behaviour before DFLT-00329, unchanged; the atomic path is
// what closes these windows.
//
// A non-required step with conditions and a status write (the loop target's
// "open the round") is written with ClaimNode, the status CAS every backend
// has, exactly as CompleteNode did before: losing it means a sibling opened
// the round first, and the step is skipped. Relative writes
// (IncrementIteration, AddMaxIterations) are turned into absolute ones from
// a fresh read.
func (e *GraphEngine) applyNodeTransitionSequential(ticketID string, t store.NodeTransition) (store.NodeTransitionResult, error) {
	if err := t.Validate(ticketID); err != nil {
		return store.NodeTransitionResult{}, err
	}
	if t.RequireTicketOpen || t.IfBlocked != nil {
		ticket, err := e.repo.GetTicket(ticketID)
		if err != nil {
			return store.NodeTransitionResult{}, err
		}
		if ticket == nil {
			return store.NodeTransitionResult{}, domain.NewAPIError(domain.ErrCodeTicketNotFound, "ticket %s not found", ticketID)
		}
		if reason := t.TicketConflict(ticket.Status, ticket.Blocked); reason != "" {
			return store.NodeTransitionResult{}, &store.NodeTransitionConflictError{Reason: reason}
		}
	}

	// Check the required steps before writing anything, so a conflict
	// leaves the graph as it was.
	for _, st := range t.Steps {
		if !st.Required {
			continue
		}
		n, err := e.repo.GetNode(st.NodeID)
		if err != nil {
			return store.NodeTransitionResult{}, err
		}
		if reason := st.Conflict(n); reason != "" {
			return store.NodeTransitionResult{}, &store.NodeTransitionConflictError{NodeID: st.NodeID, Reason: reason}
		}
	}

	for _, art := range t.Artifacts {
		if _, err := e.repo.CreateArtifact(art); err != nil {
			return store.NodeTransitionResult{}, err
		}
	}

	written := map[string]domain.GraphNode{}
	applied := make([]bool, len(t.Steps))
	for i, st := range t.Steps {
		if !st.Writes() {
			applied[i] = true
			continue
		}
		if !st.Required && st.HasConditions() && st.SetStatus != nil {
			// The status CAS: newStatus has to be one of the excluded
			// statuses (see GraphRepository.ClaimNode), which the loop
			// target's step (TODO, excluding TODO) satisfies.
			if len(st.IfStatusIn) > 0 || st.CheckClaimToken || st.IfIterationCount != nil || !containsNodeStatus(st.IfStatusNotIn, *st.SetStatus) {
				return store.NodeTransitionResult{}, fmt.Errorf("node transition step %s: a conditional non-required step can only be a status CAS (if_status_not_in including set_status) on this data source", st.NodeID)
			}
			claimed, err := e.repo.ClaimNode(st.NodeID, *st.SetStatus, st.IfStatusNotIn, nil)
			if err != nil {
				return store.NodeTransitionResult{}, err
			}
			if claimed == nil {
				continue
			}
			applied[i] = true
			written[st.NodeID] = *claimed
			if st.IncrementIteration || st.AddMaxIterations != 0 {
				patch := store.NodePatch{}
				if st.IncrementIteration {
					next := claimed.IterationCount + 1
					patch.IterationCount = &next
				}
				if st.AddMaxIterations != 0 {
					max := claimed.MaxIterations + st.AddMaxIterations
					patch.MaxIterations = &max
				}
				n, err := e.repo.UpdateNode(st.NodeID, patch)
				if err != nil {
					return store.NodeTransitionResult{}, err
				}
				written[st.NodeID] = n
			}
			continue
		}
		patch := store.NodePatch{Status: st.SetStatus}
		if st.IncrementIteration || st.AddMaxIterations != 0 {
			cur, err := e.repo.GetNode(st.NodeID)
			if err != nil {
				return store.NodeTransitionResult{}, err
			}
			if cur == nil {
				if st.Required {
					return store.NodeTransitionResult{}, fmt.Errorf("node %s not found", st.NodeID)
				}
				continue
			}
			if st.IncrementIteration {
				next := cur.IterationCount + 1
				patch.IterationCount = &next
			}
			if st.AddMaxIterations != 0 {
				max := cur.MaxIterations + st.AddMaxIterations
				patch.MaxIterations = &max
			}
		}
		n, err := e.repo.UpdateNode(st.NodeID, patch)
		if err != nil {
			return store.NodeTransitionResult{}, err
		}
		applied[i] = true
		written[st.NodeID] = n
	}

	if t.SetBlocked != nil {
		if *t.SetBlocked {
			// blockTicket, not a bare UpdateTicket: the sequential path is
			// where the pre-DFLT-00329 helper lives on (it also resyncs;
			// the caller's resync afterwards then finds nothing to write).
			if err := e.blockTicket(ticketID); err != nil {
				return store.NodeTransitionResult{}, err
			}
		} else if _, err := e.repo.UpdateTicket(ticketID, store.TicketPatch{Blocked: t.SetBlocked}); err != nil {
			return store.NodeTransitionResult{}, err
		}
	}

	out := store.NodeTransitionResult{Applied: applied}
	for _, id := range sortedKeys(written) {
		out.Nodes = append(out.Nodes, written[id])
	}
	return out, nil
}

func containsNodeStatus(list []domain.NodeStatus, s domain.NodeStatus) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]domain.GraphNode) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// transitionConflictError turns a *store.NodeTransitionConflictError into the
// INVALID_NODE_STATE the caller reports, re-reading the node and the ticket
// to say what changed. what describes the refused operation ("completing
// node X"); readStatus is the node status the caller decided on ("" when
// the operation is not about one node).
func (e *GraphEngine) transitionConflictError(err error, what, nodeID string, readStatus domain.NodeStatus, ticketID string) error {
	var conflict *store.NodeTransitionConflictError
	if !errors.As(err, &conflict) {
		return err
	}
	details := map[string]any{"reason": conflict.Reason}
	if readStatus != "" {
		details["read_status"] = string(readStatus)
	}
	current := ""
	if nodeID != "" {
		if n, gerr := e.repo.GetNode(nodeID); gerr == nil && n != nil {
			current = string(n.Status)
			details["current_status"] = current
		}
	}
	closed := conflict.Reason == store.ConflictTicketClosed
	if !closed {
		if t, gerr := e.repo.GetTicket(ticketID); gerr == nil && t != nil && t.Status == domain.TicketClosed {
			closed = true
		}
	}

	var why string
	switch {
	case closed:
		why = fmt.Sprintf("ticket %s was CLOSED in the meantime; a closed ticket's nodes cannot be changed", ticketID)
	case conflict.Reason == store.ConflictClaimToken:
		why = "the node was rewound (a loop-back, unstick-node) and handed out again since it was read, so this run's verdict belongs to superseded work and is deliberately not recorded; go back to get-executable"
	case conflict.Reason == store.ConflictTicketBlocked:
		why = "the ticket's blocked flag changed since it was read -- another reopen-nodes (or a completion) was applied first; check with get-ticket and run the command again if it is still needed"
	case conflict.Reason == store.ConflictUpdatedAt && (current == "" || readStatus == "" || current == string(readStatus)):
		why = "the node was written by somebody else since it was read -- another member or session decided it first. Nothing was written; check the result with get-ticket"
	case conflict.Reason == store.ConflictIterationCount:
		why = "the node's iteration count changed since it was read -- another reopen-nodes was applied first; check with get-ticket and run the command again if it is still needed"
	case current != "" && readStatus != "" && current != string(readStatus):
		why = fmt.Sprintf("the node moved from %s to %s since it was read: another member or session decided or completed it first (or it was rewound). Nothing was written; check the result with get-ticket", readStatus, current)
	default:
		why = "another member or session changed the node or the ticket first. Nothing was written; check the result with get-ticket"
	}
	if conflict.Detail != "" && conflict.Reason == store.ConflictRemote {
		why += " (data source: " + conflict.Detail + ")"
	}
	return domain.NewAPIError(domain.ErrCodeInvalidNodeState, "%s was refused: %s", what, why).WithDetails(details)
}
