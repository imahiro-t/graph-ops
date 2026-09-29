package store_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
	"github.com/graph-ops/core-go/internal/store/httpdatasourcetest"
)

// DFLT-00330: TicketPatch.IfUpdatedAt makes a ticket write conditional on
// the updated_at of the version it was based on.

type ifUpdatedAtFixture struct {
	ticket        domain.Ticket
	bug, feature  domain.Label
	refinedBefore *string
}

func newIfUpdatedAtFixture(t *testing.T, repo store.GraphRepository) ifUpdatedAtFixture {
	t.Helper()
	g := newClaimGraph(t, repo)
	tk, err := repo.GetTicket(g.ticketID)
	if err != nil || tk == nil {
		t.Fatalf("GetTicket: %v", err)
	}
	bug, err := repo.CreateLabel(tk.ProjectID, "bug", "red")
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	feature, err := repo.CreateLabel(tk.ProjectID, "feature", "green")
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	title, desc := "old title", "old description"
	updated, err := repo.UpdateTicket(tk.ID, store.TicketPatch{Title: &title, Description: &desc, LabelIDs: &[]string{bug.ID}})
	if err != nil {
		t.Fatalf("UpdateTicket: %v", err)
	}
	return ifUpdatedAtFixture{ticket: updated, bug: bug, feature: feature}
}

func labelIDs(tk domain.Ticket) []string {
	out := []string{}
	for _, l := range tk.Labels {
		out = append(out, l.ID)
	}
	return out
}

func TestTicketPatch_IfUpdatedAt(t *testing.T) {
	for _, b := range claimBackends() {
		t.Run(b.name, func(t *testing.T) {
			repo := b.open(t)
			get := func(id string) domain.Ticket {
				t.Helper()
				tk, err := repo.GetTicket(id)
				if err != nil || tk == nil {
					t.Fatalf("GetTicket(%s) = %v, %v", id, tk, err)
				}
				return *tk
			}

			t.Run("a matching IfUpdatedAt writes", func(t *testing.T) {
				f := newIfUpdatedAtFixture(t, repo)
				u0 := f.ticket.UpdatedAt
				title, desc := "new title", "new description"
				got, err := repo.UpdateTicket(f.ticket.ID, store.TicketPatch{Title: &title, Description: &desc, LabelIDs: &[]string{f.feature.ID}, IfUpdatedAt: &u0})
				if err != nil {
					t.Fatalf("UpdateTicket: %v", err)
				}
				after := get(f.ticket.ID)
				if after.Title != title || after.Description != desc || strings.Join(labelIDs(after), ",") != f.feature.ID {
					t.Fatalf("not written: %+v", after)
				}
				if got.UpdatedAt == u0 || after.UpdatedAt == u0 {
					t.Fatalf("updated_at stayed %s", u0)
				}
			})

			t.Run("a mismatched IfUpdatedAt writes nothing, labels included", func(t *testing.T) {
				f := newIfUpdatedAtFixture(t, repo)
				u0 := f.ticket.UpdatedAt
				stale := "2000-01-01T00:00:00Z"
				title, desc, refined := "new title", "new description", "2026-09-30T00:00:00Z"
				status := domain.TicketRefined
				_, err := repo.UpdateTicket(f.ticket.ID, store.TicketPatch{
					Title: &title, Description: &desc, Status: &status, RefinedAt: &refined,
					LabelIDs: &[]string{f.feature.ID}, IfUpdatedAt: &stale,
				})
				var apiErr *domain.APIError
				if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeTicketChanged {
					t.Fatalf("err = %v, want TICKET_CHANGED", err)
				}
				if b.name != "http-1.2" { // the plugin's error carries no details
					if apiErr.Details["expected_updated_at"] != stale || apiErr.Details["current_updated_at"] != u0 {
						t.Errorf("details = %v", apiErr.Details)
					}
				}
				after := get(f.ticket.ID)
				if after.Title != "old title" || after.Description != "old description" || after.Status != f.ticket.Status ||
					after.RefinedAt != nil || strings.Join(labelIDs(after), ",") != f.bug.ID || after.UpdatedAt != u0 {
					t.Fatalf("a refused write changed the ticket: %+v", after)
				}
			})

			t.Run("an omitted IfUpdatedAt writes unconditionally", func(t *testing.T) {
				f := newIfUpdatedAtFixture(t, repo)
				other := "other"
				if _, err := repo.UpdateTicket(f.ticket.ID, store.TicketPatch{Title: &other}); err != nil {
					t.Fatal(err)
				}
				over := "overwrite"
				if _, err := repo.UpdateTicket(f.ticket.ID, store.TicketPatch{Title: &over}); err != nil {
					t.Fatal(err)
				}
				if got := get(f.ticket.ID); got.Title != over {
					t.Fatalf("title = %q", got.Title)
				}
			})

			t.Run("IfStatus and IfUpdatedAt must both hold", func(t *testing.T) {
				f := newIfUpdatedAtFixture(t, repo)
				stale := "2000-01-01T00:00:00Z"
				status := f.ticket.Status
				title := "new title"
				_, err := repo.UpdateTicket(f.ticket.ID, store.TicketPatch{Title: &title, IfStatus: &status, IfUpdatedAt: &stale})
				var apiErr *domain.APIError
				if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeTicketChanged {
					t.Fatalf("err = %v, want TICKET_CHANGED", err)
				}
				if got := get(f.ticket.ID); got.Title != "old title" || got.UpdatedAt != f.ticket.UpdatedAt {
					t.Fatalf("a refused write changed the ticket: %+v", got)
				}
			})

			t.Run("an engine write in between makes a later conditional write conflict", func(t *testing.T) {
				f := newIfUpdatedAtFixture(t, repo)
				u0 := f.ticket.UpdatedAt
				if _, err := repo.CreateNode(domain.GraphNode{TicketID: f.ticket.ID, Name: "extra", Type: domain.NodeTypeImplementation, Status: domain.NodeTODO, MaxIterations: 3}); err != nil {
					t.Fatalf("CreateNode: %v", err)
				}
				if got := get(f.ticket.ID); got.UpdatedAt == u0 {
					t.Fatal("adding a node did not move the ticket's updated_at")
				}
				title := "new title"
				_, err := repo.UpdateTicket(f.ticket.ID, store.TicketPatch{Title: &title, IfUpdatedAt: &u0})
				var apiErr *domain.APIError
				if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeTicketChanged {
					t.Fatalf("err = %v, want TICKET_CHANGED", err)
				}
			})

			t.Run("back-to-back writes never repeat updated_at", func(t *testing.T) {
				f := newIfUpdatedAtFixture(t, repo)
				seen := map[string]bool{f.ticket.UpdatedAt: true}
				for i := 0; i < 10; i++ {
					title := "t"
					got, err := repo.UpdateTicket(f.ticket.ID, store.TicketPatch{Title: &title})
					if err != nil {
						t.Fatal(err)
					}
					if seen[got.UpdatedAt] {
						t.Fatalf("write %d repeated updated_at %s", i, got.UpdatedAt)
					}
					seen[got.UpdatedAt] = true
				}
			})
		})
	}
}

// TestTicketPatch_IfUpdatedAt_Concurrent: two writers that read the same
// version race; exactly one wins and the other gets TICKET_CHANGED.
func TestTicketPatch_IfUpdatedAt_Concurrent(t *testing.T) {
	for _, b := range transitionBackends() {
		t.Run(b.name, func(t *testing.T) {
			repo := b.open(t)
			for round := 0; round < 10; round++ {
				tk := newRaceTicket(t, repo, "race")
				u0 := tk.UpdatedAt
				titles := []string{"first", "second"}
				errs := make([]error, len(titles))
				var wg sync.WaitGroup
				start := make(chan struct{})
				for i := range titles {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						<-start
						_, errs[i] = repo.UpdateTicket(tk.ID, store.TicketPatch{Title: &titles[i], IfUpdatedAt: &u0})
					}(i)
				}
				close(start)
				wg.Wait()
				won := -1
				for i, err := range errs {
					switch {
					case err == nil:
						if won != -1 {
							t.Fatalf("round %d: both writes went through", round)
						}
						won = i
					case errCode(err) != domain.ErrCodeTicketChanged:
						t.Fatalf("round %d: err = %v, want TICKET_CHANGED", round, err)
					}
				}
				if won == -1 {
					t.Fatalf("round %d: neither write went through: %v", round, errs)
				}
				got, _ := repo.GetTicket(tk.ID)
				if got.Title != titles[won] {
					t.Fatalf("round %d: title = %q, want the winner's %q", round, got.Title, titles[won])
				}
			}
		})
	}
}

// patchBodies returns the JSON bodies of the ticket PATCHes the plugin saw,
// and how many GETs of that ticket it served.
func ticketRequests(t *testing.T, plugin *httpdatasourcetest.Plugin, ticketID string) (patches []map[string]any, gets int) {
	t.Helper()
	for _, r := range plugin.Requests() {
		if r.Path != "/tickets/"+ticketID {
			continue
		}
		switch r.Method {
		case http.MethodGet:
			gets++
		case http.MethodPatch:
			var body map[string]any
			if err := json.Unmarshal(r.Body, &body); err != nil {
				t.Fatalf("PATCH body: %v", err)
			}
			patches = append(patches, body)
		}
	}
	return patches, gets
}

func openPluginRepo(t *testing.T, version string) (store.GraphRepository, *httpdatasourcetest.Plugin, *[]string) {
	t.Helper()
	plugin := httpdatasourcetest.New("")
	plugin.Version = version
	srv := httptest.NewServer(plugin)
	t.Cleanup(srv.Close)
	repo, err := store.Open(store.Config{Backend: "http", HTTPURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	var warnings []string
	sink, ok := repo.(store.WarningSink)
	if !ok {
		t.Fatalf("%T is not a WarningSink", repo)
	}
	sink.SetLogf(func(format string, args ...any) {
		warnings = append(warnings, format)
	})
	return repo, plugin, &warnings
}

func TestTicketPatch_IfUpdatedAt_HTTPWire(t *testing.T) {
	t.Run("1.2 sends if_updated_at and neither GETs nor warns", func(t *testing.T) {
		repo, plugin, warnings := openPluginRepo(t, "1.2")
		f := newIfUpdatedAtFixture(t, repo)
		plugin.ResetRequests()
		cur := f.ticket.UpdatedAt
		for i := 0; i < 3; i++ {
			title := "t"
			got, err := repo.UpdateTicket(f.ticket.ID, store.TicketPatch{Title: &title, IfUpdatedAt: &cur})
			if err != nil {
				t.Fatalf("write %d: %v", i, err)
			}
			cur = got.UpdatedAt
		}
		patches, gets := ticketRequests(t, plugin, f.ticket.ID)
		if len(patches) != 3 || gets != 0 {
			t.Fatalf("patches=%d gets=%d, want 3 and 0", len(patches), gets)
		}
		if patches[0]["if_updated_at"] != f.ticket.UpdatedAt {
			t.Fatalf("if_updated_at = %v, want %s", patches[0]["if_updated_at"], f.ticket.UpdatedAt)
		}
		if len(*warnings) != 0 {
			t.Fatalf("warnings = %v", *warnings)
		}

		plugin.ResetRequests()
		title := "no condition"
		if _, err := repo.UpdateTicket(f.ticket.ID, store.TicketPatch{Title: &title}); err != nil {
			t.Fatal(err)
		}
		patches, _ = ticketRequests(t, plugin, f.ticket.ID)
		if _, has := patches[0]["if_updated_at"]; has {
			t.Fatal("if_updated_at sent without IfUpdatedAt")
		}
	})

	t.Run("1.1 compares first, never sends if_updated_at and warns once", func(t *testing.T) {
		repo, plugin, warnings := openPluginRepo(t, "1.1")
		f := newIfUpdatedAtFixture(t, repo)

		plugin.ResetRequests()
		u0 := f.ticket.UpdatedAt
		title := "matched"
		got, err := repo.UpdateTicket(f.ticket.ID, store.TicketPatch{Title: &title, IfUpdatedAt: &u0})
		if err != nil {
			t.Fatalf("matching write: %v", err)
		}
		patches, gets := ticketRequests(t, plugin, f.ticket.ID)
		if gets != 1 || len(patches) != 1 {
			t.Fatalf("gets=%d patches=%d, want 1 and 1", gets, len(patches))
		}
		if _, has := patches[0]["if_updated_at"]; has {
			t.Fatal("if_updated_at sent to a 1.1 data source")
		}

		plugin.ResetRequests()
		stale := u0
		title = "refused"
		_, err = repo.UpdateTicket(f.ticket.ID, store.TicketPatch{Title: &title, IfUpdatedAt: &stale})
		var apiErr *domain.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeTicketChanged {
			t.Fatalf("err = %v, want TICKET_CHANGED", err)
		}
		patches, gets = ticketRequests(t, plugin, f.ticket.ID)
		if gets != 1 || len(patches) != 0 {
			t.Fatalf("gets=%d patches=%d, want 1 and 0", gets, len(patches))
		}

		plugin.ResetRequests()
		status := got.Status
		cur := got.UpdatedAt
		title = "both"
		if _, err := repo.UpdateTicket(f.ticket.ID, store.TicketPatch{Title: &title, IfStatus: &status, IfUpdatedAt: &cur}); err != nil {
			t.Fatalf("both conditions: %v", err)
		}
		if _, gets = ticketRequests(t, plugin, f.ticket.ID); gets != 1 {
			t.Fatalf("gets = %d, want one GET for both conditions", gets)
		}

		if len(*warnings) != 1 || !strings.Contains((*warnings)[0], "not atomic") {
			t.Fatalf("warnings = %v, want exactly one", *warnings)
		}
	})

	t.Run("1.1 warns on stderr by default", func(t *testing.T) {
		plugin := httpdatasourcetest.New("")
		plugin.Version = "1.1"
		srv := httptest.NewServer(plugin)
		t.Cleanup(srv.Close)
		repo, err := store.Open(store.Config{Backend: "http", HTTPURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		f := newIfUpdatedAtFixture(t, repo)
		stderr := captureStderr(t, func() {
			for i := 0; i < 3; i++ {
				cur, _ := repo.GetTicket(f.ticket.ID)
				title := "t"
				if _, err := repo.UpdateTicket(f.ticket.ID, store.TicketPatch{Title: &title, IfUpdatedAt: &cur.UpdatedAt}); err != nil {
					t.Errorf("write %d: %v", i, err)
				}
			}
		})
		if n := strings.Count(stderr, "graph-engine: warning:"); n != 1 || !strings.HasPrefix(stderr, "graph-engine: warning:") {
			t.Fatalf("stderr = %q, want exactly one warning", stderr)
		}
	})
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	func() {
		defer func() { os.Stderr = orig }()
		fn()
	}()
	w.Close()
	return <-done
}
