// Package httpdatasourcetest is an in-memory reference implementation of the
// HTTP custom data source protocol (docs/http-datasource/openapi.yaml,
// protocol version 1.0), for tests. It is an http.Handler: wrap it in an
// httptest.Server and point store.Open at that server's URL.
//
// It deliberately lives in a regular (non-_test.go) internal package and
// depends only on internal/domain and internal/project -- never on
// internal/store -- so that both internal/store's own tests and
// internal/engine's tests can use it without an import cycle
// (store -> httpdatasourcetest is fine; httpdatasourcetest -> store would
// not be).
//
// Its semantics follow the SQLite backend's (ID minting, label validation,
// cascading deletes, artifact content omission in ticket listings), which is
// also what a real plugin is expected to do -- see the OpenAPI file.
package httpdatasourcetest

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/project"
)

const (
	// ProtocolName and DefaultVersion mirror store.HTTPDataSourceProtocolName
	// and store.HTTPDataSourceProtocolVersion (restated here because this
	// package must not import internal/store).
	ProtocolName   = "graph-ops-datasource"
	DefaultVersion = "1.0"
)

// RecordedRequest is one request the plugin received, for assertions.
type RecordedRequest struct {
	Method string
	// Path is the escaped request path, exactly as sent (e.g.
	// "/tickets/a%2Fb%20c").
	Path   string
	Header http.Header
	Body   []byte
}

// Plugin is the reference data source. The zero value is not usable; call New.
type Plugin struct {
	// Token, when non-empty, is the bearer token every request must carry.
	Token string
	// Protocol and Version are what GET /protocol reports.
	Protocol string
	Version  string

	mu       sync.Mutex
	requests []RecordedRequest

	projects       []*domain.Project
	ticketSeq      map[string]int // project ID -> last ticket seq
	tickets        []*domain.Ticket
	ticketLabelIDs map[string][]string // ticket ID -> label IDs
	nodeSeq        map[string]int      // ticket ID -> last node seq
	nodes          []*domain.GraphNode
	edges          []*domain.GraphEdge
	artifacts      []*domain.Artifact
	labels         []*domain.Label
	currentProject string
	idSeq          int

	mux *http.ServeMux
}

// New returns an empty plugin that requires token (none if "").
func New(token string) *Plugin {
	p := &Plugin{
		Token:          token,
		Protocol:       ProtocolName,
		Version:        DefaultVersion,
		ticketSeq:      map[string]int{},
		ticketLabelIDs: map[string][]string{},
		nodeSeq:        map[string]int{},
	}
	p.routes()
	return p
}

// Requests returns a copy of every request received so far.
func (p *Plugin) Requests() []RecordedRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]RecordedRequest(nil), p.requests...)
}

// ResetRequests forgets the recorded requests (state is kept).
func (p *Plugin) ResetRequests() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = nil
}

func (p *Plugin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	p.mu.Lock()
	p.requests = append(p.requests, RecordedRequest{
		Method: r.Method, Path: r.URL.EscapedPath(), Header: r.Header.Clone(), Body: body,
	})
	p.mu.Unlock()

	if p.Token != "" {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") ||
			subtle.ConstantTimeCompare([]byte(got), []byte(p.Token)) != 1 {
			writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid bearer token")
			return
		}
	}
	p.mux.ServeHTTP(w, r)
}

func (p *Plugin) routes() {
	m := http.NewServeMux()
	h := func(pattern string, fn func(w http.ResponseWriter, r *http.Request)) {
		m.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			p.mu.Lock()
			defer p.mu.Unlock()
			fn(w, r)
		})
	}
	m.HandleFunc("GET /protocol", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"protocol": p.Protocol, "version": p.Version})
	})
	h("POST /init", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })

	h("POST /projects/{projectId}/tickets", p.createTicket)
	h("GET /tickets/{ticketId}", p.getTicket)
	h("GET /tickets/{ticketId}/detail", p.getTicketDetail)
	h("GET /tickets", p.listTickets)
	h("GET /projects/{projectId}/tickets", p.listTicketsByProject)
	h("PATCH /tickets/{ticketId}", p.updateTicket)
	h("DELETE /tickets/{ticketId}", p.deleteTicket)

	h("POST /tickets/{ticketId}/nodes", p.createNode)
	h("GET /nodes/{nodeId}", p.getNode)
	h("GET /tickets/{ticketId}/nodes", p.listNodes)
	h("PATCH /nodes/{nodeId}", p.updateNode)
	h("DELETE /nodes/{nodeId}", p.deleteNode)

	h("POST /tickets/{ticketId}/edges", p.createEdge)
	h("GET /tickets/{ticketId}/edges", p.listEdges)
	h("DELETE /tickets/{ticketId}/edges", p.clearEdges)

	h("POST /tickets/{ticketId}/artifacts", p.createArtifact)
	h("GET /artifacts/{artifactId}", p.getArtifact)
	h("GET /tickets/{ticketId}/artifacts", p.listArtifactsByTicket)
	h("GET /nodes/{nodeId}/artifacts", p.listArtifactsByNode)

	h("POST /projects", p.createProject)
	h("GET /projects/{projectId}", p.getProject)
	h("GET /projects", p.listProjects)
	h("PATCH /projects/{projectId}", p.updateProject)
	h("DELETE /projects/{projectId}", p.deleteProject)

	h("POST /projects/{projectId}/labels", p.createLabel)
	h("GET /labels/{labelId}", p.getLabel)
	h("GET /projects/{projectId}/labels", p.listLabels)
	h("PATCH /labels/{labelId}", p.updateLabel)
	h("DELETE /labels/{labelId}", p.deleteLabel)

	h("GET /current-project", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"project_id": p.currentProject})
	})
	h("PUT /current-project", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ProjectID string `json:"project_id"`
		}
		if !decode(w, r, &body) {
			return
		}
		p.currentProject = body.ProjectID
		w.WriteHeader(http.StatusNoContent)
	})
	p.mux = m
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]map[string]string{"error": {"code": code, "message": msg}})
}

// writeAPIErr answers with an *domain.APIError's code and a status matching
// what graph-engine's own HTTP layer would use for it.
func writeAPIErr(w http.ResponseWriter, err error) {
	apiErr, ok := err.(*domain.APIError)
	if !ok {
		writeErr(w, http.StatusInternalServerError, string(domain.ErrCodeInternal), err.Error())
		return
	}
	status := http.StatusBadRequest
	switch apiErr.Code {
	case domain.ErrCodeTicketNotFound, domain.ErrCodeNodeNotFound, domain.ErrCodeArtifactNotFound,
		domain.ErrCodeProjectNotFound, domain.ErrCodeLabelNotFound:
		status = http.StatusNotFound
	case domain.ErrCodeLabelNameTaken, domain.ErrCodePrefixTaken:
		status = http.StatusConflict
	}
	writeErr(w, status, string(apiErr.Code), apiErr.Message)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, string(domain.ErrCodeValidation), "malformed JSON body: "+err.Error())
		return false
	}
	return true
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func (p *Plugin) nextID(prefix string) string {
	p.idSeq++
	return fmt.Sprintf("%s-%06d", prefix, p.idSeq)
}

func notFound(code domain.ErrorCode, what, id string) *domain.APIError {
	return domain.NewAPIError(code, "%s %s not found", what, id)
}

// --- projects ---

func (p *Plugin) findProject(id string) *domain.Project {
	for _, pr := range p.projects {
		if pr.ID == id {
			return pr
		}
	}
	return nil
}

func (p *Plugin) createProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string `json:"name"`
		Prefix string `json:"prefix"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Name == "" {
		writeAPIErr(w, domain.NewAPIError(domain.ErrCodeValidation, "project name is required"))
		return
	}
	var existing []string
	for _, pr := range p.projects {
		existing = append(existing, pr.Prefix)
	}
	prefix, err := project.ResolvePrefix(body.Name, body.Prefix, existing)
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	ts := now()
	pr := &domain.Project{ID: p.nextID("proj"), Name: body.Name, Prefix: prefix, CreatedAt: ts, UpdatedAt: ts}
	p.projects = append(p.projects, pr)
	writeJSON(w, http.StatusCreated, pr)
}

func (p *Plugin) getProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("projectId")
	pr := p.findProject(id)
	if pr == nil {
		writeAPIErr(w, notFound(domain.ErrCodeProjectNotFound, "project", id))
		return
	}
	writeJSON(w, http.StatusOK, pr)
}

func (p *Plugin) listProjects(w http.ResponseWriter, r *http.Request) {
	out := []domain.Project{}
	for _, pr := range p.projects {
		out = append(out, *pr)
	}
	writeJSON(w, http.StatusOK, out)
}

func (p *Plugin) updateProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("projectId")
	pr := p.findProject(id)
	if pr == nil {
		writeAPIErr(w, notFound(domain.ErrCodeProjectNotFound, "project", id))
		return
	}
	var body struct {
		Name *string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Name != nil {
		pr.Name = *body.Name
	}
	pr.UpdatedAt = now()
	writeJSON(w, http.StatusOK, pr)
}

func (p *Plugin) deleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("projectId")
	if p.findProject(id) == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if p.currentProject == id {
		p.currentProject = ""
	}
	for _, t := range append([]*domain.Ticket(nil), p.tickets...) {
		if t.ProjectID == id {
			p.removeTicket(t.ID)
		}
	}
	var keptLabels []*domain.Label
	for _, l := range p.labels {
		if l.ProjectID != id {
			keptLabels = append(keptLabels, l)
		}
	}
	p.labels = keptLabels
	var kept []*domain.Project
	for _, pr := range p.projects {
		if pr.ID != id {
			kept = append(kept, pr)
		}
	}
	p.projects = kept
	w.WriteHeader(http.StatusNoContent)
}

// --- labels ---

func (p *Plugin) findLabel(id string) *domain.Label {
	for _, l := range p.labels {
		if l.ID == id {
			return l
		}
	}
	return nil
}

func (p *Plugin) labelNameTaken(projectID, name, exceptID string) bool {
	for _, l := range p.labels {
		if l.ProjectID == projectID && l.ID != exceptID && strings.EqualFold(l.Name, name) {
			return true
		}
	}
	return false
}

func sortLabels(labels []domain.Label) {
	sort.SliceStable(labels, func(i, j int) bool {
		a, b := labels[i], labels[j]
		la, lb := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if la != lb {
			return la < lb
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
}

func (p *Plugin) createLabel(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("projectId")
	var body struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if !decode(w, r, &body) {
		return
	}
	name, err := domain.NormalizeLabelName(body.Name)
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	color, err := domain.ParseLabelColor(body.Color)
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	if p.findProject(projectID) == nil {
		writeAPIErr(w, notFound(domain.ErrCodeProjectNotFound, "project", projectID))
		return
	}
	if p.labelNameTaken(projectID, name, "") {
		writeAPIErr(w, domain.NewAPIError(domain.ErrCodeLabelNameTaken, "a label named %q already exists in project %s", name, projectID))
		return
	}
	ts := now()
	l := &domain.Label{ID: p.nextID("label"), ProjectID: projectID, Name: name, Color: color, CreatedAt: ts, UpdatedAt: ts}
	p.labels = append(p.labels, l)
	writeJSON(w, http.StatusCreated, l)
}

func (p *Plugin) getLabel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("labelId")
	l := p.findLabel(id)
	if l == nil {
		writeAPIErr(w, notFound(domain.ErrCodeLabelNotFound, "label", id))
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (p *Plugin) listLabels(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("projectId")
	if p.findProject(projectID) == nil {
		writeAPIErr(w, notFound(domain.ErrCodeProjectNotFound, "project", projectID))
		return
	}
	var labels []domain.Label
	for _, l := range p.labels {
		if l.ProjectID == projectID {
			labels = append(labels, *l)
		}
	}
	sortLabels(labels)
	out := []domain.LabelUsage{}
	for _, l := range labels {
		count := 0
		for _, ids := range p.ticketLabelIDs {
			for _, id := range ids {
				if id == l.ID {
					count++
				}
			}
		}
		out = append(out, domain.LabelUsage{Label: l, TicketCount: count})
	}
	writeJSON(w, http.StatusOK, out)
}

func (p *Plugin) updateLabel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("labelId")
	var body struct {
		Name  *string `json:"name"`
		Color *string `json:"color"`
	}
	if !decode(w, r, &body) {
		return
	}
	var name *string
	if body.Name != nil {
		n, err := domain.NormalizeLabelName(*body.Name)
		if err != nil {
			writeAPIErr(w, err)
			return
		}
		name = &n
	}
	var color *domain.LabelColor
	if body.Color != nil {
		c, err := domain.ParseLabelColor(*body.Color)
		if err != nil {
			writeAPIErr(w, err)
			return
		}
		color = &c
	}
	l := p.findLabel(id)
	if l == nil {
		writeAPIErr(w, notFound(domain.ErrCodeLabelNotFound, "label", id))
		return
	}
	if name != nil {
		if p.labelNameTaken(l.ProjectID, *name, l.ID) {
			writeAPIErr(w, domain.NewAPIError(domain.ErrCodeLabelNameTaken, "a label named %q already exists in project %s", *name, l.ProjectID))
			return
		}
		l.Name = *name
	}
	if color != nil {
		l.Color = *color
	}
	l.UpdatedAt = now()
	writeJSON(w, http.StatusOK, l)
}

func (p *Plugin) deleteLabel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("labelId")
	if p.findLabel(id) == nil {
		writeAPIErr(w, notFound(domain.ErrCodeLabelNotFound, "label", id))
		return
	}
	removed := 0
	for tid, ids := range p.ticketLabelIDs {
		var kept []string
		for _, lid := range ids {
			if lid == id {
				removed++
				continue
			}
			kept = append(kept, lid)
		}
		p.ticketLabelIDs[tid] = kept
	}
	var kept []*domain.Label
	for _, l := range p.labels {
		if l.ID != id {
			kept = append(kept, l)
		}
	}
	p.labels = kept
	writeJSON(w, http.StatusOK, map[string]int{"removed_from_tickets": removed})
}

// --- tickets ---

func (p *Plugin) findTicket(id string) *domain.Ticket {
	for _, t := range p.tickets {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// withLabels returns a copy of t with its current labels attached.
func (p *Plugin) withLabels(t *domain.Ticket) domain.Ticket {
	out := *t
	out.Labels = []domain.Label{}
	for _, id := range p.ticketLabelIDs[t.ID] {
		if l := p.findLabel(id); l != nil {
			out.Labels = append(out.Labels, *l)
		}
	}
	sortLabels(out.Labels)
	return out
}

// validateLabelIDs checks every ID names a label of projectID, returning the
// de-duplicated set.
func (p *Plugin) validateLabelIDs(projectID string, ids []string) ([]string, error) {
	seen := map[string]bool{}
	var out, missing []string
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		l := p.findLabel(id)
		if l == nil || l.ProjectID != projectID {
			missing = append(missing, id)
			continue
		}
		out = append(out, id)
	}
	if len(missing) > 0 {
		return nil, domain.NewAPIError(domain.ErrCodeLabelNotFound, "label(s) %s not found in project %s", strings.Join(missing, ", "), projectID)
	}
	return out, nil
}

func (p *Plugin) createTicket(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("projectId")
	var in domain.Ticket
	if !decode(w, r, &in) {
		return
	}
	pr := p.findProject(projectID)
	if pr == nil {
		writeAPIErr(w, notFound(domain.ErrCodeProjectNotFound, "project", projectID))
		return
	}
	var labelIDs []string
	for _, l := range in.Labels {
		labelIDs = append(labelIDs, l.ID)
	}
	labelIDs, err := p.validateLabelIDs(projectID, labelIDs)
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	p.ticketSeq[projectID]++
	ts := now()
	t := in
	t.ID = fmt.Sprintf("%s-%05d", pr.Prefix, p.ticketSeq[projectID])
	t.ProjectID = projectID
	t.CreatedAt, t.UpdatedAt = ts, ts
	t.Labels = nil
	t.RefinedAt, t.ClosedReason, t.GraphExpandedAt = nil, nil, nil
	if t.Priority == "" {
		t.Priority = domain.DefaultTicketPriority
	}
	p.tickets = append(p.tickets, &t)
	p.ticketLabelIDs[t.ID] = labelIDs
	writeJSON(w, http.StatusCreated, p.withLabels(&t))
}

func (p *Plugin) getTicket(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("ticketId")
	t := p.findTicket(id)
	if t == nil {
		writeAPIErr(w, notFound(domain.ErrCodeTicketNotFound, "ticket", id))
		return
	}
	writeJSON(w, http.StatusOK, p.withLabels(t))
}

func (p *Plugin) getTicketDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("ticketId")
	t := p.findTicket(id)
	if t == nil {
		writeAPIErr(w, notFound(domain.ErrCodeTicketNotFound, "ticket", id))
		return
	}
	writeJSON(w, http.StatusOK, domain.TicketDetail{
		Ticket:    p.withLabels(t),
		Nodes:     p.nodesOf(id),
		Edges:     p.edgesOf(id),
		Artifacts: p.artifactSummaries(id),
	})
}

// listTicketsWhere returns matching tickets newest first (created_at DESC).
func (p *Plugin) listTicketsWhere(match func(*domain.Ticket) bool) []domain.Ticket {
	out := []domain.Ticket{}
	for i := len(p.tickets) - 1; i >= 0; i-- {
		if match(p.tickets[i]) {
			out = append(out, p.withLabels(p.tickets[i]))
		}
	}
	return out
}

func (p *Plugin) listTickets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, p.listTicketsWhere(func(*domain.Ticket) bool { return true }))
}

func (p *Plugin) listTicketsByProject(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("projectId")
	writeJSON(w, http.StatusOK, p.listTicketsWhere(func(t *domain.Ticket) bool { return t.ProjectID == projectID }))
}

// optionalString decodes a JSON field that may be absent, null or a string.
type optionalString struct {
	set   bool
	value *string
}

func (o *optionalString) UnmarshalJSON(b []byte) error {
	o.set = true
	if string(b) == "null" {
		o.value = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	o.value = &s
	return nil
}

func (p *Plugin) updateTicket(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("ticketId")
	var body struct {
		Title           *string                `json:"title"`
		Description     *string                `json:"description"`
		Status          *domain.TicketStatus   `json:"status"`
		AutoExecutable  *bool                  `json:"auto_executable"`
		Blocked         *bool                  `json:"blocked"`
		RefinedAt       *string                `json:"refined_at"`
		ClosedReason    *string                `json:"closed_reason"`
		Assignee        optionalString         `json:"assignee"`
		GraphExpandedAt *string                `json:"graph_expanded_at"`
		Priority        *domain.TicketPriority `json:"priority"`
		LabelIDs        *[]string              `json:"label_ids"`
	}
	if !decode(w, r, &body) {
		return
	}
	t := p.findTicket(id)
	if t == nil {
		writeAPIErr(w, notFound(domain.ErrCodeTicketNotFound, "ticket", id))
		return
	}
	var newLabels []string
	if body.LabelIDs != nil {
		ids, err := p.validateLabelIDs(t.ProjectID, *body.LabelIDs)
		if err != nil {
			writeAPIErr(w, err)
			return
		}
		newLabels = ids
	}
	if body.Title != nil {
		t.Title = *body.Title
	}
	if body.Description != nil {
		t.Description = *body.Description
	}
	if body.Status != nil {
		t.Status = *body.Status
	}
	if body.AutoExecutable != nil {
		t.AutoExecutable = *body.AutoExecutable
	}
	if body.Blocked != nil {
		t.Blocked = *body.Blocked
	}
	if body.RefinedAt != nil {
		t.RefinedAt = body.RefinedAt
	}
	if body.ClosedReason != nil {
		t.ClosedReason = body.ClosedReason
	}
	if body.Assignee.set {
		t.Assignee = body.Assignee.value
	}
	if body.GraphExpandedAt != nil {
		t.GraphExpandedAt = body.GraphExpandedAt
	}
	if body.Priority != nil {
		t.Priority = *body.Priority
	}
	if t.Priority == "" {
		t.Priority = domain.DefaultTicketPriority
	}
	if body.LabelIDs != nil {
		p.ticketLabelIDs[t.ID] = newLabels
	}
	t.UpdatedAt = now()
	writeJSON(w, http.StatusOK, p.withLabels(t))
}

// removeTicket deletes a ticket and everything under it.
func (p *Plugin) removeTicket(id string) {
	var tickets []*domain.Ticket
	for _, t := range p.tickets {
		if t.ID != id {
			tickets = append(tickets, t)
		}
	}
	p.tickets = tickets
	delete(p.ticketLabelIDs, id)
	var nodes []*domain.GraphNode
	for _, n := range p.nodes {
		if n.TicketID != id {
			nodes = append(nodes, n)
		}
	}
	p.nodes = nodes
	var edges []*domain.GraphEdge
	for _, e := range p.edges {
		if e.TicketID != id {
			edges = append(edges, e)
		}
	}
	p.edges = edges
	var arts []*domain.Artifact
	for _, a := range p.artifacts {
		if a.TicketID != id {
			arts = append(arts, a)
		}
	}
	p.artifacts = arts
}

func (p *Plugin) deleteTicket(w http.ResponseWriter, r *http.Request) {
	p.removeTicket(r.PathValue("ticketId"))
	w.WriteHeader(http.StatusNoContent)
}

// --- nodes ---

func (p *Plugin) findNode(id string) *domain.GraphNode {
	for _, n := range p.nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

func (p *Plugin) nodesOf(ticketID string) []domain.GraphNode {
	out := []domain.GraphNode{}
	for _, n := range p.nodes {
		if n.TicketID == ticketID {
			out = append(out, *n)
		}
	}
	return out
}

func (p *Plugin) createNode(w http.ResponseWriter, r *http.Request) {
	ticketID := r.PathValue("ticketId")
	var in domain.GraphNode
	if !decode(w, r, &in) {
		return
	}
	if p.findTicket(ticketID) == nil {
		writeAPIErr(w, notFound(domain.ErrCodeTicketNotFound, "ticket", ticketID))
		return
	}
	if p.nodeSeq[ticketID] >= 99 {
		writeAPIErr(w, domain.NewAPIError(domain.ErrCodeValidation, "ticket %s already has the maximum of 99 nodes", ticketID))
		return
	}
	p.nodeSeq[ticketID]++
	n := in
	n.ID = fmt.Sprintf("%s-%02d", ticketID, p.nodeSeq[ticketID])
	n.TicketID = ticketID
	if n.MaxIterations == 0 {
		n.MaxIterations = 3
	}
	ts := now()
	n.CreatedAt, n.UpdatedAt = ts, ts
	p.nodes = append(p.nodes, &n)
	if t := p.findTicket(ticketID); t != nil {
		t.UpdatedAt = ts
	}
	writeJSON(w, http.StatusCreated, n)
}

func (p *Plugin) getNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("nodeId")
	n := p.findNode(id)
	if n == nil {
		writeAPIErr(w, notFound(domain.ErrCodeNodeNotFound, "node", id))
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (p *Plugin) listNodes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, p.nodesOf(r.PathValue("ticketId")))
}

func (p *Plugin) updateNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("nodeId")
	var body struct {
		Name           *string            `json:"name"`
		Type           *domain.NodeType   `json:"type"`
		Status         *domain.NodeStatus `json:"status"`
		IterationCount *int               `json:"iteration_count"`
		MaxIterations  *int               `json:"max_iterations"`
		Assignee       optionalString     `json:"assignee"`
		IsManual       *bool              `json:"is_manual"`
		GateID         *string            `json:"gate_id"`
		Criteria       *string            `json:"criteria"`
	}
	if !decode(w, r, &body) {
		return
	}
	n := p.findNode(id)
	if n == nil {
		writeAPIErr(w, notFound(domain.ErrCodeNodeNotFound, "node", id))
		return
	}
	if body.Name != nil {
		n.Name = *body.Name
	}
	if body.Type != nil {
		n.Type = *body.Type
	}
	if body.Status != nil {
		n.Status = *body.Status
	}
	if body.IterationCount != nil {
		n.IterationCount = *body.IterationCount
	}
	if body.MaxIterations != nil {
		n.MaxIterations = *body.MaxIterations
	}
	if body.Assignee.set {
		n.Assignee = body.Assignee.value
	}
	if body.IsManual != nil {
		n.IsManual = *body.IsManual
	}
	if body.GateID != nil {
		n.GateID = body.GateID
	}
	if body.Criteria != nil {
		n.Criteria = body.Criteria
	}
	n.UpdatedAt = now()
	writeJSON(w, http.StatusOK, n)
}

func (p *Plugin) deleteNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("nodeId")
	var nodes []*domain.GraphNode
	for _, n := range p.nodes {
		if n.ID != id {
			nodes = append(nodes, n)
		}
	}
	p.nodes = nodes
	var edges []*domain.GraphEdge
	for _, e := range p.edges {
		if e.FromNodeID != id && e.ToNodeID != id {
			edges = append(edges, e)
		}
	}
	p.edges = edges
	var arts []*domain.Artifact
	for _, a := range p.artifacts {
		if a.NodeID != id {
			arts = append(arts, a)
		}
	}
	p.artifacts = arts
	w.WriteHeader(http.StatusNoContent)
}

// --- edges ---

func (p *Plugin) edgesOf(ticketID string) []domain.GraphEdge {
	out := []domain.GraphEdge{}
	for _, e := range p.edges {
		if e.TicketID == ticketID {
			out = append(out, *e)
		}
	}
	return out
}

func (p *Plugin) createEdge(w http.ResponseWriter, r *http.Request) {
	ticketID := r.PathValue("ticketId")
	var in domain.GraphEdge
	if !decode(w, r, &in) {
		return
	}
	if p.findTicket(ticketID) == nil {
		writeAPIErr(w, notFound(domain.ErrCodeTicketNotFound, "ticket", ticketID))
		return
	}
	e := in
	if e.ID == "" {
		e.ID = p.nextID("edge")
	}
	e.TicketID = ticketID
	if e.Condition == "" {
		e.Condition = domain.EdgeAlways
	}
	e.CreatedAt = now()
	p.edges = append(p.edges, &e)
	writeJSON(w, http.StatusCreated, e)
}

func (p *Plugin) listEdges(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, p.edgesOf(r.PathValue("ticketId")))
}

func (p *Plugin) clearEdges(w http.ResponseWriter, r *http.Request) {
	ticketID := r.PathValue("ticketId")
	var edges []*domain.GraphEdge
	for _, e := range p.edges {
		if e.TicketID != ticketID {
			edges = append(edges, e)
		}
	}
	p.edges = edges
	w.WriteHeader(http.StatusNoContent)
}

// --- artifacts ---

func hasContent(a *domain.Artifact) bool { return a.Content != nil && *a.Content != "" }

// artifactSummaries lists a ticket's artifacts with html/image content
// omitted (has_content still set), as the protocol allows.
func (p *Plugin) artifactSummaries(ticketID string) []domain.Artifact {
	out := []domain.Artifact{}
	for _, a := range p.artifacts {
		if a.TicketID != ticketID {
			continue
		}
		s := *a
		s.HasContent = hasContent(a)
		if domain.IsFileBackedArtifactType(a.Type) {
			s.Content = nil
		}
		out = append(out, s)
	}
	return out
}

func (p *Plugin) createArtifact(w http.ResponseWriter, r *http.Request) {
	ticketID := r.PathValue("ticketId")
	var in domain.Artifact
	if !decode(w, r, &in) {
		return
	}
	if p.findTicket(ticketID) == nil {
		writeAPIErr(w, notFound(domain.ErrCodeTicketNotFound, "ticket", ticketID))
		return
	}
	a := in
	if a.ID == "" {
		a.ID = p.nextID("art")
	}
	a.TicketID = ticketID
	a.CreatedAt = now()
	a.HasContent = hasContent(&a)
	p.artifacts = append(p.artifacts, &a)
	writeJSON(w, http.StatusCreated, a)
}

func (p *Plugin) getArtifact(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("artifactId")
	for _, a := range p.artifacts {
		if a.ID == id {
			out := *a
			out.HasContent = hasContent(a)
			writeJSON(w, http.StatusOK, out)
			return
		}
	}
	writeAPIErr(w, notFound(domain.ErrCodeArtifactNotFound, "artifact", id))
}

func (p *Plugin) listArtifactsByTicket(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, p.artifactSummaries(r.PathValue("ticketId")))
}

func (p *Plugin) listArtifactsByNode(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("nodeId")
	out := []domain.Artifact{}
	for _, a := range p.artifacts {
		if a.NodeID == nodeID {
			s := *a
			s.HasContent = hasContent(a)
			out = append(out, s)
		}
	}
	writeJSON(w, http.StatusOK, out)
}
