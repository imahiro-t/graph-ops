package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const pluginToken = "expected"

type harness struct {
	t         *testing.T
	jira      *fakeJira
	store     *Store
	srv       *httptest.Server
	statePath string

	logMu sync.Mutex
	logs  []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	fj := newFakeJira(t)
	fj.addProject("GOPS", "GraphOps sample")
	fj.addProject("DEMO", "Demo")
	fj.addProject("TOOLONG", "Key longer than 5")
	h := &harness{t: t, jira: fj, statePath: filepath.Join(t.TempDir(), "state.json")}
	h.store = h.newStore()
	h.srv = httptest.NewServer(newHandler(h.store, pluginToken, nil))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *harness) newStore() *Store {
	c := newJiraClient(h.jira.srv.URL, h.jira.email, h.jira.token)
	c.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	s := newStore(c, h.statePath, storeOptions{
		IssueType: "Task", SubtaskIssueType: "Subtask", InProgressStatus: "In Progress", DoneStatus: "Done",
	})
	s.logf = func(format string, args ...any) {
		h.logMu.Lock()
		defer h.logMu.Unlock()
		h.logs = append(h.logs, fmt.Sprintf(format, args...))
	}
	return s
}

// logLines returns what the store has logged so far.
func (h *harness) logLines() []string {
	h.logMu.Lock()
	defer h.logMu.Unlock()
	return append([]string(nil), h.logs...)
}

func (h *harness) clearLogs() {
	h.logMu.Lock()
	defer h.logMu.Unlock()
	h.logs = nil
}

// call sends one protocol request to the plugin and decodes a JSON answer
// into out (when non-nil), returning the status.
func (h *harness) call(method, path string, body any, out any) int {
	h.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, rd)
	req.Header.Set("Authorization", "Bearer "+pluginToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw := new(bytes.Buffer)
	_, _ = raw.ReadFrom(resp.Body)
	if out != nil && raw.Len() > 0 {
		if err := json.Unmarshal(raw.Bytes(), out); err != nil {
			h.t.Fatalf("%s %s: decoding %q: %v", method, path, raw.String(), err)
		}
	}
	return resp.StatusCode
}

func (h *harness) mustCall(method, path string, body any, out any, wantStatus int) {
	h.t.Helper()
	var raw json.RawMessage
	status := h.call(method, path, body, &raw)
	if status != wantStatus {
		h.t.Fatalf("%s %s = %d (%s), want %d", method, path, status, raw, wantStatus)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			h.t.Fatalf("decoding %s: %v", raw, err)
		}
	}
}

func (h *harness) registerGOPS() Project {
	h.t.Helper()
	var p Project
	h.mustCall("POST", "/projects", map[string]string{"name": "GraphOps", "prefix": "GOPS"}, &p, 201)
	return p
}

func (h *harness) createTicket(projectID, title string) Ticket {
	h.t.Helper()
	var tk Ticket
	h.mustCall("POST", "/projects/"+projectID+"/tickets", Ticket{Title: title, Description: "desc", Status: "TODO"}, &tk, 201)
	return tk
}

type errBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// --- configuration ---

func TestLoadConfig_RequiresEachEnvVar(t *testing.T) {
	full := map[string]string{
		"JIRA_BASE_URL": "https://example.atlassian.net", "JIRA_EMAIL": "a@example.invalid",
		"JIRA_API_TOKEN": "x", "GRAPHOPS_DATASOURCE_TOKEN": "y",
	}
	for _, missing := range []string{"JIRA_BASE_URL", "JIRA_EMAIL", "JIRA_API_TOKEN", "GRAPHOPS_DATASOURCE_TOKEN"} {
		t.Run(missing, func(t *testing.T) {
			env := map[string]string{}
			for k, v := range full {
				if k != missing {
					env[k] = v
				}
			}
			_, err := loadConfig(lookupIn(env))
			if err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("err = %v, want one naming %s", err, missing)
			}
		})
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	env := map[string]string{
		"JIRA_BASE_URL": "https://example.atlassian.net", "JIRA_EMAIL": "a@example.invalid",
		"JIRA_API_TOKEN": "x", "GRAPHOPS_DATASOURCE_TOKEN": "y",
	}
	cfg, err := loadConfig(lookupIn(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "127.0.0.1:8787" || cfg.IssueType != "Task" || cfg.StateFile != "jira-datasource-state.json" {
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.SubtaskIssueType != "Subtask" || cfg.InProgressStatus != "In Progress" || cfg.DoneStatus != "Done" {
		t.Fatalf("node sub-task defaults = %+v", cfg)
	}
}

// lookupIn is an os.LookupEnv over a map.
func lookupIn(env map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := env[k]; return v, ok }
}

// Unset status names mean the defaults; names set to "" turn the workflow
// move off; other names are used as given.
func TestLoadConfig_NodeSubtaskSettings(t *testing.T) {
	base := map[string]string{
		"JIRA_BASE_URL": "https://example.atlassian.net", "JIRA_EMAIL": "a@example.invalid",
		"JIRA_API_TOKEN": "x", "GRAPHOPS_DATASOURCE_TOKEN": "y",
	}
	with := func(extra map[string]string) config {
		env := map[string]string{}
		for k, v := range base {
			env[k] = v
		}
		for k, v := range extra {
			env[k] = v
		}
		cfg, err := loadConfig(lookupIn(env))
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	cfg := with(map[string]string{"JIRA_SUBTASK_ISSUE_TYPE": "Sub-task", "JIRA_NODE_IN_PROGRESS_STATUS": "進行中", "JIRA_NODE_DONE_STATUS": " 完了 "})
	if cfg.SubtaskIssueType != "Sub-task" || cfg.InProgressStatus != "進行中" || cfg.DoneStatus != "完了" {
		t.Fatalf("configured = %+v", cfg)
	}
	cfg = with(map[string]string{"JIRA_NODE_IN_PROGRESS_STATUS": "", "JIRA_NODE_DONE_STATUS": ""})
	if cfg.InProgressStatus != "" || cfg.DoneStatus != "" {
		t.Fatalf("set-but-empty status names must turn the moves off: %+v", cfg)
	}
	cfg = with(map[string]string{"JIRA_SUBTASK_ISSUE_TYPE": ""})
	if cfg.SubtaskIssueType != "Subtask" {
		t.Fatalf("an empty sub-task type must fall back to the default: %+v", cfg)
	}
	if opts := cfg.storeOptions(); opts.SubtaskIssueType != "Subtask" || opts.DoneStatus != "Done" || opts.IssueType != "Task" {
		t.Fatalf("storeOptions = %+v", opts)
	}
}

func TestLoadConfig_RejectsPlaintextRemoteJira(t *testing.T) {
	env := map[string]string{
		"JIRA_BASE_URL": "http://example.atlassian.net", "JIRA_EMAIL": "a@example.invalid",
		"JIRA_API_TOKEN": "x", "GRAPHOPS_DATASOURCE_TOKEN": "y",
	}
	if _, err := loadConfig(lookupIn(env)); err == nil {
		t.Fatal("expected an error for a plaintext non-loopback JIRA_BASE_URL")
	}
}

// --- authentication ---

func TestBearerTokenIsRequired(t *testing.T) {
	h := newHarness(t)
	for _, auth := range []string{"", "Bearer wrong", "Basic " + base64.StdEncoding.EncodeToString([]byte("a:b"))} {
		req, _ := http.NewRequest("GET", h.srv.URL+"/projects", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Authorization %q: status %d, want 401", auth, resp.StatusCode)
		}
	}
	if n := h.jira.requestCount(); n != 0 {
		t.Fatalf("Jira received %d requests for unauthenticated calls", n)
	}
}

// --- conformance: all 32 endpoints ---

func TestConformance_All32Endpoints(t *testing.T) {
	h := newHarness(t)

	var proto map[string]string
	h.mustCall("GET", "/protocol", nil, &proto, 200)
	if proto["protocol"] != "graph-ops-datasource" || !strings.HasPrefix(proto["version"], "1.") {
		t.Fatalf("GET /protocol = %v", proto)
	}

	seen := map[string]bool{}
	step := func(op string) { seen[op] = true }

	h.mustCall("POST", "/init", map[string]any{}, nil, 204)
	step("init")

	var proj Project
	h.mustCall("POST", "/projects", map[string]string{"name": "GraphOps", "prefix": "GOPS"}, &proj, 201)
	step("createProject")
	if proj.ID == "" || proj.Prefix != "GOPS" || proj.CreatedAt == "" {
		t.Fatalf("createProject = %+v", proj)
	}
	h.mustCall("GET", "/projects/"+proj.ID, nil, &proj, 200)
	step("getProject")
	var projects []Project
	h.mustCall("GET", "/projects", nil, &projects, 200)
	step("listProjects")
	if len(projects) != 1 {
		t.Fatalf("listProjects = %+v", projects)
	}
	h.mustCall("PATCH", "/projects/"+proj.ID, map[string]string{"name": "Renamed"}, &proj, 200)
	step("updateProject")
	if proj.Name != "Renamed" {
		t.Fatalf("updateProject = %+v", proj)
	}

	var label Label
	h.mustCall("POST", "/projects/"+proj.ID+"/labels", map[string]string{"name": "bug", "color": "red"}, &label, 201)
	step("createLabel")
	h.mustCall("GET", "/labels/"+label.ID, nil, &label, 200)
	step("getLabel")
	h.mustCall("PATCH", "/labels/"+label.ID, map[string]string{"color": "blue"}, &label, 200)
	step("updateLabel")
	if label.Color != "blue" {
		t.Fatalf("updateLabel = %+v", label)
	}

	var tk Ticket
	h.mustCall("POST", "/projects/"+proj.ID+"/tickets", map[string]any{
		"title": "T", "description": "d", "status": "TODO", "priority": "HIGH", "labels": []map[string]string{{"id": label.ID}},
	}, &tk, 201)
	step("createTicket")
	if tk.ID == "" || tk.ProjectID != proj.ID || tk.Priority != "HIGH" || len(tk.Labels) != 1 {
		t.Fatalf("createTicket = %+v", tk)
	}
	h.mustCall("GET", "/tickets/"+tk.ID, nil, &tk, 200)
	step("getTicket")
	var tickets []Ticket
	h.mustCall("GET", "/tickets", nil, &tickets, 200)
	step("listTickets")
	h.mustCall("GET", "/projects/"+proj.ID+"/tickets", nil, &tickets, 200)
	step("listTicketsByProject")
	if len(tickets) != 1 || tickets[0].Labels == nil {
		t.Fatalf("listTicketsByProject = %+v", tickets)
	}
	h.mustCall("PATCH", "/tickets/"+tk.ID, map[string]any{"status": "REFINED", "assignee": "alice"}, &tk, 200)
	step("updateTicket")
	if tk.Status != "REFINED" || tk.Assignee == nil || *tk.Assignee != "alice" {
		t.Fatalf("updateTicket = %+v", tk)
	}

	var n1, n2 GraphNode
	h.mustCall("POST", "/tickets/"+tk.ID+"/nodes", GraphNode{Name: "plan", Type: "plan", Status: "TODO"}, &n1, 201)
	step("createNode")
	h.mustCall("POST", "/tickets/"+tk.ID+"/nodes", GraphNode{Name: "review", Type: "review", Status: "TODO"}, &n2, 201)
	h.mustCall("GET", "/nodes/"+n1.ID, nil, &n1, 200)
	step("getNode")
	var nodes []GraphNode
	h.mustCall("GET", "/tickets/"+tk.ID+"/nodes", nil, &nodes, 200)
	step("listNodesByTicket")
	if len(nodes) != 2 {
		t.Fatalf("listNodesByTicket = %+v", nodes)
	}
	h.mustCall("PATCH", "/nodes/"+n1.ID, map[string]any{"status": "DONE"}, &n1, 200)
	step("updateNode")

	var e GraphEdge
	h.mustCall("POST", "/tickets/"+tk.ID+"/edges", GraphEdge{ID: "edge-1", FromNodeID: n1.ID, ToNodeID: n2.ID, Condition: "success"}, &e, 201)
	step("createEdge")
	var edges []GraphEdge
	h.mustCall("GET", "/tickets/"+tk.ID+"/edges", nil, &edges, 200)
	step("listEdgesByTicket")
	if len(edges) != 1 || edges[0].ID != "edge-1" || edges[0].CreatedAt == "" {
		t.Fatalf("listEdgesByTicket = %+v", edges)
	}

	content := "hello"
	var art Artifact
	h.mustCall("POST", "/tickets/"+tk.ID+"/artifacts", Artifact{ID: "art-x", NodeID: n1.ID, Name: "plan", Type: "text", Content: &content}, &art, 201)
	step("createArtifact")
	if !art.HasContent || art.ID == "" {
		t.Fatalf("createArtifact = %+v", art)
	}
	h.mustCall("GET", "/artifacts/"+art.ID, nil, &art, 200)
	step("getArtifact")
	if art.Content == nil || *art.Content != "hello" {
		t.Fatalf("getArtifact = %+v", art)
	}
	var arts []Artifact
	h.mustCall("GET", "/tickets/"+tk.ID+"/artifacts", nil, &arts, 200)
	step("listArtifactsByTicket")
	h.mustCall("GET", "/nodes/"+n1.ID+"/artifacts", nil, &arts, 200)
	step("listArtifactsByNode")
	if len(arts) != 1 {
		t.Fatalf("listArtifactsByNode = %+v", arts)
	}

	var detail TicketDetail
	h.mustCall("GET", "/tickets/"+tk.ID+"/detail", nil, &detail, 200)
	step("getTicketDetail")
	if len(detail.Nodes) != 2 || len(detail.Edges) != 1 || len(detail.Artifacts) != 1 || detail.Title != "T" {
		t.Fatalf("getTicketDetail = %+v", detail)
	}

	var usages []LabelUsage
	h.mustCall("GET", "/projects/"+proj.ID+"/labels", nil, &usages, 200)
	step("listLabelsByProject")
	if len(usages) != 1 || usages[0].TicketCount != 1 {
		t.Fatalf("listLabelsByProject = %+v", usages)
	}

	var cur map[string]string
	h.mustCall("GET", "/current-project", nil, &cur, 200)
	step("getCurrentProjectID")
	if cur["project_id"] != "" {
		t.Fatalf("getCurrentProjectID on a fresh plugin = %v", cur)
	}
	h.mustCall("PUT", "/current-project", map[string]string{"project_id": proj.ID}, nil, 204)
	step("setCurrentProjectID")

	h.mustCall("DELETE", "/tickets/"+tk.ID+"/edges", nil, nil, 204)
	step("clearEdgesByTicket")
	h.mustCall("DELETE", "/nodes/"+n2.ID, nil, nil, 204)
	step("deleteNode")
	var removed map[string]int
	h.mustCall("DELETE", "/labels/"+label.ID, nil, &removed, 200)
	step("deleteLabel")
	if removed["removed_from_tickets"] != 1 {
		t.Fatalf("deleteLabel = %v", removed)
	}
	h.mustCall("DELETE", "/tickets/"+tk.ID, nil, nil, 204)
	step("deleteTicket")
	h.mustCall("DELETE", "/projects/"+proj.ID, nil, nil, 204)
	step("deleteProject")

	// Get operations on missing entities answer 404 + the matching code.
	for path, code := range map[string]string{
		"/tickets/GOPS-999": "TICKET_NOT_FOUND", "/tickets/GOPS-999/detail": "TICKET_NOT_FOUND",
		"/nodes/GOPS-999-n1000": "NODE_NOT_FOUND", "/artifacts/GOPS-999-n1000-c1": "ARTIFACT_NOT_FOUND",
		"/projects/jira-NOPE": "PROJECT_NOT_FOUND", "/labels/GOPS-label-99": "LABEL_NOT_FOUND",
	} {
		var eb errBody
		if status := h.call("GET", path, nil, &eb); status != 404 || eb.Error.Code != code {
			t.Errorf("GET %s = %d %+v, want 404 %s", path, status, eb, code)
		}
	}

	var want []string
	for _, op := range []string{
		"init", "createTicket", "getTicket", "getTicketDetail", "listTickets", "listTicketsByProject", "updateTicket", "deleteTicket",
		"createNode", "getNode", "listNodesByTicket", "updateNode", "deleteNode",
		"createEdge", "listEdgesByTicket", "clearEdgesByTicket",
		"createArtifact", "getArtifact", "listArtifactsByTicket", "listArtifactsByNode",
		"createProject", "getProject", "listProjects", "updateProject", "deleteProject",
		"createLabel", "getLabel", "listLabelsByProject", "updateLabel", "deleteLabel",
		"getCurrentProjectID", "setCurrentProjectID",
	} {
		want = append(want, op)
	}
	if len(want) != 32 {
		t.Fatalf("operation list has %d entries", len(want))
	}
	var got []string
	for op := range seen {
		got = append(got, op)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exercised %v\nwant %v", got, want)
	}
}

// --- projects ---

func TestCreateProject_MapsToAnExistingJiraProject(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	if p.ID != "jira-GOPS" || p.Prefix != "GOPS" || p.Name != "GraphOps" {
		t.Fatalf("project = %+v", p)
	}
	if n := len(h.jira.requestsMatching("POST", `^/rest/api/3/project$`)); n != 0 {
		t.Fatalf("a Jira project was created (%d requests)", n)
	}
	meta := h.jira.issue("GOPS-1")
	if meta == nil || !containsString(meta.Labels, "graphops-meta") {
		t.Fatalf("metadata issue = %+v", meta)
	}
	var pp projectProp
	if err := json.Unmarshal(meta.Properties["graphops.project"], &pp); err != nil || pp.Name != "GraphOps" {
		t.Fatalf("graphops.project = %s (%v)", meta.Properties["graphops.project"], err)
	}
}

func TestCreateProject_RejectsUnmappablePrefixes(t *testing.T) {
	h := newHarness(t)
	for prefix, code := range map[string]string{"NOPE": "VALIDATION_ERROR", "TOOLONG": "INVALID_PREFIX", "": "VALIDATION_ERROR"} {
		var eb errBody
		status := h.call("POST", "/projects", map[string]string{"name": "x", "prefix": prefix}, &eb)
		if status/100 != 4 || eb.Error.Code != code {
			t.Errorf("prefix %q: %d %+v, want %s", prefix, status, eb, code)
		}
	}
	h.registerGOPS()
	var eb errBody
	if status := h.call("POST", "/projects", map[string]string{"name": "again", "prefix": "gops"}, &eb); status != 409 || eb.Error.Code != "PREFIX_TAKEN" {
		t.Errorf("re-registering = %d %+v, want 409 PREFIX_TAKEN", status, eb)
	}
}

func TestProjectsAndCurrentProjectSurviveARestart(t *testing.T) {
	h := newHarness(t)
	gops := h.registerGOPS()
	var demo Project
	h.mustCall("POST", "/projects", map[string]string{"name": "Demo", "prefix": "DEMO"}, &demo, 201)
	h.mustCall("PUT", "/current-project", map[string]string{"project_id": gops.ID}, nil, 204)

	// A new plugin process: same state file, same Jira.
	restarted := httptest.NewServer(newHandler(h.newStore(), pluginToken, nil))
	defer restarted.Close()
	h.srv = restarted

	var projects []Project
	h.mustCall("GET", "/projects", nil, &projects, 200)
	var ids []string
	for _, p := range projects {
		ids = append(ids, p.ID)
	}
	if !reflect.DeepEqual(ids, []string{"jira-GOPS", "jira-DEMO"}) {
		t.Fatalf("projects after restart = %v", ids)
	}
	var cur map[string]string
	h.mustCall("GET", "/current-project", nil, &cur, 200)
	if cur["project_id"] != gops.ID {
		t.Fatalf("current project after restart = %v", cur)
	}
}

func TestReRegisteringReusesTheMetadataIssue(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	var l Label
	h.mustCall("POST", "/projects/"+p.ID+"/labels", map[string]string{"name": "keep", "color": "red"}, &l, 201)

	// Losing the state file must not orphan the Jira data.
	h.store.state = &stateFile{path: filepath.Join(h.t.TempDir(), "fresh.json")}
	h.registerGOPS()
	var labels []LabelUsage
	h.mustCall("GET", "/projects/"+p.ID+"/labels", nil, &labels, 200)
	if len(labels) != 1 || labels[0].ID != l.ID {
		t.Fatalf("labels after re-registration = %+v", labels)
	}
	if n := len(h.jira.requestsMatching("POST", `^/rest/api/3/issue$`)); n != 1 {
		t.Fatalf("expected exactly one metadata issue to be created, got %d issue creations", n)
	}
}

func TestDeleteProject_DetachesTicketsAndUnregisters(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	tk := h.createTicket(p.ID, "t")
	h.mustCall("PUT", "/current-project", map[string]string{"project_id": p.ID}, nil, 204)
	h.mustCall("DELETE", "/projects/"+p.ID, nil, nil, 204)

	if h.jira.issue("GOPS-1") != nil {
		t.Fatal("the metadata issue was not deleted")
	}
	issue := h.jira.issue(tk.ID)
	if issue == nil || containsString(issue.Labels, "graphops") {
		t.Fatalf("ticket issue after DeleteProject = %+v; want it kept but detached", issue)
	}
	var cur map[string]string
	h.mustCall("GET", "/current-project", nil, &cur, 200)
	if cur["project_id"] != "" {
		t.Fatalf("current project not cleared: %v", cur)
	}
	var projects []Project
	h.mustCall("GET", "/projects", nil, &projects, 200)
	if len(projects) != 0 {
		t.Fatalf("projects = %+v", projects)
	}
}

// --- tickets ---

func TestCreateTicket_IsAJiraIssue(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	var l Label
	h.mustCall("POST", "/projects/"+p.ID+"/labels", map[string]string{"name": "bug", "color": "red"}, &l, 201)
	var tk Ticket
	h.mustCall("POST", "/projects/"+p.ID+"/tickets", map[string]any{
		"title": "件名", "description": "first paragraph\n\nsecond **bold**", "status": "TODO",
		"priority": "HIGH", "labels": []map[string]string{{"id": l.ID}},
	}, &tk, 201)

	if !strings.HasPrefix(tk.ID, "GOPS-") {
		t.Fatalf("ticket ID %q is not a GOPS issue key", tk.ID)
	}
	issue := h.jira.issue(tk.ID)
	if issue.Summary != "件名" || !containsString(issue.Labels, "graphops") {
		t.Fatalf("issue = %+v", issue)
	}
	doc, _ := json.Marshal(issue.Description)
	if !strings.Contains(string(doc), `"type":"doc"`) || !strings.Contains(string(doc), `"type":"paragraph"`) ||
		!strings.Contains(string(doc), "second **bold**") {
		t.Fatalf("description is not ADF paragraphs: %s", doc)
	}
	var tp ticketProp
	if err := json.Unmarshal(issue.Properties["graphops.ticket"], &tp); err != nil {
		t.Fatal(err)
	}
	if tp.Status != "TODO" || tp.Priority != "HIGH" || tp.Description != "first paragraph\n\nsecond **bold**" ||
		!reflect.DeepEqual(tp.LabelIDs, []string{l.ID}) || tp.Blocked {
		t.Fatalf("graphops.ticket = %+v", tp)
	}
	if n := len(h.jira.requestsMatching("POST", `/transitions$`)); n != 0 {
		t.Fatalf("a workflow transition was made")
	}
}

func TestListTicketsByProject_UsesOneSearchWithProperties(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	for i := 0; i < 3; i++ {
		h.createTicket(p.ID, fmt.Sprintf("t%d", i))
	}
	h.jira.resetRequests()

	var tickets []Ticket
	h.mustCall("GET", "/projects/"+p.ID+"/tickets", nil, &tickets, 200)
	if len(tickets) != 3 {
		t.Fatalf("got %d tickets, want 3 (metadata issue excluded)", len(tickets))
	}
	if tickets[0].Title != "t2" {
		t.Fatalf("tickets are not newest first: %+v", tickets)
	}
	searches := h.jira.requestsMatching("POST", `^/rest/api/3/search/jql$`)
	if len(searches) != 1 {
		t.Fatalf("expected one search request, got %d", len(searches))
	}
	var body struct {
		JQL        string   `json:"jql"`
		Properties []string `json:"properties"`
	}
	_ = json.Unmarshal(searches[0].Body, &body)
	if !strings.HasPrefix(body.JQL, "project = GOPS AND labels = graphops AND labels != graphops-meta") {
		t.Fatalf("JQL = %q", body.JQL)
	}
	if !containsString(body.Properties, "graphops.ticket") {
		t.Fatalf("search properties = %v", body.Properties)
	}
	// No request per listed issue: the only issue read is the project's
	// metadata issue (GOPS-1, for label names).
	for _, r := range h.jira.requestsMatching("GET", `^/rest/api/3/issue/`) {
		if r.Path != "/rest/api/3/issue/GOPS-1" {
			t.Fatalf("per-issue request during listing: %s %s", r.Method, r.Path)
		}
	}
}

func TestListTickets_FollowsNextPageToken(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	for i := 0; i < 5; i++ {
		h.createTicket(p.ID, fmt.Sprintf("t%d", i))
	}
	h.jira.pageSize = 3
	h.jira.resetRequests()
	var tickets []Ticket
	h.mustCall("GET", "/tickets", nil, &tickets, 200)
	if len(tickets) != 5 {
		t.Fatalf("got %d tickets over two pages, want 5", len(tickets))
	}
	if n := len(h.jira.requestsMatching("POST", `^/rest/api/3/search/jql$`)); n != 2 {
		t.Fatalf("expected 2 search pages, got %d", n)
	}
}

func TestUpdateTicket_MirrorsTitleAndDescription(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	tk := h.createTicket(p.ID, "old")
	var got Ticket
	h.mustCall("PATCH", "/tickets/"+tk.ID, map[string]any{"title": "new title", "description": "refined"}, &got, 200)
	issue := h.jira.issue(tk.ID)
	doc, _ := json.Marshal(issue.Description)
	if issue.Summary != "new title" || !strings.Contains(string(doc), "refined") || got.Title != "new title" {
		t.Fatalf("issue after update = %+v / %s", issue, doc)
	}
	// assignee: null clears, absent keeps.
	h.mustCall("PATCH", "/tickets/"+tk.ID, map[string]any{"assignee": "bob"}, &got, 200)
	h.mustCall("PATCH", "/tickets/"+tk.ID, map[string]any{"status": "DONE"}, &got, 200)
	if got.Assignee == nil || *got.Assignee != "bob" {
		t.Fatalf("absent assignee key changed the value: %+v", got)
	}
	var cleared Ticket
	h.mustCall("PATCH", "/tickets/"+tk.ID, map[string]any{"assignee": nil}, &cleared, 200)
	if cleared.Assignee != nil {
		t.Fatalf("null assignee did not clear: %+v", got)
	}
	var eb errBody
	if status := h.call("PATCH", "/tickets/"+tk.ID, map[string]any{"label_ids": []string{"GOPS-label-404"}, "title": "x"}, &eb); status != 404 || eb.Error.Code != "LABEL_NOT_FOUND" {
		t.Fatalf("unknown label = %d %+v", status, eb)
	}
	if h.jira.issue(tk.ID).Summary != "new title" {
		t.Fatal("a failed patch still changed the summary")
	}
}

func TestDeleteTicket_DetachesTheIssue(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	tk := h.createTicket(p.ID, "t")
	h.mustCall("DELETE", "/tickets/"+tk.ID, nil, nil, 204)
	issue := h.jira.issue(tk.ID)
	if issue == nil {
		t.Fatal("the Jira issue itself was deleted")
	}
	if containsString(issue.Labels, "graphops") || issue.Properties["graphops.ticket"] != nil || issue.Properties["graphops.edges"] != nil {
		t.Fatalf("issue still attached to GraphOps: %+v", issue)
	}
	var eb errBody
	if status := h.call("GET", "/tickets/"+tk.ID, nil, &eb); status != 404 {
		t.Fatalf("GET after delete = %d", status)
	}
	h.mustCall("DELETE", "/tickets/"+tk.ID, nil, nil, 204) // idempotent
}

// --- nodes ---

func TestConcurrentCreateNodeIsSerialized(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	tk := h.createTicket(p.ID, "t")
	var wg sync.WaitGroup
	statuses := make([]int, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i] = h.call("POST", "/tickets/"+tk.ID+"/nodes", GraphNode{Name: fmt.Sprint(i), Type: "custom"}, nil)
		}(i)
	}
	wg.Wait()
	for i, status := range statuses {
		if status != http.StatusCreated {
			t.Errorf("creating node %d = %d, want 201", i, status)
		}
	}
	var nodes []GraphNode
	h.mustCall("GET", "/tickets/"+tk.ID+"/nodes", nil, &nodes, 200)
	ids := map[string]bool{}
	for _, n := range nodes {
		ids[n.ID] = true
	}
	if len(nodes) != 10 || len(ids) != 10 {
		t.Fatalf("got %d nodes with %d distinct IDs, want 10/10", len(nodes), len(ids))
	}
	// Each ID names its own sub-task, and there is no other sub-task.
	subtasks := map[string]bool{}
	for id := range ids {
		_, sub, ok := parseNodeID(id)
		if !ok || h.jira.issue(sub) == nil || h.jira.issue(sub).Parent != tk.ID {
			t.Fatalf("node %s does not name a sub-task of %s", id, tk.ID)
		}
		subtasks[sub] = true
	}
	if len(subtasks) != 10 || len(h.jira.subtasksOf(tk.ID)) != 10 {
		t.Fatalf("sub-tasks = %v", h.jira.subtasksOf(tk.ID))
	}
}

// --- labels ---

func TestLabelIDsLeadToTheProjectMetadataIssue(t *testing.T) {
	h := newHarness(t)
	p := h.registerGOPS()
	var l Label
	h.mustCall("POST", "/projects/"+p.ID+"/labels", map[string]string{"name": "bug", "color": "red"}, &l, 201)
	if l.ID != "GOPS-label-1" {
		t.Fatalf("label ID = %q", l.ID)
	}
	var pp projectProp
	_ = json.Unmarshal(h.jira.issue("GOPS-1").Properties["graphops.project"], &pp)
	if len(pp.Labels) != 1 || pp.Labels[0].Name != "bug" {
		t.Fatalf("graphops.project labels = %+v", pp.Labels)
	}

	h.jira.resetRequests()
	h.mustCall("GET", "/labels/"+l.ID, nil, nil, 200)
	h.mustCall("PATCH", "/labels/"+l.ID, map[string]string{"name": "Bug!"}, nil, 200)
	touched := append(h.jira.requestsMatching("GET", `^/rest/api/3/issue/`), h.jira.requestsMatching("PUT", `^/rest/api/3/issue/`)...)
	if len(touched) == 0 {
		t.Fatal("label operations made no issue requests at all")
	}
	for _, r := range touched {
		if r.Path != "/rest/api/3/issue/GOPS-1" && r.Path != "/rest/api/3/issue/GOPS-1/properties/graphops.project" {
			t.Fatalf("label operation touched %s %s", r.Method, r.Path)
		}
	}
	var eb errBody
	h.mustCall("POST", "/projects/"+p.ID+"/labels", map[string]string{"name": "other", "color": "blue"}, nil, 201)
	if status := h.call("PATCH", "/labels/"+l.ID, map[string]string{"name": "OTHER"}, &eb); status != 409 || eb.Error.Code != "LABEL_NAME_TAKEN" {
		t.Fatalf("duplicate rename = %d %+v", status, eb)
	}
	if status := h.call("POST", "/projects/"+p.ID+"/labels", map[string]string{"name": "x", "color": "neon"}, &eb); status != 400 || eb.Error.Code != "INVALID_LABEL_COLOR" {
		t.Fatalf("bad color = %d %+v", status, eb)
	}
	h.mustCall("DELETE", "/labels/"+l.ID, nil, nil, 200)
	if status := h.call("GET", "/labels/"+l.ID, nil, &eb); status != 404 {
		t.Fatalf("GET after delete = %d", status)
	}
}

// --- rate limiting ---

func TestRateLimitedRequestsAreRetried(t *testing.T) {
	h := newHarness(t)
	h.registerGOPS()

	var mu sync.Mutex
	failures := 2
	var waits []time.Duration
	h.store.jira.sleep = func(_ context.Context, d time.Duration) error {
		mu.Lock()
		waits = append(waits, d)
		mu.Unlock()
		return nil
	}
	h.jira.failWith = func(w http.ResponseWriter, r *http.Request) bool {
		mu.Lock()
		defer mu.Unlock()
		if failures > 0 {
			failures--
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		}
		return false
	}
	var projects []Project
	h.mustCall("GET", "/projects", nil, &projects, 200)
	if len(projects) != 1 || len(waits) != 2 || waits[0] != time.Second {
		t.Fatalf("projects %d, waits %v", len(projects), waits)
	}

	h.jira.failWith = func(w http.ResponseWriter, r *http.Request) bool {
		w.WriteHeader(http.StatusTooManyRequests)
		return true
	}
	var eb errBody
	if status := h.call("GET", "/projects", nil, &eb); status/100 != 5 || eb.Error.Code != "INTERNAL_ERROR" {
		t.Fatalf("always-429 = %d %+v", status, eb)
	}
	if len(waits) != 2+h.store.jira.maxRetries {
		t.Fatalf("retries = %d, want %d", len(waits)-2, h.store.jira.maxRetries)
	}
}

// --- secrets ---

func TestErrorsDoNotLeakJiraCredentials(t *testing.T) {
	h := newHarness(t)
	h.jira.failWith = func(w http.ResponseWriter, r *http.Request) bool {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
		return true
	}
	var raw json.RawMessage
	h.call("POST", "/projects", map[string]string{"name": "x", "prefix": "GOPS"}, &raw)
	if strings.Contains(string(raw), h.jira.token) || strings.Contains(string(raw), h.jira.email) {
		t.Fatalf("error leaks credentials: %s", raw)
	}
}
