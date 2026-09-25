package httpserver

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00142: POST /api/tickets' parent_ticket_id, GET /api/tickets/{id}'s
// parent/children and GET /api/tickets' parent_ticket_id.

func createTicketViaAPI(t *testing.T, s *Server, body map[string]any) domain.Ticket {
	t.Helper()
	rec := doJSON(t, s, http.MethodPost, "/api/tickets", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/tickets %v = %d %s", body, rec.Code, rec.Body.String())
	}
	var tk domain.Ticket
	if err := json.Unmarshal(rec.Body.Bytes(), &tk); err != nil {
		t.Fatal(err)
	}
	return tk
}

func TestAPI_TicketParentAndChildren(t *testing.T) {
	s, _, projectID := newTestServer(t)
	parent := createTicketViaAPI(t, s, map[string]any{"title": "P"})
	child := createTicketViaAPI(t, s, map[string]any{"title": "C", "parent_ticket_id": parent.ID})
	if child.ParentTicketID == nil || *child.ParentTicketID != parent.ID {
		t.Fatalf("created child parent_ticket_id = %v, want %s", child.ParentTicketID, parent.ID)
	}
	// An explicit null or "" means no parent.
	for _, v := range []any{nil, ""} {
		if tk := createTicketViaAPI(t, s, map[string]any{"title": "N", "parent_ticket_id": v}); tk.ParentTicketID != nil {
			t.Errorf("parent_ticket_id %#v created a ticket with parent %q", v, *tk.ParentTicketID)
		}
	}

	rec := doJSON(t, s, http.MethodGet, "/api/tickets/"+parent.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET parent = %d %s", rec.Code, rec.Body.String())
	}
	var pd domain.TicketDetailWithFamily
	if err := json.Unmarshal(rec.Body.Bytes(), &pd); err != nil {
		t.Fatal(err)
	}
	if pd.Parent != nil || len(pd.Children) != 1 || pd.Children[0] != (domain.TicketRef{ID: child.ID, Title: "C", Status: domain.TicketTODO}) {
		t.Fatalf("parent detail family = parent %+v, children %+v", pd.Parent, pd.Children)
	}
	if pd.Nodes == nil || pd.Artifacts == nil {
		t.Errorf("the detail must still carry nodes/artifacts: %s", rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/tickets/"+child.ID, nil)
	var cd domain.TicketDetailWithFamily
	if err := json.Unmarshal(rec.Body.Bytes(), &cd); err != nil {
		t.Fatal(err)
	}
	if cd.Parent == nil || cd.Parent.ID != parent.ID || cd.Parent.Title != "P" {
		t.Fatalf("child detail parent = %+v", cd.Parent)
	}

	rec = doJSON(t, s, http.MethodGet, listTicketsPath(projectID), nil)
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, tk := range raw {
		v, ok := tk["parent_ticket_id"]
		if !ok {
			t.Fatalf("GET /api/tickets element lacks parent_ticket_id: %v", tk)
		}
		var id string
		_ = json.Unmarshal(tk["id"], &id)
		if id == child.ID && string(v) != `"`+parent.ID+`"` {
			t.Errorf("listed child parent_ticket_id = %s, want %q", v, parent.ID)
		}
		if id == parent.ID && string(v) != "null" {
			t.Errorf("listed parent parent_ticket_id = %s, want null", v)
		}
	}
}

func TestAPI_CreateTicketParentErrors(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	parent := createTicketViaAPI(t, s, map[string]any{"title": "P"})
	other, err := repo.CreateProject("Other", "OTH")
	if err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, s, http.MethodPost, "/api/tickets", map[string]any{"title": "C", "parent_ticket_id": "TEST-99999"})
	if rec.Code != http.StatusNotFound || decodeError(t, rec).Code != domain.ErrCodeTicketNotFound {
		t.Errorf("missing parent = %d %s, want 404 TICKET_NOT_FOUND", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPost, "/api/tickets", map[string]any{"title": "C", "parent_ticket_id": parent.ID, "project_id": other.ID})
	if rec.Code != http.StatusBadRequest || decodeError(t, rec).Code != domain.ErrCodeValidation {
		t.Errorf("parent in another project = %d %s, want 400 VALIDATION_ERROR", rec.Code, rec.Body.String())
	}
	list, _ := repo.ListTicketsByProject(projectID)
	otherList, _ := repo.ListTicketsByProject(other.ID)
	if len(list) != 1 || len(otherList) != 0 {
		t.Fatalf("a rejected request created a ticket: %d / %d", len(list), len(otherList))
	}
}

// With no project_id, a child goes to its parent's project rather than to
// the current project.
func TestAPI_CreateTicketWithParentUsesParentsProject(t *testing.T) {
	s, repo, _ := newTestServer(t)
	other, err := repo.CreateProject("Other", "OTH")
	if err != nil {
		t.Fatal(err)
	}
	parent := createTicketViaAPI(t, s, map[string]any{"title": "P", "project_id": other.ID})
	child := createTicketViaAPI(t, s, map[string]any{"title": "C", "parent_ticket_id": parent.ID})
	if child.ProjectID != other.ID {
		t.Fatalf("child project = %s, want %s", child.ProjectID, other.ID)
	}
}

func TestStatusForError_ParentTicketUnsupportedIs400(t *testing.T) {
	err := domain.NewAPIError(domain.ErrCodeParentTicketUnsupported, "x")
	if got := statusForError(err, http.StatusInternalServerError); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", got)
	}
}
