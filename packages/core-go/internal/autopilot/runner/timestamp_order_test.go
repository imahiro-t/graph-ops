package runner

import (
	"reflect"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00189: the project index orders children by created_at as a time, not
// as a string ("…43.10910002Z" is later than "…43.1091Z" but sorts before
// it as a string).

// fixedListingRepo answers ListTicketsByProject with a fixed listing; nothing
// else is called by (*Service).index.
type fixedListingRepo struct {
	store.GraphRepository
	tickets []domain.Ticket
}

func (r fixedListingRepo) ListTicketsByProject(string) ([]domain.Ticket, error) {
	return append([]domain.Ticket(nil), r.tickets...), nil
}

func TestIndex_OrdersChildrenByCreationTime(t *testing.T) {
	parent := "AP-00001"
	tickets := []domain.Ticket{
		{ID: parent, CreatedAt: "2026-09-26T10:00:42Z"},
		// Returned out of order: the later child first.
		{ID: "AP-00003", ParentTicketID: &parent, CreatedAt: "2026-09-26T10:00:43.10910002Z"},
		{ID: "AP-00002", ParentTicketID: &parent, CreatedAt: "2026-09-26T10:00:43.1091Z"},
		// Same instant as AP-00005, spelled differently: the ID settles it.
		{ID: "AP-00005", ParentTicketID: &parent, CreatedAt: "2026-09-26T10:00:44.1Z"},
		{ID: "AP-00004", ParentTicketID: &parent, CreatedAt: "2026-09-26T10:00:44.100Z"},
	}
	s := &Service{Repo: fixedListingRepo{tickets: tickets}}
	idx, err := s.index("proj")
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	want := []string{"AP-00002", "AP-00003", "AP-00004", "AP-00005"}
	if got := idx.children[parent]; !reflect.DeepEqual(got, want) {
		t.Fatalf("children = %v, want %v (creation time order)", got, want)
	}
}
