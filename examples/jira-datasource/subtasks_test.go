package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tests for nodes as sub-tasks: layout, IDs, limits, status labels, the
// workflow mirror, artifacts on sub-tasks, the managed-node check, listing,
// deleting and detaching.

// --- helpers ---

func (h *harness) newTicket() Ticket {
	h.t.Helper()
	return h.createTicket(h.registerGOPS().ID, "t")
}

func (h *harness) createNode(ticketID string, n GraphNode) GraphNode {
	h.t.Helper()
	var out GraphNode
	h.mustCall("POST", "/tickets/"+ticketID+"/nodes", n, &out, 201)
	return out
}

func (h *harness) patchNode(id string, patch map[string]any) GraphNode {
	h.t.Helper()
	var out GraphNode
	h.mustCall("PATCH", "/nodes/"+id, patch, &out, 200)
	return out
}

// subOf returns the sub-task key of a node.
func subOf(t *testing.T, nodeID string) string {
	t.Helper()
	_, sub, ok := parseNodeID(nodeID)
	if !ok {
		t.Fatalf("node ID %q does not parse", nodeID)
	}
	return sub
}

func (h *harness) nodePropOf(sub string) nodeProp {
	h.t.Helper()
	var np nodeProp
	raw := h.jira.issue(sub).Properties["graphops.node"]
	if err := json.Unmarshal(raw, &np); err != nil {
		h.t.Fatalf("graphops.node of %s = %s: %v", sub, raw, err)
	}
	return np
}

func statusLabelsOf(labels []string) []string {
	var out []string
	for _, l := range labels {
		if strings.HasPrefix(l, statusLabelPrefix) {
			out = append(out, l)
		}
	}
	return out
}

// requestsFor returns the logged requests for path or below it.
func (h *harness) requestsFor(path string) []fakeRequest {
	var out []fakeRequest
	for _, r := range h.jira.allRequests() {
		if r.Path == path || strings.HasPrefix(r.Path, path+"/") {
			out = append(out, r)
		}
	}
	return out
}

func transitionRequests(h *harness, sub string) (gets, posts int) {
	for _, r := range h.jira.allRequests() {
		if r.Path == "/rest/api/3/issue/"+sub+"/transitions" {
			if r.Method == "GET" {
				gets++
			} else {
				posts++
			}
		}
	}
	return gets, posts
}

func (h *harness) logsContaining(parts ...string) []string {
	var out []string
	for _, l := range h.logLines() {
		ok := true
		for _, p := range parts {
			if !strings.Contains(l, p) {
				ok = false
			}
		}
		if ok {
			out = append(out, l)
		}
	}
	return out
}

// --- 1. nodes are sub-tasks ---

func TestCreateNode_MakesOneSubtaskInOneRequest(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	h.jira.resetRequests()
	n := h.createNode(tk.ID, GraphNode{Name: "計画作成", Type: "plan", Status: "TODO"})

	subs := h.jira.subtasksOf(tk.ID)
	if len(subs) != 1 {
		t.Fatalf("sub-tasks of %s = %v, want 1", tk.ID, subs)
	}
	sub := h.jira.issue(subs[0])
	if sub.IssueType != "Subtask" || sub.Parent != tk.ID || sub.Summary != "計画作成 [plan]" {
		t.Fatalf("sub-task = %+v", sub)
	}
	np := h.nodePropOf(sub.Key)
	if np.Name != "計画作成" || np.Type != "plan" || np.Status != "TODO" || np.MaxIterations != 3 || np.CreatedAt == "" {
		t.Fatalf("graphops.node = %+v", np)
	}
	if !reflect.DeepEqual(sub.Labels, []string{"graphops-status-todo"}) {
		t.Fatalf("labels = %v", sub.Labels)
	}
	creates := h.jira.requestsMatching("POST", `^/rest/api/3/issue$`)
	if len(creates) != 1 {
		t.Fatalf("issue creations = %d", len(creates))
	}
	var body struct {
		Fields struct {
			Labels []string `json:"labels"`
		} `json:"fields"`
		Properties []jiraProperty `json:"properties"`
	}
	_ = json.Unmarshal(creates[0].Body, &body)
	if len(body.Properties) != 1 || body.Properties[0].Key != "graphops.node" || !reflect.DeepEqual(body.Fields.Labels, []string{"graphops-status-todo"}) {
		t.Fatalf("the creation request did not carry the property and the label: %s", creates[0].Body)
	}
	if puts := h.jira.requestsMatching("PUT", `^/rest/api/3/issue/`+sub.Key); len(puts) != 0 {
		t.Fatalf("follow-up writes after creating the sub-task: %+v", puts)
	}
	if n.ID != tk.ID+"-n"+strings.TrimPrefix(sub.Key, "GOPS-") || n.TicketID != tk.ID {
		t.Fatalf("node = %+v (sub-task %s)", n, sub.Key)
	}
}

func TestNodeIDNamesItsSubtask(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan"})
	ticketKey, sub, ok := parseNodeID(n.ID)
	if !ok || ticketKey != tk.ID || h.jira.issue(sub) == nil || h.jira.issue(sub).Parent != tk.ID {
		t.Fatalf("node ID %q -> %q, %q, %v", n.ID, ticketKey, sub, ok)
	}
	if tk, sub, ok := parseNodeID("GOPS-12-n15"); !ok || tk != "GOPS-12" || sub != "GOPS-15" {
		t.Fatalf("parseNodeID(GOPS-12-n15) = %q, %q, %v", tk, sub, ok)
	}
	h.jira.resetRequests()
	var got GraphNode
	h.mustCall("GET", "/nodes/"+n.ID, nil, &got, 200)
	if got.Name != "a" || got.ID != n.ID {
		t.Fatalf("GetNode = %+v", got)
	}
	for _, r := range h.jira.allRequests() {
		if r.Path != "/rest/api/3/issue/"+tk.ID && r.Path != "/rest/api/3/issue/"+sub {
			t.Fatalf("GetNode touched %s %s", r.Method, r.Path)
		}
	}
}

func TestSubtaskIssueTypeIsConfigurable(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	h.jira.subtaskTypes = map[string]bool{"Sub-task": true}
	h.store.opts.SubtaskIssueType = "Sub-task"
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan"})
	if got := h.jira.issue(subOf(t, n.ID)).IssueType; got != "Sub-task" {
		t.Fatalf("issue type = %q", got)
	}

	h.store.opts.SubtaskIssueType = "NoSuchType"
	var eb errBody
	if status := h.call("POST", "/tickets/"+tk.ID+"/nodes", GraphNode{Name: "b", Type: "plan"}, &eb); status/100 == 2 {
		t.Fatalf("CreateNode with an unknown sub-task type = %d", status)
	}
	if subs := h.jira.subtasksOf(tk.ID); len(subs) != 1 {
		t.Fatalf("sub-tasks after the failed creation = %v", subs)
	}
}

func TestNodeSummaryFollowsNameAndType(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	n := h.createNode(tk.ID, GraphNode{Name: "実装", Type: "implementation"})
	sub := subOf(t, n.ID)
	h.patchNode(n.ID, map[string]any{"name": "実装（修正）"})
	if got := h.jira.issue(sub).Summary; got != "実装（修正） [implementation]" {
		t.Fatalf("summary after rename = %q", got)
	}
	h.patchNode(n.ID, map[string]any{"type": "review"})
	if got := h.jira.issue(sub).Summary; got != "実装（修正） [review]" {
		t.Fatalf("summary after retyping = %q", got)
	}
	if np := h.nodePropOf(sub); np.Name != "実装（修正）" || np.Type != "review" {
		t.Fatalf("graphops.node = %+v", np)
	}
	// A patch that changes neither sends no summary.
	h.jira.resetRequests()
	h.patchNode(n.ID, map[string]any{"iteration_count": 1})
	for _, r := range h.jira.requestsMatching("PUT", `^/rest/api/3/issue/`+sub+`$`) {
		t.Fatalf("an edit of the sub-task was sent: %s", r.Body)
	}
}

func TestLongNodeNamesAreShortenedInTheSummaryOnly(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	long := strings.Repeat("長", 300)
	n := h.createNode(tk.ID, GraphNode{Name: long, Type: "plan"})
	summary := h.jira.issue(subOf(t, n.ID)).Summary
	if runes := []rune(summary); len(runes) > 255 || !strings.HasSuffix(summary, "... [plan]") || !strings.HasPrefix(summary, strings.Repeat("長", 100)) {
		t.Fatalf("summary (%d runes) = %q", len([]rune(summary)), summary)
	}
	if np := h.nodePropOf(subOf(t, n.ID)); np.Name != long {
		t.Fatal("graphops.node lost the full name")
	}
}

func TestListNodes_ReadsSubtasksByKeyInCreationOrder(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	a := h.createNode(tk.ID, GraphNode{Name: "A", Type: "plan"})
	h.jira.seedSubtask(tk.ID, []string{"by-hand"}, nil) // made by a Jira user: not a node
	b := h.createNode(tk.ID, GraphNode{Name: "B", Type: "plan"})
	c := h.createNode(tk.ID, GraphNode{Name: "C", Type: "plan"})
	// Same created_at for B and C: the sub-task number decides.
	for _, id := range []string{b.ID, c.ID} {
		sub := subOf(t, id)
		np := h.nodePropOf(sub)
		np.CreatedAt = "2030-01-01T00:00:00Z"
		raw, _ := json.Marshal(np)
		h.jira.issue(sub).Properties["graphops.node"] = raw
	}
	h.jira.resetRequests()
	var nodes []GraphNode
	h.mustCall("GET", "/tickets/"+tk.ID+"/nodes", nil, &nodes, 200)
	var names []string
	for _, n := range nodes {
		names = append(names, n.Name)
	}
	if !reflect.DeepEqual(names, []string{"A", "B", "C"}) || nodes[0].ID != a.ID {
		t.Fatalf("nodes = %v", names)
	}
	if n := len(h.jira.requestsMatching("POST", `^/rest/api/3/search/jql$`)); n != 0 {
		t.Fatalf("node listing used JQL search (%d requests)", n)
	}
	if n := len(h.jira.requestsMatching("POST", `^/rest/api/3/issue/bulkfetch$`)); n != 1 {
		t.Fatalf("bulkfetch requests = %d, want 1", n)
	}
}

func TestEdgesLiveOnTheTicketIssue(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	a := h.createNode(tk.ID, GraphNode{Name: "A", Type: "plan"})
	b := h.createNode(tk.ID, GraphNode{Name: "B", Type: "review"})
	var e GraphEdge
	h.mustCall("POST", "/tickets/"+tk.ID+"/edges", GraphEdge{ID: "e1", FromNodeID: a.ID, ToNodeID: b.ID}, &e, 201)
	if e.Condition != "always" || e.TicketID != tk.ID || e.CreatedAt == "" {
		t.Fatalf("edge = %+v", e)
	}
	parent := h.jira.issue(tk.ID)
	var ep edgesProp
	if err := json.Unmarshal(parent.Properties["graphops.edges"], &ep); err != nil || len(ep.Edges) != 1 || ep.Edges[0].From != a.ID {
		t.Fatalf("graphops.edges = %s (%v)", parent.Properties["graphops.edges"], err)
	}
	if _, ok := parent.Properties["graphops.graph"]; ok {
		t.Fatal("the old graphops.graph property was written")
	}
	for _, sub := range h.jira.subtasksOf(tk.ID) {
		if strings.Contains(string(h.jira.issue(sub).Properties["graphops.node"]), "e1") {
			t.Fatalf("edge data on sub-task %s", sub)
		}
	}
	var edges []GraphEdge
	h.mustCall("GET", "/tickets/"+tk.ID+"/edges", nil, &edges, 200)
	if len(edges) != 1 || edges[0] != e {
		t.Fatalf("edges = %+v, want [%+v]", edges, e)
	}
	h.mustCall("DELETE", "/tickets/"+tk.ID+"/edges", nil, nil, 204)
	h.mustCall("GET", "/tickets/"+tk.ID+"/edges", nil, &edges, 200)
	if len(edges) != 0 {
		t.Fatalf("edges after clearing = %+v", edges)
	}
}

// --- 2. limits ---

func assertAllPropertiesUnder32KB(t *testing.T, h *harness, keys ...string) {
	t.Helper()
	for _, k := range keys {
		for name, raw := range h.jira.issue(k).Properties {
			if len(raw) >= maxPropertyBytes {
				t.Fatalf("%s %s is %d bytes", k, name, len(raw))
			}
		}
	}
}

func TestSeventyNodesCanBeCreatedListedAndUpdated(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	for i := 0; i < 3; i++ {
		h.jira.seedSubtask(tk.ID, nil, nil) // by hand: still room for 70 nodes
	}
	criteria := strings.Repeat("c", 400)
	var ids []string
	for i := 0; i < 70; i++ {
		ids = append(ids, h.createNode(tk.ID, GraphNode{Name: fmt.Sprintf("node %d", i), Type: "review_gate", Criteria: &criteria}).ID)
	}
	var nodes []GraphNode
	h.mustCall("GET", "/tickets/"+tk.ID+"/nodes", nil, &nodes, 200)
	if len(nodes) != 70 {
		t.Fatalf("listed %d nodes, want 70", len(nodes))
	}
	for _, id := range ids {
		h.patchNode(id, map[string]any{"status": "DONE"})
	}
	h.mustCall("GET", "/tickets/"+tk.ID+"/nodes", nil, &nodes, 200)
	for _, n := range nodes {
		if n.Status != "DONE" {
			t.Fatalf("node %s status %q", n.ID, n.Status)
		}
	}
	assertAllPropertiesUnder32KB(t, h, append([]string{tk.ID}, h.jira.subtasksOf(tk.ID)...)...)
}

func TestATicketHoldsAtMost99Nodes(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	for i := 0; i < 99; i++ {
		h.createNode(tk.ID, GraphNode{Name: fmt.Sprint(i), Type: "custom"})
	}
	before := len(h.jira.subtasksOf(tk.ID))
	var eb errBody
	if status := h.call("POST", "/tickets/"+tk.ID+"/nodes", GraphNode{Name: "100", Type: "custom"}, &eb); status != 400 || eb.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("100th node = %d %+v", status, eb)
	}
	if after := len(h.jira.subtasksOf(tk.ID)); after != before {
		t.Fatalf("sub-tasks %d -> %d", before, after)
	}
	// Sub-tasks made by hand count towards the sub-task total, but not
	// towards the node limit: deleting a node makes room again even then.
	h.jira.seedSubtask(tk.ID, nil, nil)
	var nodes []GraphNode
	h.mustCall("GET", "/tickets/"+tk.ID+"/nodes", nil, &nodes, 200)
	h.mustCall("DELETE", "/nodes/"+nodes[0].ID, nil, nil, 204)
	h.createNode(tk.ID, GraphNode{Name: "again", Type: "custom"})
}

func TestConcurrent99thAnd100thNodes(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	for i := 0; i < 98; i++ {
		h.createNode(tk.ID, GraphNode{Name: fmt.Sprint(i), Type: "custom"})
	}
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	codes := make([]string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var eb errBody
			statuses[i] = h.call("POST", "/tickets/"+tk.ID+"/nodes", GraphNode{Name: fmt.Sprint("last", i), Type: "custom"}, &eb)
			codes[i] = eb.Error.Code
		}(i)
	}
	wg.Wait()
	sort.Ints(statuses)
	if statuses[0] != 201 || statuses[1] != 400 || (codes[0] != "VALIDATION_ERROR" && codes[1] != "VALIDATION_ERROR") {
		t.Fatalf("statuses = %v, codes = %v", statuses, codes)
	}
	withNode := 0
	for _, k := range h.jira.subtasksOf(tk.ID) {
		if _, ok := h.jira.issue(k).Properties["graphops.node"]; ok {
			withNode++
		}
	}
	var nodes []GraphNode
	h.mustCall("GET", "/tickets/"+tk.ID+"/nodes", nil, &nodes, 200)
	if withNode != 99 || len(nodes) != 99 {
		t.Fatalf("sub-tasks with graphops.node = %d, listed nodes = %d, want 99", withNode, len(nodes))
	}
}

// typicalEdges connects nodes like a real execution graph: a chain, review
// loops back, and conditional skips, about 1.6 edges per node.
func typicalEdges(nodes []GraphNode, count int) []GraphEdge {
	var out []GraphEdge
	conds := []string{"success", "failure", "always"}
	for i := 0; len(out) < count; i++ {
		from := nodes[i%len(nodes)]
		to := nodes[(i*7+1)%len(nodes)]
		out = append(out, GraphEdge{ID: fmt.Sprintf("edge-%08x", i*2654435761%4294967296), FromNodeID: from.ID, ToNodeID: to.ID, Condition: conds[i%3]})
	}
	return out
}

func TestTypicalGraphEdgesFitTheTicketProperty(t *testing.T) {
	for _, tc := range []struct{ nodes, edges int }{{99, 160}, {70, 115}} {
		t.Run(fmt.Sprintf("%d nodes", tc.nodes), func(t *testing.T) {
			h := newHarness(t)
			tk := h.newTicket()
			var nodes []GraphNode
			for i := 0; i < tc.nodes; i++ {
				nodes = append(nodes, h.createNode(tk.ID, GraphNode{Name: fmt.Sprint(i), Type: "custom"}))
			}
			for _, e := range typicalEdges(nodes, tc.edges) {
				h.mustCall("POST", "/tickets/"+tk.ID+"/edges", e, nil, 201)
			}
			size := len(h.jira.issue(tk.ID).Properties["graphops.edges"])
			if size >= maxPropertyBytes {
				t.Fatalf("graphops.edges is %d bytes", size)
			}
			t.Logf("%d edges take %d bytes (%d per edge)", tc.edges, size, size/tc.edges)
		})
	}
}

func TestEdgesOverThePropertyLimitFailExplicitly(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	a := h.createNode(tk.ID, GraphNode{Name: "a", Type: "custom"})
	b := h.createNode(tk.ID, GraphNode{Name: "b", Type: "custom"})
	var before []byte
	for i := 0; ; i++ {
		before = append([]byte(nil), h.jira.issue(tk.ID).Properties["graphops.edges"]...)
		var eb errBody
		status := h.call("POST", "/tickets/"+tk.ID+"/edges", GraphEdge{ID: fmt.Sprintf("edge-%08d", i), FromNodeID: a.ID, ToNodeID: b.ID, Condition: "success"}, &eb)
		if status == 201 {
			if i > 1000 {
				t.Fatal("no limit reached after 1000 edges")
			}
			continue
		}
		if status != 400 || eb.Error.Code != "VALIDATION_ERROR" {
			t.Fatalf("edge %d = %d %+v, want 400 VALIDATION_ERROR", i, status, eb)
		}
		if i < 200 {
			t.Fatalf("the limit was reached after only %d edges", i)
		}
		break
	}
	if !reflect.DeepEqual(before, []byte(h.jira.issue(tk.ID).Properties["graphops.edges"])) {
		t.Fatal("graphops.edges changed on the failed creation")
	}
}

func TestNodePropertyOverLimitFailsExplicitly(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	criteria := strings.Repeat("c", 40000)
	var eb errBody
	if status := h.call("POST", "/tickets/"+tk.ID+"/nodes", GraphNode{Name: "a", Type: "review_gate", Criteria: &criteria}, &eb); status != 400 || eb.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("over-limit CreateNode = %d %+v", status, eb)
	}
	if subs := h.jira.subtasksOf(tk.ID); len(subs) != 0 {
		t.Fatalf("sub-tasks = %v", subs)
	}
	n := h.createNode(tk.ID, GraphNode{Name: "b", Type: "review_gate"})
	if status := h.call("PATCH", "/nodes/"+n.ID, map[string]any{"criteria": criteria}, &eb); status != 400 || eb.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("over-limit UpdateNode = %d %+v", status, eb)
	}
}

// --- 3. status labels ---

func TestStatusLabelIsExactlyOneAndMatches(t *testing.T) {
	cases := map[string]string{
		"IN PROGRESS": "graphops-status-in-progress", "IN REVIEW": "graphops-status-in-review",
		"AWAITING FIX": "graphops-status-awaiting-fix", "DONE": "graphops-status-done",
		"REJECTED": "graphops-status-rejected", "TODO": "graphops-status-todo",
	}
	for status, label := range cases {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			tk := h.newTicket()
			n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "TODO"})
			if status == "TODO" {
				h.patchNode(n.ID, map[string]any{"status": "IN PROGRESS"})
			}
			got := h.patchNode(n.ID, map[string]any{"status": status})
			sub := subOf(t, n.ID)
			if labels := statusLabelsOf(h.jira.issue(sub).Labels); !reflect.DeepEqual(labels, []string{label}) {
				t.Fatalf("status labels = %v, want [%s]", labels, label)
			}
			if got.Status != status || h.nodePropOf(sub).Status != status {
				t.Fatalf("status = %q / %q", got.Status, h.nodePropOf(sub).Status)
			}
		})
	}
}

func TestStatusLabelStaysSingleThroughManyChanges(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "TODO"})
	sub := subOf(t, n.ID)
	for _, status := range []string{"IN PROGRESS", "IN REVIEW", "AWAITING FIX", "IN PROGRESS", "DONE"} {
		h.patchNode(n.ID, map[string]any{"status": status})
		if labels := statusLabelsOf(h.jira.issue(sub).Labels); !reflect.DeepEqual(labels, []string{statusLabel(status)}) {
			t.Fatalf("after %s: status labels = %v", status, labels)
		}
	}
}

func TestUserLabelsAreLeftAlone(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "TODO"})
	sub := subOf(t, n.ID)
	h.jira.issue(sub).Labels = append(h.jira.issue(sub).Labels, "team-a", "graphops-labelx")
	h.jira.resetRequests()
	h.patchNode(n.ID, map[string]any{"status": "DONE"})
	labels := append([]string(nil), h.jira.issue(sub).Labels...)
	sort.Strings(labels)
	if !reflect.DeepEqual(labels, []string{"graphops-labelx", "graphops-status-done", "team-a"}) {
		t.Fatalf("labels = %v", labels)
	}
	for _, r := range h.jira.requestsMatching("PUT", `^/rest/api/3/issue/`+sub+`$`) {
		var body struct {
			Update map[string][]map[string]string `json:"update"`
		}
		_ = json.Unmarshal(r.Body, &body)
		for _, op := range body.Update["labels"] {
			for _, l := range op {
				if !strings.HasPrefix(l, statusLabelPrefix) {
					t.Fatalf("label operation on %q: %s", l, r.Body)
				}
			}
		}
	}
}

func TestStrayStatusLabelsAreRemoved(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "IN PROGRESS"})
	sub := subOf(t, n.ID)
	h.jira.issue(sub).Labels = append(h.jira.issue(sub).Labels, "graphops-status-foo")
	h.patchNode(n.ID, map[string]any{"status": "DONE"})
	if labels := statusLabelsOf(h.jira.issue(sub).Labels); !reflect.DeepEqual(labels, []string{"graphops-status-done"}) {
		t.Fatalf("status labels = %v", labels)
	}
}

func TestUnchangedStatusSendsNoLabelOperation(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "IN PROGRESS"})
	sub := subOf(t, n.ID)
	h.jira.resetRequests()
	h.patchNode(n.ID, map[string]any{"iteration_count": 2})
	for _, r := range h.jira.requestsMatching("PUT", `^/rest/api/3/issue/`+sub+`$`) {
		t.Fatalf("an edit was sent: %s", r.Body)
	}
	if got := h.nodePropOf(sub).IterationCount; got != 2 {
		t.Fatalf("iteration_count = %d", got)
	}
}

func TestStatusLabelsDoNotLookLikeTickets(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	tk := h.createTicket(p.ID, "t")
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "DONE"})
	sub := h.jira.issue(subOf(t, n.ID))
	if containsString(sub.Labels, "graphops") || containsString(sub.Labels, "graphops-meta") {
		t.Fatalf("sub-task labels = %v", sub.Labels)
	}
	var tickets []Ticket
	h.mustCall("GET", "/projects/"+p.ID+"/tickets", nil, &tickets, 200)
	if len(tickets) != 1 || tickets[0].ID != tk.ID {
		t.Fatalf("tickets = %+v", tickets)
	}
}

// --- 4. workflow status ---

func TestTodoNodesAreNotTransitioned(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "TODO"})
	sub := subOf(t, n.ID)
	if st := h.jira.issue(sub).Status; st != "To Do" {
		t.Fatalf("status = %q", st)
	}
	if gets, posts := transitionRequests(h, sub); gets+posts != 0 {
		t.Fatalf("transition requests: %d GET, %d POST", gets, posts)
	}
}

func TestWorkflowStatusFollowsTheNodeStatus(t *testing.T) {
	for status, want := range map[string]string{
		"IN PROGRESS": "In Progress", "IN REVIEW": "In Progress", "AWAITING FIX": "In Progress",
		"REJECTED": "In Progress", "DONE": "Done",
	} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			tk := h.newTicket()
			n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "TODO"})
			h.patchNode(n.ID, map[string]any{"status": status})
			if got := h.jira.issue(subOf(t, n.ID)).Status; got != want {
				t.Fatalf("workflow status = %q, want %q", got, want)
			}
		})
	}
}

func TestNodesCreatedUnderWayAreTransitioned(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "IN PROGRESS"})
	if got := h.jira.issue(subOf(t, n.ID)).Status; got != "In Progress" {
		t.Fatalf("workflow status = %q", got)
	}
}

func TestReopenedNodesGoBackToInProgress(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "DONE"})
	sub := subOf(t, n.ID)
	if got := h.jira.issue(sub).Status; got != "Done" {
		t.Fatalf("workflow status = %q", got)
	}
	h.patchNode(n.ID, map[string]any{"status": "IN PROGRESS"})
	if got := h.jira.issue(sub); got.Status != "In Progress" || !reflect.DeepEqual(statusLabelsOf(got.Labels), []string{"graphops-status-in-progress"}) {
		t.Fatalf("sub-task = %+v", got)
	}
}

func TestBackToTodoKeepsTheWorkflowStatus(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "IN PROGRESS"})
	sub := subOf(t, n.ID)
	h.jira.resetRequests()
	h.patchNode(n.ID, map[string]any{"status": "TODO"})
	got := h.jira.issue(sub)
	if got.Status != "In Progress" || !reflect.DeepEqual(statusLabelsOf(got.Labels), []string{"graphops-status-todo"}) {
		t.Fatalf("sub-task = %+v", got)
	}
	if _, posts := transitionRequests(h, sub); posts != 0 {
		t.Fatalf("%d transition POSTs", posts)
	}
}

func TestNoTransitionWhenAlreadyThere(t *testing.T) {
	t.Run("same status name", func(t *testing.T) {
		h := newHarness(t)
		tk := h.newTicket()
		n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "IN PROGRESS"})
		sub := subOf(t, n.ID)
		h.jira.resetRequests()
		h.patchNode(n.ID, map[string]any{"status": "IN REVIEW"})
		if gets, posts := transitionRequests(h, sub); gets+posts != 0 {
			t.Fatalf("transition requests: %d GET, %d POST", gets, posts)
		}
	})
	t.Run("status names compare without regard to case", func(t *testing.T) {
		h := newHarness(t)
		tk := h.newTicket()
		n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "IN PROGRESS"})
		sub := subOf(t, n.ID)
		h.jira.setStatus(sub, "IN PROGRESS") // configured as "In Progress"
		h.jira.resetRequests()
		h.patchNode(n.ID, map[string]any{"status": "AWAITING FIX"})
		if gets, posts := transitionRequests(h, sub); gets+posts != 0 {
			t.Fatalf("transition requests: %d GET, %d POST", gets, posts)
		}
		if got := h.jira.issue(sub).Status; got != "IN PROGRESS" {
			t.Fatalf("workflow status = %q, want it untouched", got)
		}
	})
}

func TestConfiguredStatusNamesAreUsed(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	h.store.opts.InProgressStatus, h.store.opts.DoneStatus = "進行中", "完了"
	h.jira.workflow = map[string][]fakeTransition{
		"To Do": {{"11", "開始", "進行中"}},
		"進行中":   {{"31", "完了にする", "完了"}},
	}
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "TODO"})
	sub := subOf(t, n.ID)
	h.patchNode(n.ID, map[string]any{"status": "IN PROGRESS"})
	if got := h.jira.issue(sub).Status; got != "進行中" {
		t.Fatalf("workflow status = %q", got)
	}
	h.patchNode(n.ID, map[string]any{"status": "DONE"})
	if got := h.jira.issue(sub).Status; got != "完了" {
		t.Fatalf("workflow status = %q", got)
	}
}

func TestTransitionsCanBeTurnedOff(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	h.store.opts.DoneStatus = ""
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "IN PROGRESS"})
	sub := subOf(t, n.ID)
	h.jira.resetRequests()
	h.patchNode(n.ID, map[string]any{"status": "DONE"})
	if labels := statusLabelsOf(h.jira.issue(sub).Labels); !reflect.DeepEqual(labels, []string{"graphops-status-done"}) {
		t.Fatalf("status labels = %v", labels)
	}
	if gets, posts := transitionRequests(h, sub); gets+posts != 0 {
		t.Fatalf("transition requests: %d GET, %d POST", gets, posts)
	}
}

// hang holds a matching request until the plugin gives up on it.
func hang(match func(r *http.Request) bool) func(w http.ResponseWriter, r *http.Request) bool {
	return func(w http.ResponseWriter, r *http.Request) bool {
		if !match(r) {
			return false
		}
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		w.WriteHeader(http.StatusGatewayTimeout)
		return true
	}
}

func isTransitions(method string) func(r *http.Request) bool {
	return func(r *http.Request) bool {
		return r.Method == method && strings.HasSuffix(r.URL.Path, "/transitions")
	}
}

func TestFailedTransitionsDoNotFailTheUpdate(t *testing.T) {
	cases := map[string]func(h *harness){
		"no transition leads there": func(h *harness) {
			h.jira.workflow = map[string][]fakeTransition{"In Progress": {{"41", "Stop", "To Do"}}}
		},
		"POST answers 400": func(h *harness) {
			h.jira.failWith = func(w http.ResponseWriter, r *http.Request) bool {
				if isTransitions("POST")(r) {
					fjError(w, 400, "a required field is missing on the transition screen")
					return true
				}
				return false
			}
		},
		"GET answers 403": func(h *harness) {
			h.jira.failWith = func(w http.ResponseWriter, r *http.Request) bool {
				if isTransitions("GET")(r) {
					fjError(w, 403, "no permission to transition")
					return true
				}
				return false
			}
		},
		"GET never answers": func(h *harness) { h.jira.failWith = hang(isTransitions("GET")) },
		"POST never answers": func(h *harness) {
			h.jira.failWith = hang(isTransitions("POST"))
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			tk := h.newTicket()
			n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "IN PROGRESS"})
			sub := subOf(t, n.ID)
			h.store.transitionTimeout = 300 * time.Millisecond
			h.clearLogs()
			setup(h)
			start := time.Now()
			got := h.patchNode(n.ID, map[string]any{"status": "DONE"})
			if took := time.Since(start); took > 3*time.Second {
				t.Fatalf("the update took %s", took)
			}
			h.jira.setFailWith(nil) // a hung request may still be finishing in the fake
			if got.Status != "DONE" || h.nodePropOf(sub).Status != "DONE" {
				t.Fatalf("status = %q / %q", got.Status, h.nodePropOf(sub).Status)
			}
			if labels := statusLabelsOf(h.jira.issue(sub).Labels); !reflect.DeepEqual(labels, []string{"graphops-status-done"}) {
				t.Fatalf("status labels = %v", labels)
			}
			lines := h.logsContaining(n.ID, sub, `"Done"`)
			if len(lines) != 1 {
				t.Fatalf("transition failure log lines = %q (all: %q)", lines, h.logLines())
			}
			if strings.Contains(lines[0], h.jira.token) || strings.Contains(lines[0], h.jira.email) {
				t.Fatalf("the log line leaks credentials: %q", lines[0])
			}
			if strings.Contains(name, "never") && !strings.Contains(lines[0], "deadline") {
				t.Fatalf("the log line does not say the time ran out: %q", lines[0])
			}
			// The sub-task's lock was released: the next update goes through.
			h.patchNode(n.ID, map[string]any{"iteration_count": 1})
		})
	}
}

func TestUnknownStatusNameStillWrites(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	h.store.opts.DoneStatus = "NoSuchStatus"
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "IN PROGRESS"})
	h.patchNode(n.ID, map[string]any{"status": "DONE"})
	sub := subOf(t, n.ID)
	if labels := statusLabelsOf(h.jira.issue(sub).Labels); !reflect.DeepEqual(labels, []string{"graphops-status-done"}) {
		t.Fatalf("status labels = %v", labels)
	}
	if lines := h.logsContaining(n.ID, "NoSuchStatus"); len(lines) != 1 {
		t.Fatalf("log = %q", h.logLines())
	}
}

func TestFailedTransitionOnCreationStillCreates(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	h.jira.failWith = func(w http.ResponseWriter, r *http.Request) bool {
		if isTransitions("POST")(r) {
			fjError(w, 400, "no")
			return true
		}
		return false
	}
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "IN PROGRESS"})
	sub := subOf(t, n.ID)
	if labels := statusLabelsOf(h.jira.issue(sub).Labels); !reflect.DeepEqual(labels, []string{"graphops-status-in-progress"}) {
		t.Fatalf("status labels = %v", labels)
	}
	if lines := h.logsContaining(n.ID, sub, "In Progress"); len(lines) != 1 {
		t.Fatalf("log = %q", h.logLines())
	}
}

func TestTransitionComesAfterThePropertyAndLabelWrites(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "IN PROGRESS"})
	sub := subOf(t, n.ID)
	h.jira.resetRequests()
	h.patchNode(n.ID, map[string]any{"status": "DONE"})
	var order []string
	for _, r := range h.requestsFor("/rest/api/3/issue/" + sub) {
		if r.Method != "GET" {
			order = append(order, r.Method+" "+strings.TrimPrefix(r.Path, "/rest/api/3/issue/"+sub))
		}
	}
	want := []string{"PUT /properties/graphops.node", "PUT ", "POST /transitions"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("writes = %q, want %q", order, want)
	}
}

// A workflow whose "done" status makes issues read-only (QA review, point
// 1): a node sent back from DONE is moved out of "Done" before its label is
// edited, so the update succeeds.
func TestNodesLeaveAReadOnlyDoneStatusBeforeTheirLabelsChange(t *testing.T) {
	for _, status := range []string{"IN PROGRESS", "REJECTED", "AWAITING FIX"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			h.jira.readOnlyStatuses["Done"] = true
			tk := h.newTicket()
			n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "IN PROGRESS"})
			sub := subOf(t, n.ID)
			h.patchNode(n.ID, map[string]any{"status": "DONE"})
			if got := h.jira.issue(sub); got.Status != "Done" || !reflect.DeepEqual(statusLabelsOf(got.Labels), []string{"graphops-status-done"}) {
				t.Fatalf("setup: sub-task = %+v", got)
			}
			// Still read-only: an edit now is refused.
			if err := h.store.jira.editIssue(context.Background(), sub, map[string]any{"fields": map[string]any{"summary": "x"}}); err == nil {
				t.Fatal("setup: the fake let a Done issue be edited")
			}

			h.jira.resetRequests()
			got := h.patchNode(n.ID, map[string]any{"status": status, "iteration_count": 1})
			if got.Status != status {
				t.Fatalf("PATCH answered %+v", got)
			}
			issue := h.jira.issue(sub)
			if issue.Status != "In Progress" || !reflect.DeepEqual(statusLabelsOf(issue.Labels), []string{statusLabel(status)}) {
				t.Fatalf("sub-task = %+v", issue)
			}
			if np := h.nodePropOf(sub); np.Status != status || np.IterationCount != 1 {
				t.Fatalf("graphops.node = %+v", np)
			}
			var order []string
			for _, r := range h.requestsFor("/rest/api/3/issue/" + sub) {
				if r.Method != "GET" {
					order = append(order, r.Method+" "+strings.TrimPrefix(r.Path, "/rest/api/3/issue/"+sub))
				}
			}
			want := []string{"POST /transitions", "PUT /properties/graphops.node", "PUT "}
			if !reflect.DeepEqual(order, want) {
				t.Fatalf("writes = %q, want %q", order, want)
			}
		})
	}
}

// Marking a node DONE, and updating a DONE node without changing its
// status, still work on a read-only "done" status: the label edit comes
// before the move into it, and no edit is needed afterwards.
func TestReadOnlyDoneStatusStillTakesDoneNodes(t *testing.T) {
	h := newHarness(t)
	h.jira.readOnlyStatuses["Done"] = true
	tk := h.newTicket()
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "IN PROGRESS"})
	sub := subOf(t, n.ID)
	h.patchNode(n.ID, map[string]any{"status": "DONE"})
	h.jira.resetRequests()
	h.patchNode(n.ID, map[string]any{"iteration_count": 2})
	if rs := h.jira.requestsMatching("PUT", `^/rest/api/3/issue/`+sub+`$`); len(rs) != 0 {
		t.Fatalf("an edit was sent to the Done sub-task: %+v", rs)
	}
	if gets, posts := transitionRequests(h, sub); gets+posts != 0 {
		t.Fatalf("transition requests: %d GET, %d POST", gets, posts)
	}
	if np := h.nodePropOf(sub); np.Status != "DONE" || np.IterationCount != 2 {
		t.Fatalf("graphops.node = %+v", np)
	}
}

func TestFailedLabelUpdateFailsTheRequest(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "IN PROGRESS"})
	sub := subOf(t, n.ID)
	h.jira.failWith = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPut && r.URL.Path == "/rest/api/3/issue/"+sub {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		}
		return false
	}
	h.jira.resetRequests()
	var eb errBody
	if status := h.call("PATCH", "/nodes/"+n.ID, map[string]any{"status": "DONE"}, &eb); status/100 != 5 {
		t.Fatalf("PATCH with a failing label update = %d %+v", status, eb)
	}
	if _, posts := transitionRequests(h, sub); posts != 0 {
		t.Fatalf("%d transition POSTs", posts)
	}
}

// --- 4b. concurrent updates of one node (plan review, condition 1) ---

func TestConcurrentUpdatesEndWithMatchingWorkflowStatus(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "TODO"})
	sub := subOf(t, n.ID)
	h.jira.failWith = func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/transitions") {
			time.Sleep(150 * time.Millisecond)
		}
		return false
	}
	h.jira.resetRequests()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); h.patchNode(n.ID, map[string]any{"status": "IN PROGRESS"}) }()
	time.Sleep(50 * time.Millisecond) // DONE is accepted second
	go func() { defer wg.Done(); h.patchNode(n.ID, map[string]any{"status": "DONE"}) }()
	wg.Wait()
	got := h.jira.issue(sub)
	if h.nodePropOf(sub).Status != "DONE" || got.Status != "Done" || !reflect.DeepEqual(statusLabelsOf(got.Labels), []string{"graphops-status-done"}) {
		t.Fatalf("sub-task = %+v, graphops.node status %q", got, h.nodePropOf(sub).Status)
	}
	assertUpdatesNotInterleaved(t, h, sub)
}

// assertUpdatesNotInterleaved checks that no update's property write falls
// between another update's property write and its transition.
func assertUpdatesNotInterleaved(t *testing.T, h *harness, sub string) {
	t.Helper()
	open := false
	for _, r := range h.requestsFor("/rest/api/3/issue/" + sub) {
		switch {
		case r.Method == "PUT" && strings.HasSuffix(r.Path, "/properties/graphops.node"):
			if open {
				t.Fatalf("a property write came before the previous update's transition finished")
			}
			open = true
		case r.Method == "POST" && strings.HasSuffix(r.Path, "/transitions"):
			open = false
		}
	}
}

func TestManyConcurrentUpdatesEndConsistent(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "TODO"})
	sub := subOf(t, n.ID)
	h.jira.resetRequests()
	var wg sync.WaitGroup
	for i, status := range []string{"IN PROGRESS", "DONE", "IN REVIEW", "DONE", "AWAITING FIX", "DONE"} {
		wg.Add(1)
		go func(i int, status string) {
			defer wg.Done()
			h.patchNode(n.ID, map[string]any{"status": status, "iteration_count": i})
		}(i, status)
	}
	wg.Wait()
	final := h.nodePropOf(sub).Status
	got := h.jira.issue(sub)
	wantStatus := map[bool]string{true: "Done", false: "In Progress"}[final == "DONE"]
	if got.Status != wantStatus || !reflect.DeepEqual(statusLabelsOf(got.Labels), []string{statusLabel(final)}) {
		t.Fatalf("final %q: sub-task status %q labels %v", final, got.Status, got.Labels)
	}
}

func TestGetExecutableThenCompleteNode(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "TODO"})
	h.patchNode(n.ID, map[string]any{"status": "IN PROGRESS"})
	h.patchNode(n.ID, map[string]any{"status": "DONE"})
	got := h.jira.issue(subOf(t, n.ID))
	if got.Status != "Done" || !reflect.DeepEqual(statusLabelsOf(got.Labels), []string{"graphops-status-done"}) {
		t.Fatalf("sub-task = %+v", got)
	}
}

// --- 5. artifacts on sub-tasks ---

func TestArtifactIsACommentOnTheNodeSubtask(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	n1 := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan"})
	n2 := h.createNode(tk.ID, GraphNode{Name: "b", Type: "review"})
	sub := subOf(t, n1.ID)
	text := "the plan"
	var art Artifact
	h.mustCall("POST", "/tickets/"+tk.ID+"/artifacts", Artifact{NodeID: n1.ID, Name: "計画", Type: "text", Content: &text}, &art, 201)
	other := "review notes"
	h.mustCall("POST", "/tickets/"+tk.ID+"/artifacts", Artifact{NodeID: n2.ID, Name: "review", Type: "text", Content: &other}, nil, 201)

	nodeID, commentID, ok := parseArtifactID(art.ID)
	if !ok || nodeID != n1.ID || art.ID != n1.ID+"-c"+commentID || art.NodeID != n1.ID || art.TicketID != tk.ID {
		t.Fatalf("artifact = %+v", art)
	}
	comments := h.jira.issue(sub).Comments
	if len(comments) != 1 || comments[0].ID != commentID {
		t.Fatalf("comments on %s = %+v", sub, comments)
	}
	var ap artifactProp
	if err := json.Unmarshal(comments[0].Properties["graphops.artifact"], &ap); err != nil || ap.NodeID != n1.ID {
		t.Fatalf("graphops.artifact = %s", comments[0].Properties["graphops.artifact"])
	}
	body, _ := json.Marshal(comments[0].Body)
	for _, want := range []string{"計画", "text", n1.ID, "codeBlock", "the plan"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("comment body lacks %q: %s", want, body)
		}
	}
	if ids := h.nodePropOf(sub).ArtifactCommentIDs; !reflect.DeepEqual(ids, []string{commentID}) {
		t.Fatalf("artifact_comment_ids = %v", ids)
	}
	if parent := h.jira.issue(tk.ID); len(parent.Comments) != 0 || len(parent.Attachments) != 0 {
		t.Fatalf("the ticket issue got %d comments, attachments %v", len(parent.Comments), parent.Attachments)
	}

	h.jira.resetRequests()
	var got Artifact
	h.mustCall("GET", "/artifacts/"+art.ID, nil, &got, 200)
	if got.Content == nil || *got.Content != text || got.ID != art.ID {
		t.Fatalf("GetArtifact = %+v", got)
	}
	for _, r := range h.jira.allRequests() {
		if r.Path != "/rest/api/3/issue/"+tk.ID && r.Path != "/rest/api/3/issue/"+sub && r.Path != "/rest/api/3/issue/"+sub+"/comment/"+commentID {
			t.Fatalf("GetArtifact read %s", r.Path)
		}
	}
	// A splitting check on a made-up ID.
	if nid, cid, ok := parseArtifactID("GOPS-1-n15-c10023"); !ok || nid != "GOPS-1-n15" || cid != "10023" {
		t.Fatalf("parseArtifactID = %q %q %v", nid, cid, ok)
	}
}

func TestLargeAndBinaryArtifactsAreSubtaskAttachments(t *testing.T) {
	cases := []struct {
		name, typ, content string
	}{
		{"html", "html", "<html><body>report</body></html>"},
		{"image", "image", base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G', 0, 1, 2, 3})},
		{"large text", "text", strings.Repeat("x", 40*1024)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tk := h.newTicket()
			n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "report"})
			sub := subOf(t, n.ID)
			content := tc.content
			var art Artifact
			h.mustCall("POST", "/tickets/"+tk.ID+"/artifacts", Artifact{NodeID: n.ID, Name: tc.name, Type: tc.typ, Content: &content}, &art, 201)

			issue := h.jira.issue(sub)
			if len(issue.Attachments) != 1 {
				t.Fatalf("attachments = %v", issue.Attachments)
			}
			if parent := h.jira.issue(tk.ID); len(parent.Attachments) != 0 || len(parent.Comments) != 0 {
				t.Fatalf("the ticket issue got attachments %v", parent.Attachments)
			}
			var ap artifactProp
			_ = json.Unmarshal(issue.Comments[0].Properties["graphops.artifact"], &ap)
			if ap.AttachmentID != issue.Attachments[0] || ap.Content != nil {
				t.Fatalf("graphops.artifact = %+v", ap)
			}
			var got Artifact
			h.mustCall("GET", "/artifacts/"+art.ID, nil, &got, 200)
			if got.Content == nil || *got.Content != tc.content {
				t.Fatalf("round trip lost the content")
			}
			var listed []Artifact
			h.mustCall("GET", "/tickets/"+tk.ID+"/artifacts", nil, &listed, 200)
			if len(listed) != 1 || !listed[0].HasContent {
				t.Fatalf("listing = %+v", listed)
			}
			if tc.typ == "text" && (listed[0].Content == nil || *listed[0].Content != tc.content) {
				t.Fatal("text content must be included in the ticket listing")
			}
			if tc.typ != "text" && listed[0].Content != nil {
				t.Fatal("html/image content should be omitted from the ticket listing")
			}
		})
	}
}

func TestFailedNodeWriteRemovesTheArtifactCommentAndAttachment(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "report"})
	sub := subOf(t, n.ID)
	h.jira.failWith = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPut && r.URL.Path == "/rest/api/3/issue/"+sub+"/properties/graphops.node" {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		}
		return false
	}
	html := "<p>report</p>"
	var eb errBody
	if status := h.call("POST", "/tickets/"+tk.ID+"/artifacts", Artifact{NodeID: n.ID, Name: "r", Type: "html", Content: &html}, &eb); status/100 != 5 {
		t.Fatalf("CreateArtifact = %d %+v", status, eb)
	}
	issue := h.jira.issue(sub)
	if len(issue.Comments) != 0 {
		t.Fatalf("comments left: %+v", issue.Comments)
	}
	for _, id := range issue.Attachments {
		if h.jira.hasAttachment(id) {
			t.Fatalf("attachment %s left", id)
		}
	}
}

func TestArtifactsCannotGoToAnotherTicketsNode(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	t1 := h.createTicket(p.ID, "1")
	t2 := h.createTicket(p.ID, "2")
	n := h.createNode(t2.ID, GraphNode{Name: "a", Type: "plan"})
	h.jira.resetRequests()
	text := "x"
	var eb errBody
	if status := h.call("POST", "/tickets/"+t1.ID+"/artifacts", Artifact{NodeID: n.ID, Name: "x", Type: "text", Content: &text}, &eb); status != 404 || eb.Error.Code != "NODE_NOT_FOUND" {
		t.Fatalf("CreateArtifact = %d %+v", status, eb)
	}
	if rs := h.jira.changingRequests(); len(rs) != 0 {
		t.Fatalf("changing requests: %+v", rs)
	}
}

func TestArtifactListingsPerNodeAndPerTicket(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	a := h.createNode(tk.ID, GraphNode{Name: "A", Type: "plan"})
	b := h.createNode(tk.ID, GraphNode{Name: "B", Type: "plan"})
	var want []string
	for i, n := range []GraphNode{a, b, a, b} {
		c := fmt.Sprint("content ", i)
		var art Artifact
		h.mustCall("POST", "/tickets/"+tk.ID+"/artifacts", Artifact{NodeID: n.ID, Name: fmt.Sprint(i), Type: "text", Content: &c}, &art, 201)
		want = append(want, art.ID)
	}
	var byNode []Artifact
	h.mustCall("GET", "/nodes/"+a.ID+"/artifacts", nil, &byNode, 200)
	if len(byNode) != 2 || byNode[0].ID != want[0] || byNode[1].ID != want[2] {
		t.Fatalf("ListArtifactsByNode = %+v", byNode)
	}
	var byTicket []Artifact
	h.mustCall("GET", "/tickets/"+tk.ID+"/artifacts", nil, &byTicket, 200)
	var got []string
	for _, art := range byTicket {
		got = append(got, art.ID)
		if !strings.HasPrefix(art.ID, art.NodeID+"-c") || art.TicketID != tk.ID {
			t.Fatalf("artifact %+v", art)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListArtifactsByTicket = %v, want %v", got, want)
	}
}

func TestTicketDetailDoesNotReadCommentsPerNode(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	for i := 0; i < 30; i++ {
		n := h.createNode(tk.ID, GraphNode{Name: fmt.Sprint(i), Type: "plan"})
		c := "x"
		h.mustCall("POST", "/tickets/"+tk.ID+"/artifacts", Artifact{NodeID: n.ID, Name: "a", Type: "text", Content: &c}, nil, 201)
	}
	h.jira.resetRequests()
	var d TicketDetail
	h.mustCall("GET", "/tickets/"+tk.ID+"/detail", nil, &d, 200)
	if len(d.Nodes) != 30 || len(d.Artifacts) != 30 {
		t.Fatalf("detail: %d nodes, %d artifacts", len(d.Nodes), len(d.Artifacts))
	}
	if n := len(h.jira.requestsMatching("POST", `^/rest/api/3/comment/list$`)); n != 1 {
		t.Fatalf("comment/list requests = %d", n)
	}
	if rs := h.jira.requestsMatching("GET", `/comment`); len(rs) != 0 {
		t.Fatalf("per-node comment reads: %d", len(rs))
	}
	if total := h.jira.requestCount(); total > 6 {
		t.Fatalf("GetTicketDetail made %d requests", total)
	}
}

// --- 6. the managed-node check ---

func TestManagedChecksForArtifacts(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	a := h.createNode(tk.ID, GraphNode{Name: "A", Type: "plan"})
	b := h.createNode(tk.ID, GraphNode{Name: "B", Type: "plan"})
	subA := subOf(t, a.ID)
	c := "b's"
	var artB Artifact
	h.mustCall("POST", "/tickets/"+tk.ID+"/artifacts", Artifact{NodeID: b.ID, Name: "b", Type: "text", Content: &c}, &artB, 201)
	_, commentB, _ := parseArtifactID(artB.ID)

	// A plain comment written by a Jira user on the node's sub-task.
	plain := h.jira.seedComment(subA, nil)
	var eb errBody
	if status := h.call("GET", "/artifacts/"+a.ID+"-c"+plain, nil, &eb); status != 404 || eb.Error.Code != "ARTIFACT_NOT_FOUND" {
		t.Fatalf("plain comment = %d %+v", status, eb)
	}
	var listed []Artifact
	h.mustCall("GET", "/nodes/"+a.ID+"/artifacts", nil, &listed, 200)
	if len(listed) != 0 {
		t.Fatalf("ListArtifactsByNode = %+v", listed)
	}
	// B's artifact through A's node ID.
	if status := h.call("GET", "/artifacts/"+a.ID+"-c"+commentB, nil, &eb); status != 404 || eb.Error.Code != "ARTIFACT_NOT_FOUND" {
		t.Fatalf("another node's comment = %d %+v", status, eb)
	}
}

func TestTamperedArtifactCommentIDsAreNotFollowed(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	a := h.createNode(tk.ID, GraphNode{Name: "A", Type: "plan"})
	subA := subOf(t, a.ID)
	c := "mine"
	var own Artifact
	h.mustCall("POST", "/tickets/"+tk.ID+"/artifacts", Artifact{NodeID: a.ID, Name: "own", Type: "text", Content: &c}, &own, 201)

	// A comment on an unmanaged issue, dressed up as A's artifact, whose ID
	// a Jira editor adds to A's artifact_comment_ids.
	outside := h.jira.seedIssue("GOPS", nil, nil)
	foreign := h.jira.seedComment(outside, map[string]string{
		"graphops.artifact": `{"node_id":"` + a.ID + `","name":"stolen","type":"text","has_content":true,"content":"secret"}`,
	})
	np := h.nodePropOf(subA)
	np.ArtifactCommentIDs = append(np.ArtifactCommentIDs, foreign)
	raw, _ := json.Marshal(np)
	h.jira.issue(subA).Properties["graphops.node"] = raw

	var listed []Artifact
	h.mustCall("GET", "/tickets/"+tk.ID+"/artifacts", nil, &listed, 200)
	var d TicketDetail
	h.mustCall("GET", "/tickets/"+tk.ID+"/detail", nil, &d, 200)
	for _, arts := range [][]Artifact{listed, d.Artifacts} {
		if len(arts) != 1 || arts[0].ID != own.ID {
			t.Fatalf("artifacts = %+v, want only %s", arts, own.ID)
		}
	}
}

func TestUnreadableCommentSelfFallsBackToTheSubtask(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	a := h.createNode(tk.ID, GraphNode{Name: "A", Type: "plan"})
	b := h.createNode(tk.ID, GraphNode{Name: "B", Type: "plan"})
	var arts []Artifact
	for _, n := range []GraphNode{a, b} {
		c := "x"
		var art Artifact
		h.mustCall("POST", "/tickets/"+tk.ID+"/artifacts", Artifact{NodeID: n.ID, Name: "x", Type: "text", Content: &c}, &art, 201)
		arts = append(arts, art)
	}
	// A foreign comment listed by A, whose self cannot be read either.
	subA := subOf(t, a.ID)
	outside := h.jira.seedIssue("GOPS", nil, nil)
	foreign := h.jira.seedComment(outside, map[string]string{
		"graphops.artifact": `{"node_id":"` + a.ID + `","name":"stolen","type":"text","has_content":true,"content":"secret"}`,
	})
	np := h.nodePropOf(subA)
	np.ArtifactCommentIDs = append(np.ArtifactCommentIDs, foreign)
	raw, _ := json.Marshal(np)
	h.jira.issue(subA).Properties["graphops.node"] = raw
	h.jira.commentSelf[foreign] = ""
	_, ownA, _ := parseArtifactID(arts[0].ID)
	h.jira.commentSelf[ownA] = "not a URL"

	h.jira.resetRequests()
	var listed []Artifact
	h.mustCall("GET", "/tickets/"+tk.ID+"/artifacts", nil, &listed, 200)
	var got []string
	for _, art := range listed {
		got = append(got, art.ID)
	}
	if !reflect.DeepEqual(got, []string{arts[0].ID, arts[1].ID}) {
		t.Fatalf("artifacts = %v, want %v", got, []string{arts[0].ID, arts[1].ID})
	}
	if rs := h.jira.requestsMatching("GET", `^/rest/api/3/issue/`+subA+`/comment$`); len(rs) != 1 {
		t.Fatalf("A's sub-task was not read the slow way: %+v", rs)
	}
}

func TestOldAndMalformedIDsSendNothingToJira(t *testing.T) {
	h := newHarness(t)
	h.newTicket()
	type call struct{ method, path, code string }
	var calls []call
	for _, id := range []string{"GOPS-1-01", "GOPS-1-nabc", "GOPS-1-n", "GOPS-2-n015", "GOPS-2-n2", "gops-2-n3"} {
		calls = append(calls,
			call{"GET", "/nodes/" + id, "NODE_NOT_FOUND"},
			call{"PATCH", "/nodes/" + id, "NODE_NOT_FOUND"},
		)
	}
	for _, id := range []string{"GOPS-1-c10", "GOPS-1-n15-c", "GOPS-1-n15-cxyz", "GOPS-1-nabc-c10", "GOPS-1-n15-c010"} {
		calls = append(calls, call{"GET", "/artifacts/" + id, "ARTIFACT_NOT_FOUND"})
	}
	for _, c := range calls {
		h.jira.resetRequests()
		var eb errBody
		var body any
		if c.method == "PATCH" {
			body = map[string]any{"status": "DONE"}
		}
		if status := h.call(c.method, c.path, body, &eb); status != 404 || eb.Error.Code != c.code {
			t.Errorf("%s %s = %d %+v, want 404 %s", c.method, c.path, status, eb, c.code)
		}
		if n := h.jira.requestCount(); n != 0 {
			t.Errorf("%s %s sent %d requests to Jira", c.method, c.path, n)
		}
	}
	for _, id := range []string{"GOPS-1-01", "GOPS-1-nabc", "GOPS-1-n"} {
		h.jira.resetRequests()
		h.mustCall("DELETE", "/nodes/"+id, nil, nil, 204)
		var listed []Artifact
		h.mustCall("GET", "/nodes/"+id+"/artifacts", nil, &listed, 200)
		if len(listed) != 0 {
			t.Errorf("ListArtifactsByNode(%s) = %+v", id, listed)
		}
		if n := h.jira.requestCount(); n != 0 {
			t.Errorf("DELETE/list of %s sent %d requests to Jira", id, n)
		}
		text := "x"
		var eb errBody
		if status := h.call("POST", "/tickets/GOPS-2/artifacts", Artifact{NodeID: id, Name: "x", Type: "text", Content: &text}, &eb); status != 404 || eb.Error.Code != "NODE_NOT_FOUND" {
			t.Errorf("CreateArtifact with node %s = %d %+v", id, status, eb)
		}
		if rs := h.jira.changingRequests(); len(rs) != 0 {
			t.Errorf("CreateArtifact with node %s changed Jira: %+v", id, rs)
		}
	}
}

// managedCheckWorld builds the setting of the managed-node check scenarios:
// a node of ticket 1, a node of ticket 2, an unrelated issue, a hand-made
// sub-task of ticket 1, an unregistered project's issue with a sub-task, and
// a detached ticket whose sub-task kept graphops.node.
type managedCheckWorld struct {
	t1, t2, t3                          Ticket
	node1, node2, node3                 GraphNode
	unrelated, handMade, other, otherSb string
}

func newManagedCheckWorld(t *testing.T, h *harness) managedCheckWorld {
	p := h.registerGOPS()
	var w managedCheckWorld
	w.t1 = h.createTicket(p.ID, "1")
	w.t2 = h.createTicket(p.ID, "2")
	w.t3 = h.createTicket(p.ID, "3")
	w.node1 = h.createNode(w.t1.ID, GraphNode{Name: "n1", Type: "plan"})
	w.node2 = h.createNode(w.t2.ID, GraphNode{Name: "n2", Type: "plan"})
	w.node3 = h.createNode(w.t3.ID, GraphNode{Name: "n3", Type: "plan"})
	for _, n := range []GraphNode{w.node1, w.node2, w.node3} {
		c := "x"
		h.mustCall("POST", "/tickets/"+n.TicketID+"/artifacts", Artifact{NodeID: n.ID, Name: "x", Type: "text", Content: &c}, nil, 201)
	}
	w.unrelated = h.jira.seedIssue("GOPS", nil, nil)
	w.handMade = h.jira.seedSubtask(w.t1.ID, nil, nil)
	w.other = h.jira.seedIssue("DEMO", []string{"graphops"}, map[string]string{"graphops.ticket": `{"title":"t","status":"TODO"}`})
	w.otherSb = h.jira.seedSubtask(w.other, nil, map[string]string{"graphops.node": `{"name":"x","type":"plan","status":"TODO"}`})
	h.jira.failWith = func(wr http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/properties/graphops.node") {
			wr.WriteHeader(http.StatusInternalServerError)
			return true
		}
		return false
	}
	h.mustCall("DELETE", "/tickets/"+w.t3.ID, nil, nil, 204)
	h.jira.failWith = nil
	if _, ok := h.jira.issue(subOf(t, w.node3.ID)).Properties["graphops.node"]; !ok {
		t.Fatal("setup: the detached sub-task should have kept graphops.node")
	}
	return w
}

type unmanagedCase struct {
	name, ticket, nodeID, target, createCode string
}

func (w managedCheckWorld) unmanagedCases(t *testing.T) []unmanagedCase {
	id := func(ticket, sub string) string {
		n := ticket + "-n" + sub[strings.LastIndex(sub, "-")+1:]
		return n
	}
	return []unmanagedCase{
		{"another ticket's sub-task", w.t1.ID, id(w.t1.ID, subOf(t, w.node2.ID)), subOf(t, w.node2.ID), "NODE_NOT_FOUND"},
		{"an unrelated issue", w.t1.ID, id(w.t1.ID, w.unrelated), w.unrelated, "NODE_NOT_FOUND"},
		{"the ticket itself", w.t1.ID, id(w.t1.ID, w.t1.ID), w.t1.ID, "NODE_NOT_FOUND"},
		{"a hand-made sub-task", w.t1.ID, id(w.t1.ID, w.handMade), w.handMade, "NODE_NOT_FOUND"},
		{"an unregistered project's sub-task", w.other, id(w.other, w.otherSb), w.otherSb, "TICKET_NOT_FOUND"},
		{"a detached ticket's leftover sub-task", w.t3.ID, w.node3.ID, subOf(t, w.node3.ID), "TICKET_NOT_FOUND"},
	}
}

func TestUnmanagedNodeIDsChangeNothing(t *testing.T) {
	h := newHarness(t)
	w := newManagedCheckWorld(t, h)
	watched := []string{w.t1.ID, w.t2.ID, w.t3.ID, subOf(t, w.node1.ID), subOf(t, w.node2.ID), subOf(t, w.node3.ID), w.unrelated, w.handMade, w.other, w.otherSb}
	snapshots := func() map[string]string {
		out := map[string]string{}
		for _, k := range watched {
			out[k] = h.jira.snapshot(k)
		}
		return out
	}
	for _, tc := range w.unmanagedCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			before := snapshots()

			h.jira.resetRequests()
			h.mustCall("DELETE", "/nodes/"+tc.nodeID, nil, nil, 204)
			if rs := h.jira.changingRequests(); len(rs) != 0 {
				t.Fatalf("DeleteNode changed Jira: %+v", rs)
			}

			h.jira.resetRequests()
			var eb errBody
			if status := h.call("PATCH", "/nodes/"+tc.nodeID, map[string]any{"status": "DONE", "name": "乗っ取り"}, &eb); status != 404 || eb.Error.Code != "NODE_NOT_FOUND" {
				t.Fatalf("UpdateNode = %d %+v", status, eb)
			}
			if rs := h.jira.changingRequests(); len(rs) != 0 {
				t.Fatalf("UpdateNode changed Jira: %+v", rs)
			}
			if gets, _ := transitionRequests(h, tc.target); gets != 0 {
				t.Fatal("UpdateNode read transitions")
			}

			for _, typ := range []string{"text", "html"} {
				h.jira.resetRequests()
				c := "<p>x</p>"
				if status := h.call("POST", "/tickets/"+tc.ticket+"/artifacts", Artifact{NodeID: tc.nodeID, Name: "x", Type: typ, Content: &c}, &eb); status != 404 || eb.Error.Code != tc.createCode {
					t.Fatalf("CreateArtifact(%s) = %d %+v, want 404 %s", typ, status, eb, tc.createCode)
				}
				if rs := h.jira.changingRequests(); len(rs) != 0 {
					t.Fatalf("CreateArtifact changed Jira: %+v", rs)
				}
			}

			h.jira.resetRequests()
			var listed []Artifact
			h.mustCall("GET", "/nodes/"+tc.nodeID+"/artifacts", nil, &listed, 200)
			if len(listed) != 0 {
				t.Fatalf("ListArtifactsByNode = %+v", listed)
			}
			for _, r := range h.jira.allRequests() {
				if strings.Contains(r.Path, "/comment") || strings.Contains(r.Path, "/attachment") {
					t.Fatalf("ListArtifactsByNode read %s %s", r.Method, r.Path)
				}
			}

			if after := snapshots(); !reflect.DeepEqual(before, after) {
				for k := range before {
					if before[k] != after[k] {
						t.Errorf("%s changed:\n before %s\n after  %s", k, before[k], after[k])
					}
				}
			}
		})
	}
}

func TestDeleteNodeChecksBeforeDeleting(t *testing.T) {
	h := newHarness(t)
	w := newManagedCheckWorld(t, h)
	sub := subOf(t, w.node1.ID)
	h.jira.resetRequests()
	h.mustCall("DELETE", "/nodes/"+w.node1.ID, nil, nil, 204)
	var seenTicket, seenSub bool
	for _, r := range h.jira.allRequests() {
		switch {
		case r.Method == "GET" && r.Path == "/rest/api/3/issue/"+w.t1.ID:
			seenTicket = true
		case r.Method == "GET" && r.Path == "/rest/api/3/issue/"+sub:
			seenSub = strings.Contains(r.RawQuery, "parent") && strings.Contains(r.RawQuery, "graphops.node")
		case r.Method == "DELETE" && r.Path == "/rest/api/3/issue/"+sub:
			if !seenTicket || !seenSub {
				t.Fatal("the sub-task was deleted before the ticket and the sub-task were checked")
			}
		}
	}
	if h.jira.issue(sub) != nil {
		t.Fatal("the sub-task was not deleted")
	}
	for _, k := range []string{w.t1.ID, w.handMade, subOf(t, w.node2.ID), w.unrelated} {
		if h.jira.issue(k) == nil {
			t.Fatalf("%s was deleted", k)
		}
	}
}

func TestManagedNodesWorkNormally(t *testing.T) {
	h := newHarness(t)
	w := newManagedCheckWorld(t, h)
	sub := subOf(t, w.node1.ID)
	h.patchNode(w.node1.ID, map[string]any{"status": "DONE"})
	c := "more"
	h.mustCall("POST", "/tickets/"+w.t1.ID+"/artifacts", Artifact{NodeID: w.node1.ID, Name: "m", Type: "text", Content: &c}, nil, 201)
	if n := len(h.jira.issue(sub).Comments); n != 2 {
		t.Fatalf("comments = %d", n)
	}
	var listed []Artifact
	h.mustCall("GET", "/nodes/"+w.node1.ID+"/artifacts", nil, &listed, 200)
	if len(listed) != 2 {
		t.Fatalf("listed = %+v", listed)
	}
	h.mustCall("DELETE", "/nodes/"+w.node1.ID, nil, nil, 204)
	if h.jira.issue(sub) != nil {
		t.Fatal("sub-task not deleted")
	}
}

// --- 7. listing ---

func TestSubtasksAreNotTickets(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	var l Label
	h.mustCall("POST", "/projects/"+p.ID+"/labels", map[string]string{"name": "L", "color": "red"}, &l, 201)
	var tk Ticket
	h.mustCall("POST", "/projects/"+p.ID+"/tickets", map[string]any{"title": "t", "status": "TODO", "labels": []map[string]string{{"id": l.ID}}}, &tk, 201)
	var first GraphNode
	for i := 0; i < 3; i++ {
		n := h.createNode(tk.ID, GraphNode{Name: fmt.Sprint(i), Type: "plan"})
		if i == 0 {
			first = n
		}
	}
	// A Jira user labels a node sub-task graphops by hand.
	sub := h.jira.issue(subOf(t, first.ID))
	sub.Labels = append(sub.Labels, "graphops")

	for _, path := range []string{"/tickets", "/projects/" + p.ID + "/tickets"} {
		var tickets []Ticket
		h.mustCall("GET", path, nil, &tickets, 200)
		if len(tickets) != 1 || tickets[0].ID != tk.ID {
			t.Fatalf("GET %s = %+v", path, tickets)
		}
	}
	var usages []LabelUsage
	h.mustCall("GET", "/projects/"+p.ID+"/labels", nil, &usages, 200)
	if len(usages) != 1 || usages[0].TicketCount != 1 {
		t.Fatalf("label usage = %+v", usages)
	}
}

// --- 8. deleting nodes, detaching tickets ---

func TestDeleteNodeRemovesItsSubtaskAndEdges(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	var nodes []GraphNode
	for _, name := range []string{"A", "B", "C"} {
		n := h.createNode(tk.ID, GraphNode{Name: name, Type: "plan"})
		c := "x"
		h.mustCall("POST", "/tickets/"+tk.ID+"/artifacts", Artifact{NodeID: n.ID, Name: name, Type: "text", Content: &c}, nil, 201)
		nodes = append(nodes, n)
	}
	h.mustCall("POST", "/tickets/"+tk.ID+"/edges", GraphEdge{ID: "ab", FromNodeID: nodes[0].ID, ToNodeID: nodes[1].ID}, nil, 201)
	h.mustCall("POST", "/tickets/"+tk.ID+"/edges", GraphEdge{ID: "bc", FromNodeID: nodes[1].ID, ToNodeID: nodes[2].ID}, nil, 201)
	h.mustCall("POST", "/tickets/"+tk.ID+"/edges", GraphEdge{ID: "ac", FromNodeID: nodes[0].ID, ToNodeID: nodes[2].ID}, nil, 201)

	h.mustCall("DELETE", "/nodes/"+nodes[1].ID, nil, nil, 204)
	if h.jira.issue(subOf(t, nodes[1].ID)) != nil {
		t.Fatal("B's sub-task is still there")
	}
	var edges []GraphEdge
	h.mustCall("GET", "/tickets/"+tk.ID+"/edges", nil, &edges, 200)
	if len(edges) != 1 || edges[0].ID != "ac" {
		t.Fatalf("edges = %+v", edges)
	}
	var d TicketDetail
	h.mustCall("GET", "/tickets/"+tk.ID+"/detail", nil, &d, 200)
	if len(d.Nodes) != 2 || len(d.Artifacts) != 2 {
		t.Fatalf("detail: %d nodes, %d artifacts", len(d.Nodes), len(d.Artifacts))
	}

	// A node that does not exist: nothing changes.
	h.jira.resetRequests()
	h.mustCall("DELETE", "/nodes/"+tk.ID+"-n999", nil, nil, 204)
	if rs := h.jira.changingRequests(); len(rs) != 0 {
		t.Fatalf("deleting a missing node changed Jira: %+v", rs)
	}
}

func TestDeleteNodeWhenTheSubtaskIsAlreadyGone(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	a := h.createNode(tk.ID, GraphNode{Name: "A", Type: "plan"})
	b := h.createNode(tk.ID, GraphNode{Name: "B", Type: "plan"})
	h.mustCall("POST", "/tickets/"+tk.ID+"/edges", GraphEdge{ID: "ab", FromNodeID: a.ID, ToNodeID: b.ID}, nil, 201)
	h.jira.deleteIssue(subOf(t, b.ID)) // deleted in Jira by hand
	h.mustCall("DELETE", "/nodes/"+b.ID, nil, nil, 204)
	var edges []GraphEdge
	h.mustCall("GET", "/tickets/"+tk.ID+"/edges", nil, &edges, 200)
	if len(edges) != 0 {
		t.Fatalf("edges = %+v", edges)
	}
}

func TestFailedSubtaskDeletionCanBeRetried(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	a := h.createNode(tk.ID, GraphNode{Name: "A", Type: "plan"})
	b := h.createNode(tk.ID, GraphNode{Name: "B", Type: "plan"})
	h.mustCall("POST", "/tickets/"+tk.ID+"/edges", GraphEdge{ID: "ab", FromNodeID: a.ID, ToNodeID: b.ID}, nil, 201)
	subB := subOf(t, b.ID)
	h.jira.failWith = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodDelete && r.URL.Path == "/rest/api/3/issue/"+subB {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		}
		return false
	}
	var eb errBody
	if status := h.call("DELETE", "/nodes/"+b.ID, nil, &eb); status/100 != 5 {
		t.Fatalf("DeleteNode = %d %+v", status, eb)
	}
	h.jira.failWith = nil
	h.mustCall("DELETE", "/nodes/"+b.ID, nil, nil, 204)
	var edges []GraphEdge
	h.mustCall("GET", "/tickets/"+tk.ID+"/edges", nil, &edges, 200)
	if h.jira.issue(subB) != nil || len(edges) != 0 {
		t.Fatalf("after retry: sub-task %v, edges %+v", h.jira.issue(subB) != nil, edges)
	}
}

func TestDeleteTicketKeepsSubtasksButRemovesTheirGraphOpsData(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	var nodes []GraphNode
	var arts []Artifact
	for i := 0; i < 3; i++ {
		n := h.createNode(tk.ID, GraphNode{Name: fmt.Sprint(i), Type: "report", Status: "IN PROGRESS"})
		sub := h.jira.issue(subOf(t, n.ID))
		sub.Labels = append(sub.Labels, "team-a")
		html := "<p>x</p>"
		var art Artifact
		h.mustCall("POST", "/tickets/"+tk.ID+"/artifacts", Artifact{NodeID: n.ID, Name: "r", Type: "html", Content: &html}, &art, 201)
		nodes = append(nodes, n)
		arts = append(arts, art)
	}
	h.mustCall("DELETE", "/tickets/"+tk.ID, nil, nil, 204)

	parent := h.jira.issue(tk.ID)
	if parent == nil || containsString(parent.Labels, "graphops") || parent.Properties["graphops.ticket"] != nil || parent.Properties["graphops.edges"] != nil {
		t.Fatalf("ticket issue after detaching = %+v", parent)
	}
	for i, n := range nodes {
		sub := h.jira.issue(subOf(t, n.ID))
		if sub == nil {
			t.Fatalf("sub-task of %s was deleted", n.ID)
		}
		if _, ok := sub.Properties["graphops.node"]; ok || len(statusLabelsOf(sub.Labels)) != 0 || !containsString(sub.Labels, "team-a") {
			t.Fatalf("sub-task after detaching = %+v", sub)
		}
		if len(sub.Comments) != 1 || len(sub.Attachments) != 1 || !h.jira.hasAttachment(sub.Attachments[0]) {
			t.Fatalf("sub-task lost its artifact comment or attachment: %+v", sub)
		}
		var eb errBody
		if status := h.call("GET", "/nodes/"+n.ID, nil, &eb); status != 404 {
			t.Fatalf("GetNode after detaching = %d", status)
		}
		if status := h.call("GET", "/artifacts/"+arts[i].ID, nil, &eb); status != 404 || eb.Error.Code != "ARTIFACT_NOT_FOUND" {
			t.Fatalf("GetArtifact after detaching = %d %+v", status, eb)
		}
	}
}

func TestDetachingSurvivesSubtaskCleanupFailures(t *testing.T) {
	h := newHarness(t)
	tk := h.newTicket()
	var nodes []GraphNode
	for i := 0; i < 3; i++ {
		nodes = append(nodes, h.createNode(tk.ID, GraphNode{Name: fmt.Sprint(i), Type: "plan"}))
	}
	failing := subOf(t, nodes[1].ID)
	h.jira.failWith = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodDelete && r.URL.Path == "/rest/api/3/issue/"+failing+"/properties/graphops.node" {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		}
		return false
	}
	h.clearLogs()
	h.mustCall("DELETE", "/tickets/"+tk.ID, nil, nil, 204)
	h.jira.failWith = nil
	if parent := h.jira.issue(tk.ID); containsString(parent.Labels, "graphops") || parent.Properties["graphops.ticket"] != nil {
		t.Fatalf("ticket still attached: %+v", parent)
	}
	if lines := h.logsContaining(failing, "graphops.node"); len(lines) != 1 {
		t.Fatalf("log = %q", h.logLines())
	}
	if _, ok := h.jira.issue(failing).Properties["graphops.node"]; !ok {
		t.Fatal("setup: the failing sub-task should have kept graphops.node")
	}
	var eb errBody
	if status := h.call("GET", "/nodes/"+nodes[1].ID, nil, &eb); status != 404 || eb.Error.Code != "NODE_NOT_FOUND" {
		t.Fatalf("GetNode of the leftover = %d %+v", status, eb)
	}
}

func TestDeleteProjectDetachesEveryTicketsSubtasks(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	var nodes []GraphNode
	for i := 0; i < 2; i++ {
		tk := h.createTicket(p.ID, fmt.Sprint(i))
		nodes = append(nodes, h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan", Status: "DONE"}))
	}
	h.mustCall("DELETE", "/projects/"+p.ID, nil, nil, 204)
	for _, n := range nodes {
		parent := h.jira.issue(n.TicketID)
		sub := h.jira.issue(subOf(t, n.ID))
		if parent == nil || containsString(parent.Labels, "graphops") || sub == nil {
			t.Fatalf("after DeleteProject: parent %+v, sub-task %+v", parent, sub)
		}
		if _, ok := sub.Properties["graphops.node"]; ok || len(statusLabelsOf(sub.Labels)) != 0 {
			t.Fatalf("sub-task kept its GraphOps data: %+v", sub)
		}
	}
}

// Code review, point 1: the best-effort sub-task clean-up must not use up
// the time the detaching of later tickets needs. Every ticket is detached
// and the project unregistered before any sub-task is cleaned up, and the
// clean-up has its own, shorter time limit.
func TestDeleteProjectDetachesAllTicketsBeforeCleaningUp(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	var tickets []Ticket
	var nodes []GraphNode
	for i := 0; i < 3; i++ {
		tk := h.createTicket(p.ID, fmt.Sprint(i))
		tickets = append(tickets, tk)
		for j := 0; j < 2; j++ {
			nodes = append(nodes, h.createNode(tk.ID, GraphNode{Name: fmt.Sprint(j), Type: "plan", Status: "IN PROGRESS"}))
		}
	}
	const deadline = 3 * time.Second
	h.useDeadline(deadline, nil)
	h.store.detachCleanupTimeout = 300 * time.Millisecond
	// Every sub-task clean-up hangs until the plugin gives up on it.
	h.jira.setFailWith(hang(func(r *http.Request) bool {
		return r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/properties/graphops.node")
	}))
	h.jira.resetRequests()
	h.clearLogs()
	start := time.Now()
	h.mustCall("DELETE", "/projects/"+p.ID, nil, nil, 204)
	if took := time.Since(start); took >= deadline {
		t.Fatalf("DeleteProject took %s, the whole request deadline", took)
	}
	h.jira.setFailWith(nil)

	for _, tk := range tickets {
		parent := h.jira.issue(tk.ID)
		if parent == nil || containsString(parent.Labels, "graphops") || parent.Properties["graphops.ticket"] != nil || parent.Properties["graphops.edges"] != nil {
			t.Fatalf("ticket %s still attached: %+v", tk.ID, parent)
		}
	}
	var eb errBody
	if status := h.call("GET", "/projects/"+p.ID, nil, &eb); status != 404 {
		t.Fatalf("GetProject after DeleteProject = %d", status)
	}
	// The detaching of every ticket came before the first clean-up request.
	lastDetach, firstCleanup := -1, -1
	for i, r := range h.jira.allRequests() {
		switch {
		case r.Method == http.MethodDelete && strings.HasSuffix(r.Path, "/properties/graphops.edges"):
			lastDetach = i
		case r.Method == http.MethodDelete && strings.HasSuffix(r.Path, "/properties/graphops.node") && firstCleanup < 0:
			firstCleanup = i
		}
	}
	if lastDetach < 0 || firstCleanup < 0 || firstCleanup < lastDetach {
		t.Fatalf("last ticket detached at request %d, first sub-task cleaned up at %d", lastDetach, firstCleanup)
	}
	if len(h.logsContaining("graphops.node")) == 0 {
		t.Fatalf("the unfinished clean-up was not logged: %q", h.logLines())
	}
	// The leftovers cannot be read as nodes.
	for _, n := range nodes {
		if status := h.call("GET", "/nodes/"+n.ID, nil, &eb); status != 404 || eb.Error.Code != "NODE_NOT_FOUND" {
			t.Fatalf("GetNode of %s after DeleteProject = %d %+v", n.ID, status, eb)
		}
	}
}

// Tickets left for after the clean-up time ran out are logged by key.
func TestDetachCleanupStopsWhenOutOfTime(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	var tickets []Ticket
	for i := 0; i < 3; i++ {
		tk := h.createTicket(p.ID, fmt.Sprint(i))
		h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan"})
		tickets = append(tickets, tk)
	}
	h.store.detachCleanupTimeout = 200 * time.Millisecond
	h.jira.setFailWith(hang(func(r *http.Request) bool {
		return r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/issue/bulkfetch"
	}))
	h.clearLogs()
	h.mustCall("DELETE", "/projects/"+p.ID, nil, nil, 204)
	h.jira.setFailWith(nil)
	if bulk := h.jira.requestsMatching("POST", `^/rest/api/3/issue/bulkfetch$`); len(bulk) != 1 {
		t.Fatalf("%d bulkfetch requests after the time ran out, want 1", len(bulk))
	}
	// The first ticket's sub-tasks could not be read; the other two were
	// not tried, and one line names them.
	lines := h.logsContaining("not cleaned up")
	if len(lines) != 1 {
		t.Fatalf("log = %q", h.logLines())
	}
	keys := strings.Split(strings.TrimPrefix(strings.SplitN(lines[0], " detached", 2)[0], "tickets "), ", ")
	named := 0
	for _, tk := range tickets {
		if containsString(keys, tk.ID) {
			named++
		}
	}
	if len(keys) != 2 || named != 2 {
		t.Fatalf("log line %q names %q, want 2 of the tickets", lines[0], keys)
	}
}

// --- misc ---

func TestStatusLabelAndSummaryHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"TODO": "graphops-status-todo", "IN PROGRESS": "graphops-status-in-progress",
		"AWAITING FIX": "graphops-status-awaiting-fix", "": "graphops-status-todo", " Weird  Status ": "graphops-status-weird-status",
	} {
		if got := statusLabel(in); got != want {
			t.Errorf("statusLabel(%q) = %q, want %q", in, got, want)
		}
	}
	if got := nodeSummary("line one\nline two", "plan"); got != "line one [plan]" {
		t.Errorf("nodeSummary = %q", got)
	}
	ops := statusLabelOps([]string{"team-a", "graphops-status-todo", "graphops-status-foo"}, "graphops-status-done")
	want := []map[string]string{{"remove": "graphops-status-todo"}, {"remove": "graphops-status-foo"}, {"add": "graphops-status-done"}}
	if !reflect.DeepEqual(ops, want) {
		t.Errorf("statusLabelOps = %v", ops)
	}
	if ops := statusLabelOps([]string{"graphops-status-done", "x"}, "graphops-status-done"); ops != nil {
		t.Errorf("statusLabelOps with nothing to do = %v", ops)
	}
}

// The transition timeout must leave room in the request deadline for the
// writes before it.
func TestTransitionTimeoutFitsTheRequestDeadline(t *testing.T) {
	if defaultTransitionTimeout >= requestDeadline/2 {
		t.Fatalf("defaultTransitionTimeout %s is too close to requestDeadline %s", defaultTransitionTimeout, requestDeadline)
	}
	// A request whose context is already done does not try to transition.
	h := newHarness(t)
	tk := h.newTicket()
	n := h.createNode(tk.ID, GraphNode{Name: "a", Type: "plan"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.jira.resetRequests()
	h.store.moveToStatus(ctx, n.ID, subOf(t, n.ID), "To Do", "Done")
	if n := h.jira.requestCount(); n != 0 {
		t.Fatalf("%d requests sent with a done context", n)
	}
	if len(h.logsContaining(n.ID)) != 1 {
		t.Fatalf("log = %q", h.logLines())
	}
}
