package store

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/graph-ops/core-go/internal/domain"
)

// This file holds the bulk graph read shared by SQLiteRepository and
// MySQLRepository: given a set of ticket IDs, load every one of those
// tickets' nodes and edges in a fixed number of queries instead of two
// queries per ticket.
//
// It exists for GET /api/tickets (DFLT-00112). That endpoint now returns
// each ticket's nodes/edges inline so the Web UI's 15s poll is one request
// rather than "one list request plus one detail request per ticket"; loading
// those graphs with ListNodesByTicket/ListEdgesByTicket in a loop would have
// moved the same N+1 from HTTP down into the DB instead of removing it.
//
// It takes a set of ticket IDs rather than a project ID -- which is what the
// ticket's own wording suggested -- because handleListTickets has a
// cross-project path (?all=true) that a project-scoped query could not
// serve; the IDs the handler already holds cover both of its paths with one
// API.

// Both SQL backends provide the bulk read; HTTPRepository deliberately does
// not (see TicketGraphLister), which is what the caller's fallback covers.
var (
	_ TicketGraphLister = (*SQLiteRepository)(nil)
	_ TicketGraphLister = (*MySQLRepository)(nil)
)

// Edge column lists, kept here so the bulk read and the per-ticket read
// can't drift apart. `condition` is a reserved word in MySQL and has to be
// quoted there, which is the only difference between the two.
const (
	sqliteEdgeSelectCols = "id, ticket_id, from_node_id, to_node_id, condition, created_at"
	mysqlEdgeSelectCols  = "id, ticket_id, from_node_id, to_node_id, `condition`, created_at"
)

// scanEdge reads one row of an edge select (the column list above).
func scanEdge(row interface {
	Scan(dest ...any) error
}) (domain.GraphEdge, error) {
	var e domain.GraphEdge
	if err := row.Scan(&e.ID, &e.TicketID, &e.FromNodeID, &e.ToNodeID, &e.Condition, &e.CreatedAt); err != nil {
		return domain.GraphEdge{}, err
	}
	return e, nil
}

// ticketGraphIDChunk caps how many ticket IDs go into one IN (...) clause.
// SQLite has a hard limit on bound parameters per statement
// (SQLITE_MAX_VARIABLE_NUMBER, 999 in older builds), so a project with more
// tickets than this is read in several statements rather than failing.
const ticketGraphIDChunk = 500

// chunkIDs splits ids into consecutive slices of at most size elements.
func chunkIDs(ids []string, size int) [][]string {
	var out [][]string
	for start := 0; start < len(ids); start += size {
		end := start + size
		if end > len(ids) {
			end = len(ids)
		}
		out = append(out, ids[start:end])
	}
	return out
}

// inPlaceholders renders "?,?,..." plus the matching args for ids.
func inPlaceholders(ids []string) (string, []any) {
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args
}

// loadNodesByTickets groups one chunk's nodes by ticket ID. Its rows are
// fully consumed and closed before it returns: SQLite's pool has exactly one
// connection (see SQLiteRepository's doc comment), so leaving a result set
// open would block the next query.
func loadNodesByTickets(db *sql.DB, ids []string, out map[string][]domain.GraphNode) error {
	ph, args := inPlaceholders(ids)
	rows, err := db.Query(`SELECT `+nodeSelectCols+` FROM nodes WHERE ticket_id IN (`+ph+`) ORDER BY created_at ASC, id ASC`, args...)
	if err != nil {
		return fmt.Errorf("listing nodes for %d tickets: %w", len(ids), err)
	}
	defer rows.Close()
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return err
		}
		out[n.TicketID] = append(out[n.TicketID], *n)
	}
	return rows.Err()
}

// loadEdgesByTickets is loadNodesByTickets for edges; edgeCols picks the
// backend's spelling of the `condition` column.
func loadEdgesByTickets(db *sql.DB, edgeCols string, ids []string, out map[string][]domain.GraphEdge) error {
	ph, args := inPlaceholders(ids)
	rows, err := db.Query(`SELECT `+edgeCols+` FROM edges WHERE ticket_id IN (`+ph+`) ORDER BY created_at ASC, id ASC`, args...)
	if err != nil {
		return fmt.Errorf("listing edges for %d tickets: %w", len(ids), err)
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanEdge(rows)
		if err != nil {
			return err
		}
		out[e.TicketID] = append(out[e.TicketID], e)
	}
	return rows.Err()
}

// listTicketGraphs implements TicketGraphLister for both SQL backends: two
// queries per chunk of ticket IDs, no artifacts, and the same
// created_at-ascending order ListNodesByTicket/ListEdgesByTicket return (the
// id tiebreaker only settles rows written within the same nanosecond, and
// node/edge IDs are minted in creation order). A ticket with no nodes (or no
// edges) is simply absent from the map; the caller normalizes that to an
// empty slice.
func listTicketGraphs(db *sql.DB, edgeCols string, ticketIDs []string) (map[string][]domain.GraphNode, map[string][]domain.GraphEdge, error) {
	nodes := map[string][]domain.GraphNode{}
	edges := map[string][]domain.GraphEdge{}
	ids := dedupeIDs(ticketIDs)
	if len(ids) == 0 {
		return nodes, edges, nil
	}
	for _, chunk := range chunkIDs(ids, ticketGraphIDChunk) {
		if err := loadNodesByTickets(db, chunk, nodes); err != nil {
			return nil, nil, err
		}
		if err := loadEdgesByTickets(db, edgeCols, chunk, edges); err != nil {
			return nil, nil, err
		}
	}
	return nodes, edges, nil
}
