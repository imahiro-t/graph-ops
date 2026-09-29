package engine

import (
	"errors"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00330: RefineTicketIfUnchanged compares the ticket's updated_at early
// and again atomically in the UpdateTicket that writes everything.

// writeAfterReadRepo writes the ticket (as another member would) right after
// the engine's first GetTicket of it, i.e. after the early comparison has
// passed and before the refine writes.
type writeAfterReadRepo struct {
	store.GraphRepository
	ticketID string
	done     bool
}

func (r *writeAfterReadRepo) GetTicket(id string) (*domain.Ticket, error) {
	tk, err := r.GraphRepository.GetTicket(id)
	if err == nil && id == r.ticketID && !r.done {
		r.done = true
		other := "他のメンバーの説明"
		if _, werr := r.GraphRepository.UpdateTicket(id, store.TicketPatch{Description: &other}); werr != nil {
			return nil, werr
		}
	}
	return tk, err
}

func TestRefineTicketIfUnchanged_ChangeBetweenReadAndWrite(t *testing.T) {
	_, repo, projectID := newTestEngine(t)
	if _, err := repo.CreateLabel(projectID, "改善", "blue"); err != nil {
		t.Fatal(err)
	}
	tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Description: "旧説明", Status: domain.TicketTODO, Priority: domain.TicketPriorityMedium})
	if err != nil {
		t.Fatal(err)
	}
	e := New(&writeAfterReadRepo{GraphRepository: repo, ticketID: tk.ID})
	u0 := tk.UpdatedAt
	_, err = e.RefineTicketIfUnchanged(tk.ID, "新しい説明", SetPriority(domain.TicketPriorityHigh), SetLabelsByName([]string{"改善"}), &u0)
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeTicketChanged {
		t.Fatalf("err = %v, want TICKET_CHANGED", err)
	}
	got, _ := repo.GetTicket(tk.ID)
	if got.Description != "他のメンバーの説明" || got.Status != domain.TicketTODO || got.RefinedAt != nil ||
		got.Priority != domain.TicketPriorityMedium || len(got.Labels) != 0 {
		t.Fatalf("a refused refine wrote: %+v", got)
	}
}

func TestRefineTicketIfUnchanged_NilIsUnconditional(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Description: "旧説明", Status: domain.TicketTODO})
	if err != nil {
		t.Fatal(err)
	}
	other := "他のメンバーの説明"
	if _, err := repo.UpdateTicket(tk.ID, store.TicketPatch{Description: &other}); err != nil {
		t.Fatal(err)
	}
	got, err := e.RefineTicketIfUnchanged(tk.ID, "新しい説明", NoPriorityChange(), NoLabelChange(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "新しい説明" || got.Status != domain.TicketRefined {
		t.Fatalf("not refined: %+v", got)
	}
}
