package httpserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00048: tickets carry a priority (json:"priority", one of
// "HIGH"/"MEDIUM"/"LOW"). These tests mirror ticket_assignee_test.go's shape
// for the PATCH/create/list contract.
//
// DFLT-00059: creation can set the priority via an optional "priority" body
// field, validated the same way PATCH's is.
//
// DFLT-00083: there is no unset state any more. A ticket created without a
// priority -- key omitted, or an explicit `"priority": null`, which POST
// treats the same as omitted -- is MEDIUM; the "priority" key is always
// present in responses; and PATCH with `"priority": null` is a 400 that
// leaves the stored value unchanged, like any other invalid value.

// priorityErrorBody decodes a 400 response's {"error": {"code", "message"}}.
func priorityErrorBody(t *testing.T, body []byte) (code, message string) {
	t.Helper()
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decoding error body %s: %v", body, err)
	}
	return payload.Error.Code, payload.Error.Message
}

func TestCreateTicket_PrioritySetAtCreationOrDefaultsToMedium(t *testing.T) {
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
	noPriority := decodeObject(t, recNoPriority)
	if got := noPriority["priority"]; got != "MEDIUM" {
		t.Errorf("a ticket created with no priority field should default to MEDIUM, got %v", got)
	}
	getNoPriority := doJSON(t, s, http.MethodGet, "/api/tickets/"+noPriority["id"].(string), nil)
	if got := decodeObject(t, getNoPriority)["priority"]; got != "MEDIUM" {
		t.Errorf("GET of a ticket created without priority: priority = %v, want MEDIUM", got)
	}
}

// TestCreateTicket_NullPriorityDefaultsToMedium pins the decision for an
// explicit `"priority": null` on POST: there is no stored value to clear at
// creation time, so null means "not given" -- the same as omitting the key
// -- and the ticket is created as MEDIUM (201), not rejected. This
// deliberately differs from PATCH, where null would mean "clear" and is a 400.
func TestCreateTicket_NullPriorityDefaultsToMedium(t *testing.T) {
	s, _, _ := newTestServer(t)

	rec := doJSON(t, s, http.MethodPost, "/api/tickets", map[string]any{
		"title": "null の優先度", "priority": nil,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeObject(t, rec)["priority"]; got != "MEDIUM" {
		t.Errorf("POST with priority:null should create a MEDIUM ticket, got %v", got)
	}
}

// TestCreateTicket_InvalidPriorityIs400AndNothingCreated covers the
// completion-condition edge case: an invalid priority string at creation
// time must fail the whole request (400), not silently create the ticket
// with priority dropped or defaulted.
func TestCreateTicket_InvalidPriorityIs400AndNothingCreated(t *testing.T) {
	s, repo, projectID := newTestServer(t)

	for _, invalid := range []string{"URGENT", "", "high"} {
		rec := doJSON(t, s, http.MethodPost, "/api/tickets", map[string]any{
			"title": "不正な優先度", "priority": invalid,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("priority %q: expected 400, got %d: %s", invalid, rec.Code, rec.Body.String())
		}
		if code, _ := priorityErrorBody(t, rec.Body.Bytes()); code != string(domain.ErrCodeValidation) {
			t.Errorf("priority %q: error code = %q, want %s", invalid, code, domain.ErrCodeValidation)
		}
	}

	tickets, err := repo.ListTicketsByProject(projectID)
	if err != nil {
		t.Fatalf("ListTicketsByProject: %v", err)
	}
	if len(tickets) != 0 {
		t.Errorf("a rejected create must not create any ticket, got %d", len(tickets))
	}
}

func TestUpdateTicket_PrioritySetAndChanged(t *testing.T) {
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

	get := doJSON(t, s, http.MethodGet, "/api/tickets/"+tk.ID, nil)
	if got := decodeObject(t, get)["priority"]; got != "LOW" {
		t.Errorf("GET after change: priority = %v, want LOW", got)
	}
}

// TestUpdateTicket_NullPriorityIs400AndUnchanged: clearing a priority is no
// longer possible (DFLT-00083).
func TestUpdateTicket_NullPriorityIs400AndUnchanged(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	high := domain.TicketPriorityHigh
	tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: "タイトル", Status: domain.TicketTODO, Priority: high})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	rec := doJSON(t, s, http.MethodPatch, "/api/tickets/"+tk.ID, map[string]any{"priority": nil})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if code, _ := priorityErrorBody(t, rec.Body.Bytes()); code != string(domain.ErrCodeValidation) {
		t.Errorf("error code = %q, want %s", code, domain.ErrCodeValidation)
	}

	get := doJSON(t, s, http.MethodGet, "/api/tickets/"+tk.ID, nil)
	if got := decodeObject(t, get)["priority"]; got != "HIGH" {
		t.Errorf("a rejected null PATCH must not change the stored priority, got %v", got)
	}

	// The whole PATCH is rejected: other fields in the same body don't apply.
	rec = doJSON(t, s, http.MethodPatch, "/api/tickets/"+tk.ID, map[string]any{"title": "変えてはいけない", "priority": nil})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("with title: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	got, err := repo.GetTicket(tk.ID)
	if err != nil || got == nil || got.Title != "タイトル" || got.Priority != domain.TicketPriorityHigh {
		t.Errorf("a rejected PATCH must leave the ticket unchanged, got %v, %+v", err, got)
	}
}

func TestUpdateTicket_PriorityOmittedLeavesItUnchanged(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	low := domain.TicketPriorityLow
	tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: "旧タイトル", Status: domain.TicketTODO, Priority: low})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	rec := doJSON(t, s, http.MethodPatch, "/api/tickets/"+tk.ID, map[string]any{"title": "新タイトル"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	m := decodeObject(t, rec)
	if m["title"] != "新タイトル" || m["priority"] != "LOW" {
		t.Errorf("priority should be left unchanged by an update that omits it, got %v", m)
	}
	get := doJSON(t, s, http.MethodGet, "/api/tickets/"+tk.ID, nil)
	if got := decodeObject(t, get)["priority"]; got != "LOW" {
		t.Errorf("GET after unrelated PATCH: priority = %v, want LOW", got)
	}
}

// TestUpdateTicket_InvalidPriorityIs400AndUnchanged covers the Gherkin
// scenario "HTTP API の PATCH で priority に不正な値を送ると 400 になり、優先度は変わらない".
func TestUpdateTicket_InvalidPriorityIs400AndUnchanged(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	high := domain.TicketPriorityHigh
	tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: "タイトル", Status: domain.TicketTODO, Priority: high})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	rec := doJSON(t, s, http.MethodPatch, "/api/tickets/"+tk.ID, map[string]any{"priority": "URGENT"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, message := priorityErrorBody(t, rec.Body.Bytes()); strings.Contains(message, "null") {
		t.Errorf("the error message must no longer offer null as a valid value, got %q", message)
	}

	got, err := repo.GetTicket(tk.ID)
	if err != nil || got == nil {
		t.Fatalf("GetTicket: %v, %+v", err, got)
	}
	if got.Priority != domain.TicketPriorityHigh {
		t.Errorf("a rejected PATCH must not change the stored priority, got %q", got.Priority)
	}
}

func TestListAndGetTicket_PriorityAlwaysPresent(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	tks := map[string]domain.TicketPriority{"優先度あり": domain.TicketPriorityHigh, "優先度なし": ""}
	ids := map[string]string{}
	for title, p := range tks {
		tk, err := repo.CreateTicket(projectID, domain.Ticket{Title: title, Status: domain.TicketTODO, Priority: p})
		if err != nil {
			t.Fatalf("CreateTicket: %v", err)
		}
		ids[title] = tk.ID
	}

	list := doJSON(t, s, http.MethodGet, listTicketsPath(projectID), nil)
	if list.Code != http.StatusOK {
		t.Fatalf("GET /api/tickets expected 200, got %d", list.Code)
	}
	var tickets []map[string]any
	if err := json.Unmarshal(list.Body.Bytes(), &tickets); err != nil || len(tickets) != 2 {
		t.Fatalf("decoding list: %v (%d tickets)", err, len(tickets))
	}
	gotPriority := map[string]any{}
	for _, tk := range tickets {
		p, ok := tk["priority"]
		if !ok {
			t.Errorf("GET /api/tickets: ticket %v has no priority key", tk["title"])
		}
		gotPriority[tk["title"].(string)] = p
	}
	if gotPriority["優先度あり"] != "HIGH" || gotPriority["優先度なし"] != "MEDIUM" {
		t.Errorf("GET /api/tickets: unexpected priority values: %v", gotPriority)
	}

	for title, want := range map[string]string{"優先度あり": "HIGH", "優先度なし": "MEDIUM"} {
		detail := doJSON(t, s, http.MethodGet, "/api/tickets/"+ids[title], nil)
		if detail.Code != http.StatusOK {
			t.Fatalf("GET /api/tickets/{id} expected 200, got %d", detail.Code)
		}
		if got := decodeObject(t, detail)["priority"]; got != want {
			t.Errorf("GET /api/tickets/{id} (%s): priority = %v, want %s", title, got, want)
		}
	}
}
