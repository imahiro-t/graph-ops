package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

// This file is the atomic node state transition (DFLT-00329). Completing a
// node used to be a read (GetNode, checkCompletable, the --claim check)
// followed by separate writes (CreateArtifact, UpdateNode, the loop target's
// ClaimNode, UpdateTicket), with nothing holding what the read saw true until
// the writes landed. Two members pressing "approve" and "reject" on the same
// approval_gate at once both got through, and the loser's rejection_reason
// stayed on the node next to the winner's approval. A NodeTransition is the
// whole write together with the conditions it was decided on, and a
// NodeTransitionApplier applies it atomically: every condition holds and
// everything is written, or nothing is.

// ErrNodeTransitionConflict is what ApplyNodeTransition answers (wrapped in a
// *NodeTransitionConflictError) when a condition of the transition does not
// hold any more: somebody else changed the node or the ticket after the
// caller read it. Nothing was written. The engine turns it into
// INVALID_NODE_STATE.
var ErrNodeTransitionConflict = errors.New("the node or ticket changed after it was read, so the transition was not applied")

// ErrNodeTransitionUnsupported is what ApplyNodeTransition answers when the
// data source cannot apply a transition in one request: an HTTP data source
// older than protocol 1.2. Nothing was sent; the engine falls back to the
// old one-write-at-a-time path (not atomic).
var ErrNodeTransitionUnsupported = errors.New("the data source cannot apply a node transition in one request (an HTTP data source needs protocol 1.2 or newer)")

// Why a transition conflicted (NodeTransitionConflictError.Reason).
const (
	// ConflictTicketClosed: the transition requires an open ticket and the
	// ticket is CLOSED.
	ConflictTicketClosed = "ticket_closed"
	// ConflictTicketBlocked: the ticket's blocked flag is not IfBlocked.
	ConflictTicketBlocked = "ticket_blocked"
	// ConflictNodeMissing: a required step's node does not exist.
	ConflictNodeMissing = "node_missing"
	// ConflictStatus: a required step's node is not in the expected status.
	ConflictStatus = "status"
	// ConflictClaimToken: a required step's node carries another claim.
	ConflictClaimToken = "claim_token"
	// ConflictIterationCount: a required step's node has another
	// iteration_count.
	ConflictIterationCount = "iteration_count"
	// ConflictUpdatedAt: a required step's node has been written since it
	// was read (its updated_at moved).
	ConflictUpdatedAt = "updated_at"
	// ConflictRemote: an HTTP data source refused the transition (409
	// INVALID_NODE_STATE); its message says why.
	ConflictRemote = "remote"
)

// NodeTransitionConflictError is ErrNodeTransitionConflict with what did not
// hold. errors.Is(err, ErrNodeTransitionConflict) is true for it.
type NodeTransitionConflictError struct {
	// NodeID is the step's node, "" for a ticket condition.
	NodeID string
	// Reason is one of the Conflict* constants.
	Reason string
	// Detail is a human-readable description (the data source's message
	// for ConflictRemote).
	Detail string
}

func (e *NodeTransitionConflictError) Error() string {
	if e.Detail != "" {
		return ErrNodeTransitionConflict.Error() + ": " + e.Detail
	}
	return ErrNodeTransitionConflict.Error() + ": " + e.Reason
}

func (e *NodeTransitionConflictError) Unwrap() error { return ErrNodeTransitionConflict }

// NodeStep is one node's part of a NodeTransition: conditions on the node,
// and what to write to it when they hold.
type NodeStep struct {
	NodeID string `json:"node_id"`

	// Required decides what a condition that does not hold means: for a
	// required step, the whole transition conflicts and nothing is
	// written; for any other step, only that step is skipped (the loop
	// target's "open the round" write is one -- losing it means a sibling
	// opened the round first). A required step also requires the node to
	// exist.
	Required bool `json:"required"`

	// Conditions; each one left empty/nil/false is not checked.
	IfStatusIn    []domain.NodeStatus `json:"if_status_in,omitempty"`
	IfStatusNotIn []domain.NodeStatus `json:"if_status_not_in,omitempty"`
	// CheckClaimToken makes IfClaimToken a condition: the node's claim
	// token must be exactly *IfClaimToken, or absent when IfClaimToken is
	// nil.
	CheckClaimToken  bool    `json:"check_claim_token,omitempty"`
	IfClaimToken     *string `json:"if_claim_token,omitempty"`
	IfIterationCount *int    `json:"if_iteration_count,omitempty"`
	// IfUpdatedAt requires the node's updated_at to be exactly this: the
	// node has not been written at all since it was read. The engine uses
	// it for a manual node, whose decision can end in a write that leaves
	// the status as it was (a block), so that the status alone cannot tell
	// whether somebody else decided first.
	IfUpdatedAt *string `json:"if_updated_at,omitempty"`

	// Writes; a step with none of them only checks its conditions.
	//
	// SetStatus writes the status, which clears the claim (claimFieldsFor)
	// and writes Decision -- or clears the decision when Decision is nil
	// (decisionFieldsFor). A Decision without a SetStatus is refused.
	SetStatus          *domain.NodeStatus   `json:"set_status,omitempty"`
	IncrementIteration bool                 `json:"increment_iteration,omitempty"`
	AddMaxIterations   int                  `json:"add_max_iterations,omitempty"`
	Decision           *domain.NodeDecision `json:"decision,omitempty"`
	// Touch writes updated_at even when the step writes nothing else, so
	// that a concurrent transition conditioned on IfUpdatedAt sees that
	// the node was acted on (a manual node's decision that blocks the
	// ticket without changing the node's status).
	Touch bool `json:"touch,omitempty"`
}

// NodeTransition is a set of writes to one ticket's nodes, its artifacts and
// its blocked flag, with the conditions they were decided on.
type NodeTransition struct {
	// RequireTicketOpen: the ticket must not be CLOSED.
	RequireTicketOpen bool `json:"require_ticket_open,omitempty"`
	// IfBlocked, when non-nil, requires the ticket's blocked flag to be
	// exactly that.
	IfBlocked *bool `json:"if_blocked,omitempty"`
	// SetBlocked, when non-nil, writes the ticket's blocked flag.
	SetBlocked *bool `json:"set_blocked,omitempty"`
	// Steps are checked and applied in order; a later step's conditions see
	// what an earlier step of the same transition wrote.
	Steps []NodeStep `json:"steps"`
	// Artifacts are created as they are (IDs minted by the caller); each
	// must belong to this ticket.
	Artifacts []domain.Artifact `json:"artifacts"`
}

// NodeTransitionResult is an applied transition's outcome.
type NodeTransitionResult struct {
	// Applied says, per step (same order as NodeTransition.Steps), whether
	// its conditions held and its writes were made.
	Applied []bool `json:"applied"`
	// Nodes are the steps' nodes as stored after the write, ascending ID.
	// A path that cannot read them back leaves out the nodes it did not
	// write.
	Nodes []domain.GraphNode `json:"nodes"`
}

// Node returns the node with id from Nodes, or nil.
func (r NodeTransitionResult) Node(id string) *domain.GraphNode {
	for i := range r.Nodes {
		if r.Nodes[i].ID == id {
			return &r.Nodes[i]
		}
	}
	return nil
}

// NodeTransitionApplier is an optional add-on to GraphRepository, like
// GraphBatchCreator: applying a NodeTransition atomically. It is kept off
// GraphRepository so the fakes and wrapping test repositories do not have
// to grow it -- a repository without it (or one embedding the
// GraphRepository interface, which hides it) gets the engine's old
// one-write-at-a-time path.
type NodeTransitionApplier interface {
	// ApplyNodeTransition checks every condition of t and makes every write
	// of t, or writes nothing. Concurrent calls for the same ticket are
	// serialized. It returns a *NodeTransitionConflictError when a
	// condition does not hold, ErrNodeTransitionUnsupported when the data
	// source cannot do it, a VALIDATION_ERROR *domain.APIError for a
	// malformed transition, TICKET_NOT_FOUND when there is no such ticket,
	// and on MySQL CONCURRENT_WRITE_CONFLICT when a deadlock outlasted the
	// retries -- nothing was written in any of these cases.
	ApplyNodeTransition(ticketID string, t NodeTransition) (NodeTransitionResult, error)
}

var (
	_ NodeTransitionApplier = (*SQLiteRepository)(nil)
	_ NodeTransitionApplier = (*MySQLRepository)(nil)
	_ NodeTransitionApplier = (*HTTPRepository)(nil)
)

// Validate reports a transition that is malformed rather than merely out of
// date: a step without a node, a decision with no status to go with it
// (decisionFieldsFor's rule), a negative grant, or an artifact of another
// ticket.
func (t NodeTransition) Validate(ticketID string) error {
	for i, st := range t.Steps {
		if strings.TrimSpace(st.NodeID) == "" {
			return domain.NewAPIError(domain.ErrCodeValidation, "node transition step %d names no node", i)
		}
		if st.Decision != nil && st.SetStatus == nil {
			return domain.NewAPIError(domain.ErrCodeValidation, "node transition step %d (%s) records a decision without writing the status; a decision is only ever written together with the status it set", i, st.NodeID)
		}
		if st.AddMaxIterations < 0 {
			return domain.NewAPIError(domain.ErrCodeValidation, "node transition step %d (%s) lowers max_iterations by %d; only raising it is supported", i, st.NodeID, -st.AddMaxIterations)
		}
	}
	for i, a := range t.Artifacts {
		if a.ID == "" {
			return domain.NewAPIError(domain.ErrCodeValidation, "node transition artifact %d has no id", i)
		}
		if a.TicketID != ticketID {
			return domain.NewAPIError(domain.ErrCodeValidation, "node transition artifact %s belongs to ticket %q, not %s", a.ID, a.TicketID, ticketID)
		}
	}
	return nil
}

// TicketConflict is the reason (a Conflict* constant) the ticket conditions
// of t do not hold for a ticket in status with the given blocked flag, or
// "" when they hold.
func (t NodeTransition) TicketConflict(status domain.TicketStatus, blocked bool) string {
	if t.RequireTicketOpen && status == domain.TicketClosed {
		return ConflictTicketClosed
	}
	if t.IfBlocked != nil && *t.IfBlocked != blocked {
		return ConflictTicketBlocked
	}
	return ""
}

// Conflict is the reason (a Conflict* constant) the step's conditions do not
// hold for n (nil: no such node), or "" when they hold.
func (st NodeStep) Conflict(n *domain.GraphNode) string {
	if n == nil {
		return ConflictNodeMissing
	}
	if len(st.IfStatusIn) > 0 && !slices.Contains(st.IfStatusIn, n.Status) {
		return ConflictStatus
	}
	if slices.Contains(st.IfStatusNotIn, n.Status) {
		return ConflictStatus
	}
	if st.CheckClaimToken {
		switch {
		case st.IfClaimToken == nil && n.ClaimToken != nil:
			return ConflictClaimToken
		case st.IfClaimToken != nil && (n.ClaimToken == nil || *n.ClaimToken != *st.IfClaimToken):
			return ConflictClaimToken
		}
	}
	if st.IfIterationCount != nil && *st.IfIterationCount != n.IterationCount {
		return ConflictIterationCount
	}
	if st.IfUpdatedAt != nil && *st.IfUpdatedAt != n.UpdatedAt {
		return ConflictUpdatedAt
	}
	return ""
}

// HasConditions reports whether the step checks anything beyond the node's
// existence.
func (st NodeStep) HasConditions() bool {
	return len(st.IfStatusIn) > 0 || len(st.IfStatusNotIn) > 0 || st.CheckClaimToken || st.IfIterationCount != nil || st.IfUpdatedAt != nil
}

// Writes reports whether the step writes anything (updated_at included).
func (st NodeStep) Writes() bool {
	return st.SetStatus != nil || st.IncrementIteration || st.AddMaxIterations != 0 || st.Touch
}

// ApplyTo applies the step's writes to n in memory, the way the store writes
// them (claim and decision rules included), so a later step's conditions see
// them. updated_at is set to now when the step writes anything.
func (st NodeStep) ApplyTo(n *domain.GraphNode, now string) {
	if st.Writes() {
		n.UpdatedAt = now
	}
	if st.SetStatus != nil {
		n.Status = *st.SetStatus
		n.ClaimedByName, n.ClaimedByNameIsFallback, n.ClaimToken, n.ClaimSessionID, n.ClaimedAt = nil, nil, nil, nil, nil
		f := decisionFieldsFor(st.Decision)
		n.DecidedByName, n.DecidedByNameIsFallback, n.DecidedAt, n.DecidedByAutopilot = f.Name, f.NameIsFallback, f.DecidedAt, f.Autopilot
	}
	if st.IncrementIteration {
		n.IterationCount++
	}
	n.MaxIterations += st.AddMaxIterations
}

// stepNodeIDs is the steps' distinct node IDs, ascending -- the order the SQL
// backends lock them in, so two transitions never wait on each other's node
// rows in opposite orders.
func stepNodeIDs(steps []NodeStep) []string {
	seen := map[string]bool{}
	ids := make([]string, 0, len(steps))
	for _, st := range steps {
		if !seen[st.NodeID] {
			seen[st.NodeID] = true
			ids = append(ids, st.NodeID)
		}
	}
	sort.Strings(ids)
	return ids
}

// stepAssignments is a step's writes as SQL assignments (updated_at not
// included -- the caller adds it whenever the step Writes, Touch included);
// empty when the step writes nothing but updated_at.
func stepAssignments(st NodeStep) ([]string, []any) {
	var sets []string
	var args []any
	if st.SetStatus != nil {
		sets = append(sets, "status=?")
		args = append(args, string(*st.SetStatus))
		cols, vals := claimColumnAssignments(*st.SetStatus, nil)
		sets = append(sets, cols...)
		args = append(args, vals...)
		cols, vals = decisionColumnAssignments(st.Decision)
		sets = append(sets, cols...)
		args = append(args, vals...)
	}
	if st.IncrementIteration {
		sets = append(sets, "iteration_count=iteration_count+1")
	}
	if st.AddMaxIterations != 0 {
		sets = append(sets, "max_iterations=max_iterations+?")
		args = append(args, st.AddMaxIterations)
	}
	return sets, args
}

// applyNodeTransitionSQL is ApplyNodeTransition for both SQL backends, in
// one transaction:
//
//  1. The ticket row is read first, FOR UPDATE on MySQL -- the same lock
//     CreateNode and the graph batch take first, so none of them waits on
//     another in the opposite order. On SQLite, _txlock=immediate makes
//     Begin take the database's write lock.
//  2. The steps' nodes are read by primary key, ascending ID, FOR UPDATE on
//     MySQL. A locking read on a primary-key equality takes a record lock
//     only -- no gap lock -- so, unlike the "nodes WHERE ticket_id = ?"
//     read the graph batch had to give up (DFLT-00328), it cannot make two
//     different tickets' transitions deadlock. The transaction therefore
//     stays at the server's default isolation level, and every decision is
//     made on these locking reads.
//  3. The conditions are checked in Go; one that does not hold on a
//     required step ends the transaction with nothing written.
//  4. The artifacts are inserted, the steps' nodes updated and the ticket
//     updated.
//  5. The nodes are read back.
//
// A step that writes the status clears the claim (claimColumnAssignments
// with no claim) and writes its decision or clears it
// (decisionColumnAssignments); a step that writes nothing issues no UPDATE,
// so its row -- updated_at included -- is left exactly as it was.
func applyNodeTransitionSQL(db *sql.DB, d sqlDialect, ticketID string, t NodeTransition) (NodeTransitionResult, error) {
	if err := t.Validate(ticketID); err != nil {
		return NodeTransitionResult{}, err
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return NodeTransitionResult{}, err
	}
	defer tx.Rollback() //nolint:errcheck

	var status string
	var blocked int
	var ticketUpdatedAt string
	if err := tx.QueryRow(`SELECT status, blocked, updated_at FROM tickets WHERE id = ?`+d.forUpdate, ticketID).Scan(&status, &blocked, &ticketUpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return NodeTransitionResult{}, domain.NewAPIError(domain.ErrCodeTicketNotFound, "ticket %s not found", ticketID)
		}
		return NodeTransitionResult{}, fmt.Errorf("loading ticket %s: %w", ticketID, err)
	}
	if reason := t.TicketConflict(domain.TicketStatus(status), blocked != 0); reason != "" {
		return NodeTransitionResult{}, &NodeTransitionConflictError{Reason: reason}
	}

	ids := stepNodeIDs(t.Steps)
	nodes := make(map[string]*domain.GraphNode, len(ids))
	for _, id := range ids {
		n, err := scanNode(tx.QueryRow(`SELECT `+nodeSelectCols+` FROM nodes WHERE id = ?`+d.forUpdate, id))
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return NodeTransitionResult{}, fmt.Errorf("loading node %s: %w", id, err)
		}
		if n.TicketID != ticketID {
			return NodeTransitionResult{}, domain.NewAPIError(domain.ErrCodeValidation, "node %s belongs to ticket %s, not %s", id, n.TicketID, ticketID)
		}
		nodes[id] = n
	}

	// The spec asks a plugin to advance updated_at past the previous value
	// when the clock would repeat it. This does not: writes to one ticket's
	// nodes are serialized by the ticket row lock and the clock has
	// nanosecond (macOS: microsecond) resolution, so a repeat is not
	// expected in practice.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	applied := make([]bool, len(t.Steps))
	for i, st := range t.Steps {
		n := nodes[st.NodeID]
		if reason := st.Conflict(n); reason != "" {
			if st.Required {
				return NodeTransitionResult{}, &NodeTransitionConflictError{NodeID: st.NodeID, Reason: reason}
			}
			continue
		}
		applied[i] = true
		st.ApplyTo(n, now)
	}

	stamps := graphBatchTimestamps(time.Now(), len(t.Artifacts))
	for i, a := range t.Artifacts {
		if _, err := tx.Exec(
			`INSERT INTO artifacts (id, ticket_id, node_id, name, type, content, file_path, metadata, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			a.ID, a.TicketID, a.NodeID, a.Name, a.Type,
			nullableString(a.Content), nullableString(a.FilePath), nullableString(a.Metadata), stamps[i],
		); err != nil {
			return NodeTransitionResult{}, fmt.Errorf("inserting artifact: %w", err)
		}
	}

	for i, st := range t.Steps {
		if !applied[i] || !st.Writes() {
			continue
		}
		sets, args := stepAssignments(st)
		sets = append(sets, "updated_at=?")
		args = append(args, now, st.NodeID)
		if _, err := tx.Exec(`UPDATE nodes SET `+strings.Join(sets, ", ")+` WHERE id=?`, args...); err != nil {
			return NodeTransitionResult{}, fmt.Errorf("updating node %s: %w", st.NodeID, err)
		}
	}

	if t.SetBlocked != nil {
		// The ticket's updated_at must never repeat a value (DFLT-00330).
		if _, err := tx.Exec(`UPDATE tickets SET blocked=?, updated_at=? WHERE id=?`, boolToInt(*t.SetBlocked), nextUpdatedAt(ticketUpdatedAt, time.Now()), ticketID); err != nil {
			return NodeTransitionResult{}, fmt.Errorf("updating ticket %s: %w", ticketID, err)
		}
	}

	out := NodeTransitionResult{Applied: applied, Nodes: make([]domain.GraphNode, 0, len(ids))}
	for _, id := range ids {
		n, err := scanNode(tx.QueryRow(`SELECT `+nodeSelectCols+` FROM nodes WHERE id = ?`, id))
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return NodeTransitionResult{}, fmt.Errorf("reading node %s back: %w", id, err)
		}
		out.Nodes = append(out.Nodes, *n)
	}
	if err := tx.Commit(); err != nil {
		return NodeTransitionResult{}, err
	}
	return out, nil
}

// ApplyNodeTransition implements NodeTransitionApplier.
func (r *SQLiteRepository) ApplyNodeTransition(ticketID string, t NodeTransition) (NodeTransitionResult, error) {
	return applyNodeTransitionSQL(r.db, sqliteDialect, ticketID, t)
}

// ApplyNodeTransition implements NodeTransitionApplier. A deadlock (Error
// 1213) rolls the whole transaction back and runs it again from the start,
// re-reading under the locks, so a retry decides on the state as it is then
// (retryMySQLDeadlock).
func (r *MySQLRepository) ApplyNodeTransition(ticketID string, t NodeTransition) (NodeTransitionResult, error) {
	var out NodeTransitionResult
	err := retryMySQLDeadlock("node_transition", "the node transition of ticket "+ticketID, slog.String("ticket_id", ticketID), func() error {
		var err error
		out, err = applyNodeTransitionSQL(r.db, mysqlDialect, ticketID, t)
		return err
	})
	return out, err
}

// httpDataSourceNodeTransitionMinor is the protocol minor version that added
// POST /tickets/{ticketId}/node-transition and the decision fields.
const httpDataSourceNodeTransitionMinor = 2

// httpNodeTransitionResponse is POST /tickets/{ticketId}/node-transition's
// answer.
type httpNodeTransitionResponse struct {
	Applied []bool         `json:"applied"`
	Nodes   []httpNodeWire `json:"nodes"`
}

// supportsNodeTransitions reports whether the data source keeps decisions
// and applies node transitions (protocol 1.2).
func (r *HTTPRepository) supportsNodeTransitions() bool {
	return r.serverMinor >= httpDataSourceNodeTransitionMinor
}

// ApplyNodeTransition implements NodeTransitionApplier. Against a data
// source speaking 1.2 or newer it is one POST
// /tickets/{ticketId}/node-transition, which the plugin must process
// atomically; its 409 INVALID_NODE_STATE becomes a
// *NodeTransitionConflictError. Against an older one nothing is sent
// (ErrNodeTransitionUnsupported), and the engine writes one call at a time,
// as before -- not atomic, and without the decision. A 1.2 plugin answering
// 404 is an error, not a reason to fall back (as for the graph batch).
func (r *HTTPRepository) ApplyNodeTransition(ticketID string, t NodeTransition) (NodeTransitionResult, error) {
	if !r.supportsNodeTransitions() {
		return NodeTransitionResult{}, ErrNodeTransitionUnsupported
	}
	if err := t.Validate(ticketID); err != nil {
		return NodeTransitionResult{}, err
	}
	if t.Steps == nil {
		t.Steps = []NodeStep{}
	}
	if t.Artifacts == nil {
		t.Artifacts = []domain.Artifact{}
	}
	var resp httpNodeTransitionResponse
	err := r.do(http.MethodPost, "/tickets/"+esc(ticketID)+"/node-transition", t, &resp)
	var apiErr *domain.APIError
	if errors.As(err, &apiErr) && apiErr.Code == domain.ErrCodeInvalidNodeState {
		return NodeTransitionResult{}, &NodeTransitionConflictError{Reason: ConflictRemote, Detail: apiErr.Message}
	}
	if err != nil {
		return NodeTransitionResult{}, err
	}
	out := NodeTransitionResult{Applied: resp.Applied, Nodes: r.nodesFromWire(resp.Nodes)}
	if len(out.Applied) != len(t.Steps) {
		return NodeTransitionResult{}, fmt.Errorf("http data source: POST /tickets/%s/node-transition: answered %d applied flags for %d steps", ticketID, len(out.Applied), len(t.Steps))
	}
	return out, nil
}

// addDecisionPatchFields adds decisionFieldsFor(decision) to a PATCH body
// that writes the status, as values or nulls -- nothing at all against a
// data source older than 1.2, which does not keep them. graph-engine's own
// status PATCHes never carry a decision (only a node transition records
// one), so in practice this sends the four nulls that clear it.
func (r *HTTPRepository) addDecisionPatchFields(body map[string]any, decision *domain.NodeDecision) {
	if !r.supportsNodeTransitions() {
		return
	}
	f := decisionFieldsFor(decision)
	body["decided_by_name"] = f.Name
	body["decided_by_name_is_fallback"] = f.NameIsFallback
	body["decided_at"] = f.DecidedAt
	body["decided_by_autopilot"] = f.Autopilot
}
