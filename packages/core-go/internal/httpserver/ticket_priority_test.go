package httpserver

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00048: tickets carry an optional priority (json:"priority", one of
// "HIGH"/"MEDIUM"/"LOW", null when unset). These tests mirror
// ticket_assignee_test.go's shape for the same PATCH/create/list contract:
// PATCH can set/change/clear it via the same nullableString present/null/
// value mechanism, an invalid value is rejected as 400 without changing the
// stored ticket, and GET/list both reflect whatever is stored.
//
// DFLT-00059: creation itself can now also set the priority via an optional
// "priority" body field -- TestCreateTicket_ResponsePriorityIsNullAndBodyIgnored
// (this file's former name/behavior) used to assert the opposite, that
// creation-time priority was always silently dropped; that guarantee was the
// very thing this ticket asked to remove, so the test below asserts the new
// contract instead: the field sets priority at creation when given and
// present, is validated the same way PATCH's is, and creating without it at
// all still yields an unset priority.

func TestCreateTicket_PrioritySetAtCreationOrOmitted(t *testing.T) {
	s, _, _ := newTestServer(t)

	rec := doJSON(t, s, http.MethodPost, "/api/tickets", map[string]any{
		"title": "優先度あり", "description": "説明", "priority": "HIGH",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	created := decodeObject(t, rec)
	if created["priority"] != "HIGH" {
		t.Errorf("a ticket created with priority:\"HIGH\" should start with that priority, got %v", created["priority"])
	}

	get := doJSON(t, s, http.MethodGet, "/api/tickets/"+created["id"].(string), nil)
	if get.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d", get.Code)
	}
	if got := decodeObject(t, get)["priority"]; got != "HIGH" {
		t.Errorf("the creation-time priority must have been stored, got %v: %s", got, get.Body.String())
	}

	recNoPriority := doJSON(t, s, http.MethodPost, "/api/tickets", map[string]any{
		"title": "優先度なし", "description": "説明",
	})
	if recNoPriority.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", recNoPriority.Code, recNoPriority.Body.String())
	}
	if got := decodeObject(t, recNoPriority)["priority"]; got != nil {
		t.Errorf("a ticket created with no priority field should start unset, got %v", got)
	}
}

// TestCreateTicket_InvalidPriorityIs400AndNothingCreated covers the
// completion-condition edge case: an invalid priority string at creation
// time must fail the whole request (400), not silently create the ticket
// with priority dropped or defaulted.
func TestCreateTicket_InvalidPriorityIs400AndNothingCreated(t *testing.T) {
	s, repo, projectID := newTestServer(t)

	rec := doJSON(t, s, http.MethodPost, "/api/tickets", map[string]any{
		"title": "不正な優先度", "priority": "URGENT",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	tickets, err := repo.ListTicketsByProject(projectID)
	if err != nil {
		t.Fatalf("ListTicketsByProject: %v", err)
	}
	if len(tickets) != 0 {
		t.Errorf("a rejected create must not create any ticket, got %d", len(tickets))
	}
}

func TestUpdateTicket_PrioritySetChangedCleared(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: "タイトル", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	// Set.
	rec := doJSON(t, s, http.MethodPatch, "/api/tickets/"+tk.ID, map[string]any{"priority": "HIGH"})
	if rec.Code != http.StatusOK {
		t.Fatalf("set: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeObject(t, rec)["priority"]; got != "HIGH" {
		t.Errorf("set: priority = %v, want HIGH", got)
	}

	// Change to a different level.
	rec = doJSON(t, s, http.MethodPatch, "/api/tickets/"+tk.ID, map[string]any{"priority": "LOW"})
	if rec.Code != http.StatusOK {
		t.Fatalf("change: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeObject(t, rec)["priority"]; got != "LOW" {
		t.Errorf("change: priority = %v, want LOW", got)
	}

	// Explicit null clears it back to unset.
	rec = doJSON(t, s, http.MethodPatch, "/api/tickets/"+tk.ID, map[string]any{"priority": nil})
	if rec.Code != http.StatusOK {
		t.Fatalf("clear: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeObject(t, rec)["priority"]; got != nil {
		t.Errorf("clear: priority = %v, want nil", got)
	}

	get := doJSON(t, s, http.MethodGet, "/api/tickets/"+tk.ID, nil)
	if got := decodeObject(t, get)["priority"]; got != nil {
		t.Errorf("GET after clearing: priority = %v, want nil", got)
	}
}

func TestUpdateTicket_PriorityOmittedLeavesItUnchanged(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	high := domain.TicketPriorityHigh
	tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: "旧タイトル", Status: domain.TicketTODO, Priority: &high})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	rec := doJSON(t, s, http.MethodPatch, "/api/tickets/"+tk.ID, map[string]any{"title": "新タイトル"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	m := decodeObject(t, rec)
	if m["title"] != "新タイトル" || m["priority"] != "HIGH" {
		t.Errorf("priority should be left unchanged by an update that omits it, got %v", m)
	}
}

// TestUpdateTicket_InvalidPriorityIs400AndUnchanged covers the Gherkin
// scenario "APIで不正な優先度の値を指定すると更新が拒否される".
func TestUpdateTicket_InvalidPriorityIs400AndUnchanged(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: "タイトル", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	rec := doJSON(t, s, http.MethodPatch, "/api/tickets/"+tk.ID, map[string]any{"priority": "URGENT"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	got, err := repo.GetTicket(tk.ID)
	if err != nil || got == nil {
		t.Fatalf("GetTicket: %v, %+v", err, got)
	}
	if got.Priority != nil {
		t.Errorf("a rejected PATCH must not change the stored priority, got %+v", got.Priority)
	}
}

func TestListAndGetTicket_PriorityReflectsStoredValue(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	high := domain.TicketPriorityHigh
	tks := map[string]*domain.TicketPriority{"優先度あり": &high, "優先度なし": nil}
	ids := map[string]string{}
	for title, p := range tks {
		tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: title, Status: domain.TicketTODO, Priority: p})
		if err != nil {
			t.Fatalf("CreateTicket: %v", err)
		}
		ids[title] = tk.ID
	}

	list := doJSON(t, s, http.MethodGet, "/api/tickets", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("GET /api/tickets expected 200, got %d", list.Code)
	}
	var tickets []map[string]any
	if err := json.Unmarshal(list.Body.Bytes(), &tickets); err != nil || len(tickets) != 2 {
		t.Fatalf("decoding list: %v (%d tickets)", err, len(tickets))
	}
	gotPriority := map[string]any{}
	for _, tk := range tickets {
		gotPriority[tk["title"].(string)] = tk["priority"]
	}
	if gotPriority["優先度あり"] != "HIGH" || gotPriority["優先度なし"] != nil {
		t.Errorf("GET /api/tickets: unexpected priority values: %v", gotPriority)
	}

	detail := doJSON(t, s, http.MethodGet, "/api/tickets/"+ids["優先度あり"], nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("GET /api/tickets/{id} expected 200, got %d", detail.Code)
	}
	if got := decodeObject(t, detail)["priority"]; got != "HIGH" {
		t.Errorf("GET /api/tickets/{id}: priority = %v, want HIGH", got)
	}
}
