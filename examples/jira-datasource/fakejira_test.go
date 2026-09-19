package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeJira is an in-memory stand-in for the slice of the Jira Cloud REST API
// v3 the plugin uses. It never talks to a real Jira.
type fakeJira struct {
	t     *testing.T
	email string
	token string

	mu          sync.Mutex
	projects    map[string]string // key -> name
	issues      map[string]*fakeIssue
	order       []string // issue keys, creation order
	seq         map[string]int
	issueSeq    int // numeric issue IDs, across projects
	commentSeq  int
	attachments map[string][]byte
	requests    []fakeRequest
	// pageSize is how many issues one search page returns.
	pageSize int
	// standardTypes and subtaskTypes are the issue type names that exist.
	standardTypes map[string]bool
	subtaskTypes  map[string]bool
	// workflow maps a status name to the transitions available from it.
	workflow map[string][]fakeTransition
	// commentSelf, when it has an entry for a comment ID, replaces that
	// comment's self URL in answers.
	commentSelf map[string]string
	// failWith, when non-nil, may answer a request instead of the fake
	// (returning true when it did).
	failWith func(w http.ResponseWriter, r *http.Request) bool

	srv *httptest.Server
}

type fakeRequest struct {
	Method, Path, RawQuery string
	Body                   []byte
}

type fakeIssue struct {
	ID          string // numeric, as in Jira
	Key         string
	Project     string
	IssueType   string
	Parent      string // parent issue key, for a sub-task
	Status      string // workflow status name
	Summary     string
	Description any
	Labels      []string
	Properties  map[string]json.RawMessage
	Created     time.Time
	Comments    []*fakeComment
	Attachments []string
}

type fakeTransition struct {
	ID, Name, To string
}

// defaultFakeWorkflow lets a new issue ("To Do") start or finish, and a
// finished one be reopened.
func defaultFakeWorkflow() map[string][]fakeTransition {
	return map[string][]fakeTransition{
		"To Do":       {{"11", "Start", "In Progress"}, {"31", "Finish", "Done"}},
		"In Progress": {{"31", "Finish", "Done"}, {"41", "Stop", "To Do"}},
		"Done":        {{"21", "Reopen", "In Progress"}},
	}
}

type fakeComment struct {
	ID         string
	Body       any
	Properties map[string]json.RawMessage
	Created    string
}

func newFakeJira(t *testing.T) *fakeJira {
	f := &fakeJira{
		t: t, email: "user@example.invalid", token: "fake-api-token",
		projects: map[string]string{}, issues: map[string]*fakeIssue{}, seq: map[string]int{},
		attachments: map[string][]byte{}, pageSize: 50,
		standardTypes: map[string]bool{"Task": true}, subtaskTypes: map[string]bool{"Subtask": true},
		workflow: defaultFakeWorkflow(), commentSelf: map[string]string{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeJira) addProject(key, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projects[key] = name
}

func (f *fakeJira) issue(key string) *fakeIssue {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.issues[key]
}

// seedIssue creates an issue directly in the fake (as a Jira user could,
// outside the plugin) and returns its key.
func (f *fakeJira) seedIssue(project string, labels []string, props map[string]string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seedLocked(project, "Task", "", labels, props)
}

// seedSubtask creates a sub-task of parent directly in the fake (as a Jira
// user could) and returns its key.
func (f *fakeJira) seedSubtask(parent string, labels []string, props map[string]string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seedLocked(f.issues[parent].Project, "Subtask", parent, labels, props)
}

func (f *fakeJira) seedLocked(project, issueType, parent string, labels []string, props map[string]string) string {
	f.seq[project]++
	f.issueSeq++
	issue := &fakeIssue{
		ID: strconv.Itoa(10000 + f.issueSeq), Key: fmt.Sprintf("%s-%d", project, f.seq[project]), Project: project,
		IssueType: issueType, Parent: parent, Status: "To Do", Summary: "seeded",
		Labels: labels, Properties: map[string]json.RawMessage{},
		Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(len(f.order)) * time.Second),
	}
	for k, v := range props {
		issue.Properties[k] = json.RawMessage(v)
	}
	f.issues[issue.Key] = issue
	f.order = append(f.order, issue.Key)
	return issue.Key
}

// subtasksOf returns the keys of an issue's sub-tasks, oldest first.
func (f *fakeJira) subtasksOf(key string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subtasksLocked(key)
}

func (f *fakeJira) subtasksLocked(key string) []string {
	var out []string
	for _, k := range f.order {
		if issue, ok := f.issues[k]; ok && issue.Parent == key {
			out = append(out, k)
		}
	}
	return out
}

// setFailWith replaces failWith safely while requests the plugin gave up on
// may still be running in the fake.
func (f *fakeJira) setFailWith(fn func(w http.ResponseWriter, r *http.Request) bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failWith = fn
}

// setStatus puts an issue in a workflow status directly.
func (f *fakeJira) setStatus(key, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issues[key].Status = status
}

// snapshot returns a copy of an issue's state, for before/after checks.
func (f *fakeJira) snapshot(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	issue := f.issues[key]
	if issue == nil {
		return "<missing>"
	}
	var comments []string
	for _, cm := range issue.Comments {
		props, _ := json.Marshal(cm.Properties)
		comments = append(comments, cm.ID+string(props))
	}
	props, _ := json.Marshal(issue.Properties)
	return fmt.Sprintf("%s|%s|%v|%s|%s|%v|%v", issue.Summary, issue.Status, issue.Labels, props, issue.Parent, comments, issue.Attachments)
}

// requests returns a copy of the request log.
func (f *fakeJira) allRequests() []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeRequest(nil), f.requests...)
}

// isReadOnly reports whether a request only reads: a GET, or one of the
// POST endpoints that read (search, bulkfetch, comment/list).
func (r fakeRequest) isReadOnly() bool {
	if r.Method == http.MethodGet {
		return true
	}
	return r.Method == http.MethodPost && (r.Path == "/rest/api/3/search/jql" || r.Path == "/rest/api/3/issue/bulkfetch" || r.Path == "/rest/api/3/comment/list")
}

// changingRequests returns the logged requests that change something in
// Jira (issues, properties, labels, comments, attachments, transitions).
func (f *fakeJira) changingRequests() []fakeRequest {
	var out []fakeRequest
	for _, r := range f.allRequests() {
		if !r.isReadOnly() {
			out = append(out, r)
		}
	}
	return out
}

// seedAttachment stores an attachment on an issue and returns its ID.
func (f *fakeJira) seedAttachment(issueKey string, data []byte) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := strconv.Itoa(20000 + len(f.attachments) + 1)
	f.attachments[id] = data
	f.issues[issueKey].Attachments = append(f.issues[issueKey].Attachments, id)
	return id
}

// seedComment adds a comment with the given properties and returns its ID.
func (f *fakeJira) seedComment(issueKey string, props map[string]string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commentSeq++
	cm := &fakeComment{ID: strconv.Itoa(10000 + f.commentSeq), Body: "seeded", Properties: map[string]json.RawMessage{}, Created: time.Now().UTC().Format(time.RFC3339)}
	for k, v := range props {
		cm.Properties[k] = json.RawMessage(v)
	}
	f.issues[issueKey].Comments = append(f.issues[issueKey].Comments, cm)
	return cm.ID
}

func (f *fakeJira) hasAttachment(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.attachments[id]
	return ok
}

// issueByID finds an issue by its numeric ID.
func (f *fakeJira) issueByIDLocked(id string) *fakeIssue {
	for _, issue := range f.issues {
		if issue.ID == id {
			return issue
		}
	}
	return nil
}

func (f *fakeJira) deleteIssue(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.issues, key)
}

func (f *fakeJira) requestsMatching(method string, pathPattern string) []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	re := regexp.MustCompile(pathPattern)
	var out []fakeRequest
	for _, r := range f.requests {
		if r.Method == method && re.MatchString(r.Path) {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeJira) resetRequests() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = nil
}

func (f *fakeJira) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func fjWrite(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func fjError(w http.ResponseWriter, status int, msg string) {
	fjWrite(w, status, map[string]any{"errorMessages": []string{msg}})
}

var (
	reProject      = regexp.MustCompile(`^/rest/api/3/project/([^/]+)$`)
	reIssue        = regexp.MustCompile(`^/rest/api/3/issue/([^/]+)$`)
	reProperty     = regexp.MustCompile(`^/rest/api/3/issue/([^/]+)/properties/([^/]+)$`)
	reComments     = regexp.MustCompile(`^/rest/api/3/issue/([^/]+)/comment$`)
	reComment      = regexp.MustCompile(`^/rest/api/3/issue/([^/]+)/comment/([^/]+)$`)
	reAttachments  = regexp.MustCompile(`^/rest/api/3/issue/([^/]+)/attachments$`)
	reAttachment   = regexp.MustCompile(`^/rest/api/3/attachment/content/([^/]+)$`)
	reAttachmentID = regexp.MustCompile(`^/rest/api/3/attachment/([^/]+)$`)
	reTransitions  = regexp.MustCompile(`^/rest/api/3/issue/([^/]+)/transitions$`)
)

func (f *fakeJira) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, fakeRequest{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Body: body})
	fail := f.failWith
	f.mu.Unlock()

	if user, pass, ok := r.BasicAuth(); !ok || user != f.email || pass != f.token {
		fjError(w, http.StatusUnauthorized, "bad credentials")
		return
	}
	if fail != nil && fail(w, r) {
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	switch {
	case r.Method == http.MethodGet && reProject.MatchString(p):
		key := reProject.FindStringSubmatch(p)[1]
		name, ok := f.projects[key]
		if !ok {
			fjError(w, 404, "No project could be found with key '"+key+"'.")
			return
		}
		fjWrite(w, 200, map[string]string{"id": "1" + key, "key": key, "name": name})

	case r.Method == http.MethodPost && p == "/rest/api/3/issue":
		f.createIssue(w, body)

	case r.Method == http.MethodPost && p == "/rest/api/3/search/jql":
		f.search(w, body)

	case r.Method == http.MethodPost && p == "/rest/api/3/issue/bulkfetch":
		f.bulkFetch(w, body)

	case r.Method == http.MethodPost && p == "/rest/api/3/comment/list":
		f.commentList(w, r, body)

	case reTransitions.MatchString(p):
		f.transitions(w, r, reTransitions.FindStringSubmatch(p)[1], body)

	case reProperty.MatchString(p):
		m := reProperty.FindStringSubmatch(p)
		issue := f.issues[m[1]]
		if issue == nil {
			fjError(w, 404, "Issue does not exist")
			return
		}
		switch r.Method {
		case http.MethodPut:
			if !json.Valid(body) {
				fjError(w, 400, "invalid JSON")
				return
			}
			if len(body) > 32768 {
				fjError(w, 400, "The property value is too long")
				return
			}
			issue.Properties[m[2]] = json.RawMessage(append([]byte(nil), body...))
			fjWrite(w, 200, nil)
		case http.MethodGet:
			v, ok := issue.Properties[m[2]]
			if !ok {
				fjError(w, 404, "property not found")
				return
			}
			fjWrite(w, 200, map[string]any{"key": m[2], "value": v})
		case http.MethodDelete:
			if _, ok := issue.Properties[m[2]]; !ok {
				fjError(w, 404, "property not found")
				return
			}
			delete(issue.Properties, m[2])
			w.WriteHeader(204)
		}

	case reComments.MatchString(p):
		key := reComments.FindStringSubmatch(p)[1]
		issue := f.issues[key]
		if issue == nil {
			fjError(w, 404, "Issue does not exist")
			return
		}
		if r.Method == http.MethodPost {
			var in struct {
				Body       any `json:"body"`
				Properties []struct {
					Key   string          `json:"key"`
					Value json.RawMessage `json:"value"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(body, &in); err != nil {
				fjError(w, 400, err.Error())
				return
			}
			f.commentSeq++
			cm := &fakeComment{ID: strconv.Itoa(10000 + f.commentSeq), Body: in.Body, Properties: map[string]json.RawMessage{}, Created: time.Now().UTC().Format(time.RFC3339)}
			for _, prop := range in.Properties {
				cm.Properties[prop.Key] = prop.Value
			}
			issue.Comments = append(issue.Comments, cm)
			fjWrite(w, 201, f.commentJSON(issue, cm, false))
			return
		}
		startAt, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		maxResults, _ := strconv.Atoi(r.URL.Query().Get("maxResults"))
		if maxResults <= 0 {
			maxResults = 50
		}
		expand := strings.Contains(r.URL.Query().Get("expand"), "properties")
		var page []any
		for i := startAt; i < len(issue.Comments) && len(page) < maxResults; i++ {
			page = append(page, f.commentJSON(issue, issue.Comments[i], expand))
		}
		if page == nil {
			page = []any{}
		}
		fjWrite(w, 200, map[string]any{"startAt": startAt, "maxResults": maxResults, "total": len(issue.Comments), "comments": page})

	case reComment.MatchString(p):
		m := reComment.FindStringSubmatch(p)
		issue := f.issues[m[1]]
		if issue == nil {
			fjError(w, 404, "Issue does not exist")
			return
		}
		for i, cm := range issue.Comments {
			if cm.ID != m[2] {
				continue
			}
			if r.Method == http.MethodDelete {
				issue.Comments = append(issue.Comments[:i], issue.Comments[i+1:]...)
				w.WriteHeader(204)
				return
			}
			fjWrite(w, 200, f.commentJSON(issue, cm, strings.Contains(r.URL.Query().Get("expand"), "properties")))
			return
		}
		fjError(w, 404, "comment not found")

	case r.Method == http.MethodPost && reAttachments.MatchString(p):
		key := reAttachments.FindStringSubmatch(p)[1]
		issue := f.issues[key]
		if issue == nil {
			fjError(w, 404, "Issue does not exist")
			return
		}
		if r.Header.Get("X-Atlassian-Token") != "no-check" {
			fjError(w, 403, "XSRF check failed")
			return
		}
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		file, header, err := r.FormFile("file")
		if err != nil {
			fjError(w, 400, err.Error())
			return
		}
		data, _ := io.ReadAll(file)
		id := strconv.Itoa(20000 + len(f.attachments) + 1)
		f.attachments[id] = data
		issue.Attachments = append(issue.Attachments, id)
		fjWrite(w, 200, []map[string]any{{"id": id, "filename": header.Filename, "size": len(data)}})

	case r.Method == http.MethodGet && reAttachment.MatchString(p):
		data, ok := f.attachments[reAttachment.FindStringSubmatch(p)[1]]
		if !ok {
			fjError(w, 404, "attachment not found")
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write(data)

	case r.Method == http.MethodDelete && reAttachmentID.MatchString(p):
		delete(f.attachments, reAttachmentID.FindStringSubmatch(p)[1])
		w.WriteHeader(204)

	case reIssue.MatchString(p):
		key := reIssue.FindStringSubmatch(p)[1]
		issue := f.issues[key]
		if issue == nil {
			fjError(w, 404, "Issue does not exist or you do not have permission to see it.")
			return
		}
		switch r.Method {
		case http.MethodGet:
			var props, fields []string
			if q := r.URL.Query().Get("properties"); q != "" {
				props = strings.Split(q, ",")
			}
			if q := r.URL.Query().Get("fields"); q != "" {
				fields = strings.Split(q, ",")
			}
			fjWrite(w, 200, f.issueJSON(issue, fields, props))
		case http.MethodPut:
			var in struct {
				Fields map[string]json.RawMessage `json:"fields"`
				Update map[string][]map[string]string
			}
			if err := json.Unmarshal(body, &in); err != nil {
				fjError(w, 400, err.Error())
				return
			}
			if raw, ok := in.Fields["summary"]; ok {
				_ = json.Unmarshal(raw, &issue.Summary)
			}
			if raw, ok := in.Fields["description"]; ok {
				_ = json.Unmarshal(raw, &issue.Description)
			}
			for _, op := range in.Update["labels"] {
				for _, l := range op {
					if strings.ContainsAny(l, " \t") {
						fjError(w, 400, "labels: a label cannot contain spaces")
						return
					}
				}
			}
			for _, op := range in.Update["labels"] {
				if l, ok := op["remove"]; ok {
					var kept []string
					for _, x := range issue.Labels {
						if x != l {
							kept = append(kept, x)
						}
					}
					issue.Labels = kept
				}
				if l, ok := op["add"]; ok && !containsString(issue.Labels, l) {
					issue.Labels = append(issue.Labels, l)
				}
			}
			w.WriteHeader(204)
		case http.MethodDelete:
			if len(f.subtasksLocked(key)) > 0 && r.URL.Query().Get("deleteSubtasks") != "true" {
				fjError(w, 400, "The issue has subtasks; set deleteSubtasks=true to delete them too")
				return
			}
			for _, k := range f.subtasksLocked(key) {
				delete(f.issues, k)
			}
			delete(f.issues, key)
			w.WriteHeader(204)
		}

	default:
		fjError(w, 404, "fake jira: no route for "+r.Method+" "+p)
	}
}

func (f *fakeJira) commentJSON(issue *fakeIssue, cm *fakeComment, withProps bool) map[string]any {
	self := f.srv.URL + "/rest/api/3/issue/" + issue.ID + "/comment/" + cm.ID
	if s, ok := f.commentSelf[cm.ID]; ok {
		self = s
	}
	out := map[string]any{"id": cm.ID, "self": self, "created": cm.Created, "body": cm.Body}
	if withProps {
		props := []map[string]any{}
		for k, v := range cm.Properties {
			props = append(props, map[string]any{"key": k, "value": v})
		}
		out["properties"] = props
	}
	return out
}

// issueJSON answers an issue with the requested fields (every field the
// fake knows when fields is empty) and properties.
func (f *fakeJira) issueJSON(issue *fakeIssue, fields, props []string) map[string]any {
	properties := map[string]json.RawMessage{}
	for _, p := range props {
		if v, ok := issue.Properties[p]; ok {
			properties[p] = v
		}
	}
	labels := issue.Labels
	if labels == nil {
		labels = []string{}
	}
	all := map[string]any{
		"summary":   issue.Summary,
		"labels":    labels,
		"created":   issue.Created.Format("2006-01-02T15:04:05.000-0700"),
		"status":    map[string]string{"id": "s-" + issue.Status, "name": issue.Status},
		"issuetype": map[string]any{"name": issue.IssueType, "subtask": f.subtaskTypes[issue.IssueType]},
	}
	if parent := f.issues[issue.Parent]; parent != nil {
		all["parent"] = map[string]string{"id": parent.ID, "key": parent.Key}
	}
	subtasks := []map[string]string{}
	for _, k := range f.subtasksLocked(issue.Key) {
		subtasks = append(subtasks, map[string]string{"id": f.issues[k].ID, "key": k})
	}
	all["subtasks"] = subtasks
	out := all
	if len(fields) > 0 {
		out = map[string]any{}
		for _, name := range fields {
			if v, ok := all[strings.TrimSpace(name)]; ok {
				out[strings.TrimSpace(name)] = v
			}
		}
	}
	return map[string]any{"id": issue.ID, "key": issue.Key, "fields": out, "properties": properties}
}

func (f *fakeJira) bulkFetch(w http.ResponseWriter, body []byte) {
	var in struct {
		Keys       []string `json:"issueIdsOrKeys"`
		Fields     []string `json:"fields"`
		Properties []string `json:"properties"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		fjError(w, 400, err.Error())
		return
	}
	if len(in.Keys) > 100 {
		fjError(w, 400, "at most 100 issues can be fetched at once")
		return
	}
	issues := []any{}
	errs := []any{}
	for _, k := range in.Keys {
		issue := f.issues[k]
		if issue == nil {
			errs = append(errs, map[string]any{"issueIdsOrKeys": []string{k}, "status": 404})
			continue
		}
		issues = append(issues, f.issueJSON(issue, in.Fields, in.Properties))
	}
	fjWrite(w, 200, map[string]any{"issues": issues, "issueErrors": errs})
}

func (f *fakeJira) commentList(w http.ResponseWriter, r *http.Request, body []byte) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		fjError(w, 400, err.Error())
		return
	}
	if len(in.IDs) > 1000 {
		fjError(w, 400, "at most 1000 comment IDs")
		return
	}
	expand := strings.Contains(r.URL.Query().Get("expand"), "properties")
	var matched []any
	for _, id := range in.IDs {
		want := strconv.FormatInt(id, 10)
		for _, key := range f.order {
			issue := f.issues[key]
			if issue == nil {
				continue
			}
			for _, cm := range issue.Comments {
				if cm.ID == want {
					matched = append(matched, f.commentJSON(issue, cm, expand))
				}
			}
		}
	}
	startAt, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
	maxResults, _ := strconv.Atoi(r.URL.Query().Get("maxResults"))
	if maxResults <= 0 || maxResults > 1000 {
		maxResults = 1000
	}
	end := min(startAt+maxResults, len(matched))
	page := []any{}
	if startAt < len(matched) {
		page = matched[startAt:end]
	}
	fjWrite(w, 200, map[string]any{"startAt": startAt, "maxResults": maxResults, "total": len(matched), "isLast": end >= len(matched), "values": page})
}

func (f *fakeJira) transitions(w http.ResponseWriter, r *http.Request, key string, body []byte) {
	issue := f.issues[key]
	if issue == nil {
		fjError(w, 404, "Issue does not exist")
		return
	}
	available := f.workflow[issue.Status]
	switch r.Method {
	case http.MethodGet:
		out := []any{}
		for _, t := range available {
			out = append(out, map[string]any{"id": t.ID, "name": t.Name, "to": map[string]string{"id": "s-" + t.To, "name": t.To}})
		}
		fjWrite(w, 200, map[string]any{"transitions": out})
	case http.MethodPost:
		var in struct {
			Transition struct {
				ID string `json:"id"`
			} `json:"transition"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			fjError(w, 400, err.Error())
			return
		}
		for _, t := range available {
			if t.ID == in.Transition.ID {
				issue.Status = t.To
				w.WriteHeader(204)
				return
			}
		}
		fjError(w, 400, "Transition id '"+in.Transition.ID+"' is not valid for this issue.")
	default:
		fjError(w, 405, "method not allowed")
	}
}

func (f *fakeJira) createIssue(w http.ResponseWriter, body []byte) {
	var in struct {
		Fields struct {
			Project struct {
				Key string `json:"key"`
			} `json:"project"`
			Summary     string   `json:"summary"`
			Description any      `json:"description"`
			Labels      []string `json:"labels"`
			IssueType   struct {
				Name string `json:"name"`
			} `json:"issuetype"`
			Parent *struct {
				Key string `json:"key"`
			} `json:"parent"`
		} `json:"fields"`
		Properties []struct {
			Key   string          `json:"key"`
			Value json.RawMessage `json:"value"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		fjError(w, 400, err.Error())
		return
	}
	key := in.Fields.Project.Key
	if _, ok := f.projects[key]; !ok {
		fjError(w, 400, "project is required")
		return
	}
	if in.Fields.Summary == "" || in.Fields.IssueType.Name == "" {
		fjError(w, 400, "summary and issuetype are required")
		return
	}
	if len([]rune(in.Fields.Summary)) > 255 {
		fjError(w, 400, "summary: Summary must be less than 255 characters.")
		return
	}
	typeName := in.Fields.IssueType.Name
	parent := ""
	switch {
	case f.subtaskTypes[typeName]:
		if in.Fields.Parent == nil {
			fjError(w, 400, "parent: a sub-task needs a parent")
			return
		}
		p := f.issues[in.Fields.Parent.Key]
		if p == nil || p.Project != key || f.subtaskTypes[p.IssueType] {
			fjError(w, 400, "parent: the parent issue is not valid")
			return
		}
		parent = p.Key
	case f.standardTypes[typeName]:
		if in.Fields.Parent != nil {
			fjError(w, 400, "issuetype: only a sub-task issue type can have a parent")
			return
		}
	default:
		fjError(w, 400, "issuetype: Specify a valid issue type")
		return
	}
	for _, l := range in.Fields.Labels {
		if strings.ContainsAny(l, " \t") {
			fjError(w, 400, "labels: a label cannot contain spaces")
			return
		}
	}
	for _, p := range in.Properties {
		if len(p.Value) > 32768 {
			fjError(w, 400, "The property value is too long")
			return
		}
	}
	f.seq[key]++
	f.issueSeq++
	issue := &fakeIssue{
		ID: strconv.Itoa(10000 + f.issueSeq), Key: fmt.Sprintf("%s-%d", key, f.seq[key]), Project: key,
		IssueType: typeName, Parent: parent, Status: "To Do", Summary: in.Fields.Summary,
		Description: in.Fields.Description, Labels: in.Fields.Labels,
		Properties: map[string]json.RawMessage{},
		// Strictly increasing creation times so ORDER BY created is total.
		Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(len(f.order)) * time.Second),
	}
	for _, p := range in.Properties {
		issue.Properties[p.Key] = p.Value
	}
	f.issues[issue.Key] = issue
	f.order = append(f.order, issue.Key)
	fjWrite(w, 201, map[string]string{"id": issue.ID, "key": issue.Key})
}

// search supports the JQL shapes the plugin sends:
// "project = K" / "project in (A, B)", "labels = x", "labels != x",
// joined by AND, with an optional ORDER BY created ASC|DESC.
func (f *fakeJira) search(w http.ResponseWriter, body []byte) {
	var in struct {
		JQL           string   `json:"jql"`
		Fields        []string `json:"fields"`
		Properties    []string `json:"properties"`
		NextPageToken string   `json:"nextPageToken"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		fjError(w, 400, err.Error())
		return
	}
	jql := in.JQL
	desc := false
	if i := strings.Index(strings.ToUpper(jql), " ORDER BY "); i >= 0 {
		desc = strings.Contains(strings.ToUpper(jql[i:]), "DESC")
		jql = jql[:i]
	}
	var matches []*fakeIssue
	for _, key := range f.order {
		issue, ok := f.issues[key]
		if !ok {
			continue
		}
		if f.matches(issue, jql) {
			matches = append(matches, issue)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if desc {
			return matches[i].Created.After(matches[j].Created)
		}
		return matches[i].Created.Before(matches[j].Created)
	})
	start := 0
	if in.NextPageToken != "" {
		start, _ = strconv.Atoi(strings.TrimPrefix(in.NextPageToken, "page-"))
	}
	end := start + f.pageSize
	if end > len(matches) {
		end = len(matches)
	}
	issues := []any{}
	for _, issue := range matches[start:end] {
		issues = append(issues, f.issueJSON(issue, in.Fields, in.Properties))
	}
	out := map[string]any{"issues": issues, "isLast": end >= len(matches)}
	if end < len(matches) {
		out["nextPageToken"] = "page-" + strconv.Itoa(end)
	}
	fjWrite(w, 200, out)
}

func (f *fakeJira) matches(issue *fakeIssue, jql string) bool {
	for _, clause := range strings.Split(jql, " AND ") {
		clause = strings.TrimSpace(clause)
		switch {
		case strings.HasPrefix(clause, "project in ("):
			list := strings.TrimSuffix(strings.TrimPrefix(clause, "project in ("), ")")
			found := false
			for _, k := range strings.Split(list, ",") {
				if strings.TrimSpace(k) == issue.Project {
					found = true
				}
			}
			if !found {
				return false
			}
		case strings.HasPrefix(clause, "project = "):
			if strings.TrimPrefix(clause, "project = ") != issue.Project {
				return false
			}
		case strings.HasPrefix(clause, "labels != "):
			if containsString(issue.Labels, strings.TrimPrefix(clause, "labels != ")) {
				return false
			}
		case strings.HasPrefix(clause, "labels = "):
			if !containsString(issue.Labels, strings.TrimPrefix(clause, "labels = ")) {
				return false
			}
		default:
			f.t.Errorf("fake jira: unsupported JQL clause %q", clause)
			return false
		}
	}
	return true
}
