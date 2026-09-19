package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
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
//   - A node is a sub-task of the ticket's issue (issue type
//     JIRA_SUBTASK_ISSUE_TYPE), summary "<name> [<type>]". Its data lives in
//     the sub-task's graphops.node property. Its ID is
//     "<ticket key>-n<sub-task number>" ("GOPS-12-n15" is sub-task GOPS-15
//     of ticket GOPS-12). Its status is mirrored on the sub-task as exactly
//     one graphops-status-<status> label and, best effort, as a workflow
//     status (see nodes.go).
//   - Edges live in the graphops.edges property of the ticket's issue.
//   - An artifact is a comment on its node's sub-task, with its metadata in
//     the graphops.artifact comment property. Its ID is
//     "<node ID>-c<comment id>". Large text content and html/image content
//     are stored as attachments of that sub-task.
//   - Labels are stored in the project's metadata issue. Label IDs are
//     "<KEY>-label-<n>".
const (
	jiraLabelManaged = "graphops"
	jiraLabelMeta    = "graphops-meta"

	propTicket   = "graphops.ticket"
	propEdges    = "graphops.edges"
	propNode     = "graphops.node"
	propProject  = "graphops.project"
	propArtifact = "graphops.artifact"

	projectIDPrefix = "jira-"

	// maxPropertyBytes is Jira's limit on one entity property's JSON value.
	maxPropertyBytes = 32768
	// inlineContentLimit is the largest text/gherkin/json artifact content
	// kept inline (in the comment and its property); anything larger, and
	// every html/image artifact, goes to an attachment.
	inlineContentLimit = 16 * 1024

	// attachmentCleanupTimeout bounds removing an uploaded attachment after
	// its artifact comment could not be created.
	attachmentCleanupTimeout = 5 * time.Second

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

// storeOptions is the Jira-side configuration the store needs.
type storeOptions struct {
	// IssueType is the issue type of ticket and metadata issues.
	IssueType string
	// SubtaskIssueType is the (sub-task) issue type of node sub-tasks.
	SubtaskIssueType string
	// InProgressStatus and DoneStatus are the workflow status names a node
	// sub-task is moved to; "" turns that direction off.
	InProgressStatus string
	DoneStatus       string
}

// Store implements the 32 protocol operations on top of Jira.
type Store struct {
	jira *jiraClient
	// state is the local state file (registered projects, current project).
	state *stateFile
	opts  storeOptions
	now   func() string
	// logf, when non-nil, receives problems that do not fail the request.
	logf func(format string, args ...any)
	// transitionTimeout bounds moving one node sub-task through the
	// workflow (reading its transitions and making one).
	transitionTimeout time.Duration
	// detachParallelism is how many sub-tasks DeleteTicket cleans up at once.
	detachParallelism int
	// detachCleanupTimeout bounds the whole sub-task clean-up of one
	// DeleteTicket or DeleteProject request (cleanUpDetached).
	detachCleanupTimeout time.Duration

	locksMu sync.Mutex
	locks   map[string]*sync.Mutex
}

func newStore(jira *jiraClient, statePath string, opts storeOptions) *Store {
	return &Store{
		jira:                 jira,
		state:                &stateFile{path: statePath},
		opts:                 opts,
		now:                  func() string { return time.Now().UTC().Format(time.RFC3339Nano) },
		transitionTimeout:    defaultTransitionTimeout,
		detachParallelism:    4,
		detachCleanupTimeout: defaultDetachCleanupTimeout,
		locks:                map[string]*sync.Mutex{},
	}
}

// log reports a problem that does not fail the request.
func (s *Store) log(format string, args ...any) {
	if s.logf != nil {
		s.logf(format, args...)
	}
}

// lock serializes read-modify-write of one issue's properties within this
// process. The sample assumes a single plugin process per Jira site (see
// README.md, "Limitations").
//
// Lock order: an operation that needs both a ticket's lock and one of its
// node sub-tasks' locks (CreateNode, DeleteNode) always takes the ticket's
// first. Operations on one node (UpdateNode, CreateArtifact) and the
// sub-task clean-up after detaching a ticket (detachSubtasks, which runs
// after the ticket's lock was released) take only the sub-task's lock, so
// no two operations can wait on each other in opposite orders.
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
func (s *Store) loadProject(ctx context.Context, projectID string) (registeredProject, projectProp, error) {
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
	issue, err := s.jira.getIssue(ctx, reg.MetaIssueKey, []string{propProject})
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

func (s *Store) saveProjectMeta(ctx context.Context, reg registeredProject, meta projectProp) error {
	raw, err := marshalProperty("the project metadata (graphops.project)", meta)
	if err != nil {
		return err
	}
	if err := s.jira.setIssueProperty(ctx, reg.MetaIssueKey, propProject, raw); err != nil {
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
func (s *Store) CreateProject(ctx context.Context, name, prefix string) (Project, error) {
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
	jp, err := s.jira.getProject(ctx, key)
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
	existing, err := s.jira.search(ctx, fmt.Sprintf("project = %s AND labels = %s ORDER BY created ASC", key, jiraLabelMeta), []string{propProject})
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
		if err := s.saveProjectMeta(ctx, registeredProject{Key: key, MetaIssueKey: metaKey}, meta); err != nil {
			return Project{}, err
		}
	} else {
		raw, err := marshalProperty("the project metadata (graphops.project)", meta)
		if err != nil {
			return Project{}, err
		}
		metaKey, err = s.jira.createIssue(ctx, map[string]any{
			"project":     map[string]string{"key": key},
			"summary":     "GraphOps metadata (do not delete)",
			"issuetype":   map[string]string{"name": s.opts.IssueType},
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

func (s *Store) GetProject(ctx context.Context, id string) (Project, error) {
	reg, meta, err := s.loadProject(ctx, id)
	if err != nil {
		return Project{}, err
	}
	return projectFrom(reg, meta), nil
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	st, err := s.state.read()
	if err != nil {
		return nil, jiraFailure(err)
	}
	out := []Project{}
	for _, reg := range st.Projects {
		p, err := s.GetProject(ctx, projectIDFromKey(reg.Key))
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

func (s *Store) UpdateProject(ctx context.Context, id string, name *string) (Project, error) {
	reg, _, err := s.loadProject(ctx, id)
	if err != nil {
		return Project{}, err
	}
	defer s.lock(reg.MetaIssueKey)()
	reg, meta, err := s.loadProject(ctx, id)
	if err != nil {
		return Project{}, err
	}
	if name != nil {
		meta.Name = *name
	}
	meta.UpdatedAt = s.now()
	if err := s.saveProjectMeta(ctx, reg, meta); err != nil {
		return Project{}, err
	}
	return projectFrom(reg, meta), nil
}

// DeleteProject unregisters the project: its tickets are detached from
// GraphOps (see DeleteTicket), its metadata issue is deleted, and it is
// removed from the local state (clearing the current project if it pointed
// here). The Jira project itself is untouched. A missing project is a no-op.
//
// What the request's success means -- every ticket detached, the project
// unregistered -- is done first, for all tickets; only then are the node
// sub-tasks of all of them cleaned up (cleanUpDetached), best effort and
// within detachCleanupTimeout. The clean-up grows with the number of nodes,
// so doing it ticket by ticket in between could use up the request's
// deadline and fail the detaching of the later tickets.
func (s *Store) DeleteProject(ctx context.Context, id string) error {
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
	issues, err := s.jira.search(ctx, ticketJQL([]string{reg.Key}), []string{propTicket})
	if err != nil {
		return jiraFailure(err)
	}
	var detached []*jiraIssue
	for _, issue := range issues {
		ticket, err := s.detachTicket(ctx, issue.Key)
		if err != nil {
			// The tickets detached so far are gone from GraphOps; clean up
			// their sub-tasks now, as a retry will not find them again.
			s.cleanUpDetached(ctx, detached)
			return err
		}
		if ticket != nil {
			detached = append(detached, ticket)
		}
	}
	if err := s.jira.deleteIssue(ctx, reg.MetaIssueKey); err != nil && !isJiraStatus(err, http.StatusNotFound) {
		s.cleanUpDetached(ctx, detached)
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
		s.cleanUpDetached(ctx, detached)
		return jiraFailure(err)
	}
	s.cleanUpDetached(ctx, detached)
	return nil
}

// --- current project ---

func (s *Store) GetCurrentProjectID(ctx context.Context) (string, error) {
	st, err := s.state.read()
	if err != nil {
		return "", jiraFailure(err)
	}
	return st.CurrentProjectID, nil
}

func (s *Store) SetCurrentProjectID(ctx context.Context, id string) error {
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

func (s *Store) CreateLabel(ctx context.Context, projectID, rawName, color string) (Label, error) {
	name, err := normalizeLabelName(rawName)
	if err != nil {
		return Label{}, err
	}
	if err := checkLabelColor(color); err != nil {
		return Label{}, err
	}
	reg, _, err := s.loadProject(ctx, projectID)
	if err != nil {
		return Label{}, err
	}
	defer s.lock(reg.MetaIssueKey)()
	reg, meta, err := s.loadProject(ctx, projectID)
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
	if err := s.saveProjectMeta(ctx, reg, meta); err != nil {
		return Label{}, err
	}
	return l, nil
}

// findLabel resolves a label ID to its project's metadata.
func (s *Store) findLabel(ctx context.Context, id string) (registeredProject, projectProp, int, error) {
	key, ok := projectKeyOfLabel(id)
	if !ok {
		return registeredProject{}, projectProp{}, -1, notFound("LABEL_NOT_FOUND", "label", id)
	}
	reg, meta, err := s.loadProject(ctx, projectIDFromKey(key))
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

func (s *Store) GetLabel(ctx context.Context, id string) (Label, error) {
	_, meta, i, err := s.findLabel(ctx, id)
	if err != nil {
		return Label{}, err
	}
	return meta.Labels[i], nil
}

func (s *Store) ListLabelsByProject(ctx context.Context, projectID string) ([]LabelUsage, error) {
	reg, meta, err := s.loadProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	issues, err := s.jira.search(ctx, ticketJQL([]string{reg.Key}), []string{propTicket})
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

func (s *Store) UpdateLabel(ctx context.Context, id string, name, color *string) (Label, error) {
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
	reg, _, _, err := s.findLabel(ctx, id)
	if err != nil {
		return Label{}, err
	}
	defer s.lock(reg.MetaIssueKey)()
	reg, meta, i, err := s.findLabel(ctx, id)
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
	if err := s.saveProjectMeta(ctx, reg, meta); err != nil {
		return Label{}, err
	}
	return *l, nil
}

// DeleteLabel removes the label from every ticket of its project and then
// from the project's label definitions.
func (s *Store) DeleteLabel(ctx context.Context, id string) (int, error) {
	reg, _, _, err := s.findLabel(ctx, id)
	if err != nil {
		return 0, err
	}
	issues, err := s.jira.search(ctx, ticketJQL([]string{reg.Key}), []string{propTicket})
	if err != nil {
		return 0, jiraFailure(err)
	}
	removed := 0
	for _, issue := range issues {
		var tp ticketProp
		if ok, _ := decodeProp(issue.Properties, propTicket, &tp); !ok || !containsString(tp.LabelIDs, id) {
			continue
		}
		n, err := s.detachLabel(ctx, issue.Key, id)
		if err != nil {
			return 0, err
		}
		removed += n
	}
	defer s.lock(reg.MetaIssueKey)()
	reg, meta, i, err := s.findLabel(ctx, id)
	if err != nil {
		return 0, err
	}
	meta.Labels = append(meta.Labels[:i], meta.Labels[i+1:]...)
	if err := s.saveProjectMeta(ctx, reg, meta); err != nil {
		return 0, err
	}
	return removed, nil
}

func (s *Store) detachLabel(ctx context.Context, issueKey, labelID string) (int, error) {
	defer s.lock(issueKey)()
	issue, tp, err := s.readTicket(ctx, issueKey)
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
	if err := s.writeTicketProp(ctx, issueKey, tp); err != nil {
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
func (s *Store) readTicket(ctx context.Context, issueKey string, extraProps ...string) (*jiraIssue, ticketProp, error) {
	return s.readTicketFields(ctx, issueKey, defaultIssueFields, extraProps...)
}

// ticketFieldsWithSubtasks is what reading a ticket together with its node
// sub-tasks asks for.
var ticketFieldsWithSubtasks = []string{"summary", "labels", "created", "subtasks"}

// readTicketFields is readTicket with the issue fields to read.
func (s *Store) readTicketFields(ctx context.Context, issueKey string, fields []string, extraProps ...string) (*jiraIssue, ticketProp, error) {
	issue, err := s.jira.getIssueFields(ctx, issueKey, fields, append([]string{propTicket}, extraProps...))
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

func (s *Store) writeTicketProp(ctx context.Context, issueKey string, tp ticketProp) error {
	raw, err := marshalProperty("the ticket (graphops.ticket)", tp)
	if err != nil {
		return err
	}
	if err := s.jira.setIssueProperty(ctx, issueKey, propTicket, raw); err != nil {
		return jiraFailure(err)
	}
	return nil
}

// labelsByID returns every label of the given projects, by ID.
func (s *Store) labelsByID(ctx context.Context, keys ...string) (map[string]Label, error) {
	out := map[string]Label{}
	for _, key := range keys {
		_, meta, err := s.loadProject(ctx, projectIDFromKey(key))
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
func (s *Store) CreateTicket(ctx context.Context, projectID string, in Ticket) (Ticket, error) {
	reg, meta, err := s.loadProject(ctx, projectID)
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
	rawEdges, _ := json.Marshal(edgesProp{Edges: []storedEdge{}})
	key, err := s.jira.createIssue(ctx, map[string]any{
		"project":     map[string]string{"key": reg.Key},
		"summary":     summaryFor(in.Title),
		"issuetype":   map[string]string{"name": s.opts.IssueType},
		"labels":      []string{jiraLabelManaged},
		"description": markdownToADF(in.Description),
	}, []jiraProperty{
		{Key: propTicket, Value: json.RawMessage(rawTicket)},
		{Key: propEdges, Value: json.RawMessage(rawEdges)},
	})
	if err != nil {
		return Ticket{}, jiraFailure(err)
	}
	return ticketFrom(key, tp, labels), nil
}

func (s *Store) GetTicket(ctx context.Context, id string) (Ticket, error) {
	issue, tp, err := s.readTicket(ctx, id)
	if err != nil {
		return Ticket{}, err
	}
	labels, err := s.labelsByID(ctx, projectKeyOfIssue(issue.Key))
	if err != nil {
		return Ticket{}, err
	}
	return ticketFrom(issue.Key, tp, labels), nil
}

func (s *Store) GetTicketDetail(ctx context.Context, id string) (TicketDetail, error) {
	issue, tp, err := s.readTicketFields(ctx, id, ticketFieldsWithSubtasks, propEdges)
	if err != nil {
		return TicketDetail{}, err
	}
	labels, err := s.labelsByID(ctx, projectKeyOfIssue(issue.Key))
	if err != nil {
		return TicketDetail{}, err
	}
	ep, err := edgesOf(issue)
	if err != nil {
		return TicketDetail{}, err
	}
	nodes, err := s.loadNodes(ctx, issue)
	if err != nil {
		return TicketDetail{}, err
	}
	arts, err := s.ticketArtifacts(ctx, issue.Key, nodes)
	if err != nil {
		return TicketDetail{}, err
	}
	d := TicketDetail{Ticket: ticketFrom(issue.Key, tp, labels), Nodes: []GraphNode{}, Edges: ep.graphEdges(issue.Key), Artifacts: arts}
	for _, n := range nodes {
		d.Nodes = append(d.Nodes, n.graphNode())
	}
	return d, nil
}

// listTickets runs one JQL search (paged) that also returns every issue's
// graphops.ticket property, so listing is never one request per issue.
func (s *Store) listTickets(ctx context.Context, keys []string) ([]Ticket, error) {
	out := []Ticket{}
	// Load each project's labels; like ListProjects, skip a registered
	// project whose metadata issue was deleted in Jira instead of failing
	// the whole listing.
	labels := map[string]Label{}
	var live []string
	for _, key := range keys {
		_, meta, err := s.loadProject(ctx, projectIDFromKey(key))
		if err != nil {
			var apiErr *apiError
			if errors.As(err, &apiErr) && apiErr.Code == "PROJECT_NOT_FOUND" {
				continue
			}
			return nil, err
		}
		for _, l := range meta.Labels {
			labels[l.ID] = l
		}
		live = append(live, key)
	}
	keys = live
	if len(keys) == 0 {
		return out, nil
	}
	issues, err := s.jira.search(ctx, ticketJQL(keys), []string{propTicket})
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

func (s *Store) ListTickets(ctx context.Context) ([]Ticket, error) {
	st, err := s.state.read()
	if err != nil {
		return nil, jiraFailure(err)
	}
	var keys []string
	for _, p := range st.Projects {
		keys = append(keys, p.Key)
	}
	return s.listTickets(ctx, keys)
}

func (s *Store) ListTicketsByProject(ctx context.Context, projectID string) ([]Ticket, error) {
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
	return s.listTickets(ctx, []string{reg.Key})
}

// UpdateTicket applies a patch to graphops.ticket, and mirrors a changed
// title/description to the issue's summary/description. It never
// transitions the issue in Jira's workflow.
func (s *Store) UpdateTicket(ctx context.Context, id string, p TicketPatch) (Ticket, error) {
	defer s.lock(id)()
	issue, tp, err := s.readTicket(ctx, id)
	if err != nil {
		return Ticket{}, err
	}
	labels, err := s.labelsByID(ctx, projectKeyOfIssue(issue.Key))
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
	if err := s.writeTicketProp(ctx, issue.Key, tp); err != nil {
		return Ticket{}, err
	}
	if len(fields) > 0 {
		if err := s.jira.editIssue(ctx, issue.Key, map[string]any{"fields": fields}); err != nil {
			return Ticket{}, jiraFailure(err)
		}
	}
	return ticketFrom(issue.Key, tp, labels), nil
}

// DeleteTicket detaches the issue from GraphOps instead of deleting it from
// Jira. First the graphops label and the graphops.ticket/graphops.edges
// properties are removed from the ticket's issue (detachTicket): from then
// on the ticket, its nodes, edges and artifacts are gone from GraphOps
// (every read checks the ticket first), and that is what the request's
// success means. Then, best effort, each node sub-task loses its
// graphops.node property and its graphops-status-* label (cleanUpDetached);
// the sub-tasks themselves, their artifact comments and attachments stay for
// the humans who use Jira. A missing ticket is a no-op.
func (s *Store) DeleteTicket(ctx context.Context, id string) error {
	ticket, err := s.detachTicket(ctx, id)
	if err != nil || ticket == nil {
		return err
	}
	s.cleanUpDetached(ctx, []*jiraIssue{ticket})
	return nil
}

// detachTicket removes the graphops label and the graphops.ticket and
// graphops.edges properties from a ticket's issue, under the ticket's lock,
// and returns the issue as it was read before (with its sub-tasks), or nil
// when there is no such ticket.
func (s *Store) detachTicket(ctx context.Context, id string) (*jiraIssue, error) {
	defer s.lock(id)()
	issue, _, err := s.readTicketFields(ctx, id, ticketFieldsWithSubtasks)
	if err != nil {
		if isTicketNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := s.jira.editIssue(ctx, issue.Key, map[string]any{
		"update": map[string]any{"labels": []map[string]string{{"remove": jiraLabelManaged}}},
	}); err != nil {
		return nil, jiraFailure(err)
	}
	for _, prop := range []string{propTicket, propEdges} {
		if err := s.jira.deleteIssueProperty(ctx, issue.Key, prop); err != nil {
			return nil, jiraFailure(err)
		}
	}
	return issue, nil
}
