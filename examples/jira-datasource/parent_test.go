package main

import (
	"encoding/json"
	"testing"
)

// DFLT-00142: protocol 1.1's Ticket.parent_ticket_id, kept in the
// graphops.ticket issue property rather than in Jira's native parent field.

func TestProtocol_Reports11(t *testing.T) {
	h := newHarness(t)
	var proto map[string]string
	h.mustCall("GET", "/protocol", nil, &proto, 200)
	if proto["version"] != "1.1" {
		t.Fatalf("GET /protocol version = %q, want 1.1", proto["version"])
	}
}

func TestCreateTicket_StoresParentInTicketProperty(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	parent := h.createTicket(p.ID, "parent")

	var child Ticket
	h.mustCall("POST", "/projects/"+p.ID+"/tickets", map[string]any{
		"title": "child", "description": "d", "status": "TODO", "parent_ticket_id": parent.ID,
	}, &child, 201)
	if child.ParentTicketID == nil || *child.ParentTicketID != parent.ID {
		t.Fatalf("created ticket parent_ticket_id = %v, want %s", child.ParentTicketID, parent.ID)
	}

	issue := h.jira.issue(child.ID)
	var tp ticketProp
	if err := json.Unmarshal(issue.Properties["graphops.ticket"], &tp); err != nil {
		t.Fatal(err)
	}
	if tp.ParentTicketID == nil || *tp.ParentTicketID != parent.ID {
		t.Fatalf("graphops.ticket parent_ticket_id = %v, want %s", tp.ParentTicketID, parent.ID)
	}
	if issue.Parent != "" {
		t.Fatalf("Jira's native parent field must stay unset, got %q", issue.Parent)
	}

	var got Ticket
	h.mustCall("GET", "/tickets/"+child.ID, nil, &got, 200)
	if got.ParentTicketID == nil || *got.ParentTicketID != parent.ID {
		t.Fatalf("GET parent_ticket_id = %v, want %s", got.ParentTicketID, parent.ID)
	}
	var list []Ticket
	h.mustCall("GET", "/projects/"+p.ID+"/tickets", nil, &list, 200)
	found := false
	for _, tk := range list {
		if tk.ID == child.ID {
			found = tk.ParentTicketID != nil && *tk.ParentTicketID == parent.ID
		}
		if tk.ID == parent.ID && tk.ParentTicketID != nil {
			t.Fatalf("the parent must have no parent, got %v", *tk.ParentTicketID)
		}
	}
	if !found {
		t.Fatalf("listing does not carry the child's parent_ticket_id: %+v", list)
	}

	// An update keeps the parent (read-modify-write of the property).
	h.mustCall("PATCH", "/tickets/"+child.ID, map[string]any{"title": "renamed"}, &got, 200)
	if got.ParentTicketID == nil || *got.ParentTicketID != parent.ID {
		t.Fatalf("parent_ticket_id after update = %v, want %s", got.ParentTicketID, parent.ID)
	}
}

func TestCreateTicket_WithoutParentHasNullParent(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	var raw map[string]json.RawMessage
	h.mustCall("POST", "/projects/"+p.ID+"/tickets", Ticket{Title: "t", Status: "TODO"}, &raw, 201)
	if string(raw["parent_ticket_id"]) != "null" {
		t.Fatalf("parent_ticket_id = %s, want null", raw["parent_ticket_id"])
	}
}

func TestCreateTicket_RejectsParentThatIsNotAGraphOpsTicket(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	managed := h.createTicket(p.ID, "managed")
	unmanaged := h.jira.seedIssue("GOPS", nil, nil)
	subtask := h.jira.seedSubtask(managed.ID, []string{"graphops"}, nil)

	for name, parent := range map[string]string{
		"plain Jira issue": unmanaged,
		"node sub-task":    subtask,
		"missing issue":    "GOPS-9999",
	} {
		before := len(h.jira.requestsMatching("POST", `/rest/api/3/issue$`))
		var eb errBody
		status := h.call("POST", "/projects/"+p.ID+"/tickets", map[string]any{
			"title": "child", "status": "TODO", "parent_ticket_id": parent,
		}, &eb)
		if status != 400 || eb.Error.Code != "VALIDATION_ERROR" {
			t.Fatalf("%s: status %d %+v, want 400 VALIDATION_ERROR", name, status, eb)
		}
		if after := len(h.jira.requestsMatching("POST", `/rest/api/3/issue$`)); after != before {
			t.Fatalf("%s: an issue was created", name)
		}
	}
}
