package main

import (
	"bytes"
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
type jiraClient struct {
	baseURL    string // no trailing slash
	email      string
	apiToken   string
	httpClient *http.Client
	// maxRetries is how many times a 429/503 answer is retried before the
	// error is returned; sleep is how the wait happens (replaced in tests).
	maxRetries int
	sleep      func(time.Duration)
}

func newJiraClient(baseURL, email, apiToken string) *jiraClient {
	return &jiraClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		email:      email,
		apiToken:   apiToken,
		httpClient: &http.Client{Timeout: 60 * time.Second},
		maxRetries: 4,
		sleep:      time.Sleep,
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
func (c *jiraClient) send(method, path string, header http.Header, makeBody func() io.Reader) (*http.Response, []byte, error) {
	for attempt := 0; ; attempt++ {
		var body io.Reader
		if makeBody != nil {
			body = makeBody()
		}
		req, err := http.NewRequest(method, c.baseURL+path, body)
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
			c.sleep(retryAfter(resp.Header))
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return resp, raw, &jiraError{Method: method, Path: path, Status: resp.StatusCode, Body: string(raw)}
		}
		return resp, raw, nil
	}
}

// doJSON sends in (if non-nil) as JSON and decodes a JSON answer into out
// (if non-nil).
func (c *jiraClient) doJSON(method, path string, in, out any) error {
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
	_, raw, err := c.send(method, path, header, makeBody)
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

func (c *jiraClient) getProject(key string) (*jiraProject, error) {
	var p jiraProject
	if err := c.doJSON(http.MethodGet, "/rest/api/3/project/"+url.PathEscape(key), nil, &p); err != nil {
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
	} `json:"fields"`
	Properties map[string]json.RawMessage `json:"properties"`
}

type jiraProperty struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// createIssue creates an issue with fields and (atomically, in the same
// request) the given issue properties.
func (c *jiraClient) createIssue(fields map[string]any, props []jiraProperty) (string, error) {
	var out struct {
		Key string `json:"key"`
	}
	body := map[string]any{"fields": fields}
	if len(props) > 0 {
		body["properties"] = props
	}
	if err := c.doJSON(http.MethodPost, "/rest/api/3/issue", body, &out); err != nil {
		return "", err
	}
	return out.Key, nil
}

func (c *jiraClient) getIssue(key string, properties []string) (*jiraIssue, error) {
	q := url.Values{}
	q.Set("fields", "summary,labels,created")
	if len(properties) > 0 {
		q.Set("properties", strings.Join(properties, ","))
	}
	var issue jiraIssue
	if err := c.doJSON(http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"?"+q.Encode(), nil, &issue); err != nil {
		return nil, err
	}
	return &issue, nil
}

// editIssue sends PUT /issue/{key} with fields and/or update operations.
func (c *jiraClient) editIssue(key string, body map[string]any) error {
	return c.doJSON(http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(key), body, nil)
}

func (c *jiraClient) deleteIssue(key string) error {
	return c.doJSON(http.MethodDelete, "/rest/api/3/issue/"+url.PathEscape(key), nil, nil)
}

func (c *jiraClient) setIssueProperty(key, prop string, value []byte) error {
	header := http.Header{"Content-Type": {"application/json"}}
	_, _, err := c.send(http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(key)+"/properties/"+url.PathEscape(prop),
		header, func() io.Reader { return bytes.NewReader(value) })
	return err
}

func (c *jiraClient) deleteIssueProperty(key, prop string) error {
	err := c.doJSON(http.MethodDelete, "/rest/api/3/issue/"+url.PathEscape(key)+"/properties/"+url.PathEscape(prop), nil, nil)
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
func (c *jiraClient) search(jql string, properties []string) ([]jiraIssue, error) {
	var all []jiraIssue
	token := ""
	for page := 0; page < 1000; page++ {
		body := map[string]any{
			"jql":        jql,
			"fields":     []string{"summary", "labels", "created"},
			"properties": properties,
			"maxResults": 100,
		}
		if token != "" {
			body["nextPageToken"] = token
		}
		var out jiraSearchPage
		if err := c.doJSON(http.MethodPost, "/rest/api/3/search/jql", body, &out); err != nil {
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
	ID         string         `json:"id"`
	Created    string         `json:"created"`
	Properties []jiraProperty `json:"properties"`
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

func (c *jiraClient) addComment(issueKey string, body any, props []jiraProperty) (*jiraComment, error) {
	var out jiraComment
	in := map[string]any{"body": body}
	if len(props) > 0 {
		in["properties"] = props
	}
	if err := c.doJSON(http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(issueKey)+"/comment", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *jiraClient) getComment(issueKey, id string) (*jiraComment, error) {
	var out jiraComment
	path := "/rest/api/3/issue/" + url.PathEscape(issueKey) + "/comment/" + url.PathEscape(id) + "?expand=properties"
	if err := c.doJSON(http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// listComments returns every comment of an issue, oldest first, with their
// properties.
func (c *jiraClient) listComments(issueKey string) ([]jiraComment, error) {
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
		if err := c.doJSON(http.MethodGet, path, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Comments...)
		startAt += len(out.Comments)
		if len(out.Comments) == 0 || startAt >= out.Total {
			return all, nil
		}
	}
}

func (c *jiraClient) deleteComment(issueKey, id string) error {
	err := c.doJSON(http.MethodDelete, "/rest/api/3/issue/"+url.PathEscape(issueKey)+"/comment/"+url.PathEscape(id), nil, nil)
	if isJiraStatus(err, http.StatusNotFound) {
		return nil
	}
	return err
}

// addAttachment uploads one file to an issue and returns its attachment ID.
func (c *jiraClient) addAttachment(issueKey, filename string, data []byte) (string, error) {
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
	_, raw, err := c.send(http.MethodPost, "/rest/api/3/issue/"+url.PathEscape(issueKey)+"/attachments", header,
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
func (c *jiraClient) attachmentContent(id string) ([]byte, error) {
	_, raw, err := c.send(http.MethodGet, "/rest/api/3/attachment/content/"+url.PathEscape(id), nil, nil)
	return raw, err
}

func (c *jiraClient) deleteAttachment(id string) error {
	err := c.doJSON(http.MethodDelete, "/rest/api/3/attachment/"+url.PathEscape(id), nil, nil)
	if isJiraStatus(err, http.StatusNotFound) {
		return nil
	}
	return err
}
