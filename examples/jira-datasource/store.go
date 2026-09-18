package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// How GraphOps data is laid out in Jira (see README.md for the full table):
//
//   - A GraphOps project is an existing Jira project. Its ID is
//     "jira-<KEY>" and its prefix is the Jira project key. Its name and
//     label definitions live in the graphops.project property of a
//     per-project "metadata issue" (Jira label graphops-meta).
//   - Which projects are registered, and the current project, live in the
//     plugin's local state file (state.go).
//   - A ticket is a Jira issue labelled graphops; its ID is the issue key.
//     Every GraphOps field lives in the graphops.ticket issue property (the
//     summary and description mirror title/description for humans).
//   - Nodes and edges (no Jira counterpart) live in the graphops.graph
//     property of the ticket's issue. Node IDs are "<issue key>-NN".
//   - An artifact (no Jira counterpart) is a comment on the ticket's issue,
//     with its metadata in the graphops.artifact comment property. Its ID is
//     "<issue key>-c<comment id>". Large text content and html/image content
//     are stored as issue attachments.
//   - Labels are stored in the project's metadata issue. Label IDs are
//     "<KEY>-label-<n>".
const (
	jiraLabelManaged = "graphops"
	jiraLabelMeta    = "graphops-meta"

	propTicket   = "graphops.ticket"
	propGraph    = "graphops.graph"
	propProject  = "graphops.project"
	propArtifact = "graphops.artifact"

	projectIDPrefix = "jira-"

	// maxPropertyBytes is Jira's limit on one entity property's JSON value.
	maxPropertyBytes = 32768
	// inlineContentLimit is the largest text/gherkin/json artifact content
	// kept inline (in the comment and its property); anything larger, and
	// every html/image artifact, goes to an attachment.
	inlineContentLimit = 16 * 1024

	maxNodesPerTicket  = 99
	maxLabelNameLength = 50
)

var (
	prefixPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,5}$`)
	labelColors   = map[string]bool{
		"gray": true, "red": true, "orange": true, "amber": true, "green": true,
		"teal": true, "blue": true, "indigo": true, "purple": true, "pink": true,
	}
)

// --- property shapes ---

type ticketProp struct {
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	Status          string   `json:"status"`
	AutoExecutable  bool     `json:"auto_executable"`
	Blocked         bool     `json:"blocked"`
	RefinedAt       *string  `json:"refined_at,omitempty"`
	ClosedReason    *string  `json:"closed_reason,omitempty"`
	Assignee        *string  `json:"assignee,omitempty"`
	GraphExpandedAt *string  `json:"graph_expanded_at,omitempty"`
	Priority        string   `json:"priority"`
	LabelIDs        []string `json:"label_ids"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
}

type graphProp struct {
	NodeSeq int         `json:"node_seq"`
	EdgeSeq int         `json:"edge_seq"`
	Nodes   []GraphNode `json:"nodes"`
	Edges   []GraphEdge `json:"edges"`
}

type projectProp struct {
	Name      string  `json:"name"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
	LabelSeq  int     `json:"label_seq"`
	Labels    []Label `json:"labels"`
}

type artifactProp struct {
	NodeID    string  `json:"node_id"`
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	FilePath  *string `json:"file_path,omitempty"`
	Metadata  *string `json:"metadata,omitempty"`
	CreatedAt string  `json:"created_at"`
	// Content is set when the content is stored inline.
	Content    *string `json:"content,omitempty"`
	HasContent bool    `json:"has_content"`
	// AttachmentID/AttachmentEncoding are set when the content is stored as
	// an issue attachment. Encoding "base64" means the attachment holds the
	// decoded bytes of a base64 content string (images); "raw" means it
	// holds the content string itself.
	AttachmentID       string `json:"attachment_id,omitempty"`
	AttachmentEncoding string `json:"attachment_encoding,omitempty"`
}

// Store implements the 32 protocol operations on top of Jira.
type Store struct {
	jira      *jiraClient
	state     *stateFile
	issueType string
	now       func() string

	locksMu sync.Mutex
	locks   map[string]*sync.Mutex
}

func newStore(jira *jiraClient, statePath, issueType string) *Store {
	return &Store{
		jira:      jira,
		state:     &stateFile{path: statePath},
		issueType: issueType,
		now:       func() string { return time.Now().UTC().Format(time.RFC3339Nano) },
		locks:     map[string]*sync.Mutex{},
	}
}

// lock serializes read-modify-write of one issue's properties within this
// process. The sample assumes a single plugin process per Jira site (see
// README.md, "Limitations").
func (s *Store) lock(issueKey string) func() {
	s.locksMu.Lock()
	m, ok := s.locks[issueKey]
	if !ok {
		m = &sync.Mutex{}
		s.locks[issueKey] = m
	}
	s.locksMu.Unlock()
	m.Lock()
	return m.Unlock
}

// jiraFailure wraps an unexpected Jira error as INTERNAL_ERROR.
func jiraFailure(err error) error {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return newAPIError(http.StatusBadGateway, "INTERNAL_ERROR", "%v", err)
}

func marshalProperty(what string, v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxPropertyBytes {
		return nil, validationError("%s would be %d bytes, over Jira's %d-byte issue property limit", what, len(raw), maxPropertyBytes)
	}
	return raw, nil
}

func decodeProp(props map[string]json.RawMessage, key string, v any) (bool, error) {
	raw, ok := props[key]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return false, nil
	}
	return true, json.Unmarshal(raw, v)
}

// --- ID helpers ---

func projectKeyFromID(projectID string) (string, bool) {
	if !strings.HasPrefix(projectID, projectIDPrefix) || len(projectID) == len(projectIDPrefix) {
		return "", false
	}
	return projectID[len(projectIDPrefix):], true
}

func projectIDFromKey(key string) string { return projectIDPrefix + key }

// projectKeyOfIssue returns the project key part of an issue key
// ("GOPS-12" -> "GOPS").
func projectKeyOfIssue(issueKey string) string {
	if i := strings.LastIndex(issueKey, "-"); i > 0 {
		return issueKey[:i]
	}
	return ""
}

// issueKeyOfNode returns the issue key a node ID belongs to
// ("GOPS-12-03" -> "GOPS-12").
func issueKeyOfNode(nodeID string) (string, bool) {
	i := strings.LastIndex(nodeID, "-")
	if i <= 0 || len(nodeID)-i-1 != 2 {
		return "", false
	}
	if _, err := strconv.Atoi(nodeID[i+1:]); err != nil {
		return "", false
	}
	return nodeID[:i], true
}

// parseArtifactID splits "<issue key>-c<comment id>".
func parseArtifactID(id string) (issueKey, commentID string, ok bool) {
	i := strings.LastIndex(id, "-c")
	if i <= 0 || i+2 >= len(id) {
		return "", "", false
	}
	if _, err := strconv.ParseUint(id[i+2:], 10, 64); err != nil {
		return "", "", false
	}
	return id[:i], id[i+2:], true
}

func projectKeyOfLabel(labelID string) (string, bool) {
	i := strings.Index(labelID, "-label-")
	if i <= 0 {
		return "", false
	}
	return labelID[:i], true
}

func summaryFor(title string) string {
	line := strings.TrimSpace(strings.SplitN(title, "\n", 2)[0])
	if line == "" {
		line = "(untitled)"
	}
	if utf8.RuneCountInString(line) > 250 {
		line = string([]rune(line)[:250]) + "..."
	}
	return line
}

// --- projects ---

func (s *Store) registered(key string) (registeredProject, bool, error) {
	st, err := s.state.read()
	if err != nil {
		return registeredProject{}, false, err
	}
	for _, p := range st.Projects {
		if strings.EqualFold(p.Key, key) {
			return p, true, nil
		}
	}
	return registeredProject{}, false, nil
}

// loadProject reads a registered project's metadata.
func (s *Store) loadProject(projectID string) (registeredProject, projectProp, error) {
	key, ok := projectKeyFromID(projectID)
	if !ok {
		return registeredProject{}, projectProp{}, notFound("PROJECT_NOT_FOUND", "project", projectID)
	}
	reg, ok, err := s.registered(key)
	if err != nil {
		return registeredProject{}, projectProp{}, jiraFailure(err)
	}
	if !ok {
		return registeredProject{}, projectProp{}, notFound("PROJECT_NOT_FOUND", "project", projectID)
	}
	issue, err := s.jira.getIssue(reg.MetaIssueKey, []string{propProject})
	if err != nil {
		if isJiraStatus(err, http.StatusNotFound) {
			return registeredProject{}, projectProp{}, notFound("PROJECT_NOT_FOUND", "project", projectID)
		}
		return registeredProject{}, projectProp{}, jiraFailure(err)
	}
	var meta projectProp
	if _, err := decodeProp(issue.Properties, propProject, &meta); err != nil {
		return registeredProject{}, projectProp{}, jiraFailure(err)
	}
	return reg, meta, nil
}

func (s *Store) saveProjectMeta(reg registeredProject, meta projectProp) error {
	raw, err := marshalProperty("the project metadata (graphops.project)", meta)
	if err != nil {
		return err
	}
	if err := s.jira.setIssueProperty(reg.MetaIssueKey, propProject, raw); err != nil {
		return jiraFailure(err)
	}
	return nil
}

func projectFrom(reg registeredProject, meta projectProp) Project {
	return Project{ID: projectIDFromKey(reg.Key), Name: meta.Name, Prefix: reg.Key, CreatedAt: meta.CreatedAt, UpdatedAt: meta.UpdatedAt}
}

// CreateProject registers an existing Jira project. It never creates a Jira
// project: prefix must be the key of a project the configured account can
// access (the protocol lets a plugin constrain the prefix this way).
func (s *Store) CreateProject(name, prefix string) (Project, error) {
	name = strings.TrimSpace(name)
	prefix = strings.TrimSpace(prefix)
	if name == "" {
		return Project{}, validationError("project name is required")
	}
	if prefix == "" {
		return Project{}, validationError("prefix is required: it must be the key of an existing Jira project")
	}
	if !prefixPattern.MatchString(prefix) {
		return Project{}, newAPIError(http.StatusBadRequest, "INVALID_PREFIX",
			"prefix %q must be 1-5 letters or digits (a Jira project key of at most 5 characters)", prefix)
	}
	key := strings.ToUpper(prefix)
	if _, ok, err := s.registered(key); err != nil {
		return Project{}, jiraFailure(err)
	} else if ok {
		return Project{}, newAPIError(http.StatusConflict, "PREFIX_TAKEN", "project %s is already registered", key)
	}
	jp, err := s.jira.getProject(key)
	if err != nil {
		if isJiraStatus(err, http.StatusNotFound) {
			return Project{}, validationError("no Jira project with key %q is accessible to the configured account", key)
		}
		return Project{}, jiraFailure(err)
	}
	key = jp.Key

	now := s.now()
	meta := projectProp{Name: name, CreatedAt: now, UpdatedAt: now, Labels: []Label{}}
	// Reuse an existing metadata issue (a project registered before, e.g.
	// by a plugin whose state file was lost) so its labels survive.
	existing, err := s.jira.search(fmt.Sprintf("project = %s AND labels = %s ORDER BY created ASC", key, jiraLabelMeta), []string{propProject})
	if err != nil {
		return Project{}, jiraFailure(err)
	}
	var metaKey string
	if len(existing) > 0 {
		metaKey = existing[0].Key
		var old projectProp
		if ok, _ := decodeProp(existing[0].Properties, propProject, &old); ok {
			meta.CreatedAt, meta.LabelSeq, meta.Labels = old.CreatedAt, old.LabelSeq, old.Labels
		}
		if err := s.saveProjectMeta(registeredProject{Key: key, MetaIssueKey: metaKey}, meta); err != nil {
			return Project{}, err
		}
	} else {
		raw, err := marshalProperty("the project metadata (graphops.project)", meta)
		if err != nil {
			return Project{}, err
		}
		metaKey, err = s.jira.createIssue(map[string]any{
			"project":     map[string]string{"key": key},
			"summary":     "GraphOps metadata (do not delete)",
			"issuetype":   map[string]string{"name": s.issueType},
			"labels":      []string{jiraLabelMeta},
			"description": markdownToADF("This issue stores GraphOps project metadata (project name and labels) in its graphops.project property. It is managed by the GraphOps Jira data source plugin."),
		}, []jiraProperty{{Key: propProject, Value: json.RawMessage(raw)}})
		if err != nil {
			return Project{}, jiraFailure(err)
		}
	}
	reg := registeredProject{Key: key, MetaIssueKey: metaKey}
	if err := s.state.update(func(st *localState) error {
		st.Projects = append(st.Projects, reg)
		return nil
	}); err != nil {
		return Project{}, jiraFailure(err)
	}
	return projectFrom(reg, meta), nil
}

func (s *Store) GetProject(id string) (Project, error) {
	reg, meta, err := s.loadProject(id)
	if err != nil {
		return Project{}, err
	}
	return projectFrom(reg, meta), nil
}

func (s *Store) ListProjects() ([]Project, error) {
	st, err := s.state.read()
	if err != nil {
		return nil, jiraFailure(err)
	}
	out := []Project{}
	for _, reg := range st.Projects {
		p, err := s.GetProject(projectIDFromKey(reg.Key))
		if err != nil {
			var apiErr *apiError
			if errors.As(err, &apiErr) && apiErr.Code == "PROJECT_NOT_FOUND" {
				continue // metadata issue deleted in Jira
			}
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) UpdateProject(id string, name *string) (Project, error) {
	reg, _, err := s.loadProject(id)
	if err != nil {
		return Project{}, err
	}
	defer s.lock(reg.MetaIssueKey)()
	reg, meta, err := s.loadProject(id)
	if err != nil {
		return Project{}, err
	}
	if name != nil {
		meta.Name = *name
	}
	meta.UpdatedAt = s.now()
	if err := s.saveProjectMeta(reg, meta); err != nil {
		return Project{}, err
	}
	return projectFrom(reg, meta), nil
}

// DeleteProject unregisters the project: its tickets are detached from
// GraphOps (see DeleteTicket), its metadata issue is deleted, and it is
// removed from the local state (clearing the current project if it pointed
// here). The Jira project itself is untouched. A missing project is a no-op.
func (s *Store) DeleteProject(id string) error {
	key, ok := projectKeyFromID(id)
	if !ok {
		return nil
	}
	reg, ok, err := s.registered(key)
	if err != nil {
		return jiraFailure(err)
	}
	if !ok {
		return nil
	}
	issues, err := s.jira.search(ticketJQL([]string{reg.Key}), []string{propTicket})
	if err != nil {
		return jiraFailure(err)
	}
	for _, issue := range issues {
		if err := s.DeleteTicket(issue.Key); err != nil {
			return err
		}
	}
	if err := s.jira.deleteIssue(reg.MetaIssueKey); err != nil && !isJiraStatus(err, http.StatusNotFound) {
		return jiraFailure(err)
	}
	if err := s.state.update(func(st *localState) error {
		var kept []registeredProject
		for _, p := range st.Projects {
			if !strings.EqualFold(p.Key, reg.Key) {
				kept = append(kept, p)
			}
		}
		st.Projects = kept
		if st.CurrentProjectID == id {
			st.CurrentProjectID = ""
		}
		return nil
	}); err != nil {
		return jiraFailure(err)
	}
	return nil
}

// --- current project ---

func (s *Store) GetCurrentProjectID() (string, error) {
	st, err := s.state.read()
	if err != nil {
		return "", jiraFailure(err)
	}
	return st.CurrentProjectID, nil
}

func (s *Store) SetCurrentProjectID(id string) error {
	if err := s.state.update(func(st *localState) error {
		st.CurrentProjectID = id
		return nil
	}); err != nil {
		return jiraFailure(err)
	}
	return nil
}

// --- labels ---

func normalizeLabelName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", newAPIError(http.StatusBadRequest, "INVALID_LABEL_NAME", "label name is required")
	}
	if n := utf8.RuneCountInString(name); n > maxLabelNameLength {
		return "", newAPIError(http.StatusBadRequest, "INVALID_LABEL_NAME", "label name is %d characters long; the maximum is %d", n, maxLabelNameLength)
	}
	return name, nil
}

func checkLabelColor(color string) error {
	if !labelColors[color] {
		return newAPIError(http.StatusBadRequest, "INVALID_LABEL_COLOR", "invalid label color %q", color)
	}
	return nil
}

func sortLabels(labels []Label) {
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

func labelNameTaken(labels []Label, name, exceptID string) bool {
	for _, l := range labels {
		if l.ID != exceptID && strings.EqualFold(l.Name, name) {
			return true
		}
	}
	return false
}

func (s *Store) CreateLabel(projectID, rawName, color string) (Label, error) {
	name, err := normalizeLabelName(rawName)
	if err != nil {
		return Label{}, err
	}
	if err := checkLabelColor(color); err != nil {
		return Label{}, err
	}
	reg, _, err := s.loadProject(projectID)
	if err != nil {
		return Label{}, err
	}
	defer s.lock(reg.MetaIssueKey)()
	reg, meta, err := s.loadProject(projectID)
	if err != nil {
		return Label{}, err
	}
	if labelNameTaken(meta.Labels, name, "") {
		return Label{}, newAPIError(http.StatusConflict, "LABEL_NAME_TAKEN", "a label named %q already exists in project %s", name, projectID)
	}
	meta.LabelSeq++
	now := s.now()
	l := Label{ID: fmt.Sprintf("%s-label-%d", reg.Key, meta.LabelSeq), ProjectID: projectID, Name: name, Color: color, CreatedAt: now, UpdatedAt: now}
	meta.Labels = append(meta.Labels, l)
	if err := s.saveProjectMeta(reg, meta); err != nil {
		return Label{}, err
	}
	return l, nil
}

// findLabel resolves a label ID to its project's metadata.
func (s *Store) findLabel(id string) (registeredProject, projectProp, int, error) {
	key, ok := projectKeyOfLabel(id)
	if !ok {
		return registeredProject{}, projectProp{}, -1, notFound("LABEL_NOT_FOUND", "label", id)
	}
	reg, meta, err := s.loadProject(projectIDFromKey(key))
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.Code == "PROJECT_NOT_FOUND" {
			return registeredProject{}, projectProp{}, -1, notFound("LABEL_NOT_FOUND", "label", id)
		}
		return registeredProject{}, projectProp{}, -1, err
	}
	for i, l := range meta.Labels {
		if l.ID == id {
			return reg, meta, i, nil
		}
	}
	return registeredProject{}, projectProp{}, -1, notFound("LABEL_NOT_FOUND", "label", id)
}

func (s *Store) GetLabel(id string) (Label, error) {
	_, meta, i, err := s.findLabel(id)
	if err != nil {
		return Label{}, err
	}
	return meta.Labels[i], nil
}

func (s *Store) ListLabelsByProject(projectID string) ([]LabelUsage, error) {
	reg, meta, err := s.loadProject(projectID)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	issues, err := s.jira.search(ticketJQL([]string{reg.Key}), []string{propTicket})
	if err != nil {
		return nil, jiraFailure(err)
	}
	for _, issue := range issues {
		var tp ticketProp
		if ok, _ := decodeProp(issue.Properties, propTicket, &tp); ok {
			for _, id := range tp.LabelIDs {
				counts[id]++
			}
		}
	}
	labels := append([]Label(nil), meta.Labels...)
	sortLabels(labels)
	out := []LabelUsage{}
	for _, l := range labels {
		out = append(out, LabelUsage{Label: l, TicketCount: counts[l.ID]})
	}
	return out, nil
}

func (s *Store) UpdateLabel(id string, name, color *string) (Label, error) {
	var newName string
	if name != nil {
		n, err := normalizeLabelName(*name)
		if err != nil {
			return Label{}, err
		}
		newName = n
	}
	if color != nil {
		if err := checkLabelColor(*color); err != nil {
			return Label{}, err
		}
	}
	reg, _, _, err := s.findLabel(id)
	if err != nil {
		return Label{}, err
	}
	defer s.lock(reg.MetaIssueKey)()
	reg, meta, i, err := s.findLabel(id)
	if err != nil {
		return Label{}, err
	}
	l := &meta.Labels[i]
	if name != nil {
		if labelNameTaken(meta.Labels, newName, id) {
			return Label{}, newAPIError(http.StatusConflict, "LABEL_NAME_TAKEN", "a label named %q already exists", newName)
		}
		l.Name = newName
	}
	if color != nil {
		l.Color = *color
	}
	l.UpdatedAt = s.now()
	if err := s.saveProjectMeta(reg, meta); err != nil {
		return Label{}, err
	}
	return *l, nil
}

// DeleteLabel removes the label from every ticket of its project and then
// from the project's label definitions.
func (s *Store) DeleteLabel(id string) (int, error) {
	reg, _, _, err := s.findLabel(id)
	if err != nil {
		return 0, err
	}
	issues, err := s.jira.search(ticketJQL([]string{reg.Key}), []string{propTicket})
	if err != nil {
		return 0, jiraFailure(err)
	}
	removed := 0
	for _, issue := range issues {
		var tp ticketProp
		if ok, _ := decodeProp(issue.Properties, propTicket, &tp); !ok || !containsString(tp.LabelIDs, id) {
			continue
		}
		n, err := s.detachLabel(issue.Key, id)
		if err != nil {
			return 0, err
		}
		removed += n
	}
	defer s.lock(reg.MetaIssueKey)()
	reg, meta, i, err := s.findLabel(id)
	if err != nil {
		return 0, err
	}
	meta.Labels = append(meta.Labels[:i], meta.Labels[i+1:]...)
	if err := s.saveProjectMeta(reg, meta); err != nil {
		return 0, err
	}
	return removed, nil
}

func (s *Store) detachLabel(issueKey, labelID string) (int, error) {
	defer s.lock(issueKey)()
	issue, tp, err := s.readTicket(issueKey)
	if err != nil {
		return 0, err
	}
	_ = issue
	var kept []string
	removed := 0
	for _, id := range tp.LabelIDs {
		if id == labelID {
			removed = 1
			continue
		}
		kept = append(kept, id)
	}
	if removed == 0 {
		return 0, nil
	}
	tp.LabelIDs = kept
	if err := s.writeTicketProp(issueKey, tp); err != nil {
		return 0, err
	}
	return removed, nil
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// --- tickets ---

// ticketJQL selects the managed (non-metadata) issues of the given projects.
func ticketJQL(keys []string) string {
	project := "project = " + keys[0]
	if len(keys) > 1 {
		project = "project in (" + strings.Join(keys, ", ") + ")"
	}
	return project + " AND labels = " + jiraLabelManaged + " AND labels != " + jiraLabelMeta + " ORDER BY created DESC"
}

// readTicket loads a managed ticket issue and its graphops.ticket property.
func (s *Store) readTicket(issueKey string, extraProps ...string) (*jiraIssue, ticketProp, error) {
	issue, err := s.jira.getIssue(issueKey, append([]string{propTicket}, extraProps...))
	if err != nil {
		if isJiraStatus(err, http.StatusNotFound) {
			return nil, ticketProp{}, notFound("TICKET_NOT_FOUND", "ticket", issueKey)
		}
		return nil, ticketProp{}, jiraFailure(err)
	}
	var tp ticketProp
	ok, err := decodeProp(issue.Properties, propTicket, &tp)
	if err != nil {
		return nil, ticketProp{}, jiraFailure(err)
	}
	if !ok || !containsString(issue.Fields.Labels, jiraLabelManaged) {
		return nil, ticketProp{}, notFound("TICKET_NOT_FOUND", "ticket", issueKey)
	}
	if _, registered, err := s.registered(projectKeyOfIssue(issue.Key)); err != nil {
		return nil, ticketProp{}, jiraFailure(err)
	} else if !registered {
		return nil, ticketProp{}, notFound("TICKET_NOT_FOUND", "ticket", issueKey)
	}
	return issue, tp, nil
}

func (s *Store) writeTicketProp(issueKey string, tp ticketProp) error {
	raw, err := marshalProperty("the ticket (graphops.ticket)", tp)
	if err != nil {
		return err
	}
	if err := s.jira.setIssueProperty(issueKey, propTicket, raw); err != nil {
		return jiraFailure(err)
	}
	return nil
}

// labelsByID returns every label of the given projects, by ID.
func (s *Store) labelsByID(keys ...string) (map[string]Label, error) {
	out := map[string]Label{}
	for _, key := range keys {
		_, meta, err := s.loadProject(projectIDFromKey(key))
		if err != nil {
			return nil, err
		}
		for _, l := range meta.Labels {
			out[l.ID] = l
		}
	}
	return out, nil
}

func ticketFrom(issueKey string, tp ticketProp, labels map[string]Label) Ticket {
	t := Ticket{
		ID: issueKey, ProjectID: projectIDFromKey(projectKeyOfIssue(issueKey)),
		Title: tp.Title, Description: tp.Description, Status: tp.Status,
		AutoExecutable: tp.AutoExecutable, Blocked: tp.Blocked,
		CreatedAt: tp.CreatedAt, UpdatedAt: tp.UpdatedAt,
		RefinedAt: tp.RefinedAt, ClosedReason: tp.ClosedReason, Assignee: tp.Assignee,
		GraphExpandedAt: tp.GraphExpandedAt, Priority: tp.Priority, Labels: []Label{},
	}
	if t.Priority == "" {
		t.Priority = "MEDIUM"
	}
	for _, id := range tp.LabelIDs {
		if l, ok := labels[id]; ok {
			t.Labels = append(t.Labels, l)
		}
	}
	sortLabels(t.Labels)
	return t
}

func validateLabelIDs(ids []string, labels map[string]Label, projectID string) ([]string, error) {
	seen := map[string]bool{}
	var out, missing []string
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, ok := labels[id]; !ok {
			missing = append(missing, id)
			continue
		}
		out = append(out, id)
	}
	if len(missing) > 0 {
		return nil, notFound("LABEL_NOT_FOUND", "label(s)", strings.Join(missing, ", ")+" in project "+projectID)
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// CreateTicket creates a Jira issue labelled graphops. The ticket ID is the
// new issue key. No workflow transition is made.
func (s *Store) CreateTicket(projectID string, in Ticket) (Ticket, error) {
	reg, meta, err := s.loadProject(projectID)
	if err != nil {
		return Ticket{}, err
	}
	labels := map[string]Label{}
	for _, l := range meta.Labels {
		labels[l.ID] = l
	}
	var ids []string
	for _, l := range in.Labels {
		ids = append(ids, l.ID)
	}
	labelIDs, err := validateLabelIDs(ids, labels, projectID)
	if err != nil {
		return Ticket{}, err
	}
	now := s.now()
	tp := ticketProp{
		Title: in.Title, Description: in.Description, Status: in.Status,
		AutoExecutable: in.AutoExecutable, Blocked: in.Blocked, Assignee: in.Assignee,
		Priority: in.Priority, LabelIDs: labelIDs, CreatedAt: now, UpdatedAt: now,
	}
	if tp.Priority == "" {
		tp.Priority = "MEDIUM"
	}
	rawTicket, err := marshalProperty("the ticket (graphops.ticket)", tp)
	if err != nil {
		return Ticket{}, err
	}
	rawGraph, _ := json.Marshal(graphProp{Nodes: []GraphNode{}, Edges: []GraphEdge{}})
	key, err := s.jira.createIssue(map[string]any{
		"project":     map[string]string{"key": reg.Key},
		"summary":     summaryFor(in.Title),
		"issuetype":   map[string]string{"name": s.issueType},
		"labels":      []string{jiraLabelManaged},
		"description": markdownToADF(in.Description),
	}, []jiraProperty{
		{Key: propTicket, Value: json.RawMessage(rawTicket)},
		{Key: propGraph, Value: json.RawMessage(rawGraph)},
	})
	if err != nil {
		return Ticket{}, jiraFailure(err)
	}
	return ticketFrom(key, tp, labels), nil
}

func (s *Store) GetTicket(id string) (Ticket, error) {
	issue, tp, err := s.readTicket(id)
	if err != nil {
		return Ticket{}, err
	}
	labels, err := s.labelsByID(projectKeyOfIssue(issue.Key))
	if err != nil {
		return Ticket{}, err
	}
	return ticketFrom(issue.Key, tp, labels), nil
}

func (s *Store) GetTicketDetail(id string) (TicketDetail, error) {
	issue, tp, err := s.readTicket(id, propGraph)
	if err != nil {
		return TicketDetail{}, err
	}
	labels, err := s.labelsByID(projectKeyOfIssue(issue.Key))
	if err != nil {
		return TicketDetail{}, err
	}
	var g graphProp
	if _, err := decodeProp(issue.Properties, propGraph, &g); err != nil {
		return TicketDetail{}, jiraFailure(err)
	}
	arts, err := s.listArtifacts(issue.Key, "", false)
	if err != nil {
		return TicketDetail{}, err
	}
	d := TicketDetail{Ticket: ticketFrom(issue.Key, tp, labels), Nodes: g.Nodes, Edges: g.Edges, Artifacts: arts}
	if d.Nodes == nil {
		d.Nodes = []GraphNode{}
	}
	if d.Edges == nil {
		d.Edges = []GraphEdge{}
	}
	return d, nil
}

// listTickets runs one JQL search (paged) that also returns every issue's
// graphops.ticket property, so listing is never one request per issue.
func (s *Store) listTickets(keys []string) ([]Ticket, error) {
	out := []Ticket{}
	if len(keys) == 0 {
		return out, nil
	}
	labels, err := s.labelsByID(keys...)
	if err != nil {
		return nil, err
	}
	issues, err := s.jira.search(ticketJQL(keys), []string{propTicket})
	if err != nil {
		return nil, jiraFailure(err)
	}
	for _, issue := range issues {
		var tp ticketProp
		ok, err := decodeProp(issue.Properties, propTicket, &tp)
		if err != nil || !ok {
			continue // labelled graphops by hand, never created through GraphOps
		}
		out = append(out, ticketFrom(issue.Key, tp, labels))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

func (s *Store) ListTickets() ([]Ticket, error) {
	st, err := s.state.read()
	if err != nil {
		return nil, jiraFailure(err)
	}
	var keys []string
	for _, p := range st.Projects {
		keys = append(keys, p.Key)
	}
	return s.listTickets(keys)
}

func (s *Store) ListTicketsByProject(projectID string) ([]Ticket, error) {
	key, ok := projectKeyFromID(projectID)
	if !ok {
		return []Ticket{}, nil
	}
	reg, ok, err := s.registered(key)
	if err != nil {
		return nil, jiraFailure(err)
	}
	if !ok {
		return []Ticket{}, nil
	}
	return s.listTickets([]string{reg.Key})
}

// UpdateTicket applies a patch to graphops.ticket, and mirrors a changed
// title/description to the issue's summary/description. It never
// transitions the issue in Jira's workflow.
func (s *Store) UpdateTicket(id string, p TicketPatch) (Ticket, error) {
	defer s.lock(id)()
	issue, tp, err := s.readTicket(id)
	if err != nil {
		return Ticket{}, err
	}
	labels, err := s.labelsByID(projectKeyOfIssue(issue.Key))
	if err != nil {
		return Ticket{}, err
	}
	if p.LabelIDs != nil {
		ids, err := validateLabelIDs(*p.LabelIDs, labels, projectIDFromKey(projectKeyOfIssue(issue.Key)))
		if err != nil {
			return Ticket{}, err
		}
		tp.LabelIDs = ids
	}
	fields := map[string]any{}
	if p.Title != nil && *p.Title != tp.Title {
		tp.Title = *p.Title
		fields["summary"] = summaryFor(tp.Title)
	}
	if p.Description != nil && *p.Description != tp.Description {
		tp.Description = *p.Description
		fields["description"] = markdownToADF(tp.Description)
	}
	if p.Status != nil {
		tp.Status = *p.Status
	}
	if p.AutoExecutable != nil {
		tp.AutoExecutable = *p.AutoExecutable
	}
	if p.Blocked != nil {
		tp.Blocked = *p.Blocked
	}
	if p.RefinedAt != nil {
		tp.RefinedAt = p.RefinedAt
	}
	if p.ClosedReason != nil {
		tp.ClosedReason = p.ClosedReason
	}
	if p.Assignee.Set {
		tp.Assignee = p.Assignee.Value
	}
	if p.GraphExpandedAt != nil {
		tp.GraphExpandedAt = p.GraphExpandedAt
	}
	if p.Priority != nil {
		tp.Priority = *p.Priority
	}
	if tp.Priority == "" {
		tp.Priority = "MEDIUM"
	}
	tp.UpdatedAt = s.now()
	if err := s.writeTicketProp(issue.Key, tp); err != nil {
		return Ticket{}, err
	}
	if len(fields) > 0 {
		if err := s.jira.editIssue(issue.Key, map[string]any{"fields": fields}); err != nil {
			return Ticket{}, jiraFailure(err)
		}
	}
	return ticketFrom(issue.Key, tp, labels), nil
}

// DeleteTicket detaches the issue from GraphOps instead of deleting it from
// Jira: the graphops label and the graphops.ticket/graphops.graph
// properties are removed, so the ticket (with its nodes and edges) is gone
// from GraphOps, while the Jira issue -- and the artifact comments on it --
// stay for the humans who use Jira. A missing ticket is a no-op.
func (s *Store) DeleteTicket(id string) error {
	defer s.lock(id)()
	issue, _, err := s.readTicket(id)
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.Code == "TICKET_NOT_FOUND" {
			return nil
		}
		return err
	}
	if err := s.jira.editIssue(issue.Key, map[string]any{
		"update": map[string]any{"labels": []map[string]string{{"remove": jiraLabelManaged}}},
	}); err != nil {
		return jiraFailure(err)
	}
	for _, prop := range []string{propTicket, propGraph} {
		if err := s.jira.deleteIssueProperty(issue.Key, prop); err != nil {
			return jiraFailure(err)
		}
	}
	return nil
}

// --- nodes and edges ---

func (s *Store) readGraph(issueKey string) (graphProp, error) {
	issue, _, err := s.readTicket(issueKey, propGraph)
	if err != nil {
		return graphProp{}, err
	}
	var g graphProp
	if _, err := decodeProp(issue.Properties, propGraph, &g); err != nil {
		return graphProp{}, jiraFailure(err)
	}
	if g.Nodes == nil {
		g.Nodes = []GraphNode{}
	}
	if g.Edges == nil {
		g.Edges = []GraphEdge{}
	}
	return g, nil
}

func (s *Store) writeGraph(issueKey string, g graphProp) error {
	raw, err := marshalProperty("the execution graph (graphops.graph) of "+issueKey, g)
	if err != nil {
		return err
	}
	if err := s.jira.setIssueProperty(issueKey, propGraph, raw); err != nil {
		return jiraFailure(err)
	}
	return nil
}

func (s *Store) CreateNode(ticketID string, in GraphNode) (GraphNode, error) {
	defer s.lock(ticketID)()
	g, err := s.readGraph(ticketID)
	if err != nil {
		return GraphNode{}, err
	}
	if g.NodeSeq >= maxNodesPerTicket {
		return GraphNode{}, validationError("ticket %s already has the maximum of %d nodes", ticketID, maxNodesPerTicket)
	}
	g.NodeSeq++
	n := in
	n.ID = fmt.Sprintf("%s-%02d", ticketID, g.NodeSeq)
	n.TicketID = ticketID
	if n.MaxIterations == 0 {
		n.MaxIterations = 3
	}
	now := s.now()
	n.CreatedAt, n.UpdatedAt = now, now
	g.Nodes = append(g.Nodes, n)
	if err := s.writeGraph(ticketID, g); err != nil {
		return GraphNode{}, err
	}
	return n, nil
}

func (s *Store) findNode(nodeID string) (string, graphProp, int, error) {
	issueKey, ok := issueKeyOfNode(nodeID)
	if !ok {
		return "", graphProp{}, -1, notFound("NODE_NOT_FOUND", "node", nodeID)
	}
	g, err := s.readGraph(issueKey)
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.Code == "TICKET_NOT_FOUND" {
			return "", graphProp{}, -1, notFound("NODE_NOT_FOUND", "node", nodeID)
		}
		return "", graphProp{}, -1, err
	}
	for i, n := range g.Nodes {
		if n.ID == nodeID {
			return issueKey, g, i, nil
		}
	}
	return "", graphProp{}, -1, notFound("NODE_NOT_FOUND", "node", nodeID)
}

func (s *Store) GetNode(id string) (GraphNode, error) {
	_, g, i, err := s.findNode(id)
	if err != nil {
		return GraphNode{}, err
	}
	return g.Nodes[i], nil
}

func (s *Store) ListNodesByTicket(ticketID string) ([]GraphNode, error) {
	g, err := s.readGraph(ticketID)
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.Code == "TICKET_NOT_FOUND" {
			return []GraphNode{}, nil
		}
		return nil, err
	}
	return g.Nodes, nil
}

func (s *Store) UpdateNode(id string, p NodePatch) (GraphNode, error) {
	issueKey, ok := issueKeyOfNode(id)
	if !ok {
		return GraphNode{}, notFound("NODE_NOT_FOUND", "node", id)
	}
	defer s.lock(issueKey)()
	_, g, i, err := s.findNode(id)
	if err != nil {
		return GraphNode{}, err
	}
	n := &g.Nodes[i]
	if p.Name != nil {
		n.Name = *p.Name
	}
	if p.Type != nil {
		n.Type = *p.Type
	}
	if p.Status != nil {
		n.Status = *p.Status
	}
	if p.IterationCount != nil {
		n.IterationCount = *p.IterationCount
	}
	if p.MaxIterations != nil {
		n.MaxIterations = *p.MaxIterations
	}
	if p.Assignee.Set {
		n.Assignee = p.Assignee.Value
	}
	if p.IsManual != nil {
		n.IsManual = *p.IsManual
	}
	if p.GateID != nil {
		n.GateID = p.GateID
	}
	if p.Criteria != nil {
		n.Criteria = p.Criteria
	}
	n.UpdatedAt = s.now()
	if err := s.writeGraph(issueKey, g); err != nil {
		return GraphNode{}, err
	}
	return *n, nil
}

// DeleteNode removes a node, the edges touching it and its artifact
// comments (with their attachments). A missing node is a no-op.
func (s *Store) DeleteNode(id string) error {
	issueKey, ok := issueKeyOfNode(id)
	if !ok {
		return nil
	}
	defer s.lock(issueKey)()
	_, g, i, err := s.findNode(id)
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.Code == "NODE_NOT_FOUND" {
			return nil
		}
		return err
	}
	g.Nodes = append(g.Nodes[:i], g.Nodes[i+1:]...)
	var edges []GraphEdge
	for _, e := range g.Edges {
		if e.FromNodeID != id && e.ToNodeID != id {
			edges = append(edges, e)
		}
	}
	g.Edges = edges
	if g.Edges == nil {
		g.Edges = []GraphEdge{}
	}
	if err := s.writeGraph(issueKey, g); err != nil {
		return err
	}
	comments, err := s.jira.listComments(issueKey)
	if err != nil {
		return jiraFailure(err)
	}
	for _, cm := range comments {
		ap, ok := artifactPropOf(cm)
		if !ok || ap.NodeID != id {
			continue
		}
		if ap.AttachmentID != "" {
			if err := s.jira.deleteAttachment(ap.AttachmentID); err != nil {
				return jiraFailure(err)
			}
		}
		if err := s.jira.deleteComment(issueKey, cm.ID); err != nil {
			return jiraFailure(err)
		}
	}
	return nil
}

func (s *Store) CreateEdge(ticketID string, in GraphEdge) (GraphEdge, error) {
	defer s.lock(ticketID)()
	g, err := s.readGraph(ticketID)
	if err != nil {
		return GraphEdge{}, err
	}
	e := in
	e.TicketID = ticketID
	if e.ID == "" {
		g.EdgeSeq++
		e.ID = fmt.Sprintf("%s-e%d", ticketID, g.EdgeSeq)
	}
	if e.Condition == "" {
		e.Condition = "always"
	}
	e.CreatedAt = s.now()
	g.Edges = append(g.Edges, e)
	if err := s.writeGraph(ticketID, g); err != nil {
		return GraphEdge{}, err
	}
	return e, nil
}

func (s *Store) ListEdgesByTicket(ticketID string) ([]GraphEdge, error) {
	g, err := s.readGraph(ticketID)
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.Code == "TICKET_NOT_FOUND" {
			return []GraphEdge{}, nil
		}
		return nil, err
	}
	return g.Edges, nil
}

func (s *Store) ClearEdgesByTicket(ticketID string) error {
	defer s.lock(ticketID)()
	g, err := s.readGraph(ticketID)
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.Code == "TICKET_NOT_FOUND" {
			return nil
		}
		return err
	}
	g.Edges = []GraphEdge{}
	return s.writeGraph(ticketID, g)
}

// --- artifacts ---

func artifactPropOf(cm jiraComment) (artifactProp, bool) {
	raw, ok := cm.property(propArtifact)
	if !ok {
		return artifactProp{}, false
	}
	var ap artifactProp
	if err := json.Unmarshal(raw, &ap); err != nil || ap.Type == "" {
		return artifactProp{}, false
	}
	return ap, true
}

func isFileBacked(artType string) bool { return artType == "html" || artType == "image" }

var attachmentExt = map[string]string{"html": ".html", "image": ".bin", "json": ".json", "gherkin": ".feature", "text": ".md"}

// CreateArtifact stores an artifact as a comment on the ticket's issue.
// text/gherkin/json content up to inlineContentLimit is kept inline (in the
// comment's code block and its graphops.artifact property); larger content
// and every html/image artifact is uploaded as an issue attachment that the
// property points to. The returned ID is "<issue key>-c<comment id>".
func (s *Store) CreateArtifact(ticketID string, in Artifact) (Artifact, error) {
	defer s.lock(ticketID)()
	if _, _, err := s.readTicket(ticketID); err != nil {
		return Artifact{}, err
	}
	ap := artifactProp{
		NodeID: in.NodeID, Name: in.Name, Type: in.Type, FilePath: in.FilePath, Metadata: in.Metadata,
		CreatedAt: s.now(), HasContent: in.Content != nil && *in.Content != "",
	}
	var inline *string
	attachmentName := ""
	if ap.HasContent {
		content := *in.Content
		if !isFileBacked(in.Type) && len(content) <= inlineContentLimit {
			inline = &content
			ap.Content = &content
			if _, err := marshalProperty("artifact", ap); err != nil {
				inline, ap.Content = nil, nil
			}
		}
		if inline == nil {
			data := []byte(content)
			ap.AttachmentEncoding = "raw"
			if in.Type == "image" {
				if decoded, err := base64.StdEncoding.DecodeString(content); err == nil {
					data = decoded
					ap.AttachmentEncoding = "base64"
				}
			}
			ext := attachmentExt[in.Type]
			if ext == "" {
				ext = ".txt"
			}
			attachmentName = "graphops-artifact-" + strconv.FormatInt(time.Now().UnixNano(), 36) + ext
			id, err := s.jira.addAttachment(ticketID, attachmentName, data)
			if err != nil {
				return Artifact{}, jiraFailure(err)
			}
			ap.AttachmentID = id
		}
	}
	raw, err := marshalProperty("the artifact metadata (graphops.artifact)", ap)
	if err != nil {
		return Artifact{}, err
	}
	cm, err := s.jira.addComment(ticketID, artifactCommentBody(in, inline, attachmentName),
		[]jiraProperty{{Key: propArtifact, Value: json.RawMessage(raw)}})
	if err != nil {
		return Artifact{}, jiraFailure(err)
	}
	out := Artifact{
		ID: ticketID + "-c" + cm.ID, TicketID: ticketID, NodeID: ap.NodeID, Name: ap.Name, Type: ap.Type,
		Content: in.Content, FilePath: ap.FilePath, Metadata: ap.Metadata, HasContent: ap.HasContent, CreatedAt: ap.CreatedAt,
	}
	return out, nil
}

// artifactFrom rebuilds an Artifact from a comment. withContent controls
// whether attachment-backed content is downloaded.
func (s *Store) artifactFrom(issueKey string, cm jiraComment, ap artifactProp, withContent bool) (Artifact, error) {
	a := Artifact{
		ID: issueKey + "-c" + cm.ID, TicketID: issueKey, NodeID: ap.NodeID, Name: ap.Name, Type: ap.Type,
		FilePath: ap.FilePath, Metadata: ap.Metadata, HasContent: ap.HasContent, CreatedAt: ap.CreatedAt,
		Content: ap.Content,
	}
	if withContent && ap.AttachmentID != "" {
		data, err := s.jira.attachmentContent(ap.AttachmentID)
		if err != nil {
			return Artifact{}, jiraFailure(err)
		}
		content := string(data)
		if ap.AttachmentEncoding == "base64" {
			content = base64.StdEncoding.EncodeToString(data)
		}
		a.Content = &content
	}
	return a, nil
}

// listArtifacts lists an issue's artifacts, oldest first. With nodeID set,
// only that node's. For a ticket listing (fullContent false), html/image
// content is omitted (has_content still set), as the protocol allows;
// text/gherkin/json content is always included.
func (s *Store) listArtifacts(issueKey, nodeID string, fullContent bool) ([]Artifact, error) {
	comments, err := s.jira.listComments(issueKey)
	if err != nil {
		if isJiraStatus(err, http.StatusNotFound) {
			return []Artifact{}, nil
		}
		return nil, jiraFailure(err)
	}
	out := []Artifact{}
	for _, cm := range comments {
		ap, ok := artifactPropOf(cm)
		if !ok || (nodeID != "" && ap.NodeID != nodeID) {
			continue
		}
		withContent := fullContent || !isFileBacked(ap.Type)
		a, err := s.artifactFrom(issueKey, cm, ap, withContent)
		if err != nil {
			return nil, err
		}
		if !withContent {
			a.Content = nil
		}
		out = append(out, a)
	}
	return out, nil
}

func (s *Store) GetArtifact(id string) (Artifact, error) {
	issueKey, commentID, ok := parseArtifactID(id)
	if !ok {
		return Artifact{}, notFound("ARTIFACT_NOT_FOUND", "artifact", id)
	}
	cm, err := s.jira.getComment(issueKey, commentID)
	if err != nil {
		if isJiraStatus(err, http.StatusNotFound) {
			return Artifact{}, notFound("ARTIFACT_NOT_FOUND", "artifact", id)
		}
		return Artifact{}, jiraFailure(err)
	}
	ap, ok := artifactPropOf(*cm)
	if !ok {
		return Artifact{}, notFound("ARTIFACT_NOT_FOUND", "artifact", id)
	}
	return s.artifactFrom(issueKey, *cm, ap, true)
}

func (s *Store) ListArtifactsByTicket(ticketID string) ([]Artifact, error) {
	if _, _, err := s.readTicket(ticketID); err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.Code == "TICKET_NOT_FOUND" {
			return []Artifact{}, nil
		}
		return nil, err
	}
	return s.listArtifacts(ticketID, "", false)
}

func (s *Store) ListArtifactsByNode(nodeID string) ([]Artifact, error) {
	issueKey, ok := issueKeyOfNode(nodeID)
	if !ok {
		return []Artifact{}, nil
	}
	return s.listArtifacts(issueKey, nodeID, true)
}
