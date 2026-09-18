package store

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
)

// HTTP custom data source (DFLT-00088): dbBackend "http" hands every
// GraphRepository operation to a user-supplied HTTP server ("plugin") that
// speaks the protocol published in docs/http-datasource/openapi.yaml. That
// file is the source of truth for the wire format; this file is the
// graph-engine side of it.

const (
	// HTTPDataSourceProtocolName is what GET /protocol's "protocol" field must
	// say for graph-engine to accept the server as a GraphOps data source.
	HTTPDataSourceProtocolName = "graph-ops-datasource"
	// HTTPDataSourceProtocolVersion is the protocol version this build speaks
	// (MAJOR.MINOR). It is also openapi.yaml's info.version -- a test keeps the
	// two equal. A plugin whose MAJOR differs is refused at startup; a
	// different MINOR is accepted (minor versions only add optional things).
	HTTPDataSourceProtocolVersion = "1.0"
	// HTTPDataSourceProtocolHeader carries HTTPDataSourceProtocolVersion on
	// every request, so a plugin can adapt to (or refuse) an older client.
	HTTPDataSourceProtocolHeader = "GraphOps-Protocol-Version"

	httpDataSourceMajor = 1

	defaultHTTPDataSourceTimeout = 30 * time.Second
	// defaultHTTPDataSourceMaxResponseBytes caps how much of one response
	// body is read into memory. GetArtifact returns image bytes base64-encoded
	// inline, so the cap is generous, but a misbehaving plugin can never make
	// graph-engine read an unbounded body.
	defaultHTTPDataSourceMaxResponseBytes = 64 << 20
)

// ValidateBackend checks a dbBackend value. "" (meaning the sqlite default),
// "sqlite", "mysql" and "http" are accepted; anything else is an error. This
// is the single definition of the accepted set: cmd/graph-engine's
// loadRuntimeConfig, the Web UI's settings API and Open all use it.
func ValidateBackend(name string) error {
	switch name {
	case "", "sqlite", "mysql", "http":
		return nil
	default:
		return fmt.Errorf(`unsupported db backend %q (must be "sqlite", "mysql" or "http")`, name)
	}
}

// ValidateHTTPDataSourceSettings checks an HTTP data source URL and its
// (already resolved) bearer token against the transport security rules:
//
//   - The URL must be an absolute http:// or https:// URL with a host, and
//     must carry no userinfo, query or fragment.
//   - Plaintext http:// is allowed only when the host is loopback
//     (runtimeconfig.IsLoopbackHost: "localhost", 127.0.0.0/8, ::1).
//   - A non-loopback (therefore https://) data source requires a token:
//     anything reachable from the network must authenticate the caller.
//
// A "${ENV_VAR}" reference that has not been resolved yet counts as a
// non-empty token here -- the settings API validates before resolving,
// graph-engine's startup after.
func ValidateHTTPDataSourceSettings(rawURL, token string) error {
	if strings.TrimSpace(rawURL) == "" {
		return errors.New("httpDataSourceUrl is required when dbBackend is \"http\"")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("httpDataSourceUrl is not a valid URL: %v", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("httpDataSourceUrl must start with https:// (or http:// for a loopback address), got scheme %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return errors.New("httpDataSourceUrl must include a host")
	}
	if u.User != nil {
		return errors.New("httpDataSourceUrl must not contain user information; use httpDataSourceToken for credentials")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return errors.New("httpDataSourceUrl must not contain a query string")
	}
	if u.Fragment != "" || strings.Contains(rawURL, "#") {
		return errors.New("httpDataSourceUrl must not contain a fragment")
	}
	loopback := runtimeconfig.IsLoopbackHost(u.Hostname())
	if scheme == "http" && !loopback {
		return fmt.Errorf("plaintext http:// is only allowed for a loopback data source (localhost, 127.0.0.0/8, ::1); use https:// for host %q", u.Hostname())
	}
	if !loopback && token == "" {
		return fmt.Errorf("a remote data source (host %q) requires a bearer token: set httpDataSourceToken", u.Hostname())
	}
	return nil
}

// NormalizeHTTPDataSourceURL returns the canonical form of rawURL used to
// decide whether two URLs name the same destination: scheme and host
// lower-cased, the default port made explicit, and trailing slashes removed
// from the path. An unparsable URL is returned unchanged (it can never equal
// a valid stored one, and validation rejects it anyway).
func NormalizeHTTPDataSourceURL(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return rawURL
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		switch scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	hostPort := host
	if strings.Contains(host, ":") {
		hostPort = "[" + host + "]"
	}
	if port != "" {
		hostPort += ":" + port
	}
	return scheme + "://" + hostPort + strings.TrimRight(u.EscapedPath(), "/")
}

// HTTPRepository implements GraphRepository by calling an HTTP data source
// plugin. Every method is one request; the plugin must process each request
// atomically (see openapi.yaml), since there is no cross-request transaction.
type HTTPRepository struct {
	baseURL          string // no trailing slash
	token            string
	client           *http.Client
	maxResponseBytes int64
}

var _ GraphRepository = (*HTTPRepository)(nil)

// newHTTPDataSourceClient builds the client used for every request: TLS
// certificates are always verified (no InsecureSkipVerify, TLS 1.2 or newer),
// redirects are never followed (a redirect could downgrade https to http or
// hand the bearer token to another host), and there is no plaintext fallback
// of any kind -- a failed TLS handshake is just an error.
func newHTTPDataSourceClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if timeout <= 0 {
		timeout = defaultHTTPDataSourceTimeout
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// NewHTTPRepository validates cfg's HTTP data source settings and performs
// the protocol handshake (GET /protocol). It does not call Init; Open does.
func NewHTTPRepository(cfg Config) (*HTTPRepository, error) {
	if err := ValidateHTTPDataSourceSettings(cfg.HTTPURL, cfg.HTTPToken); err != nil {
		return nil, err
	}
	r := &HTTPRepository{
		baseURL:          strings.TrimRight(strings.TrimSpace(cfg.HTTPURL), "/"),
		token:            cfg.HTTPToken,
		client:           newHTTPDataSourceClient(cfg.HTTPTimeout),
		maxResponseBytes: defaultHTTPDataSourceMaxResponseBytes,
	}
	if err := r.handshake(); err != nil {
		return nil, err
	}
	return r, nil
}

type protocolInfo struct {
	Protocol string `json:"protocol"`
	Version  string `json:"version"`
}

// handshake checks that the server is a GraphOps data source speaking a
// compatible protocol major version. The token is sent here too, so a wrong
// token is reported at startup rather than on the first real operation.
func (r *HTTPRepository) handshake() error {
	var info protocolInfo
	err := r.do(http.MethodGet, "/protocol", nil, &info)
	if err != nil {
		if errors.Is(err, errHTTPDataSourceUnauthorized) {
			return err
		}
		var apiErr *domain.APIError
		var statusErr *httpStatusError
		if errors.As(err, &apiErr) || errors.As(err, &statusErr) {
			return fmt.Errorf("%s does not look like a GraphOps data source (GET /protocol failed: %v)", r.baseURL, err)
		}
		var decodeErr *httpDecodeError
		if errors.As(err, &decodeErr) {
			return fmt.Errorf("%s does not look like a GraphOps data source (GET /protocol did not return protocol JSON)", r.baseURL)
		}
		return err
	}
	if info.Protocol != HTTPDataSourceProtocolName {
		return fmt.Errorf("%s does not look like a GraphOps data source: GET /protocol reported protocol %q, want %q",
			r.baseURL, info.Protocol, HTTPDataSourceProtocolName)
	}
	major, ok := protocolMajor(info.Version)
	if !ok || major != httpDataSourceMajor {
		return fmt.Errorf("incompatible data source protocol: server speaks %s, graph-engine requires %d.x", info.Version, httpDataSourceMajor)
	}
	return nil
}

func protocolMajor(version string) (int, bool) {
	majorStr, _, ok := strings.Cut(version, ".")
	if !ok {
		return 0, false
	}
	major, err := strconv.Atoi(majorStr)
	if err != nil {
		return 0, false
	}
	return major, true
}

// errHTTPDataSourceUnauthorized marks a 401/403 answer: the plugin rejected
// the bearer token.
var errHTTPDataSourceUnauthorized = errors.New("the HTTP data source rejected the bearer token")

// httpStatusError is the generic error for a non-2xx answer that carries no
// recognized error code.
type httpStatusError struct {
	method, path string
	status       int
	detail       string
}

func (e *httpStatusError) Error() string {
	msg := fmt.Sprintf("http data source: %s %s: status %d", e.method, e.path, e.status)
	if e.detail != "" {
		msg += ": " + e.detail
	}
	return msg
}

// httpDecodeError is a 2xx answer whose body is not the expected JSON.
type httpDecodeError struct {
	method, path string
	err          error
}

func (e *httpDecodeError) Error() string {
	return fmt.Sprintf("http data source: %s %s: decoding response: %v", e.method, e.path, e.err)
}

// knownHTTPDataSourceErrorCodes are the error codes a plugin may return that
// graph-engine maps onto its own domain errors (openapi.yaml lists the same
// set under ErrorResponse).
var knownHTTPDataSourceErrorCodes = map[domain.ErrorCode]bool{
	domain.ErrCodeTicketNotFound:    true,
	domain.ErrCodeNodeNotFound:      true,
	domain.ErrCodeArtifactNotFound:  true,
	domain.ErrCodeProjectNotFound:   true,
	domain.ErrCodeLabelNotFound:     true,
	domain.ErrCodeLabelNameTaken:    true,
	domain.ErrCodeInvalidLabelName:  true,
	domain.ErrCodeInvalidLabelColor: true,
	domain.ErrCodeInvalidPrefix:     true,
	domain.ErrCodePrefixTaken:       true,
	domain.ErrCodeValidation:        true,
	domain.ErrCodeInternal:          true,
}

type httpErrorBody struct {
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// mapHTTPError turns a non-2xx answer into an error. The rule (also written
// in openapi.yaml's ErrorResponse description):
//
//  1. 401 or 403: the token was rejected (errHTTPDataSourceUnauthorized),
//     whatever the body says.
//  2. Otherwise, if the body is {"error":{"code":C,"message":M}} and C is one
//     of knownHTTPDataSourceErrorCodes, the result is *domain.APIError{C, M}
//     -- regardless of the status code, so e.g. 500 + INTERNAL_ERROR is an
//     APIError too. The HTTP layer (httpserver.writeError) then answers the
//     Web UI with that same code.
//  3. Anything else -- an unknown code, a body that is not that JSON, an
//     empty body, any status -- is a generic *httpStatusError naming the
//     method, path and status.
func (r *HTTPRepository) mapHTTPError(method, path string, status int, body []byte) error {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return fmt.Errorf("%w (%s %s: status %d); check httpDataSourceToken", errHTTPDataSourceUnauthorized, method, path, status)
	}
	var parsed httpErrorBody
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error != nil {
		code := domain.ErrorCode(parsed.Error.Code)
		if knownHTTPDataSourceErrorCodes[code] {
			return &domain.APIError{Code: code, Message: r.redact(parsed.Error.Message)}
		}
		return &httpStatusError{method: method, path: path, status: status,
			detail: r.redact(fmt.Sprintf("unrecognized error code %q: %s", parsed.Error.Code, truncate(parsed.Error.Message, 200)))}
	}
	detail := strings.TrimSpace(string(body))
	if detail != "" {
		detail = r.redact("body: " + truncate(detail, 200))
	}
	return &httpStatusError{method: method, path: path, status: status, detail: detail}
}

// redact removes the bearer token from text that came from the plugin, so
// an error message can never carry it even if a plugin echoes it back.
//
// Tokens shorter than minRedactableTokenLen are left alone: replacing every
// occurrence of, say, "t" would mangle the message without protecting
// anything (such a token is no secret), and a remote data source's token is
// expected to be a long random string anyway.
func (r *HTTPRepository) redact(s string) string {
	if len(r.token) < minRedactableTokenLen {
		return s
	}
	return strings.ReplaceAll(s, r.token, "[redacted]")
}

const minRedactableTokenLen = 4

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// do sends one request and decodes a 2xx JSON answer into out (when out is
// non-nil and the body is non-empty). path must already be escaped.
func (r *HTTPRepository) do(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("http data source: %s %s: encoding request: %w", method, path, err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, r.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("http data source: %s %s: %w", method, path, err)
	}
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(HTTPDataSourceProtocolHeader, HTTPDataSourceProtocolVersion)
	req.Header.Set("User-Agent", "graph-engine (graph-ops-datasource/"+HTTPDataSourceProtocolVersion+")")

	resp, err := r.client.Do(req)
	if err != nil {
		// *url.Error names the URL (never the headers), so the token cannot
		// appear here; redact anyway in case a proxy error echoes something.
		return errors.New(r.redact(fmt.Sprintf("http data source: %s %s: %v", method, path, err)))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, r.maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("http data source: %s %s: reading response: %w", method, path, err)
	}
	if int64(len(raw)) > r.maxResponseBytes {
		return fmt.Errorf("http data source: %s %s: response body exceeds the %d byte limit", method, path, r.maxResponseBytes)
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return &httpStatusError{method: method, path: path, status: resp.StatusCode, detail: "redirects are not followed"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return r.mapHTTPError(method, path, resp.StatusCode, raw)
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return &httpDecodeError{method: method, path: path, err: err}
		}
	}
	return nil
}

// isNotFoundCode reports whether err is a *domain.APIError with code -- a
// Get operation's "does not exist" answer, which GraphRepository expresses
// as (nil, nil) rather than an error.
func isNotFoundCode(err error, code domain.ErrorCode) bool {
	var apiErr *domain.APIError
	return errors.As(err, &apiErr) && apiErr.Code == code
}

func esc(id string) string { return url.PathEscape(id) }

// --- wire bodies ---

// ticketPatchBody encodes a TicketPatch with the protocol's three-state
// rules: a key that is absent means "leave unchanged"; for assignee, null
// means "clear"; for label_ids, an array (even an empty one) replaces the set.
func ticketPatchBody(p TicketPatch) map[string]any {
	m := map[string]any{}
	if p.Title != nil {
		m["title"] = *p.Title
	}
	if p.Description != nil {
		m["description"] = *p.Description
	}
	if p.Status != nil {
		m["status"] = *p.Status
	}
	if p.AutoExecutable != nil {
		m["auto_executable"] = *p.AutoExecutable
	}
	if p.Blocked != nil {
		m["blocked"] = *p.Blocked
	}
	if p.RefinedAt != nil {
		m["refined_at"] = *p.RefinedAt
	}
	if p.ClosedReason != nil {
		m["closed_reason"] = *p.ClosedReason
	}
	if p.Assignee != nil {
		if *p.Assignee == nil {
			m["assignee"] = nil
		} else {
			m["assignee"] = **p.Assignee
		}
	}
	if p.GraphExpandedAt != nil {
		m["graph_expanded_at"] = *p.GraphExpandedAt
	}
	if p.Priority != nil {
		m["priority"] = *p.Priority
	}
	if p.LabelIDs != nil {
		ids := *p.LabelIDs
		if ids == nil {
			ids = []string{}
		}
		m["label_ids"] = ids
	}
	return m
}

// nodePatchBody encodes a NodePatch; see ticketPatchBody for the rules.
func nodePatchBody(p NodePatch) map[string]any {
	m := map[string]any{}
	if p.Name != nil {
		m["name"] = *p.Name
	}
	if p.Type != nil {
		m["type"] = *p.Type
	}
	if p.Status != nil {
		m["status"] = *p.Status
	}
	if p.IterationCount != nil {
		m["iteration_count"] = *p.IterationCount
	}
	if p.MaxIterations != nil {
		m["max_iterations"] = *p.MaxIterations
	}
	if p.Assignee != nil {
		if *p.Assignee == nil {
			m["assignee"] = nil
		} else {
			m["assignee"] = **p.Assignee
		}
	}
	if p.IsManual != nil {
		m["is_manual"] = *p.IsManual
	}
	if p.GateID != nil {
		m["gate_id"] = *p.GateID
	}
	if p.Criteria != nil {
		m["criteria"] = *p.Criteria
	}
	return m
}

// normalizeTicket guarantees the non-nil Labels slice the other backends
// always return (it serializes as [] rather than null).
func normalizeTicket(t *domain.Ticket) {
	if t.Labels == nil {
		t.Labels = []domain.Label{}
	}
}

// --- GraphRepository ---

func (r *HTTPRepository) Init() error {
	return r.do(http.MethodPost, "/init", map[string]any{}, nil)
}

func (r *HTTPRepository) CreateTicket(projectID string, t domain.Ticket) (domain.Ticket, error) {
	if t.Labels == nil {
		t.Labels = []domain.Label{}
	}
	var out domain.Ticket
	if err := r.do(http.MethodPost, "/projects/"+esc(projectID)+"/tickets", t, &out); err != nil {
		return domain.Ticket{}, err
	}
	normalizeTicket(&out)
	return out, nil
}

func (r *HTTPRepository) GetTicket(id string) (*domain.Ticket, error) {
	var out domain.Ticket
	if err := r.do(http.MethodGet, "/tickets/"+esc(id), nil, &out); err != nil {
		if isNotFoundCode(err, domain.ErrCodeTicketNotFound) {
			return nil, nil
		}
		return nil, err
	}
	normalizeTicket(&out)
	return &out, nil
}

func (r *HTTPRepository) GetTicketDetail(id string) (*domain.TicketDetail, error) {
	var out domain.TicketDetail
	if err := r.do(http.MethodGet, "/tickets/"+esc(id)+"/detail", nil, &out); err != nil {
		if isNotFoundCode(err, domain.ErrCodeTicketNotFound) {
			return nil, nil
		}
		return nil, err
	}
	normalizeTicket(&out.Ticket)
	if out.Nodes == nil {
		out.Nodes = []domain.GraphNode{}
	}
	if out.Edges == nil {
		out.Edges = []domain.GraphEdge{}
	}
	if out.Artifacts == nil {
		out.Artifacts = []domain.Artifact{}
	}
	return &out, nil
}

func (r *HTTPRepository) listTickets(path string) ([]domain.Ticket, error) {
	out := []domain.Ticket{}
	if err := r.do(http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []domain.Ticket{}
	}
	for i := range out {
		normalizeTicket(&out[i])
	}
	return out, nil
}

func (r *HTTPRepository) ListTickets() ([]domain.Ticket, error) {
	return r.listTickets("/tickets")
}

func (r *HTTPRepository) ListTicketsByProject(projectID string) ([]domain.Ticket, error) {
	return r.listTickets("/projects/" + esc(projectID) + "/tickets")
}

func (r *HTTPRepository) UpdateTicket(id string, patch TicketPatch) (domain.Ticket, error) {
	var out domain.Ticket
	if err := r.do(http.MethodPatch, "/tickets/"+esc(id), ticketPatchBody(patch), &out); err != nil {
		return domain.Ticket{}, err
	}
	normalizeTicket(&out)
	return out, nil
}

func (r *HTTPRepository) DeleteTicket(id string) error {
	return r.do(http.MethodDelete, "/tickets/"+esc(id), nil, nil)
}

func (r *HTTPRepository) CreateNode(n domain.GraphNode) (domain.GraphNode, error) {
	var out domain.GraphNode
	if err := r.do(http.MethodPost, "/tickets/"+esc(n.TicketID)+"/nodes", n, &out); err != nil {
		return domain.GraphNode{}, err
	}
	return out, nil
}

func (r *HTTPRepository) GetNode(id string) (*domain.GraphNode, error) {
	var out domain.GraphNode
	if err := r.do(http.MethodGet, "/nodes/"+esc(id), nil, &out); err != nil {
		if isNotFoundCode(err, domain.ErrCodeNodeNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func (r *HTTPRepository) ListNodesByTicket(ticketID string) ([]domain.GraphNode, error) {
	out := []domain.GraphNode{}
	if err := r.do(http.MethodGet, "/tickets/"+esc(ticketID)+"/nodes", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []domain.GraphNode{}
	}
	return out, nil
}

func (r *HTTPRepository) UpdateNode(id string, patch NodePatch) (domain.GraphNode, error) {
	var out domain.GraphNode
	if err := r.do(http.MethodPatch, "/nodes/"+esc(id), nodePatchBody(patch), &out); err != nil {
		return domain.GraphNode{}, err
	}
	return out, nil
}

func (r *HTTPRepository) DeleteNode(id string) error {
	return r.do(http.MethodDelete, "/nodes/"+esc(id), nil, nil)
}

func (r *HTTPRepository) CreateEdge(e domain.GraphEdge) (domain.GraphEdge, error) {
	var out domain.GraphEdge
	if err := r.do(http.MethodPost, "/tickets/"+esc(e.TicketID)+"/edges", e, &out); err != nil {
		return domain.GraphEdge{}, err
	}
	return out, nil
}

func (r *HTTPRepository) ListEdgesByTicket(ticketID string) ([]domain.GraphEdge, error) {
	out := []domain.GraphEdge{}
	if err := r.do(http.MethodGet, "/tickets/"+esc(ticketID)+"/edges", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []domain.GraphEdge{}
	}
	return out, nil
}

func (r *HTTPRepository) ClearEdgesByTicket(ticketID string) error {
	return r.do(http.MethodDelete, "/tickets/"+esc(ticketID)+"/edges", nil, nil)
}

func (r *HTTPRepository) CreateArtifact(a domain.Artifact) (domain.Artifact, error) {
	var out domain.Artifact
	if err := r.do(http.MethodPost, "/tickets/"+esc(a.TicketID)+"/artifacts", a, &out); err != nil {
		return domain.Artifact{}, err
	}
	return out, nil
}

func (r *HTTPRepository) GetArtifact(id string) (*domain.Artifact, error) {
	var out domain.Artifact
	if err := r.do(http.MethodGet, "/artifacts/"+esc(id), nil, &out); err != nil {
		if isNotFoundCode(err, domain.ErrCodeArtifactNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func (r *HTTPRepository) listArtifacts(path string) ([]domain.Artifact, error) {
	out := []domain.Artifact{}
	if err := r.do(http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []domain.Artifact{}
	}
	return out, nil
}

func (r *HTTPRepository) ListArtifactsByTicket(ticketID string) ([]domain.Artifact, error) {
	return r.listArtifacts("/tickets/" + esc(ticketID) + "/artifacts")
}

func (r *HTTPRepository) ListArtifactsByNode(nodeID string) ([]domain.Artifact, error) {
	return r.listArtifacts("/nodes/" + esc(nodeID) + "/artifacts")
}

func (r *HTTPRepository) CreateProject(name, prefix string) (domain.Project, error) {
	var out domain.Project
	body := map[string]string{"name": name, "prefix": prefix}
	if err := r.do(http.MethodPost, "/projects", body, &out); err != nil {
		return domain.Project{}, err
	}
	return out, nil
}

func (r *HTTPRepository) GetProject(id string) (*domain.Project, error) {
	var out domain.Project
	if err := r.do(http.MethodGet, "/projects/"+esc(id), nil, &out); err != nil {
		if isNotFoundCode(err, domain.ErrCodeProjectNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func (r *HTTPRepository) ListProjects() ([]domain.Project, error) {
	out := []domain.Project{}
	if err := r.do(http.MethodGet, "/projects", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []domain.Project{}
	}
	return out, nil
}

func (r *HTTPRepository) UpdateProject(id string, patch ProjectPatch) (domain.Project, error) {
	body := map[string]any{}
	if patch.Name != nil {
		body["name"] = *patch.Name
	}
	var out domain.Project
	if err := r.do(http.MethodPatch, "/projects/"+esc(id), body, &out); err != nil {
		return domain.Project{}, err
	}
	return out, nil
}

func (r *HTTPRepository) DeleteProject(id string) error {
	return r.do(http.MethodDelete, "/projects/"+esc(id), nil, nil)
}

func (r *HTTPRepository) CreateLabel(projectID, name, color string) (domain.Label, error) {
	var out domain.Label
	body := map[string]string{"name": name, "color": color}
	if err := r.do(http.MethodPost, "/projects/"+esc(projectID)+"/labels", body, &out); err != nil {
		return domain.Label{}, err
	}
	return out, nil
}

func (r *HTTPRepository) GetLabel(id string) (*domain.Label, error) {
	var out domain.Label
	if err := r.do(http.MethodGet, "/labels/"+esc(id), nil, &out); err != nil {
		if isNotFoundCode(err, domain.ErrCodeLabelNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func (r *HTTPRepository) ListLabelsByProject(projectID string) ([]domain.LabelUsage, error) {
	out := []domain.LabelUsage{}
	if err := r.do(http.MethodGet, "/projects/"+esc(projectID)+"/labels", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []domain.LabelUsage{}
	}
	return out, nil
}

func (r *HTTPRepository) UpdateLabel(id string, patch LabelPatch) (domain.Label, error) {
	body := map[string]any{}
	if patch.Name != nil {
		body["name"] = *patch.Name
	}
	if patch.Color != nil {
		body["color"] = *patch.Color
	}
	var out domain.Label
	if err := r.do(http.MethodPatch, "/labels/"+esc(id), body, &out); err != nil {
		return domain.Label{}, err
	}
	return out, nil
}

func (r *HTTPRepository) DeleteLabel(id string) (int, error) {
	var out struct {
		RemovedFromTickets int `json:"removed_from_tickets"`
	}
	if err := r.do(http.MethodDelete, "/labels/"+esc(id), nil, &out); err != nil {
		return 0, err
	}
	return out.RemovedFromTickets, nil
}

type currentProjectBody struct {
	ProjectID string `json:"project_id"`
}

func (r *HTTPRepository) GetCurrentProjectID() (string, error) {
	var out currentProjectBody
	if err := r.do(http.MethodGet, "/current-project", nil, &out); err != nil {
		return "", err
	}
	return out.ProjectID, nil
}

func (r *HTTPRepository) SetCurrentProjectID(projectID string) error {
	return r.do(http.MethodPut, "/current-project", currentProjectBody{ProjectID: projectID}, nil)
}
