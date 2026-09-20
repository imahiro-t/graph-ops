package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00047: tickets carry a real assignee again -- the display name of
// whoever last pressed "assign to me" (json:"assignee", null when
// unassigned) -- replacing DFLT-00024's shared assigned_to_me boolean, which
// couldn't tell "I assigned this" from "someone else did" on a shared
// backend. The ticket endpoints below assert the new contract: creation
// always yields an unassigned ticket (even if a caller's body includes an
// "assignee"), and PATCH can both set and clear it. The node endpoint keeps
// its own, unrelated, always-existed assignee.

func decodeObject(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decoding response %q: %v", rec.Body.String(), err)
	}
	return m
}

func assertNoAssigneeKey(t *testing.T, where string, m map[string]any) {
	t.Helper()
	if _, ok := m["assignee"]; ok {
		t.Errorf("%s: expected no \"assignee\" key, got %v", where, m)
	}
}

func TestCreateTicket_ResponseAssigneeIsNull(t *testing.T) {
	s, _, _ := newTestServer(t)
	rec := doJSON(t, s, http.MethodPost, "/api/tickets", map[string]any{"title": "新規チケット", "description": "説明"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	m := decodeObject(t, rec)
	if m["title"] != "新規チケット" || m["assignee"] != nil {
		t.Errorf("unexpected ticket: %v", m)
	}
}

func TestCreateTicket_AssigneeInBodyIsIgnored(t *testing.T) {
	s, _, _ := newTestServer(t)
	rec := doJSON(t, s, http.MethodPost, "/api/tickets", map[string]any{"title": "旧クライアントからの作成", "description": "説明", "assignee": "山田"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	created := decodeObject(t, rec)
	if created["assignee"] != nil {
		t.Errorf("a new ticket must always start unassigned, got assignee=%v", created["assignee"])
	}

	get := doJSON(t, s, http.MethodGet, "/api/tickets/"+created["id"].(string), nil)
	if get.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d", get.Code)
	}
	if strings.Contains(get.Body.String(), "山田") {
		t.Errorf("the ignored assignee value leaked into the stored ticket: %s", get.Body.String())
	}
}

func TestCreateTicket_TitleStillRequiredWhenAssigneeSent(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	rec := doJSON(t, s, http.MethodPost, "/api/tickets", map[string]any{"description": "タイトルなし", "assignee": "山田"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if tickets, _ := repo.ListTicketsByProject(projectID); len(tickets) != 0 {
		t.Errorf("no ticket should be created, got %d", len(tickets))
	}
}

func TestUpdateTicket_AssigneeSetOrCleared(t *testing.T) {
	for name, tc := range map[string]struct {
		body map[string]any
		want any
	}{
		"set":   {map[string]any{"assignee": "山田"}, "山田"},
		"clear": {map[string]any{"assignee": nil}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			s, repo, projectID := newTestServer(t)
			existing := "旧担当"
			tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: "タイトル", Status: domain.TicketTODO, Assignee: &existing})
			if err != nil {
				t.Fatalf("CreateTicket: %v", err)
			}
			rec := doJSON(t, s, http.MethodPatch, "/api/tickets/"+tk.ID, tc.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			m := decodeObject(t, rec)
			if m["title"] != "タイトル" || m["assignee"] != tc.want {
				t.Errorf("unexpected ticket: %v (want assignee=%v)", m, tc.want)
			}
			get := doJSON(t, s, http.MethodGet, "/api/tickets/"+tk.ID, nil)
			if decodeObject(t, get)["assignee"] != tc.want {
				t.Errorf("GET after PATCH: assignee should be %v, got %s", tc.want, get.Body.String())
			}
		})
	}
}

func TestUpdateTicket_AssigneeOmittedLeavesItUnchanged(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	existing := "山田"
	tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: "旧タイトル", Status: domain.TicketTODO, Assignee: &existing})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	rec := doJSON(t, s, http.MethodPatch, "/api/tickets/"+tk.ID, map[string]any{"title": "新タイトル"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	m := decodeObject(t, rec)
	if m["title"] != "新タイトル" || m["assignee"] != "山田" {
		t.Errorf("assignee should be left unchanged by an update that omits it, got %v", m)
	}
}

func TestListAndGetTicket_AssigneeReflectsStoredValue(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	nodeAssignee := "鈴木"
	names := map[string]string{"a": "田中", "b": ""}
	for title, assignee := range names {
		var assigneePtr *string
		if assignee != "" {
			assigneePtr = &assignee
		}
		tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: title, Status: domain.TicketTODO, Assignee: assigneePtr})
		if err != nil {
			t.Fatalf("CreateTicket: %v", err)
		}
		// A node with its own, unrelated assignee must not affect the
		// ticket-level assignee.
		if _, err := repo.CreateNode(domain.GraphNode{TicketID: tk.ID, Name: "n", Type: domain.NodeTypePlan, Status: domain.NodeTODO, MaxIterations: 3, Assignee: &nodeAssignee}); err != nil {
			t.Fatalf("CreateNode: %v", err)
		}
	}

	list := doJSON(t, s, http.MethodGet, "/api/tickets", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("GET /api/tickets expected 200, got %d", list.Code)
	}
	var tickets []map[string]any
	if err := json.Unmarshal(list.Body.Bytes(), &tickets); err != nil || len(tickets) != 2 {
		t.Fatalf("decoding list: %v (%d tickets)", err, len(tickets))
	}
	gotAssignee := map[string]any{}
	var assignedID string
	for _, tk := range tickets {
		gotAssignee[tk["title"].(string)] = tk["assignee"]
		if tk["title"] == "a" {
			assignedID = tk["id"].(string)
		}
	}
	if gotAssignee["a"] != "田中" || gotAssignee["b"] != nil {
		t.Errorf("GET /api/tickets: unexpected assignee values: %v", gotAssignee)
	}

	detail := doJSON(t, s, http.MethodGet, "/api/tickets/"+assignedID, nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("GET /api/tickets/{id} expected 200, got %d", detail.Code)
	}
	if got := decodeObject(t, detail)["assignee"]; got != "田中" {
		t.Errorf("GET /api/tickets/{id}: assignee = %v, want 田中", got)
	}
}

// --- Nodes keep their assignee (out of scope for DFLT-00024) ---

func newNodeWithAssignee(t *testing.T) (*Server, string, string) {
	t.Helper()
	s, repo, projectID := newTestServer(t)
	tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	a := "鈴木"
	n, err := repo.CreateNode(domain.GraphNode{TicketID: tk.ID, Name: "n", Type: domain.NodeTypePlan, Status: domain.NodeTODO, MaxIterations: 3, Assignee: &a})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	return s, tk.ID, n.ID
}

// nodeFromDetail finds nodeID inside GET /api/tickets/{ticketID}'s nodes[].
func nodeFromDetail(t *testing.T, s *Server, ticketID, nodeID string) map[string]any {
	t.Helper()
	rec := doJSON(t, s, http.MethodGet, "/api/tickets/"+ticketID, nil)
	nodes, _ := decodeObject(t, rec)["nodes"].([]any)
	for _, n := range nodes {
		if m, ok := n.(map[string]any); ok && m["id"] == nodeID {
			return m
		}
	}
	t.Fatalf("node %s not found in ticket detail %s", nodeID, rec.Body.String())
	return nil
}

func TestUpdateNode_AssigneeSetOrLeftAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		body map[string]any
		want string
	}{
		{"set", map[string]any{"assignee": "佐藤"}, "佐藤"},
		// Any still-accepted field other than assignee: assignee must stay
		// put when its key is absent. ("name" stood in here until
		// DFLT-00103 withdrew it from this endpoint.)
		{"omitted", map[string]any{"is_manual": true}, "鈴木"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ticketID, nodeID := newNodeWithAssignee(t)
			rec := doJSON(t, s, http.MethodPatch, "/api/nodes/"+nodeID, tc.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			if got := decodeObject(t, rec)["assignee"]; got != tc.want {
				t.Errorf("response assignee = %v, want %q", got, tc.want)
			}
			if got := nodeFromDetail(t, s, ticketID, nodeID)["assignee"]; got != tc.want {
				t.Errorf("persisted assignee = %v, want %q", got, tc.want)
			}
		})
	}
}

func TestUpdateNode_AssigneeNullClears(t *testing.T) {
	s, ticketID, nodeID := newNodeWithAssignee(t)
	rec := doJSON(t, s, http.MethodPatch, "/api/nodes/"+nodeID, map[string]any{"assignee": nil})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	assertNoAssigneeKey(t, "PATCH /api/nodes/{id}", decodeObject(t, rec))
	assertNoAssigneeKey(t, "GET /api/tickets/{id} nodes[]", nodeFromDetail(t, s, ticketID, nodeID))
}

func TestUpdateNode_AssigneeNonStringIs400(t *testing.T) {
	s, _, nodeID := newNodeWithAssignee(t)
	rec := doJSON(t, s, http.MethodPatch, "/api/nodes/"+nodeID, map[string]any{"assignee": 123})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
