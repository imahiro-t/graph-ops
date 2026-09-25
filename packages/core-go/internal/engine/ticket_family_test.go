package engine

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
	"github.com/graph-ops/core-go/internal/store/httpdatasourcetest"
)

// DFLT-00142: CreateTicketWithOptions' ParentTicketID validation and
// GetTicketDetailWithFamily.

func ptr(s string) *string { return &s }

func countTickets(t *testing.T, repo store.GraphRepository, projectID string) int {
	t.Helper()
	list, err := repo.ListTicketsByProject(projectID)
	if err != nil {
		t.Fatalf("ListTicketsByProject: %v", err)
	}
	return len(list)
}

func TestCreateTicketWithParent_FamilyIsVisibleFromBothSides(t *testing.T) {
	e, _, projectID := newTestEngine(t)
	parent, err := e.CreateTicket(projectID, "P", "")
	if err != nil {
		t.Fatal(err)
	}
	c1, err := e.CreateTicketWithOptions(projectID, "子1", "", CreateTicketOptions{ParentTicketID: ptr(parent.ID)})
	if err != nil {
		t.Fatalf("CreateTicketWithOptions(child 1): %v", err)
	}
	c2, err := e.CreateTicketWithOptions(projectID, "子2", "", CreateTicketOptions{ParentTicketID: ptr(parent.ID)})
	if err != nil {
		t.Fatalf("CreateTicketWithOptions(child 2): %v", err)
	}
	if c1.ParentTicketID == nil || *c1.ParentTicketID != parent.ID {
		t.Fatalf("child parent_ticket_id = %v, want %s", c1.ParentTicketID, parent.ID)
	}

	pd, err := e.GetTicketDetailWithFamily(parent.ID)
	if err != nil || pd == nil {
		t.Fatalf("GetTicketDetailWithFamily(parent) = %v, %v", pd, err)
	}
	if pd.Parent != nil {
		t.Errorf("the root has parent %+v", pd.Parent)
	}
	want := []domain.TicketRef{
		{ID: c1.ID, Title: "子1", Status: domain.TicketTODO},
		{ID: c2.ID, Title: "子2", Status: domain.TicketTODO},
	}
	if len(pd.Children) != 2 || pd.Children[0] != want[0] || pd.Children[1] != want[1] {
		t.Fatalf("children = %+v, want %+v", pd.Children, want)
	}

	cd, err := e.GetTicketDetailWithFamily(c1.ID)
	if err != nil || cd == nil {
		t.Fatalf("GetTicketDetailWithFamily(child) = %v, %v", cd, err)
	}
	if cd.Parent == nil || *cd.Parent != (domain.TicketRef{ID: parent.ID, Title: "P", Status: domain.TicketTODO}) {
		t.Fatalf("parent = %+v", cd.Parent)
	}
	raw, _ := json.Marshal(cd)
	var shape map[string]json.RawMessage
	_ = json.Unmarshal(raw, &shape)
	if string(shape["children"]) != "[]" {
		t.Errorf("a childless ticket must serialize children as [], got %s", shape["children"])
	}
	for _, key := range []string{"nodes", "edges", "artifacts", "parent_ticket_id", "parent"} {
		if _, ok := shape[key]; !ok {
			t.Errorf("get-ticket JSON lacks %q: %s", key, raw)
		}
	}

	missing, err := e.GetTicketDetailWithFamily("TEST-99999")
	if err != nil || missing != nil {
		t.Fatalf("missing ticket = %+v, %v; want nil, nil", missing, err)
	}
}

func TestCreateTicketWithParent_RejectsMissingParent(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	before := countTickets(t, repo, projectID)
	_, err := e.CreateTicketWithOptions(projectID, "child", "", CreateTicketOptions{ParentTicketID: ptr("TEST-99999")})
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeTicketNotFound {
		t.Fatalf("err = %v, want TICKET_NOT_FOUND", err)
	}
	if after := countTickets(t, repo, projectID); after != before {
		t.Fatalf("a ticket was created: %d -> %d", before, after)
	}
}

func TestCreateTicketWithParent_RejectsOtherProject(t *testing.T) {
	e, repo, projectID := newTestEngine(t)
	parent, err := e.CreateTicket(projectID, "P", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.CreateProject("Other", "OTH")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.CreateTicketWithOptions(other.ID, "child", "", CreateTicketOptions{ParentTicketID: ptr(parent.ID)})
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeValidation {
		t.Fatalf("err = %v, want VALIDATION_ERROR", err)
	}
	if n := countTickets(t, repo, other.ID); n != 0 {
		t.Fatalf("a ticket was created in the other project: %d", n)
	}
}

// On the HTTP data source the children come from the project listing
// (there is no TicketChildLister there); a 1.0 plugin refuses a parent but
// keeps working without one.
func TestGetTicketDetailWithFamily_HTTPDataSource(t *testing.T) {
	for _, version := range []string{"1.1", "1.0"} {
		t.Run(version, func(t *testing.T) {
			plugin := httpdatasourcetest.New("family-token")
			plugin.Version = version
			srv := httptest.NewServer(plugin)
			t.Cleanup(srv.Close)
			repo, err := store.Open(store.Config{Backend: "http", HTTPURL: srv.URL, HTTPToken: "family-token"})
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			proj, err := repo.CreateProject("HTTP", "HTTP")
			if err != nil {
				t.Fatal(err)
			}
			e := New(repo)
			parent, err := e.CreateTicket(proj.ID, "P", "")
			if err != nil {
				t.Fatal(err)
			}
			child, err := e.CreateTicketWithOptions(proj.ID, "C", "", CreateTicketOptions{ParentTicketID: ptr(parent.ID)})
			if version == "1.0" {
				var apiErr *domain.APIError
				if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeParentTicketUnsupported {
					t.Fatalf("err = %v, want PARENT_TICKET_UNSUPPORTED", err)
				}
				d, err := e.GetTicketDetailWithFamily(parent.ID)
				if err != nil || d == nil || d.ParentTicketID != nil || d.Parent != nil || len(d.Children) != 0 || d.Children == nil {
					t.Fatalf("1.0 detail = %+v, %v; want no parent and [] children", d, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateTicketWithOptions: %v", err)
			}
			if _, err := e.CreateTicket(proj.ID, "unrelated", ""); err != nil {
				t.Fatal(err)
			}
			d, err := e.GetTicketDetailWithFamily(parent.ID)
			if err != nil || d == nil {
				t.Fatalf("GetTicketDetailWithFamily: %v", err)
			}
			if len(d.Children) != 1 || d.Children[0].ID != child.ID {
				t.Fatalf("children = %+v, want only %s", d.Children, child.ID)
			}
			cd, err := e.GetTicketDetailWithFamily(child.ID)
			if err != nil || cd.Parent == nil || cd.Parent.ID != parent.ID {
				t.Fatalf("child's parent = %+v, %v", cd.Parent, err)
			}
		})
	}
}
