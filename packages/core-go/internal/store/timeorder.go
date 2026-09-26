package store

import (
	"slices"

	"github.com/graph-ops/core-go/internal/domain"
)

// The SQL backends keep created_at as a TEXT / VARCHAR RFC3339Nano string,
// so `ORDER BY created_at` is string order, which disagrees with time order
// when two values differ in their number of fractional digits (see
// domain.TimestampKey). Every listing therefore re-sorts its rows by the
// parsed timestamp once they are read. The ORDER BY stays in the queries: the
// sort is stable, so rows at the same instant keep the order SQL returned
// them in (including any id tiebreaker the query has), exactly as before.
// No listing uses LIMIT, so sorting after reading everything is complete.

// sortByCreatedAt stably sorts xs oldest first (newestFirst=false) or newest
// first (newestFirst=true) by the timestamp createdAt returns, parsing each
// element's timestamp once.
func sortByCreatedAt[T any](xs []T, createdAt func(*T) string, newestFirst bool) {
	if len(xs) < 2 {
		return
	}
	type keyed struct {
		key domain.TimestampKey
		v   T
	}
	ks := make([]keyed, len(xs))
	for i := range xs {
		ks[i] = keyed{key: domain.NewTimestampKey(createdAt(&xs[i])), v: xs[i]}
	}
	slices.SortStableFunc(ks, func(a, b keyed) int {
		if newestFirst {
			return b.key.Compare(a.key)
		}
		return a.key.Compare(b.key)
	})
	for i := range ks {
		xs[i] = ks[i].v
	}
}

func ticketCreatedAt(t *domain.Ticket) string     { return t.CreatedAt }
func nodeCreatedAt(n *domain.GraphNode) string    { return n.CreatedAt }
func edgeCreatedAt(e *domain.GraphEdge) string    { return e.CreatedAt }
func artifactCreatedAt(a *domain.Artifact) string { return a.CreatedAt }
func projectCreatedAt(p *domain.Project) string   { return p.CreatedAt }

// oldestFirst re-sorts a listing's rows oldest first, passing a read error
// through (with nil rows), so a listing's final `return out, rows.Err()` can
// be wrapped as a whole.
func oldestFirst[T any](xs []T, err error, createdAt func(*T) string) ([]T, error) {
	if err != nil {
		return nil, err
	}
	sortByCreatedAt(xs, createdAt, false)
	return xs, nil
}

// ticketsNewestFirst re-sorts a ticket listing newest first, passing err
// through (with nil tickets). It takes exactly listTicketsWithLabels's two
// results, so `return ticketsNewestFirst(listTicketsWithLabels(...))` wraps
// the call as a whole.
func ticketsNewestFirst(ts []domain.Ticket, err error) ([]domain.Ticket, error) {
	if err != nil {
		return nil, err
	}
	sortByCreatedAt(ts, ticketCreatedAt, true)
	return ts, nil
}
