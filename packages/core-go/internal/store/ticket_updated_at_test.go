package store

import (
	"database/sql"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00330: a ticket's updated_at is what TicketPatch.IfUpdatedAt compares
// as an exact string, so every ticket write must store a value the row has
// never held -- even when the clock has not moved past the stored one.

func TestNextUpdatedAt(t *testing.T) {
	at := func(s string) time.Time {
		t.Helper()
		v, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct{ prev, now, want string }{
		{"2026-09-30T00:00:00Z", "2026-09-30T00:00:01Z", "2026-09-30T00:00:01Z"},
		{"2026-09-30T00:00:01Z", "2026-09-30T00:00:01Z", "2026-09-30T00:00:01.000000001Z"},
		{"2026-09-30T00:00:02Z", "2026-09-30T00:00:01Z", "2026-09-30T00:00:02.000000001Z"},
		{"2026-09-30T00:00:01.5Z", "2026-09-30T00:00:01.25Z", "2026-09-30T00:00:01.500000001Z"},
		// "…01Z" sorts after "…01.5Z" as text but is earlier as a time.
		{"2026-09-30T00:00:01.5Z", "2026-09-30T00:00:01Z", "2026-09-30T00:00:01.500000001Z"},
		{"2026-09-30T00:00:01Z", "2026-09-30T00:00:01.5Z", "2026-09-30T00:00:01.5Z"},
		// A non-UTC prev is compared as a time and the result is UTC.
		{"2026-09-30T09:00:01+09:00", "2026-09-30T00:00:01Z", "2026-09-30T00:00:01.000000001Z"},
		{"not a time", "2026-09-30T00:00:01Z", "2026-09-30T00:00:01Z"},
		{"", "2026-09-30T00:00:01Z", "2026-09-30T00:00:01Z"},
	} {
		if got := nextUpdatedAt(c.prev, at(c.now)); got != c.want {
			t.Errorf("nextUpdatedAt(%q, %s) = %q, want %q", c.prev, c.now, got, c.want)
		}
	}
}

func sqlDBOf(t *testing.T, repo GraphRepository) *sql.DB {
	t.Helper()
	switch r := repo.(type) {
	case *SQLiteRepository:
		return r.db
	case *MySQLRepository:
		return r.db
	}
	t.Fatalf("not a SQL repository: %T", repo)
	return nil
}

// TestTicketUpdatedAt_AdvancesPastAFutureValue: whichever path writes the
// ticket row, the new updated_at differs from a stored value that is equal
// to or later than the clock, and is later as a time.
func TestTicketUpdatedAt_AdvancesPastAFutureValue(t *testing.T) {
	eachSQLBackend(t, func(t *testing.T, b backend) {
		future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
		setFuture := func(t *testing.T, ticketID string) {
			t.Helper()
			if _, err := sqlDBOf(t, b.repo).Exec(`UPDATE tickets SET updated_at = ? WHERE id = ?`, future, ticketID); err != nil {
				t.Fatal(err)
			}
		}
		check := func(t *testing.T, ticketID string) {
			t.Helper()
			tk, err := b.repo.GetTicket(ticketID)
			if err != nil || tk == nil {
				t.Fatalf("GetTicket: %v", err)
			}
			if tk.UpdatedAt == future {
				t.Fatalf("updated_at stayed %s", future)
			}
			got, err1 := time.Parse(time.RFC3339Nano, tk.UpdatedAt)
			prev, _ := time.Parse(time.RFC3339Nano, future)
			if err1 != nil || !got.After(prev) {
				t.Fatalf("updated_at %s is not after %s", tk.UpdatedAt, future)
			}
		}
		newTicket := func(t *testing.T) domain.Ticket {
			t.Helper()
			tk, err := b.repo.CreateTicket(b.proj.ID, domain.Ticket{Title: "t", Status: domain.TicketInProgress, AutoExecutable: true})
			if err != nil {
				t.Fatal(err)
			}
			return tk
		}

		t.Run("UpdateTicket", func(t *testing.T) {
			tk := newTicket(t)
			setFuture(t, tk.ID)
			title := "x"
			if _, err := b.repo.UpdateTicket(tk.ID, TicketPatch{Title: &title}); err != nil {
				t.Fatal(err)
			}
			check(t, tk.ID)
		})
		t.Run("CreateNode", func(t *testing.T) {
			tk := newTicket(t)
			setFuture(t, tk.ID)
			if _, err := b.repo.CreateNode(domain.GraphNode{TicketID: tk.ID, Name: "n", Type: domain.NodeTypeImplementation, Status: domain.NodeTODO, MaxIterations: 3}); err != nil {
				t.Fatal(err)
			}
			check(t, tk.ID)
		})
		t.Run("CreateGraphBatch", func(t *testing.T) {
			tk := newTicket(t)
			setFuture(t, tk.ID)
			creator := b.repo.(GraphBatchCreator)
			cfg := "a"
			if err := creator.CreateGraphBatch(tk.ID, GraphBatch{Nodes: []domain.GraphNode{
				{ConfigID: &cfg, Name: "a", Type: domain.NodeTypeImplementation, Status: domain.NodeTODO, MaxIterations: 3},
			}}); err != nil {
				t.Fatal(err)
			}
			check(t, tk.ID)
		})
		t.Run("ApplyNodeTransition blocked", func(t *testing.T) {
			tk := newTicket(t)
			n, err := b.repo.CreateNode(domain.GraphNode{TicketID: tk.ID, Name: "n", Type: domain.NodeTypeImplementation, Status: domain.NodeTODO, MaxIterations: 3})
			if err != nil {
				t.Fatal(err)
			}
			setFuture(t, tk.ID)
			blocked := true
			if _, err := b.repo.(NodeTransitionApplier).ApplyNodeTransition(tk.ID, NodeTransition{SetBlocked: &blocked, Steps: []NodeStep{{NodeID: n.ID}}}); err != nil {
				t.Fatal(err)
			}
			check(t, tk.ID)
		})
	})
}

// TestTicketUpdatedAt_BackToBackWritesDiffer: two updates with no pause in
// between still leave two different updated_at strings.
func TestTicketUpdatedAt_BackToBackWritesDiffer(t *testing.T) {
	eachSQLBackend(t, func(t *testing.T, b backend) {
		tk, err := b.repo.CreateTicket(b.proj.ID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{tk.UpdatedAt: true}
		for i := 0; i < 20; i++ {
			title := "t"
			got, err := b.repo.UpdateTicket(tk.ID, TicketPatch{Title: &title})
			if err != nil {
				t.Fatal(err)
			}
			if seen[got.UpdatedAt] {
				t.Fatalf("write %d repeated updated_at %s", i, got.UpdatedAt)
			}
			seen[got.UpdatedAt] = true
		}
	})
}
