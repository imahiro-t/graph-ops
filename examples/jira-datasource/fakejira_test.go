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
	commentSeq  int
	attachments map[string][]byte
	requests    []fakeRequest
	// pageSize is how many issues one search page returns.
	pageSize int
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
	Key         string
	Project     string
	Summary     string
	Description any
	Labels      []string
	Properties  map[string]json.RawMessage
	Created     time.Time
	Comments    []*fakeComment
	Attachments []string
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
	f.seq[project]++
	issue := &fakeIssue{
		Key: fmt.Sprintf("%s-%d", project, f.seq[project]), Project: project, Summary: "seeded",
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
			fjWrite(w, 201, commentJSON(cm, false))
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
			page = append(page, commentJSON(issue.Comments[i], expand))
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
			fjWrite(w, 200, commentJSON(cm, strings.Contains(r.URL.Query().Get("expand"), "properties")))
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
			var props []string
			if q := r.URL.Query().Get("properties"); q != "" {
				props = strings.Split(q, ",")
			}
			fjWrite(w, 200, issueJSON(issue, props))
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
				if l, ok := op["remove"]; ok {
					var kept []string
					for _, x := range issue.Labels {
						if x != l {
							kept = append(kept, x)
						}
					}
					issue.Labels = kept
				}
				if l, ok := op["add"]; ok {
					issue.Labels = append(issue.Labels, l)
				}
			}
			w.WriteHeader(204)
		case http.MethodDelete:
			delete(f.issues, key)
			w.WriteHeader(204)
		}

	default:
		fjError(w, 404, "fake jira: no route for "+r.Method+" "+p)
	}
}

func commentJSON(cm *fakeComment, withProps bool) map[string]any {
	out := map[string]any{"id": cm.ID, "created": cm.Created, "body": cm.Body}
	if withProps {
		props := []map[string]any{}
		for k, v := range cm.Properties {
			props = append(props, map[string]any{"key": k, "value": v})
		}
		out["properties"] = props
	}
	return out
}

func issueJSON(issue *fakeIssue, props []string) map[string]any {
	properties := map[string]json.RawMessage{}
	for _, p := range props {
		if v, ok := issue.Properties[p]; ok {
			properties[p] = v
		}
	}
	return map[string]any{
		"id":  "id-" + issue.Key,
		"key": issue.Key,
		"fields": map[string]any{
			"summary": issue.Summary,
			"labels":  issue.Labels,
			"created": issue.Created.Format("2006-01-02T15:04:05.000-0700"),
		},
		"properties": properties,
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
	f.seq[key]++
	issue := &fakeIssue{
		Key: fmt.Sprintf("%s-%d", key, f.seq[key]), Project: key, Summary: in.Fields.Summary,
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
	fjWrite(w, 201, map[string]string{"id": "id-" + issue.Key, "key": issue.Key})
}

// search supports the JQL shapes the plugin sends:
// "project = K" / "project in (A, B)", "labels = x", "labels != x",
// joined by AND, with an optional ORDER BY created ASC|DESC.
func (f *fakeJira) search(w http.ResponseWriter, body []byte) {
	var in struct {
		JQL           string   `json:"jql"`
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
		issues = append(issues, issueJSON(issue, in.Properties))
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
