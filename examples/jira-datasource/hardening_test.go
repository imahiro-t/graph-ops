package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// graphEngineTimeout mirrors graph-engine's per-request timeout for the
// HTTP data source (packages/core-go/internal/store/http.go and
// docs/http-datasource/openapi.yaml).
const graphEngineTimeout = 30 * time.Second

// useDeadline replaces the harness's plugin server with one whose
// per-request deadline is d. When done is non-nil, it is closed each time a
// request handler has returned.
func (h *harness) useDeadline(d time.Duration, done chan<- struct{}) {
	h.t.Helper()
	inner := newHandlerWithDeadline(h.store, pluginToken, nil, d)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r)
		if done != nil {
			done <- struct{}{}
		}
	}))
	h.t.Cleanup(srv.Close)
	h.srv = srv
}

// --- timeouts ---

func TestRequestDeadlineIsShorterThanGraphEngineTimeout(t *testing.T) {
	if requestDeadline >= graphEngineTimeout-2*time.Second {
		t.Fatalf("requestDeadline %s leaves no margin under graph-engine's %s", requestDeadline, graphEngineTimeout)
	}
	if jiraAttemptTimeout > requestDeadline {
		t.Fatalf("one Jira attempt (%s) may outlast the request deadline (%s)", jiraAttemptTimeout, requestDeadline)
	}
	srv := newServer("127.0.0.1:0", http.NotFoundHandler())
	if srv.ReadHeaderTimeout == 0 || srv.ReadTimeout == 0 || srv.WriteTimeout == 0 || srv.IdleTimeout == 0 {
		t.Fatalf("server timeouts not all set: %+v", srv)
	}
	if srv.WriteTimeout <= requestDeadline {
		t.Fatalf("WriteTimeout %s would cut off answers of requests that use the whole deadline (%s)", srv.WriteTimeout, requestDeadline)
	}
}

// A Jira that keeps answering 429 must not keep the plugin busy past its
// deadline: the request fails before graph-engine's timeout, after only the
// retries that fit.
func TestPersistentRateLimitFailsWithinTheDeadline(t *testing.T) {
	h := newHarness(t)
	h.registerGOPS()
	h.store.jira.sleep = sleepContext // real waits
	const deadline = 1500 * time.Millisecond
	h.useDeadline(deadline, nil)

	var mu sync.Mutex
	limited := 0
	h.jira.failWith = func(w http.ResponseWriter, r *http.Request) bool {
		mu.Lock()
		limited++
		mu.Unlock()
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		return true
	}
	start := time.Now()
	var eb errBody
	status := h.call("GET", "/projects", nil, &eb)
	took := time.Since(start)
	if status/100 != 5 || eb.Error.Code != "INTERNAL_ERROR" {
		t.Fatalf("always-429 = %d %+v", status, eb)
	}
	if took > deadline+500*time.Millisecond {
		t.Fatalf("always-429 took %s, past the %s deadline", took, deadline)
	}
	mu.Lock()
	defer mu.Unlock()
	// 1 s waits in a 1.5 s budget: the first attempt and one retry.
	if limited != 2 {
		t.Fatalf("Jira was called %d times, want 2 (one retry fits in the deadline)", limited)
	}
}

// A Retry-After longer than what is left of the deadline is not waited for.
func TestRetryAfterPastTheDeadlineIsNotWaitedFor(t *testing.T) {
	h := newHarness(t)
	h.registerGOPS()
	waited := false
	h.store.jira.sleep = func(ctx context.Context, d time.Duration) error { waited = true; return sleepContext(ctx, d) }
	calls := 0
	h.jira.failWith = func(w http.ResponseWriter, r *http.Request) bool {
		calls++
		w.Header().Set("Retry-After", "30") // longer than requestDeadline
		w.WriteHeader(http.StatusTooManyRequests)
		return true
	}
	start := time.Now()
	var eb errBody
	if status := h.call("GET", "/projects", nil, &eb); status/100 != 5 {
		t.Fatalf("status = %d %+v", status, eb)
	}
	if took := time.Since(start); took > 2*time.Second || waited || calls != 1 {
		t.Fatalf("took %s, waited %v, calls %d; want an immediate failure without waiting", took, waited, calls)
	}
	if !strings.Contains(eb.Error.Message, "429") {
		t.Fatalf("error does not say Jira rate-limited the request: %q", eb.Error.Message)
	}
}

// When graph-engine gives up on a request (disconnects), the plugin must not
// go on to write to Jira.
func TestDisconnectedClientStopsJiraWrites(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	tk := h.createTicket(p.ID, "t")
	done := make(chan struct{}, 1)
	h.useDeadline(requestDeadline, done)
	createsBefore := len(h.jira.requestsMatching("POST", `^/rest/api/3/issue$`))

	reading := make(chan struct{})
	var once sync.Once
	h.jira.failWith = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issue/"+tk.ID {
			return false
		}
		once.Do(func() { close(reading) })
		// Hold the read (CreateNode's read-modify-write) until the plugin
		// abandons it; if it never does, let it through so the write that
		// must not happen does happen and the test fails.
		select {
		case <-r.Context().Done():
			return true
		case <-time.After(5 * time.Second):
			return false
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		req, _ := http.NewRequestWithContext(ctx, "POST", h.srv.URL+"/tickets/"+tk.ID+"/nodes", strings.NewReader(`{"name":"a","type":"plan"}`))
		req.Header.Set("Authorization", "Bearer "+pluginToken)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-reading:
	case <-time.After(5 * time.Second):
		t.Fatal("the plugin never read the ticket")
	}
	cancel() // graph-engine times out and disconnects
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the plugin kept working on an abandoned request")
	}
	if creates := h.jira.requestsMatching("POST", `^/rest/api/3/issue$`); len(creates) != createsBefore {
		t.Fatalf("the plugin created the node sub-task after the client disconnected: %d issue creations, want %d", len(creates), createsBefore)
	}
}

// --- artifact reads are limited to managed tickets ---

func TestArtifactReadsOnlyTouchManagedTickets(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	ticketProp := `{"title":"t","status":"TODO","priority":"MEDIUM","label_ids":[]}`

	type target struct{ nodeID, commentID string }
	// plant makes, next to parent, a sub-task that looks like a node and
	// has an artifact-looking comment pointing at an attachment.
	plant := func(parent string) target {
		sub := h.jira.seedSubtask(parent, nil, map[string]string{"graphops.node": `{"name":"x","type":"report","status":"TODO"}`})
		nodeID, _ := nodeIDOf(parent, sub)
		att := h.jira.seedAttachment(sub, []byte("secret attachment"))
		cm := h.jira.seedComment(sub, map[string]string{
			"graphops.artifact": `{"node_id":"` + nodeID + `","name":"x","type":"html","has_content":true,"attachment_id":"` + att + `","attachment_encoding":"raw"}`,
		})
		return target{nodeID, cm}
	}

	// A detached ticket: created and given an artifact through GraphOps,
	// then deleted (the Jira issues and the comments stay). Its sub-task
	// cleanup is made to fail, so the sub-task keeps graphops.node.
	tk := h.createTicket(p.ID, "detached")
	var n GraphNode
	h.mustCall("POST", "/tickets/"+tk.ID+"/nodes", GraphNode{Name: "a", Type: "report"}, &n, 201)
	html := "<p>report</p>"
	var art Artifact
	h.mustCall("POST", "/tickets/"+tk.ID+"/artifacts", Artifact{NodeID: n.ID, Name: "r", Type: "html", Content: &html}, &art, 201)
	h.jira.failWith = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/properties/graphops.node") {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		}
		return false
	}
	h.mustCall("DELETE", "/tickets/"+tk.ID, nil, nil, 204)
	h.jira.failWith = nil
	nodeID, detachedComment, _ := parseArtifactID(art.ID)

	otherTicket := h.createTicket(p.ID, "other")
	var otherNode GraphNode
	h.mustCall("POST", "/tickets/"+otherTicket.ID+"/nodes", GraphNode{Name: "o", Type: "report"}, &otherNode, 201)
	_, otherSub, _ := parseNodeID(otherNode.ID)
	otherComment := h.jira.seedComment(otherSub, map[string]string{
		"graphops.artifact": `{"node_id":"` + otherNode.ID + `","name":"x","type":"text","has_content":true,"content":"other"}`,
	})
	managed := h.createTicket(p.ID, "managed")
	crossID, _ := nodeIDOf(managed.ID, otherSub) // another ticket's sub-task under this ticket's key

	cases := map[string]target{
		// DEMO exists in Jira but is not registered with the plugin.
		"unregistered project": plant(h.jira.seedIssue("DEMO", []string{"graphops"}, map[string]string{"graphops.ticket": ticketProp})),
		// In a registered project, but never created through GraphOps.
		"unmanaged issue": plant(h.jira.seedIssue("GOPS", nil, nil)),
		"detached ticket": {nodeID, detachedComment},
		// A managed ticket's key with the number of another ticket's sub-task.
		"another ticket's sub-task": {crossID, otherComment},
		// A sub-task of a managed ticket, made by hand (no graphops.node).
		"sub-task without graphops.node": func() target {
			sub := h.jira.seedSubtask(managed.ID, nil, nil)
			id, _ := nodeIDOf(managed.ID, sub)
			cm := h.jira.seedComment(sub, map[string]string{
				"graphops.artifact": `{"node_id":"` + id + `","name":"x","type":"text","has_content":true,"content":"x"}`,
			})
			return target{id, cm}
		}(),
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h.jira.resetRequests()
			var eb errBody
			if status := h.call("GET", "/artifacts/"+tc.nodeID+"-c"+tc.commentID, nil, &eb); status != 404 || eb.Error.Code != "ARTIFACT_NOT_FOUND" {
				t.Fatalf("GetArtifact = %d %+v, want 404 ARTIFACT_NOT_FOUND", status, eb)
			}
			if status := h.call("GET", "/nodes/"+tc.nodeID, nil, &eb); status != 404 || eb.Error.Code != "NODE_NOT_FOUND" {
				t.Fatalf("GetNode = %d %+v, want 404 NODE_NOT_FOUND", status, eb)
			}
			var listed []Artifact
			h.mustCall("GET", "/nodes/"+tc.nodeID+"/artifacts", nil, &listed, 200)
			if len(listed) != 0 {
				t.Fatalf("ListArtifactsByNode = %+v, want []", listed)
			}
			// The ticket-wide listing of the ticket the node ID names.
			ticketKey, _, _ := parseNodeID(tc.nodeID)
			listed = nil
			h.mustCall("GET", "/tickets/"+ticketKey+"/artifacts", nil, &listed, 200)
			if len(listed) != 0 {
				t.Fatalf("ListArtifactsByTicket(%s) = %+v, want []", ticketKey, listed)
			}
			if rs := h.jira.requestsMatching("GET", `/comment`); len(rs) != 0 {
				t.Fatalf("comments were read: %+v", rs)
			}
			if rs := h.jira.requestsMatching("POST", `^/rest/api/3/comment/list$`); len(rs) != 0 {
				t.Fatalf("comments were read: %+v", rs)
			}
			if rs := h.jira.requestsMatching("GET", `^/rest/api/3/attachment/`); len(rs) != 0 {
				t.Fatalf("an attachment was fetched: %+v", rs)
			}
		})
	}
}

// The ticket-wide artifact listing (GET /tickets/{id}/artifacts) of a
// ticket detached normally, and of issues GraphOps does not manage, is an
// empty list, and no comment is read for it.
func TestTicketArtifactListingOnlyTouchesManagedTickets(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	ticketProp := `{"title":"t","status":"TODO","priority":"MEDIUM","label_ids":[]}`

	detached := h.createTicket(p.ID, "detached")
	var n GraphNode
	h.mustCall("POST", "/tickets/"+detached.ID+"/nodes", GraphNode{Name: "a", Type: "plan"}, &n, 201)
	text := "findings"
	h.mustCall("POST", "/tickets/"+detached.ID+"/artifacts", Artifact{NodeID: n.ID, Name: "notes", Type: "text", Content: &text}, nil, 201)
	var before []Artifact
	h.mustCall("GET", "/tickets/"+detached.ID+"/artifacts", nil, &before, 200)
	if len(before) != 1 {
		t.Fatalf("setup: listing before detaching = %+v", before)
	}
	h.mustCall("DELETE", "/tickets/"+detached.ID, nil, nil, 204)

	// seeded makes an issue with a sub-task that looks like a node, holding
	// an artifact-looking comment.
	seeded := func(parent string) string {
		sub := h.jira.seedSubtask(parent, nil, map[string]string{"graphops.node": `{"name":"x","type":"plan","status":"TODO"}`})
		nodeID, _ := nodeIDOf(parent, sub)
		h.jira.seedComment(sub, map[string]string{
			"graphops.artifact": `{"node_id":"` + nodeID + `","name":"x","type":"text","has_content":true,"content":"secret"}`,
		})
		return parent
	}
	for name, ticketID := range map[string]string{
		"detached ticket":      detached.ID,
		"unmanaged issue":      seeded(h.jira.seedIssue("GOPS", nil, nil)),
		"unregistered project": seeded(h.jira.seedIssue("DEMO", []string{"graphops"}, map[string]string{"graphops.ticket": ticketProp})),
	} {
		t.Run(name, func(t *testing.T) {
			h.jira.resetRequests()
			var listed []Artifact
			status := h.call("GET", "/tickets/"+ticketID+"/artifacts", nil, &listed)
			if status != 200 || listed == nil || len(listed) != 0 {
				t.Fatalf("GET /tickets/%s/artifacts = %d %+v, want 200 []", ticketID, status, listed)
			}
			if rs := h.jira.requestsMatching("GET", `/comment`); len(rs) != 0 {
				t.Fatalf("comments were read: %+v", rs)
			}
			if rs := h.jira.requestsMatching("POST", `^/rest/api/3/comment/list$`); len(rs) != 0 {
				t.Fatalf("comments were read: %+v", rs)
			}
		})
	}
}

// --- listing with a missing metadata issue ---

func TestListTicketsSkipsProjectsWhoseMetadataIssueIsGone(t *testing.T) {
	h := newHarness(t)
	gops := h.registerGOPS()
	var demo Project
	h.mustCall("POST", "/projects", map[string]string{"name": "Demo", "prefix": "DEMO"}, &demo, 201)
	h.createTicket(gops.ID, "in GOPS")
	kept := h.createTicket(demo.ID, "in DEMO")
	h.jira.deleteIssue("GOPS-1") // GOPS's metadata issue, deleted by hand in Jira

	var tickets []Ticket
	h.mustCall("GET", "/tickets", nil, &tickets, 200)
	if len(tickets) != 1 || tickets[0].ID != kept.ID {
		t.Fatalf("ListTickets = %+v, want only %s", tickets, kept.ID)
	}
	var projects []Project
	h.mustCall("GET", "/projects", nil, &projects, 200)
	if len(projects) != 1 || projects[0].ID != demo.ID {
		t.Fatalf("ListProjects = %+v", projects)
	}
	h.mustCall("GET", "/projects/"+gops.ID+"/tickets", nil, &tickets, 200)
	if len(tickets) != 0 {
		t.Fatalf("ListTicketsByProject of the broken project = %+v, want []", tickets)
	}
}

// --- no orphaned attachments ---

func TestCreateArtifactRemovesTheAttachmentWhenTheCommentFails(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	tk := h.createTicket(p.ID, "t")
	var n GraphNode
	h.mustCall("POST", "/tickets/"+tk.ID+"/nodes", GraphNode{Name: "a", Type: "report"}, &n, 201)
	_, sub, _ := parseNodeID(n.ID)
	h.jira.failWith = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comment") {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		}
		return false
	}
	html := "<p>report</p>"
	var eb errBody
	if status := h.call("POST", "/tickets/"+tk.ID+"/artifacts", Artifact{NodeID: n.ID, Name: "r", Type: "html", Content: &html}, &eb); status/100 != 5 {
		t.Fatalf("CreateArtifact with a failing comment = %d %+v", status, eb)
	}
	issue := h.jira.issue(sub)
	if len(issue.Attachments) != 1 {
		t.Fatalf("expected one uploaded attachment on the sub-task, got %v", issue.Attachments)
	}
	if h.jira.hasAttachment(issue.Attachments[0]) {
		t.Fatal("the attachment of the failed artifact was left behind")
	}
	if dels := h.jira.requestsMatching("DELETE", `^/rest/api/3/attachment/`+issue.Attachments[0]+`$`); len(dels) != 1 {
		t.Fatalf("attachment deletions = %+v", dels)
	}
	if parent := h.jira.issue(tk.ID); len(parent.Attachments) != 0 || len(parent.Comments) != 0 {
		t.Fatalf("the ticket issue got attachments %v / comments %d", parent.Attachments, len(parent.Comments))
	}

}
