package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

// This file is the one-shot creation of a ticket's graph (DFLT-00328): the
// seed nodes on the first get-executable, and the rest of the graph on
// expand-graph. Both used to be a check ("no nodes yet" / "not expanded
// yet") followed by one CreateNode / CreateEdge per row, with nothing
// holding the check true until the rows were written -- so two sessions
// starting the same ticket at once could both create the seed, or both
// expand it, and a failure halfway left a partial graph. A GraphBatch is
// the whole write together with the check it depends on, and a
// GraphBatchCreator applies it atomically.

// ErrGraphChanged is what CreateGraphBatch answers when the check the batch
// was built on no longer holds -- the ticket's node count is not
// GraphBatch.ExpectedNodeCount, or the batch sets graph_expanded_at and it
// is already set. Someone else created the graph first; nothing was
// written. The engine turns it into "carry on with the graph that is there"
// (seed) or "already been expanded" (expansion).
var ErrGraphChanged = errors.New("the ticket's graph was changed by someone else before the batch could be written")

// ErrGraphBatchUnsupported is what CreateGraphBatch answers when the data
// source cannot create a graph in one request: an HTTP data source older
// than protocol 1.2. Nothing was sent; the engine falls back to creating
// the nodes and edges one at a time (not atomic).
var ErrGraphBatchUnsupported = errors.New("the data source cannot create a graph in one request (an HTTP data source needs protocol 1.2 or newer)")

// GraphBatchNodeRef names an edge's end: either a node that already exists
// (NodeID) or one the same batch creates (ConfigID, matching one of
// GraphBatch.Nodes' ConfigID). Exactly one of the two is set.
type GraphBatchNodeRef struct {
	NodeID   string `json:"node_id,omitempty"`
	ConfigID string `json:"config_id,omitempty"`
}

// GraphBatchEdge is an edge to create. ID is minted by the engine
// (edge-<uuid>, like every CreateEdge call) and stored as is.
type GraphBatchEdge struct {
	ID        string               `json:"id"`
	From      GraphBatchNodeRef    `json:"from"`
	To        GraphBatchNodeRef    `json:"to"`
	Condition domain.EdgeCondition `json:"condition"`
}

// GraphBatch is a set of nodes and edges to create for one ticket, together
// with the state it was planned against.
type GraphBatch struct {
	// ExpectedNodeCount is how many nodes the ticket had when the batch was
	// planned (0 for the seed, the seed's size for an expansion). The batch
	// is written only if the ticket still has exactly that many.
	ExpectedNodeCount int
	// Nodes are created in order. Each must have a ConfigID, which edges use
	// to refer to it; its ID is ignored -- the store mints
	// <ticketID>-<seq:02d> from the ticket's node sequence, as CreateNode
	// does.
	Nodes []domain.GraphNode
	// Edges are created in order, after every node.
	Edges []GraphBatchEdge
	// GraphExpandedAt, when set, is written to the ticket's
	// graph_expanded_at, and the batch is written only if that column is
	// still NULL.
	GraphExpandedAt *string
}

// GraphBatchCreator is an optional add-on to GraphRepository, like
// ProcessingSessionStore: creating a GraphBatch atomically. It is kept off
// GraphRepository so the fakes and wrapping test repositories do not have to
// grow it -- a repository without it gets the old one-row-at-a-time
// creation.
type GraphBatchCreator interface {
	// CreateGraphBatch writes every node and edge of b, and b's
	// GraphExpandedAt, or nothing at all. Concurrent calls for the same
	// ticket are serialized. It returns ErrGraphChanged when b's check fails,
	// ErrGraphBatchUnsupported when the data source cannot do it (nothing
	// was written in either case), a VALIDATION_ERROR *domain.APIError for
	// an edge whose ends cannot be resolved, and TICKET_NOT_FOUND when there
	// is no such ticket.
	CreateGraphBatch(ticketID string, b GraphBatch) error
}

var (
	_ GraphBatchCreator = (*SQLiteRepository)(nil)
	_ GraphBatchCreator = (*MySQLRepository)(nil)
	_ GraphBatchCreator = (*HTTPRepository)(nil)
)

// graphBatchTimestampLayout writes a batch's created_at values with a fixed
// number of fractional digits. A batch gives each row its own instant, one
// microsecond apart, so the rows list back in the order they were created;
// with RFC3339Nano, which drops trailing zeros, "…00.1Z" would sort after
// "…00.100001Z" as a string. (Every listing re-sorts by the parsed time too
// -- see timeorder.go -- so this is belt and braces for anything that
// compares the stored text.)
const graphBatchTimestampLayout = "2006-01-02T15:04:05.000000000Z"

// graphBatchTimestamps returns n created_at values from base on, one
// microsecond apart, in graphBatchTimestampLayout.
func graphBatchTimestamps(base time.Time, n int) []string {
	base = base.UTC()
	out := make([]string, n)
	for i := range out {
		out[i] = base.Add(time.Duration(i) * time.Microsecond).Format(graphBatchTimestampLayout)
	}
	return out
}

// createGraphBatchSQL is CreateGraphBatch for both SQL backends, in one
// transaction. The ticket row is read first (FOR UPDATE on MySQL -- the same
// lock CreateNode takes first, so the two never wait on each other in
// opposite orders; SQLite's _txlock=immediate already makes Begin take the
// database's write lock), then the check is made against what is committed
// now, then everything is written. Any error rolls the whole thing back.
//
// On MySQL the ticket row lock is the only lock the check needs: every
// writer of a ticket's nodes (CreateNode, another batch) takes that lock
// first, so while it is held nobody can add a node to this ticket. The
// nodes are therefore read with a plain SELECT, not a locking read, and the
// transaction runs at READ COMMITTED (d.graphBatchTx):
//   - A locking read of "nodes WHERE ticket_id = ?" at REPEATABLE READ (the
//     server default) takes next-key/gap locks on idx_nodes_ticket. For a
//     ticket with no nodes yet that is the empty gap where its rows will go
//     -- for tickets created one after another, the same gap at the end of
//     the index. Gap locks do not conflict with each other, so two such
//     tickets' batches both got one and then each INSERT waited on the
//     other's: Error 1213, a deadlock between different tickets.
//   - At READ COMMITTED every plain SELECT reads the latest committed rows,
//     so the nodes seen after the ticket row lock is granted include
//     everything committed by whoever held it before -- without relying on
//     REPEATABLE READ taking its snapshot at the first plain read, and
//     without the server's default level (SERIALIZABLE would turn the plain
//     SELECT back into a locking read with gap locks) mattering.
//
// The INSERTs still take insert-intention locks, which wait only on another
// transaction's gap lock; a batch holds none, so batches (and CreateNode,
// a plain INSERT) of different tickets never block or deadlock each other.
// TestGraphCreation_ConcurrentAcrossTickets_MySQL starts several new
// tickets at once to keep it that way.
//
// READ COMMITTED is also why a MySQL server that writes binary logs with
// binlog_format=STATEMENT refuses the batch's INSERTs with Error 1665; ROW
// (the server default since 8.0) and MIXED are fine.
// MySQLRepository.CreateGraphBatch adds that cause and fix to the error
// (explainMySQLGraphBatchError).
func createGraphBatchSQL(db *sql.DB, d sqlDialect, ticketID string, b GraphBatch) error {
	tx, err := db.BeginTx(context.Background(), d.graphBatchTx)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	var seq int
	var expandedAt sql.NullString
	var prevUpdatedAt string
	row := tx.QueryRow(`SELECT node_seq, graph_expanded_at, updated_at FROM tickets WHERE id = ?`+d.forUpdate, ticketID)
	if err := row.Scan(&seq, &expandedAt, &prevUpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return domain.NewAPIError(domain.ErrCodeTicketNotFound, "ticket %s not found", ticketID)
		}
		return fmt.Errorf("loading ticket %s: %w", ticketID, err)
	}

	existing := map[string]bool{}
	// A plain read on purpose -- see above.
	rows, err := tx.Query(`SELECT id FROM nodes WHERE ticket_id = ?`, ticketID)
	if err != nil {
		return fmt.Errorf("listing nodes for ticket %s: %w", ticketID, err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		existing[id] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(existing) != b.ExpectedNodeCount || (b.GraphExpandedAt != nil && expandedAt.Valid) {
		return ErrGraphChanged
	}

	stamps := graphBatchTimestamps(time.Now(), len(b.Nodes)+len(b.Edges))
	newIDByConfigID := make(map[string]string, len(b.Nodes))
	for i, n := range b.Nodes {
		if n.ConfigID == nil || *n.ConfigID == "" {
			return domain.NewAPIError(domain.ErrCodeValidation, "graph batch node %d (%q) has no config_id", i, n.Name)
		}
		if _, dup := newIDByConfigID[*n.ConfigID]; dup {
			return domain.NewAPIError(domain.ErrCodeValidation, "graph batch has two nodes with config_id %q", *n.ConfigID)
		}
		seq++
		id := fmt.Sprintf("%s-%02d", ticketID, seq)
		maxIter := n.MaxIterations
		if maxIter == 0 {
			maxIter = 3
		}
		ts := stamps[i]
		if _, err := tx.Exec(
			`INSERT INTO nodes (id, ticket_id, name, type, status, iteration_count, max_iterations, assignee, is_manual, gate_id, criteria, config_id, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, ticketID, n.Name, n.Type, n.Status, n.IterationCount, maxIter,
			nullableString(n.Assignee), boolToInt(n.IsManual), nullableString(n.GateID), nullableString(n.Criteria), nullableString(n.ConfigID),
			ts, ts,
		); err != nil {
			return fmt.Errorf("inserting node %s: %w", *n.ConfigID, err)
		}
		newIDByConfigID[*n.ConfigID] = id
	}

	resolve := func(edge int, end string, ref GraphBatchNodeRef) (string, error) {
		switch {
		case ref.NodeID != "" && ref.ConfigID != "":
			return "", domain.NewAPIError(domain.ErrCodeValidation, "graph batch edge %d: %s names both node_id and config_id", edge, end)
		case ref.NodeID != "":
			if !existing[ref.NodeID] {
				return "", domain.NewAPIError(domain.ErrCodeValidation, "graph batch edge %d: %s node %q is not a node of ticket %s", edge, end, ref.NodeID, ticketID)
			}
			return ref.NodeID, nil
		case ref.ConfigID != "":
			id, ok := newIDByConfigID[ref.ConfigID]
			if !ok {
				return "", domain.NewAPIError(domain.ErrCodeValidation, "graph batch edge %d: %s config_id %q is not a node of this batch", edge, end, ref.ConfigID)
			}
			return id, nil
		default:
			return "", domain.NewAPIError(domain.ErrCodeValidation, "graph batch edge %d: %s names no node", edge, end)
		}
	}
	for j, e := range b.Edges {
		if e.ID == "" {
			return domain.NewAPIError(domain.ErrCodeValidation, "graph batch edge %d has no id", j)
		}
		from, err := resolve(j, "from", e.From)
		if err != nil {
			return err
		}
		to, err := resolve(j, "to", e.To)
		if err != nil {
			return err
		}
		condition := e.Condition
		if condition == "" {
			condition = domain.EdgeAlways
		}
		// `condition` is quoted for MySQL, where it is a reserved word;
		// SQLite accepts the same backquotes.
		if _, err := tx.Exec(
			"INSERT INTO edges (id, ticket_id, from_node_id, to_node_id, `condition`, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			e.ID, ticketID, from, to, condition, stamps[len(b.Nodes)+j],
		); err != nil {
			return fmt.Errorf("inserting edge %s: %w", e.ID, err)
		}
	}

	// The ticket's updated_at must never repeat a value (DFLT-00330).
	now := nextUpdatedAt(prevUpdatedAt, time.Now())
	if b.GraphExpandedAt != nil {
		_, err = tx.Exec(`UPDATE tickets SET node_seq = ?, updated_at = ?, graph_expanded_at = ? WHERE id = ?`, seq, now, *b.GraphExpandedAt, ticketID)
	} else {
		_, err = tx.Exec(`UPDATE tickets SET node_seq = ?, updated_at = ? WHERE id = ?`, seq, now, ticketID)
	}
	if err != nil {
		return fmt.Errorf("updating ticket %s: %w", ticketID, err)
	}
	return tx.Commit()
}

// CreateGraphBatch implements GraphBatchCreator.
func (r *SQLiteRepository) CreateGraphBatch(ticketID string, b GraphBatch) error {
	return createGraphBatchSQL(r.db, sqliteDialect, ticketID, b)
}

// CreateGraphBatch implements GraphBatchCreator.
func (r *MySQLRepository) CreateGraphBatch(ticketID string, b GraphBatch) error {
	return explainMySQLGraphBatchError(createGraphBatchSQL(r.db, mysqlDialect, ticketID, b))
}

// httpDataSourceGraphBatchMinor is the protocol minor version that added
// POST /tickets/{ticketId}/graph.
const httpDataSourceGraphBatchMinor = 2

// httpGraphBatchRequest is POST /tickets/{ticketId}/graph's body.
type httpGraphBatchRequest struct {
	ExpectedNodeCount int              `json:"expected_node_count"`
	Nodes             []httpNodeWire   `json:"nodes"`
	Edges             []GraphBatchEdge `json:"edges"`
	GraphExpandedAt   *string          `json:"graph_expanded_at,omitempty"`
}

// CreateGraphBatch implements GraphBatchCreator. Against a data source
// speaking 1.2 or newer it is one POST /tickets/{ticketId}/graph, which the
// plugin must process atomically; its 409 GRAPH_CHANGED becomes
// ErrGraphChanged. Against an older one nothing is sent
// (ErrGraphBatchUnsupported), and the engine creates the graph one row at a
// time, as before -- not atomic.
func (r *HTTPRepository) CreateGraphBatch(ticketID string, b GraphBatch) error {
	if r.serverMinor < httpDataSourceGraphBatchMinor {
		return ErrGraphBatchUnsupported
	}
	req := httpGraphBatchRequest{
		ExpectedNodeCount: b.ExpectedNodeCount,
		Nodes:             make([]httpNodeWire, 0, len(b.Nodes)),
		Edges:             b.Edges,
		GraphExpandedAt:   b.GraphExpandedAt,
	}
	if req.Edges == nil {
		req.Edges = []GraphBatchEdge{}
	}
	for _, n := range b.Nodes {
		n.TicketID = ticketID
		req.Nodes = append(req.Nodes, nodeToWire(n))
	}
	err := r.do(http.MethodPost, "/tickets/"+esc(ticketID)+"/graph", req, nil)
	var apiErr *domain.APIError
	if errors.As(err, &apiErr) && apiErr.Code == domain.ErrCodeGraphChanged {
		return ErrGraphChanged
	}
	return err
}
