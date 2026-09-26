package store

import (
	"database/sql"
	"fmt"
	"sort"

	"github.com/graph-ops/core-go/internal/domain"
)

// Ticket parent/child relation (DFLT-00142). A ticket may name a parent
// (domain.Ticket.ParentTicketID) at creation time; the children of a ticket
// are derived from that column, never stored on the parent.

// TicketChildLister is an optional add-on to GraphRepository, in the same
// spirit as TicketGraphLister: the SQL backends answer "which tickets name
// this one as their parent?" with one indexed query. It is deliberately not
// part of GraphRepository, so the HTTP data source protocol needs no new
// endpoint -- ListChildTickets (the function) falls back to filtering the
// parent's project listing for a backend without it.
//
// The result is in creation order (created_at, then ID), each ticket with its
// labels, and [] -- never nil -- when there are none.
type TicketChildLister interface {
	ListChildTickets(parentID string) ([]domain.Ticket, error)
}

// ListChildTickets returns parent's children in creation order, through
// TicketChildLister when repo has it and otherwise by filtering
// ListTicketsByProject(parent.ProjectID) on parent_ticket_id. A parent and
// its children are always in the same project (the engine refuses anything
// else at creation), so the project listing is enough.
func ListChildTickets(repo GraphRepository, parent domain.Ticket) ([]domain.Ticket, error) {
	if lister, ok := repo.(TicketChildLister); ok {
		return lister.ListChildTickets(parent.ID)
	}
	all, err := repo.ListTicketsByProject(parent.ProjectID)
	if err != nil {
		return nil, err
	}
	out := []domain.Ticket{}
	for _, t := range all {
		if t.ParentTicketID != nil && *t.ParentTicketID == parent.ID {
			out = append(out, t)
		}
	}
	sortTicketsByCreation(out)
	return out, nil
}

// sortTicketsByCreation orders tickets oldest first, comparing created_at as
// times (not strings; see domain.TimestampKey), ties broken by ID (IDs
// within a project are minted from a zero-padded sequence, so this is still
// creation order when two timestamps collide).
func sortTicketsByCreation(tickets []domain.Ticket) {
	sort.SliceStable(tickets, func(i, j int) bool {
		if c := domain.CompareTimestamps(tickets[i].CreatedAt, tickets[j].CreatedAt); c != 0 {
			return c < 0
		}
		return tickets[i].ID < tickets[j].ID
	})
}

// listChildTickets is the shared SQLite/MySQL body of ListChildTickets
// (idx_tickets_parent backs the WHERE). The ORDER BY is string order, so the
// rows are re-sorted by sortTicketsByCreation, which means the same
// (created_at, then id) with created_at compared as a time.
func listChildTickets(db *sql.DB, parentID string) ([]domain.Ticket, error) {
	out, err := listTicketsWithLabels(db,
		`SELECT `+ticketSelectCols+` FROM tickets WHERE parent_ticket_id = ? ORDER BY created_at ASC, id ASC`, []any{parentID},
		`JOIN tickets t ON t.id = tl.ticket_id WHERE t.parent_ticket_id = ?`, []any{parentID})
	if err != nil {
		return nil, err
	}
	sortTicketsByCreation(out)
	return out, nil
}

// ensureTicketParentColumn is the backend-independent body of the DFLT-00142
// migration that adds tickets.parent_ticket_id to a DB created before it.
// Like dropLegacyProjectsWorkDirColumn, the check-then-alter is not atomic:
// when the ALTER fails, the column is checked again, and if another
// process's concurrent Init added it in the meantime the failure is treated
// as success.
func ensureTicketParentColumn(backend string, columnExists func() (bool, error), addColumn func() error) error {
	has, err := columnExists()
	if err != nil {
		return fmt.Errorf("inspecting tickets columns: %w", err)
	}
	if has {
		return nil
	}
	if addErr := addColumn(); addErr != nil {
		nowHas, checkErr := columnExists()
		if checkErr != nil || !nowHas {
			return fmt.Errorf("adding tickets.parent_ticket_id column (%s): %w", backend, addErr)
		}
	}
	return nil
}
