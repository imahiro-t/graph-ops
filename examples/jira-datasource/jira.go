package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// jiraClient is a minimal Jira Cloud REST API v3 client: basic auth with an
// account email and API token, JSON in and out, and bounded retries on rate
// limiting (429) and temporary unavailability (503).
//
// Every call takes the context of the protocol request it serves (see
// server.go: the request's own context plus the per-request deadline).
// Attempts, retries and Retry-After waits all stop when that context is
// done, so no Jira call is started after graph-engine has given up on the
// request, and no wait is started that would outlast its deadline.
type jiraClient struct {
	baseURL    string // no trailing slash
	email      string
	apiToken   string
	httpClient *http.Client
	// maxRetries is how many times a 429/503 answer is retried before the
	// error is returned; sleep is how the wait happens (replaced in tests).
	// sleep must return early with ctx.Err() when ctx is done.
	maxRetries int
	sleep      func(ctx context.Context, d time.Duration) error
	// logf, when non-nil, receives one line per retry.
	logf func(format string, args ...any)
}

// jiraAttemptTimeout bounds a single HTTP attempt against Jira. The
// request's context deadline (server.go, requestDeadline) bounds the whole
// operation; this only keeps one stuck attempt from using all of it.
const jiraAttemptTimeout = 20 * time.Second

func newJiraClient(baseURL, email, apiToken string) *jiraClient {
	return &jiraClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		email:      email,
		apiToken:   apiToken,
		httpClient: &http.Client{Timeout: jiraAttemptTimeout},
		maxRetries: 4,
		sleep:      sleepContext,
	}
}

// sleepContext waits for d, or until ctx is done (returning ctx.Err()).
func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// jiraError is a non-2xx answer from Jira. Its message never includes the
// request's credentials.
type jiraError struct {
	Method, Path string
	Status       int
	Body         string
}

func (e *jiraError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 300 {
		body = body[:300] + "..."
	}
	return fmt.Sprintf("jira: %s %s: status %d: %s", e.Method, e.Path, e.Status, body)
}

func isJiraStatus(err error, status int) bool {
	je, ok := err.(*jiraError)
	return ok && je.Status == status
}

// maxRetryWait caps a single Retry-After wait.
const maxRetryWait = 30 * time.Second

func retryAfter(h http.Header) time.Duration {
	if s := h.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n >= 0 {
			d := time.Duration(n) * time.Second
			if d > maxRetryWait {
				d = maxRetryWait
			}
			return d
		}
	}
	return time.Second
}

// send performs one logical request, retrying 429/503 up to maxRetries
// times. body is re-created for each attempt by makeBody.
//
// The request is bound to ctx: once ctx is done (graph-engine disconnected,
// or the per-request deadline passed) no further attempt is made, and a
// Retry-After wait that would end after ctx's deadline is not started at
// all -- the 429/503 is returned straight away instead.
func (c *jiraClient) send(ctx context.Context, method, path string, header http.Header, makeBody func() io.Reader) (*http.Response, []byte, error) {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, fmt.Errorf("jira: %s %s: not sent: %v", method, path, err)
		}
		var body io.Reader
		if makeBody != nil {
			body = makeBody()
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
		if err != nil {
			return nil, nil, err
		}
		req.SetBasicAuth(c.email, c.apiToken)
		req.Header.Set("Accept", "application/json")
		for k, vs := range header {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, nil, fmt.Errorf("jira: %s %s: %v", method, path, err)
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 100<<20))
		resp.Body.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("jira: %s %s: reading response: %v", method, path, err)
		}
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) && attempt < c.maxRetries {
			wait := retryAfter(resp.Header)
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > wait {
				c.logRetry("jira: %s %s: status %d, retrying in %s (retry %d of %d)", method, path, resp.StatusCode, wait, attempt+1, c.maxRetries)
				if err := c.sleep(ctx, wait); err != nil {
					return nil, nil, fmt.Errorf("jira: %s %s: gave up waiting to retry after status %d: %v", method, path, resp.StatusCode, err)
				}
				continue
			}
			c.logRetry("jira: %s %s: status %d, not retrying: a %s wait would pass the request deadline", method, path, resp.StatusCode, wait)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return resp, raw, &jiraError{Method: method, Path: path, Status: resp.StatusCode, Body: string(raw)}
		}
		return resp, raw, nil
	}
}

func (c *jiraClient) logRetry(format string, args ...any) {
	if c.logf != nil {
		c.logf(format, args...)
	}
}

// doJSON sends in (if non-nil) as JSON and decodes a JSON answer into out
// (if non-nil).
func (c *jiraClient) doJSON(ctx context.Context, method, path string, in, out any) error {
	var payload []byte
	header := http.Header{}
	if in != nil {
		var err error
		if payload, err = json.Marshal(in); err != nil {
			return err
		}
		header.Set("Content-Type", "application/json")
	}
	var makeBody func() io.Reader
	if in != nil {
		makeBody = func() io.Reader { return bytes.NewReader(payload) }
	}
	_, raw, err := c.send(ctx, method, path, header, makeBody)
	if err != nil {
		return err
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("jira: %s %s: decoding response: %v", method, path, err)
		}
	}
	return nil
}

// --- endpoints ---

type jiraProject struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

func (c *jiraClient) getProject(ctx context.Context, key string) (*jiraProject, error) {
	var p jiraProject
	if err := c.doJSON(ctx, http.MethodGet, "/rest/api/3/project/"+url.PathEscape(key), nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

type jiraIssue struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Fields struct {
		Summary string   `json:"summary"`
		Labels  []string `json:"labels"`
		Created string   `json:"created"`
		// Status, Parent, Subtasks and IssueType are only filled when the
		// request asked for those fields.
		Status    *jiraStatus    `json:"status,omitempty"`
		Parent    *jiraIssueRef  `json:"parent,omitempty"`
		Subtasks  []jiraIssueRef `json:"subtasks,omitempty"`
		IssueType *struct {
			Name    string `json:"name"`
			Subtask bool   `json:"subtask"`
		} `json:"issuetype,omitempty"`
	} `json:"fields"`
	Properties map[string]json.RawMessage `json:"properties"`
}

type jiraStatus struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// jiraIssueRef is the short form Jira uses for a parent or a sub-task.
type jiraIssueRef struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

// defaultIssueFields is what getIssue and search ask for.
var defaultIssueFields = []string{"summary", "labels", "created"}

type jiraProperty struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// createIssue creates an issue with fields and (atomically, in the same
// request) the given issue properties.
func (c *jiraClient) createIssue(ctx context.Context, fields map[string]any, props []jiraProperty) (string, error) {
	var out struct {
		Key string `json:"key"`
	}
	body := map[string]any{"fields": fields}
	if len(props) > 0 {
		body["properties"] = props
	}
	if err := c.doJSON(ctx, http.MethodPost, "/rest/api/3/issue", body, &out); err != nil {
		return "", err
	}
	return out.Key, nil
}

func (c *jiraClient) getIssue(ctx context.Context, key string, properties []string) (*jiraIssue, error) {
	return c.getIssueFields(ctx, key, defaultIssueFields, properties)
}

// getIssueFields reads one issue with the given fields and properties.
func (c *jiraClient) getIssueFields(ctx context.Context, key string, fields, properties []string) (*jiraIssue, error) {
	q := url.Values{}
	q.Set("fields", strings.Join(fields, ","))
	if len(properties) > 0 {
		q.Set("properties", strings.Join(properties, ","))
	}
	var issue jiraIssue
	if err := c.doJSON(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"?"+q.Encode(), nil, &issue); err != nil {
		return nil, err
	}
	return &issue, nil
}

// editIssue sends PUT /issue/{key} with fields and/or update operations.
func (c *jiraClient) editIssue(ctx context.Context, key string, body map[string]any) error {
	return c.doJSON(ctx, http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(key), body, nil)
}

// bulkFetchChunk is the most issues POST /issue/bulkfetch accepts at once.
const bulkFetchChunk = 100

// bulkFetchIssues reads many issues by key with POST /rest/api/3/issue/bulkfetch
// (bulkFetchChunk per request), with the given fields and properties. Keys
// Jira cannot return (deleted, or not visible) are simply missing from the
// result. Unlike a JQL search it reads the issues themselves, so an issue
// created a moment ago is never missing because the search index lags.
func (c *jiraClient) bulkFetchIssues(ctx context.Context, keys, fields, properties []string) ([]jiraIssue, error) {
	var all []jiraIssue
	for start := 0; start < len(keys); start += bulkFetchChunk {
		end := min(start+bulkFetchChunk, len(keys))
		body := map[string]any{"issueIdsOrKeys": keys[start:end], "fields": fields}
		if len(properties) > 0 {
			body["properties"] = properties
		}
		var out struct {
			Issues []jiraIssue `json:"issues"`
		}
		if err := c.doJSON(ctx, http.MethodPost, "/rest/api/3/issue/bulkfetch", body, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Issues...)
	}
	return all, nil
}

type jiraTransition struct {
	ID   string     `json:"id"`
	Name string     `json:"name"`
	To   jiraStatus `json:"to"`
}

// getTransitions lists the workflow transitions available on an issue now.
func (c *jiraClient) getTransitions(ctx context.Context, key string) ([]jiraTransition, error) {
	var out struct {
		Transitions []jiraTransition `json:"transitions"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"/transitions", nil, &out); err != nil {
		return nil, err
	}
	return out.Transitions, nil
}

// doTransition moves an issue through one workflow transition.
func (c *jiraClient) doTransition(ctx context.Context, key, transitionID string) error {
	return c.doJSON(ctx, http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(key)+"/transitions",
		map[string]any{"transition": map[string]string{"id": transitionID}}, nil)
}

func (c *jiraClient) deleteIssue(ctx context.Context, key string) error {
	return c.doJSON(ctx, http.MethodDelete, "/rest/api/3/issue/"+url.PathEscape(key), nil, nil)
}

func (c *jiraClient) setIssueProperty(ctx context.Context, key, prop string, value []byte) error {
	header := http.Header{"Content-Type": {"application/json"}}
	_, _, err := c.send(ctx, http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(key)+"/properties/"+url.PathEscape(prop),
		header, func() io.Reader { return bytes.NewReader(value) })
	return err
}

func (c *jiraClient) deleteIssueProperty(ctx context.Context, key, prop string) error {
	err := c.doJSON(ctx, http.MethodDelete, "/rest/api/3/issue/"+url.PathEscape(key)+"/properties/"+url.PathEscape(prop), nil, nil)
	if isJiraStatus(err, http.StatusNotFound) {
		return nil
	}
	return err
}

type jiraSearchPage struct {
	Issues        []jiraIssue `json:"issues"`
	NextPageToken string      `json:"nextPageToken"`
	IsLast        bool        `json:"isLast"`
}

// search runs a JQL query through the enhanced search API
// (/rest/api/3/search/jql), following nextPageToken until the last page, and
// fetches the given issue properties with the results in the same requests
// (no per-issue follow-up calls).
func (c *jiraClient) search(ctx context.Context, jql string, properties []string) ([]jiraIssue, error) {
	var all []jiraIssue
	token := ""
	for page := 0; page < 1000; page++ {
		body := map[string]any{
			"jql":        jql,
			"fields":     defaultIssueFields,
			"properties": properties,
			"maxResults": 100,
		}
		if token != "" {
			body["nextPageToken"] = token
		}
		var out jiraSearchPage
		if err := c.doJSON(ctx, http.MethodPost, "/rest/api/3/search/jql", body, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Issues...)
		if out.IsLast || out.NextPageToken == "" {
			return all, nil
		}
		token = out.NextPageToken
	}
	return nil, fmt.Errorf("jira: search did not finish after 1000 pages")
}

type jiraComment struct {
	ID      string `json:"id"`
	Created string `json:"created"`
	// Self is the comment's REST URL, ".../issue/<issue id>/comment/<id>":
	// the only place a comment read by ID says which issue it is on.
	Self       string         `json:"self"`
	Properties []jiraProperty `json:"properties"`
}

// issueIDOfCommentSelf extracts the issue ID (or key) from a comment's self
// URL, or "" when self does not have the expected shape.
func issueIDOfCommentSelf(self string) string {
	const marker = "/issue/"
	i := strings.LastIndex(self, marker)
	if i < 0 {
		return ""
	}
	rest := self[i+len(marker):]
	j := strings.Index(rest, "/comment/")
	if j <= 0 {
		return ""
	}
	return rest[:j]
}

// commentProperty returns the raw value of a comment property, if present.
func (cm jiraComment) property(key string) (json.RawMessage, bool) {
	for _, p := range cm.Properties {
		if p.Key == key {
			raw, err := json.Marshal(p.Value)
			if err != nil {
				return nil, false
			}
			return raw, true
		}
	}
	return nil, false
}

func (c *jiraClient) addComment(ctx context.Context, issueKey string, body any, props []jiraProperty) (*jiraComment, error) {
	var out jiraComment
	in := map[string]any{"body": body}
	if len(props) > 0 {
		in["properties"] = props
	}
	if err := c.doJSON(ctx, http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(issueKey)+"/comment", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *jiraClient) getComment(ctx context.Context, issueKey, id string) (*jiraComment, error) {
	var out jiraComment
	path := "/rest/api/3/issue/" + url.PathEscape(issueKey) + "/comment/" + url.PathEscape(id) + "?expand=properties"
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// listComments returns every comment of an issue, oldest first, with their
// properties.
func (c *jiraClient) listComments(ctx context.Context, issueKey string) ([]jiraComment, error) {
	var all []jiraComment
	for startAt := 0; ; {
		var out struct {
			Comments   []jiraComment `json:"comments"`
			Total      int           `json:"total"`
			StartAt    int           `json:"startAt"`
			MaxResults int           `json:"maxResults"`
		}
		path := fmt.Sprintf("/rest/api/3/issue/%s/comment?startAt=%d&maxResults=100&orderBy=created&expand=properties",
			url.PathEscape(issueKey), startAt)
		if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Comments...)
		startAt += len(out.Comments)
		if len(out.Comments) == 0 || startAt >= out.Total {
			return all, nil
		}
	}
}

// commentListChunk is the most comment IDs POST /comment/list accepts at once.
const commentListChunk = 1000

// listCommentsByIDs reads comments by ID, wherever they are, with
// POST /rest/api/3/comment/list?expand=properties (commentListChunk IDs per
// request, following its pages). IDs that are not numbers are skipped, and
// comments Jira cannot return are missing from the result. Callers must
// check each comment's Self before trusting which issue it belongs to.
func (c *jiraClient) listCommentsByIDs(ctx context.Context, ids []string) ([]jiraComment, error) {
	var nums []int64
	for _, id := range ids {
		if n, err := strconv.ParseInt(id, 10, 64); err == nil {
			nums = append(nums, n)
		}
	}
	var all []jiraComment
	for start := 0; start < len(nums); start += commentListChunk {
		chunk := nums[start:min(start+commentListChunk, len(nums))]
		for startAt, page := 0, 0; page < 100; page++ {
			var out struct {
				Values []jiraComment `json:"values"`
				Total  int           `json:"total"`
				IsLast bool          `json:"isLast"`
			}
			path := fmt.Sprintf("/rest/api/3/comment/list?expand=properties&startAt=%d&maxResults=%d", startAt, commentListChunk)
			if err := c.doJSON(ctx, http.MethodPost, path, map[string]any{"ids": chunk}, &out); err != nil {
				return nil, err
			}
			all = append(all, out.Values...)
			startAt += len(out.Values)
			if out.IsLast || len(out.Values) == 0 || startAt >= out.Total {
				break
			}
		}
	}
	return all, nil
}

func (c *jiraClient) deleteComment(ctx context.Context, issueKey, id string) error {
	err := c.doJSON(ctx, http.MethodDelete, "/rest/api/3/issue/"+url.PathEscape(issueKey)+"/comment/"+url.PathEscape(id), nil, nil)
	if isJiraStatus(err, http.StatusNotFound) {
		return nil
	}
	return err
}

// addAttachment uploads one file to an issue and returns its attachment ID.
func (c *jiraClient) addAttachment(ctx context.Context, issueKey, filename string, data []byte) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	payload := buf.Bytes()
	header := http.Header{
		"Content-Type":      {w.FormDataContentType()},
		"X-Atlassian-Token": {"no-check"},
	}
	_, raw, err := c.send(ctx, http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(issueKey)+"/attachments", header,
		func() io.Reader { return bytes.NewReader(payload) })
	if err != nil {
		return "", err
	}
	var out []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out) == 0 {
		return "", fmt.Errorf("jira: unexpected attachment response")
	}
	return out[0].ID, nil
}

// attachmentContent downloads an attachment's bytes (Jira may redirect to
// its media host; the standard client follows that and drops the
// Authorization header when the host changes).
func (c *jiraClient) attachmentContent(ctx context.Context, id string) ([]byte, error) {
	_, raw, err := c.send(ctx, http.MethodGet, "/rest/api/3/attachment/content/"+url.PathEscape(id), nil, nil)
	return raw, err
}

func (c *jiraClient) deleteAttachment(ctx context.Context, id string) error {
	err := c.doJSON(ctx, http.MethodDelete, "/rest/api/3/attachment/"+url.PathEscape(id), nil, nil)
	if isJiraStatus(err, http.StatusNotFound) {
		return nil
	}
	return err
}
