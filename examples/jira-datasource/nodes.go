package main

import (
	"context"
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

// Nodes, edges and artifacts (see the layout at the top of store.go).
//
// Every operation that reaches a node, or an artifact through its node,
// first checks that the node ID names a managed node -- and nothing else
// the Jira account can see -- before it reads comments or attachments, or
// changes or deletes anything:
//
//  1. the ID has the node ID shape (parseNodeID);
//  2. the ticket part is a managed ticket of a registered project
//     (readTicket);
//  3. the sub-task part is an issue whose parent is that ticket's issue
//     (not the ticket itself, not another ticket's sub-task, not an
//     unrelated issue);
//  4. that sub-task has a graphops.node property.
//
// An artifact must further be a comment of that sub-task (its self URL
// names the sub-task) whose graphops.artifact property names that node.

const (
	// statusLabelPrefix marks the one Jira label on a node sub-task that
	// mirrors the node's status. Labels with this prefix are GraphOps's:
	// any other one found on a sub-task is removed on the next status
	// update. No other label is ever added or removed.
	statusLabelPrefix = "graphops-status-"

	// defaultTransitionTimeout bounds moving one node sub-task through the
	// workflow. It is well under requestDeadline, so a stuck transition
	// cannot use up the time of the request it is part of.
	defaultTransitionTimeout = 8 * time.Second
)

var (
	ticketKeyPattern   = regexp.MustCompile(`^([A-Z][A-Z0-9_]*)-([1-9][0-9]*)$`)
	issueNumberPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
)

// nodeFields are the sub-task fields a node read asks for.
var nodeFields = []string{"summary", "labels", "created", "status", "parent"}

// --- IDs ---

// parseNodeID splits a node ID "<ticket key>-n<sub-task number>" into the
// ticket's issue key and the sub-task's issue key ("GOPS-12-n15" ->
// "GOPS-12", "GOPS-15"; a sub-task is always in its parent's project).
// Numbers with a leading zero are rejected, so a node has exactly one ID,
// and so is an ID that names the ticket's own issue.
func parseNodeID(id string) (ticketKey, subtaskKey string, ok bool) {
	i := strings.LastIndex(id, "-n")
	if i <= 0 {
		return "", "", false
	}
	ticketKey, num := id[:i], id[i+2:]
	m := ticketKeyPattern.FindStringSubmatch(ticketKey)
	if m == nil || !issueNumberPattern.MatchString(num) {
		return "", "", false
	}
	subtaskKey = m[1] + "-" + num
	if subtaskKey == ticketKey {
		return "", "", false
	}
	return ticketKey, subtaskKey, true
}

// nodeIDOf is the node ID of sub-task subtaskKey of ticket ticketKey.
func nodeIDOf(ticketKey, subtaskKey string) (string, bool) {
	t := ticketKeyPattern.FindStringSubmatch(ticketKey)
	st := ticketKeyPattern.FindStringSubmatch(subtaskKey)
	if t == nil || st == nil || t[1] != st[1] || ticketKey == subtaskKey {
		return "", false
	}
	return ticketKey + "-n" + st[2], true
}

// parseArtifactID splits "<node ID>-c<comment id>".
func parseArtifactID(id string) (nodeID, commentID string, ok bool) {
	i := strings.LastIndex(id, "-c")
	if i <= 0 {
		return "", "", false
	}
	nodeID, commentID = id[:i], id[i+2:]
	if !issueNumberPattern.MatchString(commentID) {
		return "", "", false
	}
	if _, _, ok := parseNodeID(nodeID); !ok {
		return "", "", false
	}
	return nodeID, commentID, true
}

// issueNumber is the number part of an issue key (0 when there is none).
func issueNumber(key string) int {
	n, _ := strconv.Atoi(key[strings.LastIndex(key, "-")+1:])
	return n
}

// --- node data ---

// nodeProp is a node sub-task's graphops.node property: the node's data,
// except its ID and ticket ID, which come from the sub-task's key and
// parent, plus the IDs of its artifact comments (so that a ticket's
// artifacts can be read in one request, see ticketArtifacts).
type nodeProp struct {
	Name               string   `json:"name"`
	Type               string   `json:"type"`
	Status             string   `json:"status"`
	IterationCount     int      `json:"iteration_count"`
	MaxIterations      int      `json:"max_iterations"`
	Assignee           *string  `json:"assignee,omitempty"`
	IsManual           bool     `json:"is_manual"`
	GateID             *string  `json:"gate_id,omitempty"`
	Criteria           *string  `json:"criteria,omitempty"`
	ConfigID           *string  `json:"config_id,omitempty"`
	CreatedAt          string   `json:"created_at"`
	UpdatedAt          string   `json:"updated_at"`
	ArtifactCommentIDs []string `json:"artifact_comment_ids"`
}

// managedNode is a node sub-task that passed the managed-node check.
type managedNode struct {
	id        string
	ticketKey string
	issue     *jiraIssue // the sub-task
	prop      nodeProp
}

func (n managedNode) graphNode() GraphNode {
	p := n.prop
	return GraphNode{
		ID: n.id, TicketID: n.ticketKey, Name: p.Name, Type: p.Type, Status: p.Status,
		IterationCount: p.IterationCount, MaxIterations: p.MaxIterations, Assignee: p.Assignee,
		IsManual: p.IsManual, GateID: p.GateID, Criteria: p.Criteria, ConfigID: p.ConfigID,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

// statusLabel is the graphops-status-* label for a node status
// ("AWAITING FIX" -> "graphops-status-awaiting-fix"). An empty status is
// labelled like TODO.
func statusLabel(status string) string {
	s := strings.Join(strings.Fields(strings.ToLower(status)), "-")
	if s == "" {
		s = "todo"
	}
	return statusLabelPrefix + s
}

// statusLabelOps returns the label update operations that leave want as
// the only graphops-status-* label of an issue whose labels are current
// (every other label untouched), or nil when nothing has to change.
func statusLabelOps(current []string, want string) []map[string]string {
	var ops []map[string]string
	has := false
	for _, l := range current {
		switch {
		case l == want:
			has = true
		case strings.HasPrefix(l, statusLabelPrefix):
			ops = append(ops, map[string]string{"remove": l})
		}
	}
	if !has {
		ops = append(ops, map[string]string{"add": want})
	}
	return ops
}

// nodeSummary is a node sub-task's summary, "<name> [<type>]": the first
// line of the name, shortened like summaryFor so that the whole summary
// stays under Jira's 255 characters with the type still visible.
func nodeSummary(name, typ string) string {
	suffix := " [" + typ + "]"
	room := 250 - utf8.RuneCountInString(suffix)
	if room < 20 {
		return summaryFor(name + suffix)
	}
	line := strings.TrimSpace(strings.SplitN(name, "\n", 2)[0])
	if line == "" {
		line = "(untitled)"
	}
	if utf8.RuneCountInString(line) > room {
		line = string([]rune(line)[:room]) + "..."
	}
	return line + suffix
}

// workflowTarget is the Jira status a node sub-task is moved to for a
// GraphOps node status, or "" when it is not moved: TODO leaves the sub-task
// where it is (as created, or wherever an earlier status put it), DONE is
// the "done" status, and every other status -- REJECTED included, as a
// rejected node waits for a person's (or process-ticket's) next decision
// and is not finished -- is the "in progress" status.
func (s *Store) workflowTarget(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "", "TODO":
		return ""
	case "DONE":
		return s.opts.DoneStatus
	default:
		return s.opts.InProgressStatus
	}
}

// moveToStatus moves a node sub-task to the workflow status target, best
// effort: when current already is target (ignoring case), or target is "",
// nothing is sent; when the move fails for any reason -- no transition
// leads there, Jira refuses it, the time runs out -- one line is logged and
// the caller carries on. GraphOps's data (graphops.node) and the status
// label are the record; the workflow status is a courtesy to Jira users.
//
// Callers hold the sub-task's lock, and have written graphops.node and the
// label before: two updates of the same node therefore make their
// transitions in the order they wrote the status, and the workflow status
// ends matching the last one.
func (s *Store) moveToStatus(ctx context.Context, nodeID, subtaskKey, current, target string) {
	if target == "" || strings.EqualFold(strings.TrimSpace(current), target) {
		return
	}
	tctx, cancel := context.WithTimeout(ctx, s.transitionTimeout)
	defer cancel()
	fail := func(reason string, args ...any) {
		s.log("node %s (sub-task %s): could not move it to workflow status %q: %s", nodeID, subtaskKey, target, fmt.Sprintf(reason, args...))
	}
	transitions, err := s.jira.getTransitions(tctx, subtaskKey)
	if err != nil {
		fail("%v", err)
		return
	}
	var names []string
	for _, t := range transitions {
		if strings.EqualFold(strings.TrimSpace(t.To.Name), target) {
			if err := s.jira.doTransition(tctx, subtaskKey, t.ID); err != nil {
				fail("%v", err)
			}
			return
		}
		names = append(names, strconv.Quote(t.To.Name))
	}
	fail("no transition leads there from its current status (reachable: %s)", strings.Join(names, ", "))
}

func statusName(issue *jiraIssue) string {
	if issue.Fields.Status == nil {
		return ""
	}
	return issue.Fields.Status.Name
}

// --- the managed-node check ---

// isAPIError reports whether err is an API error with the given code.
func isAPIError(err error, code string) bool {
	var apiErr *apiError
	return errors.As(err, &apiErr) && apiErr.Code == code
}

// managedSubtask checks steps 3 and 4 of the managed-node check for an
// issue read with nodeFields and the graphops.node property.
func managedSubtask(ticketKey string, sub *jiraIssue) (managedNode, bool) {
	if sub.Fields.Parent == nil || sub.Fields.Parent.Key != ticketKey {
		return managedNode{}, false
	}
	id, ok := nodeIDOf(ticketKey, sub.Key)
	if !ok {
		return managedNode{}, false
	}
	var np nodeProp
	if ok, err := decodeProp(sub.Properties, propNode, &np); !ok || err != nil {
		return managedNode{}, false
	}
	return managedNode{id: id, ticketKey: ticketKey, issue: sub, prop: np}, true
}

// loadNodeIn runs the managed-node check for sub-task subtaskKey of ticket
// ticketKey, reading the ticket with ticketProps as well. A ticket that is
// not managed is TICKET_NOT_FOUND; a sub-task that fails the check is
// NODE_NOT_FOUND, with gone set when the sub-task does not exist at all
// (the ticket is then returned too).
func (s *Store) loadNodeIn(ctx context.Context, ticketKey, subtaskKey string, ticketProps ...string) (ticket *jiraIssue, n managedNode, gone bool, err error) {
	ticket, _, err = s.readTicket(ctx, ticketKey, ticketProps...)
	if err != nil {
		return nil, managedNode{}, false, err
	}
	nodeID, _ := nodeIDOf(ticketKey, subtaskKey)
	if ticket.Key != ticketKey {
		// The ticket's issue was moved and has another key now.
		return nil, managedNode{}, false, notFound("NODE_NOT_FOUND", "node", nodeID)
	}
	sub, err := s.jira.getIssueFields(ctx, subtaskKey, nodeFields, []string{propNode})
	if err != nil {
		if isJiraStatus(err, http.StatusNotFound) {
			return ticket, managedNode{}, true, notFound("NODE_NOT_FOUND", "node", nodeID)
		}
		return nil, managedNode{}, false, jiraFailure(err)
	}
	if sub.Key != subtaskKey {
		return nil, managedNode{}, false, notFound("NODE_NOT_FOUND", "node", nodeID)
	}
	n, ok := managedSubtask(ticket.Key, sub)
	if !ok {
		return nil, managedNode{}, false, notFound("NODE_NOT_FOUND", "node", nodeID)
	}
	return ticket, n, false, nil
}

// loadNode runs the managed-node check for a node ID: anything that fails
// it is NODE_NOT_FOUND.
func (s *Store) loadNode(ctx context.Context, nodeID string) (managedNode, error) {
	ticketKey, subtaskKey, ok := parseNodeID(nodeID)
	if !ok {
		return managedNode{}, notFound("NODE_NOT_FOUND", "node", nodeID)
	}
	_, n, _, err := s.loadNodeIn(ctx, ticketKey, subtaskKey)
	if isAPIError(err, "TICKET_NOT_FOUND") {
		return managedNode{}, notFound("NODE_NOT_FOUND", "node", nodeID)
	}
	return n, err
}

// loadNodes returns the nodes of a managed ticket whose issue was read with
// its sub-tasks (ticketFieldsWithSubtasks), oldest first. It reads the
// sub-tasks by key (bulkfetch), not through a JQL search, so a node created
// a moment ago is never missing because the search index lags. Sub-tasks
// without graphops.node (made by hand in Jira) are not nodes.
func (s *Store) loadNodes(ctx context.Context, ticket *jiraIssue) ([]managedNode, error) {
	var keys []string
	for _, st := range ticket.Fields.Subtasks {
		keys = append(keys, st.Key)
	}
	if len(keys) == 0 {
		return nil, nil
	}
	issues, err := s.jira.bulkFetchIssues(ctx, keys, nodeFields, []string{propNode})
	if err != nil {
		return nil, jiraFailure(err)
	}
	var out []managedNode
	for i := range issues {
		if n, ok := managedSubtask(ticket.Key, &issues[i]); ok {
			out = append(out, n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		ta, errA := time.Parse(time.RFC3339Nano, a.prop.CreatedAt)
		tb, errB := time.Parse(time.RFC3339Nano, b.prop.CreatedAt)
		if errA == nil && errB == nil && !ta.Equal(tb) {
			return ta.Before(tb)
		}
		if (errA != nil || errB != nil) && a.prop.CreatedAt != b.prop.CreatedAt {
			return a.prop.CreatedAt < b.prop.CreatedAt
		}
		return issueNumber(a.issue.Key) < issueNumber(b.issue.Key)
	})
	return out, nil
}

// --- nodes ---

// CreateNode creates the node's sub-task under the ticket's issue, with its
// graphops.node property and its status label in the same request. A
// status other than TODO is then mirrored on the workflow (best effort).
//
// A ticket holds at most maxNodesPerTicket nodes. The ticket's sub-task
// count bounds its node count from above, so only when the ticket already
// has that many sub-tasks (nodes, or sub-tasks made by hand) are the nodes
// counted exactly -- CreateNode normally costs no extra request for it.
func (s *Store) CreateNode(ctx context.Context, ticketID string, in GraphNode) (GraphNode, error) {
	defer s.lock(ticketID)()
	ticket, _, err := s.readTicketFields(ctx, ticketID, ticketFieldsWithSubtasks)
	if err != nil {
		return GraphNode{}, err
	}
	if len(ticket.Fields.Subtasks) >= maxNodesPerTicket {
		nodes, err := s.loadNodes(ctx, ticket)
		if err != nil {
			return GraphNode{}, err
		}
		if len(nodes) >= maxNodesPerTicket {
			return GraphNode{}, validationError("ticket %s already has the maximum of %d nodes", ticketID, maxNodesPerTicket)
		}
	}
	now := s.now()
	np := nodeProp{
		Name: in.Name, Type: in.Type, Status: in.Status, IterationCount: in.IterationCount,
		MaxIterations: in.MaxIterations, Assignee: in.Assignee, IsManual: in.IsManual,
		GateID: in.GateID, Criteria: in.Criteria, ConfigID: in.ConfigID,
		CreatedAt: now, UpdatedAt: now, ArtifactCommentIDs: []string{},
	}
	if np.MaxIterations == 0 {
		np.MaxIterations = 3
	}
	raw, err := marshalProperty("the node (graphops.node)", np)
	if err != nil {
		return GraphNode{}, err
	}
	key, err := s.jira.createIssue(ctx, map[string]any{
		"project":   map[string]string{"key": projectKeyOfIssue(ticket.Key)},
		"parent":    map[string]string{"key": ticket.Key},
		"issuetype": map[string]string{"name": s.opts.SubtaskIssueType},
		"summary":   nodeSummary(np.Name, np.Type),
		"labels":    []string{statusLabel(np.Status)},
		"description": markdownToADF("This sub-task is a node of the GraphOps execution graph of " + ticket.Key +
			". Its data is kept in the graphops.node property and managed by the GraphOps Jira data source plugin; " +
			"its graphops-status-* label shows the node's status."),
	}, []jiraProperty{{Key: propNode, Value: json.RawMessage(raw)}})
	if err != nil {
		return GraphNode{}, jiraFailure(err)
	}
	id, ok := nodeIDOf(ticket.Key, key)
	if !ok {
		return GraphNode{}, newAPIError(http.StatusBadGateway, "INTERNAL_ERROR", "Jira created the node sub-task as %s, outside the project of %s", key, ticket.Key)
	}
	n := managedNode{id: id, ticketKey: ticket.Key, prop: np}
	if target := s.workflowTarget(np.Status); target != "" {
		defer s.lock(key)()
		s.moveToStatus(ctx, id, key, "", target)
	}
	return n.graphNode(), nil
}

func (s *Store) GetNode(ctx context.Context, id string) (GraphNode, error) {
	n, err := s.loadNode(ctx, id)
	if err != nil {
		return GraphNode{}, err
	}
	return n.graphNode(), nil
}

func (s *Store) ListNodesByTicket(ctx context.Context, ticketID string) ([]GraphNode, error) {
	ticket, _, err := s.readTicketFields(ctx, ticketID, ticketFieldsWithSubtasks)
	if err != nil {
		if isTicketNotFound(err) {
			return []GraphNode{}, nil
		}
		return nil, err
	}
	nodes, err := s.loadNodes(ctx, ticket)
	if err != nil {
		return nil, err
	}
	out := []GraphNode{}
	for _, n := range nodes {
		out = append(out, n.graphNode())
	}
	return out, nil
}

// UpdateNode applies a patch under the sub-task's lock: graphops.node is
// written first (it is the record), then -- only if they change -- the
// summary and the status label in one edit (a failure there fails the
// request, and re-running it makes them agree), and last the workflow
// status, best effort (moveToStatus).
func (s *Store) UpdateNode(ctx context.Context, id string, p NodePatch) (GraphNode, error) {
	ticketKey, subtaskKey, ok := parseNodeID(id)
	if !ok {
		return GraphNode{}, notFound("NODE_NOT_FOUND", "node", id)
	}
	defer s.lock(subtaskKey)()
	_, n, _, err := s.loadNodeIn(ctx, ticketKey, subtaskKey)
	if err != nil {
		if isTicketNotFound(err) {
			return GraphNode{}, notFound("NODE_NOT_FOUND", "node", id)
		}
		return GraphNode{}, err
	}
	np := n.prop
	oldSummary := nodeSummary(np.Name, np.Type)
	if p.Name != nil {
		np.Name = *p.Name
	}
	if p.Type != nil {
		np.Type = *p.Type
	}
	if p.Status != nil {
		np.Status = *p.Status
	}
	if p.IterationCount != nil {
		np.IterationCount = *p.IterationCount
	}
	if p.MaxIterations != nil {
		np.MaxIterations = *p.MaxIterations
	}
	if p.Assignee.Set {
		np.Assignee = p.Assignee.Value
	}
	if p.IsManual != nil {
		np.IsManual = *p.IsManual
	}
	if p.GateID != nil {
		np.GateID = p.GateID
	}
	if p.Criteria != nil {
		np.Criteria = p.Criteria
	}
	np.UpdatedAt = s.now()
	raw, err := marshalProperty("the node (graphops.node)", np)
	if err != nil {
		return GraphNode{}, err
	}
	if err := s.jira.setIssueProperty(ctx, subtaskKey, propNode, raw); err != nil {
		return GraphNode{}, jiraFailure(err)
	}
	edit := map[string]any{}
	if summary := nodeSummary(np.Name, np.Type); summary != oldSummary {
		edit["fields"] = map[string]any{"summary": summary}
	}
	if ops := statusLabelOps(n.issue.Fields.Labels, statusLabel(np.Status)); len(ops) > 0 {
		edit["update"] = map[string]any{"labels": ops}
	}
	if len(edit) > 0 {
		if err := s.jira.editIssue(ctx, subtaskKey, edit); err != nil {
			return GraphNode{}, jiraFailure(err)
		}
	}
	s.moveToStatus(ctx, id, subtaskKey, statusName(n.issue), s.workflowTarget(np.Status))
	n.prop = np
	return n.graphNode(), nil
}

// DeleteNode deletes the node's sub-task -- and with it, in Jira, its
// artifact comments and attachments -- after removing the edges touching
// the node from the ticket. Edges go first: if deleting the sub-task then
// fails, the request fails and running it again finishes the job. An ID
// that does not name a managed node is a no-op that changes nothing (a
// sub-task already gone from Jira only has its edges removed).
func (s *Store) DeleteNode(ctx context.Context, id string) error {
	ticketKey, subtaskKey, ok := parseNodeID(id)
	if !ok {
		return nil
	}
	defer s.lock(ticketKey)()
	defer s.lock(subtaskKey)()
	ticket, _, gone, err := s.loadNodeIn(ctx, ticketKey, subtaskKey, propEdges)
	if err != nil {
		if isTicketNotFound(err) {
			return nil
		}
		if isAPIError(err, "NODE_NOT_FOUND") {
			if gone {
				return s.removeEdgesOf(ctx, ticket, id)
			}
			return nil
		}
		return err
	}
	if err := s.removeEdgesOf(ctx, ticket, id); err != nil {
		return err
	}
	if err := s.jira.deleteIssue(ctx, subtaskKey); err != nil && !isJiraStatus(err, http.StatusNotFound) {
		return jiraFailure(err)
	}
	return nil
}

// detachSubtasks removes graphops.node and the graphops-status-* labels
// from the node sub-tasks of a ticket that was just detached (DeleteTicket),
// a few sub-tasks at a time. It is best effort: the ticket is already gone
// from GraphOps, so a sub-task that keeps its data can no longer be read as
// a node anyway; each failure is logged and the request still succeeds.
func (s *Store) detachSubtasks(ctx context.Context, ticket *jiraIssue) {
	var keys []string
	for _, st := range ticket.Fields.Subtasks {
		keys = append(keys, st.Key)
	}
	if len(keys) == 0 {
		return
	}
	issues, err := s.jira.bulkFetchIssues(ctx, keys, []string{"labels", "parent"}, []string{propNode})
	if err != nil {
		s.log("ticket %s detached, but its node sub-tasks could not be read to remove their GraphOps data: %v", ticket.Key, err)
		return
	}
	sem := make(chan struct{}, max(1, s.detachParallelism))
	var wg sync.WaitGroup
	for i := range issues {
		sub := &issues[i]
		if _, ok := sub.Properties[propNode]; !ok || sub.Fields.Parent == nil || sub.Fields.Parent.Key != ticket.Key {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			defer s.lock(sub.Key)()
			if err := s.jira.deleteIssueProperty(ctx, sub.Key, propNode); err != nil {
				s.log("ticket %s detached, but sub-task %s keeps its graphops.node property: %v", ticket.Key, sub.Key, err)
			}
			var ops []map[string]string
			for _, l := range sub.Fields.Labels {
				if strings.HasPrefix(l, statusLabelPrefix) {
					ops = append(ops, map[string]string{"remove": l})
				}
			}
			if len(ops) == 0 {
				return
			}
			if err := s.jira.editIssue(ctx, sub.Key, map[string]any{"update": map[string]any{"labels": ops}}); err != nil {
				s.log("ticket %s detached, but sub-task %s keeps its graphops-status-* label: %v", ticket.Key, sub.Key, err)
			}
		}()
	}
	wg.Wait()
}

// --- edges ---

// edgesProp is a ticket issue's graphops.edges property. Edges are stored
// in a compact form (storedEdge) so that the typical graph of a full ticket
// (99 nodes, about 1.6 edges per node) takes about half of Jira's 32 KB.
type edgesProp struct {
	EdgeSeq int          `json:"edge_seq"`
	Edges   []storedEdge `json:"edges"`
}

// storedEdge is one edge in graphops.edges: the ticket ID is implied, and
// the condition "always" is left out.
type storedEdge struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Condition string `json:"cond,omitempty"`
	CreatedAt string `json:"at"`
}

func edgesOf(ticket *jiraIssue) (edgesProp, error) {
	var ep edgesProp
	if _, err := decodeProp(ticket.Properties, propEdges, &ep); err != nil {
		return edgesProp{}, jiraFailure(err)
	}
	if ep.Edges == nil {
		ep.Edges = []storedEdge{}
	}
	return ep, nil
}

func (ep edgesProp) graphEdges(ticketKey string) []GraphEdge {
	out := []GraphEdge{}
	for _, e := range ep.Edges {
		cond := e.Condition
		if cond == "" {
			cond = "always"
		}
		out = append(out, GraphEdge{ID: e.ID, TicketID: ticketKey, FromNodeID: e.From, ToNodeID: e.To, Condition: cond, CreatedAt: e.CreatedAt})
	}
	return out
}

func (s *Store) writeEdges(ctx context.Context, ticketKey string, ep edgesProp) error {
	raw, err := marshalProperty("the edges (graphops.edges) of "+ticketKey, ep)
	if err != nil {
		return err
	}
	if err := s.jira.setIssueProperty(ctx, ticketKey, propEdges, raw); err != nil {
		return jiraFailure(err)
	}
	return nil
}

// removeEdgesOf removes the edges touching a node from a ticket read with
// graphops.edges; it writes only when something changes.
func (s *Store) removeEdgesOf(ctx context.Context, ticket *jiraIssue, nodeID string) error {
	ep, err := edgesOf(ticket)
	if err != nil {
		return err
	}
	kept := []storedEdge{}
	for _, e := range ep.Edges {
		if e.From != nodeID && e.To != nodeID {
			kept = append(kept, e)
		}
	}
	if len(kept) == len(ep.Edges) {
		return nil
	}
	ep.Edges = kept
	return s.writeEdges(ctx, ticket.Key, ep)
}

func (s *Store) CreateEdge(ctx context.Context, ticketID string, in GraphEdge) (GraphEdge, error) {
	defer s.lock(ticketID)()
	ticket, _, err := s.readTicket(ctx, ticketID, propEdges)
	if err != nil {
		return GraphEdge{}, err
	}
	ep, err := edgesOf(ticket)
	if err != nil {
		return GraphEdge{}, err
	}
	e := storedEdge{ID: in.ID, From: in.FromNodeID, To: in.ToNodeID, Condition: in.Condition, CreatedAt: s.now()}
	if e.ID == "" {
		ep.EdgeSeq++
		e.ID = fmt.Sprintf("%s-e%d", ticket.Key, ep.EdgeSeq)
	}
	if e.Condition == "always" {
		e.Condition = ""
	}
	ep.Edges = append(ep.Edges, e)
	if err := s.writeEdges(ctx, ticket.Key, ep); err != nil {
		return GraphEdge{}, err
	}
	return edgesProp{Edges: []storedEdge{e}}.graphEdges(ticket.Key)[0], nil
}

func (s *Store) ListEdgesByTicket(ctx context.Context, ticketID string) ([]GraphEdge, error) {
	ticket, _, err := s.readTicket(ctx, ticketID, propEdges)
	if err != nil {
		if isTicketNotFound(err) {
			return []GraphEdge{}, nil
		}
		return nil, err
	}
	ep, err := edgesOf(ticket)
	if err != nil {
		return nil, err
	}
	return ep.graphEdges(ticket.Key), nil
}

func (s *Store) ClearEdgesByTicket(ctx context.Context, ticketID string) error {
	defer s.lock(ticketID)()
	ticket, _, err := s.readTicket(ctx, ticketID, propEdges)
	if err != nil {
		if isTicketNotFound(err) {
			return nil
		}
		return err
	}
	ep, err := edgesOf(ticket)
	if err != nil {
		return err
	}
	ep.Edges = []storedEdge{}
	return s.writeEdges(ctx, ticket.Key, ep)
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

// isTicketNotFound reports whether err is a TICKET_NOT_FOUND API error.
func isTicketNotFound(err error) bool { return isAPIError(err, "TICKET_NOT_FOUND") }

// removeArtifactLeftovers deletes the comment and/or attachment of an
// artifact whose creation failed half-way (best effort, and even when ctx
// is already done, with a short deadline of its own).
func (s *Store) removeArtifactLeftovers(ctx context.Context, subtaskKey, commentID, attachmentID string) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), attachmentCleanupTimeout)
	defer cancel()
	if commentID != "" {
		if err := s.jira.deleteComment(cleanupCtx, subtaskKey, commentID); err != nil {
			s.log("could not remove the orphaned artifact comment %s of %s: %v", commentID, subtaskKey, err)
		}
	}
	if attachmentID != "" {
		if err := s.jira.deleteAttachment(cleanupCtx, attachmentID); err != nil {
			s.log("could not remove the orphaned attachment %s of %s: %v", attachmentID, subtaskKey, err)
		}
	}
}

// CreateArtifact stores an artifact as a comment on its node's sub-task
// (never on the ticket's issue). text/gherkin/json content up to
// inlineContentLimit is kept inline (in the comment's code block and its
// graphops.artifact property); larger content and every html/image artifact
// is uploaded as an attachment of the sub-task that the property points to.
// The comment's ID is then added to the node's artifact_comment_ids. If the
// comment or that last write fails, what was already created is removed.
// The returned ID is "<node ID>-c<comment id>".
//
// The node must be a managed node of the ticket: a ticket that is not
// managed is TICKET_NOT_FOUND, any other node ID is NODE_NOT_FOUND, and in
// both cases nothing is written to Jira.
func (s *Store) CreateArtifact(ctx context.Context, ticketID string, in Artifact) (Artifact, error) {
	nodeTicket, subtaskKey, ok := parseNodeID(in.NodeID)
	if !ok || nodeTicket != ticketID {
		return Artifact{}, notFound("NODE_NOT_FOUND", "node", in.NodeID+" of ticket "+ticketID)
	}
	defer s.lock(subtaskKey)()
	_, n, _, err := s.loadNodeIn(ctx, ticketID, subtaskKey)
	if err != nil {
		return Artifact{}, err
	}
	ap := artifactProp{
		NodeID: n.id, Name: in.Name, Type: in.Type, FilePath: in.FilePath, Metadata: in.Metadata,
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
			id, err := s.jira.addAttachment(ctx, subtaskKey, attachmentName, data)
			if err != nil {
				return Artifact{}, jiraFailure(err)
			}
			ap.AttachmentID = id
		}
	}
	raw, err := marshalProperty("the artifact metadata (graphops.artifact)", ap)
	if err != nil {
		s.removeArtifactLeftovers(ctx, subtaskKey, "", ap.AttachmentID)
		return Artifact{}, err
	}
	cm, err := s.jira.addComment(ctx, subtaskKey, artifactCommentBody(in, inline, attachmentName),
		[]jiraProperty{{Key: propArtifact, Value: json.RawMessage(raw)}})
	if err != nil {
		s.removeArtifactLeftovers(ctx, subtaskKey, "", ap.AttachmentID)
		return Artifact{}, jiraFailure(err)
	}
	np := n.prop
	np.ArtifactCommentIDs = append(append([]string{}, np.ArtifactCommentIDs...), cm.ID)
	rawNode, err := marshalProperty("the node (graphops.node)", np)
	if err == nil {
		if werr := s.jira.setIssueProperty(ctx, subtaskKey, propNode, rawNode); werr != nil {
			err = jiraFailure(werr)
		}
	}
	if err != nil {
		s.removeArtifactLeftovers(ctx, subtaskKey, cm.ID, ap.AttachmentID)
		return Artifact{}, err
	}
	return Artifact{
		ID: n.id + "-c" + cm.ID, TicketID: ticketID, NodeID: n.id, Name: ap.Name, Type: ap.Type,
		Content: in.Content, FilePath: ap.FilePath, Metadata: ap.Metadata, HasContent: ap.HasContent, CreatedAt: ap.CreatedAt,
	}, nil
}

// artifactFrom rebuilds an Artifact from a comment of node n's sub-task.
// withContent controls whether attachment-backed content is downloaded.
func (s *Store) artifactFrom(ctx context.Context, n managedNode, cm jiraComment, ap artifactProp, withContent bool) (Artifact, error) {
	a := Artifact{
		ID: n.id + "-c" + cm.ID, TicketID: n.ticketKey, NodeID: n.id, Name: ap.Name, Type: ap.Type,
		FilePath: ap.FilePath, Metadata: ap.Metadata, HasContent: ap.HasContent, CreatedAt: ap.CreatedAt,
		Content: ap.Content,
	}
	if withContent && ap.AttachmentID != "" {
		data, err := s.jira.attachmentContent(ctx, ap.AttachmentID)
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

// commentIsOn reports whether a comment's self URL does not contradict it
// being on the given issue (Jira names the issue by ID there).
func commentIsOn(cm jiraComment, issue *jiraIssue) bool {
	ref := issueIDOfCommentSelf(cm.Self)
	return ref == "" || ref == issue.ID || ref == issue.Key
}

// GetArtifact reads one artifact comment. The node part of the ID must pass
// the managed-node check before the comment is read, and the comment must
// be on that node's sub-task and name that node: an artifact ID naming any
// other issue or comment the account can see -- an unregistered project's,
// one never created through GraphOps, a detached ticket's -- is
// ARTIFACT_NOT_FOUND, and neither its comments nor any attachment a comment
// property points to are fetched.
func (s *Store) GetArtifact(ctx context.Context, id string) (Artifact, error) {
	nodeID, commentID, ok := parseArtifactID(id)
	if !ok {
		return Artifact{}, notFound("ARTIFACT_NOT_FOUND", "artifact", id)
	}
	n, err := s.loadNode(ctx, nodeID)
	if err != nil {
		if isAPIError(err, "NODE_NOT_FOUND") {
			return Artifact{}, notFound("ARTIFACT_NOT_FOUND", "artifact", id)
		}
		return Artifact{}, err
	}
	cm, err := s.jira.getComment(ctx, n.issue.Key, commentID)
	if err != nil {
		if isJiraStatus(err, http.StatusNotFound) {
			return Artifact{}, notFound("ARTIFACT_NOT_FOUND", "artifact", id)
		}
		return Artifact{}, jiraFailure(err)
	}
	ap, ok := artifactPropOf(*cm)
	if !ok || ap.NodeID != n.id || !commentIsOn(*cm, n.issue) {
		return Artifact{}, notFound("ARTIFACT_NOT_FOUND", "artifact", id)
	}
	return s.artifactFrom(ctx, n, *cm, ap, true)
}

// ticketArtifacts lists the artifacts of a ticket's nodes, oldest first, in
// one request for all of them (POST /comment/list with every node's
// artifact_comment_ids) rather than one per node. Since those IDs are only
// as trustworthy as the Jira users who can edit the property, a comment is
// used only when its self URL names one of these node sub-tasks and its
// graphops.artifact names that sub-task's node. A comment whose self cannot
// be read is not used; its node is read the slow way instead (its
// sub-task's comments). html/image content is omitted (has_content still
// set), as the protocol allows for listings; text/gherkin/json content is
// always included.
func (s *Store) ticketArtifacts(ctx context.Context, ticketKey string, nodes []managedNode) ([]Artifact, error) {
	type found struct {
		node int
		cm   jiraComment
		ap   artifactProp
	}
	bySubtask := map[string]int{}
	listedBy := map[string][]int{}
	var ids []string
	for i, n := range nodes {
		bySubtask[n.issue.ID] = i
		bySubtask[n.issue.Key] = i
		for _, cid := range n.prop.ArtifactCommentIDs {
			if len(listedBy[cid]) == 0 {
				ids = append(ids, cid)
			}
			listedBy[cid] = append(listedBy[cid], i)
		}
	}
	out := []Artifact{}
	if len(ids) == 0 {
		return out, nil
	}
	comments, err := s.jira.listCommentsByIDs(ctx, ids)
	if err != nil {
		return nil, jiraFailure(err)
	}
	slow := map[int]bool{}
	for _, cm := range comments {
		if issueIDOfCommentSelf(cm.Self) == "" {
			for _, i := range listedBy[cm.ID] {
				slow[i] = true
			}
		}
	}
	var all []found
	seen := map[string]bool{}
	for _, cm := range comments {
		i, ok := bySubtask[issueIDOfCommentSelf(cm.Self)]
		if !ok || slow[i] || seen[cm.ID] {
			continue
		}
		if ap, ok := artifactPropOf(cm); ok && ap.NodeID == nodes[i].id {
			seen[cm.ID] = true
			all = append(all, found{i, cm, ap})
		}
	}
	for i := range nodes {
		if !slow[i] {
			continue
		}
		cms, err := s.jira.listComments(ctx, nodes[i].issue.Key)
		if err != nil {
			if isJiraStatus(err, http.StatusNotFound) {
				continue
			}
			return nil, jiraFailure(err)
		}
		for _, cm := range cms {
			if ap, ok := artifactPropOf(cm); ok && ap.NodeID == nodes[i].id && !seen[cm.ID] {
				seen[cm.ID] = true
				all = append(all, found{i, cm, ap})
			}
		}
	}
	sort.SliceStable(all, func(a, b int) bool { return commentOrder(all[a].cm.ID, all[b].cm.ID) })
	for _, f := range all {
		withContent := !isFileBacked(f.ap.Type)
		a, err := s.artifactFrom(ctx, nodes[f.node], f.cm, f.ap, withContent)
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

// commentOrder orders comment IDs as Jira assigns them (increasing numbers).
func commentOrder(a, b string) bool {
	na, errA := strconv.ParseInt(a, 10, 64)
	nb, errB := strconv.ParseInt(b, 10, 64)
	if errA == nil && errB == nil {
		return na < nb
	}
	return a < b
}

func (s *Store) ListArtifactsByTicket(ctx context.Context, ticketID string) ([]Artifact, error) {
	ticket, _, err := s.readTicketFields(ctx, ticketID, ticketFieldsWithSubtasks)
	if err != nil {
		if isTicketNotFound(err) {
			return []Artifact{}, nil
		}
		return nil, err
	}
	nodes, err := s.loadNodes(ctx, ticket)
	if err != nil {
		return nil, err
	}
	return s.ticketArtifacts(ctx, ticket.Key, nodes)
}

// ListArtifactsByNode lists one node's artifacts, oldest first, with their
// full content. A node ID that fails the managed-node check has none, and
// no comment is read for it.
func (s *Store) ListArtifactsByNode(ctx context.Context, nodeID string) ([]Artifact, error) {
	n, err := s.loadNode(ctx, nodeID)
	if err != nil {
		if isAPIError(err, "NODE_NOT_FOUND") {
			return []Artifact{}, nil
		}
		return nil, err
	}
	comments, err := s.jira.listComments(ctx, n.issue.Key)
	if err != nil {
		if isJiraStatus(err, http.StatusNotFound) {
			return []Artifact{}, nil
		}
		return nil, jiraFailure(err)
	}
	out := []Artifact{}
	for _, cm := range comments {
		ap, ok := artifactPropOf(cm)
		if !ok || ap.NodeID != n.id {
			continue
		}
		a, err := s.artifactFrom(ctx, n, cm, ap, true)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}
