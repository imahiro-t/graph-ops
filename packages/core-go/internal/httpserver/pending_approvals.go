package httpserver

import (
	"net/http"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
)

// pendingApprovalsResponse is GET /api/projects/pending-approvals's body.
// Counts maps a project ID to the number of that project's tickets awaiting
// approval; a project with none is left out rather than listed with 0, so
// the Web UI reads "no key" as "no badge". It is always an object, never
// null, even when nothing anywhere awaits approval.
type pendingApprovalsResponse struct {
	Counts map[string]int `json:"counts"`
}

// handlePendingApprovals counts, per project, the tickets that have at least
// one approval_gate awaiting a human decision (engine.HasPendingApproval:
// a reached approval_gate still at TODO -- the same definition as the Web
// UI's "awaiting approval" blink in the ticket list). The Web UI's project
// switcher fetches it every time the menu opens to badge those projects
// (DFLT-00144).
//
// The unit is the ticket: a ticket with two such gates counts once, since the
// ticket is what a person opens to act on them.
//
// CLOSED tickets are left out even when a gate in them matches: the engine
// refuses to complete any node of a CLOSED ticket, so nobody can act on that
// gate and badging it would only point at work that cannot be done. Every
// other ticket status counts, DONE included -- a ticket's status can be set
// to DONE directly through PATCH /api/tickets/{id} without touching its
// graph, and such a ticket still blinks in the list and its gate can still be
// approved.
//
// This is a separate endpoint rather than a field on GET /api/projects so
// that a failure while computing the counts (it reads every open ticket's
// graph across every project, which against the HTTP data source is one
// remote read per ticket -- see ticketGraphs) can only cost the badges, never
// the project list itself.
func (s *Server) handlePendingApprovals(w http.ResponseWriter, r *http.Request) {
	tickets, err := s.repo.ListTickets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	open := make([]domain.Ticket, 0, len(tickets))
	for _, t := range tickets {
		if t.Status == domain.TicketClosed {
			continue
		}
		open = append(open, t)
	}
	graphs, err := s.ticketGraphs(open)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	counts := map[string]int{}
	for _, g := range graphs {
		if engine.HasPendingApproval(g.Nodes, g.Edges) {
			counts[g.Ticket.ProjectID]++
		}
	}
	writeJSON(w, http.StatusOK, pendingApprovalsResponse{Counts: counts})
}
