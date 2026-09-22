// Package domain holds the core entities persisted by the graph engine.
//
// Timestamps are plain RFC3339 strings (not time.Time): this matches the
// original TS contract (new Date().toISOString()) that the web UI and JSON
// API consumers already expect, and sidesteps database/sql's driver-specific
// handling of time.Time scan destinations.
package domain

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type TicketStatus string

const (
	TicketTODO       TicketStatus = "TODO"
	TicketRefined    TicketStatus = "REFINED"
	TicketInProgress TicketStatus = "IN PROGRESS"
	TicketInReview   TicketStatus = "IN REVIEW"
	TicketInRelease  TicketStatus = "IN RELEASE"
	TicketDone       TicketStatus = "DONE"
	// TicketClosed marks a ticket withdrawn without being completed (DFLT-00043),
	// as opposed to TicketDone, which means the work actually finished. It can
	// be reached from any status via engine.CloseTicket, is left untouched by
	// syncTicketStatus (see the guard at the top of that function) so that
	// completing/rejecting nodes on an already-closed ticket never resurrects
	// it, and excludes the ticket from GetExecutableNodes (including seed-node
	// creation). engine.ReopenTicket is the only way back out.
	TicketClosed TicketStatus = "CLOSED"
)

// TicketPriority is the ticket's priority (DFLT-00048): always one of three
// fixed levels. There is no "unset" state (DFLT-00083): a ticket created
// without an explicit priority gets DefaultTicketPriority, and rows written
// before that change are backfilled to it by the store's Init migration.
type TicketPriority string

const (
	TicketPriorityHigh   TicketPriority = "HIGH"
	TicketPriorityMedium TicketPriority = "MEDIUM"
	TicketPriorityLow    TicketPriority = "LOW"
)

// DefaultTicketPriority is the priority a ticket gets when none is given at
// creation time (DFLT-00083). It is the single definition of that default:
// the engine applies it for CLI/HTTP creation, and the store applies it as a
// safety net for direct CreateTicket calls and in its NULL backfill.
const DefaultTicketPriority = TicketPriorityMedium

// ParseTicketPriority validates s against the three allowed priority levels
// and is the single place that validation rule lives (DFLT-00059): every
// entry point that accepts a priority value from outside the process --
// PATCH /api/tickets/{id} and POST /api/tickets (internal/httpserver/
// tickets.go) and the CLI's create-ticket/refine-ticket --priority flags
// (cmd/graph-engine/main.go) -- calls this instead of open-coding its own
// switch. There is no "unset"/"clear" value: a priority can be changed to
// another level but never removed (DFLT-00083).
func ParseTicketPriority(s string) (TicketPriority, error) {
	switch p := TicketPriority(s); p {
	case TicketPriorityHigh, TicketPriorityMedium, TicketPriorityLow:
		return p, nil
	default:
		return "", fmt.Errorf("invalid priority %q: must be one of %q, %q, %q", s,
			TicketPriorityHigh, TicketPriorityMedium, TicketPriorityLow)
	}
}

// ParseTicketStatus validates s against the ticket statuses this domain
// defines, and is the single place that rule lives -- the same role
// ParseTicketPriority plays for priorities. Every entry point that accepts a
// status from outside the process (today: PATCH /api/tickets/{id}) calls
// this instead of casting the caller's string straight into a TicketStatus,
// which is how an arbitrary string used to end up stored in the column
// (DFLT-00103 / BUG-05).
//
// TicketClosed is accepted here -- it is a real status, and refusing to parse
// it would make this function lie about the domain. Whether a particular
// entry point may *set* it is a separate question belonging to that entry
// point: the HTTP PATCH handler rejects it, because closing carries a reason
// (engine.CloseTicket) that a bare status write would silently skip.
func ParseTicketStatus(s string) (TicketStatus, error) {
	switch st := TicketStatus(s); st {
	case TicketTODO, TicketRefined, TicketInProgress, TicketInReview,
		TicketInRelease, TicketDone, TicketClosed:
		return st, nil
	default:
		return "", fmt.Errorf("invalid ticket status %q: must be one of %q, %q, %q, %q, %q, %q, %q", s,
			TicketTODO, TicketRefined, TicketInProgress, TicketInReview,
			TicketInRelease, TicketDone, TicketClosed)
	}
}

type NodeStatus string

const (
	NodeTODO       NodeStatus = "TODO"
	NodeInProgress NodeStatus = "IN PROGRESS"
	NodeInReview   NodeStatus = "IN REVIEW"
	NodeDone       NodeStatus = "DONE"
	// NodeRejected marks an approval_gate node a human explicitly rejected
	// (CompleteNode with passed=false), as opposed to NodeTODO, which also
	// covers a gate nobody has judged yet. Without this distinction the two
	// cases -- "waiting for a first decision" and "already decided against,
	// waiting on a human/process-ticket triage" -- were indistinguishable
	// from the node's status alone (DFLT-00016). It participates in no
	// syncTicketStatus branch (see engine.go), so introducing it changes no
	// existing ticket-status derivation; the column has no CHECK constraint
	// (store/sqlite.go), so no migration is needed to store the new value.
	NodeRejected NodeStatus = "REJECTED"
	// NodeAwaitingFix marks a node (in practice a review/review_gate, the
	// only built-in types with loop_back_to) that failed and walked its
	// iteration_loop edge back to its loop target: it has already judged the
	// target's output and is now waiting for that target to be reworked, as
	// opposed to NodeTODO, which also covers a node that has never run at
	// all (DFLT-00042). GetExecutableNodes claims it exactly like a TODO node
	// once its non-loop prerequisites are all DONE again. Since DFLT-00119 a
	// loop-back rewinds the loop target's whole forward closure, so those
	// prerequisites are normally rewound along with it (test_review's
	// gherkin_test, say) and this node waits for the rework rather than being
	// re-offered at once. It can be claimed on the very next call only where
	// its remaining prerequisites lie outside that closure -- a sideways
	// loop_back_to -- so the status is only briefly visible there. It
	// participates in no syncTicketStatus branch (see engine.go), so it
	// changes no ticket-status derivation; like NodeRejected, the column has
	// no CHECK constraint, so no migration is needed to store the new value.
	NodeAwaitingFix NodeStatus = "AWAITING FIX"
)

// ParseNodeStatus validates s against the node statuses this domain defines,
// the NodeStatus counterpart to ParseTicketStatus. Unlike NodeType below,
// NodeStatus really is a closed enum: the engine decides what to do with a
// node by comparing against every one of these values (GetExecutableNodes,
// deriveTicketStatus), so a status outside the set is not an extension point
// -- it is a node the engine can no longer reason about.
func ParseNodeStatus(s string) (NodeStatus, error) {
	st := NodeStatus(s)
	for _, known := range allNodeStatuses {
		if st == known {
			return st, nil
		}
	}
	quoted := make([]string, len(allNodeStatuses))
	for i, known := range allNodeStatuses {
		quoted[i] = fmt.Sprintf("%q", known)
	}
	return "", fmt.Errorf("invalid node status %q: must be one of %s", s, strings.Join(quoted, ", "))
}

// allNodeStatuses is the one definition of the closed NodeStatus enum, in
// declaration order. ParseNodeStatus validates against it, and the engine
// builds sets from it that must cover every status -- GetExecutableNodes'
// claim compensation (DFLT-00136) excludes "every status except the one I
// claimed", and a status added to the const block but not here would slip
// out of that exclusion and let the compensation overwrite it.
var allNodeStatuses = []NodeStatus{NodeTODO, NodeInProgress, NodeInReview, NodeDone, NodeRejected, NodeAwaitingFix}

// AllNodeStatuses returns every NodeStatus this domain defines, as a fresh
// slice the caller may modify.
func AllNodeStatuses() []NodeStatus {
	return append([]NodeStatus(nil), allNodeStatuses...)
}

// NodeType is a plain string, not a closed enum: project/user config and
// per-ticket LLM patches can introduce new node types without code changes.
// The values below are the ones the engine treats structurally; anything
// else is executed as an opaque custom step.
type NodeType string

const (
	NodeTypePlan           NodeType = "plan"
	NodeTypeReview         NodeType = "review"
	NodeTypeGherkinSpec    NodeType = "gherkin_spec"
	NodeTypeImplementation NodeType = "implementation"
	NodeTypeReviewGate     NodeType = "review_gate"
	NodeTypeApprovalGate   NodeType = "approval_gate"
	NodeTypeGherkinTest    NodeType = "gherkin_test"
	NodeTypeReport         NodeType = "report"
	NodeTypeRelease        NodeType = "release"
	NodeTypeInvestigation  NodeType = "investigation"
	// NodeTypeCustom has zero references from Go code, and that is
	// deliberate -- do not "clean it up" (DFLT-00023 C-2). Nothing in the
	// engine branches on it: an unrecognized type is executed as an opaque
	// custom step by falling through every structural case above, so the
	// engine never needs to name the value. It is kept because "custom" is
	// a real, documented type outside Go -- the web UI's
	// packages/web/src/nodeTypeMeta.ts gives it its own badge entry, and
	// internal/engine's DAG tests use the bare string "custom" as their
	// stand-in for a workflow.yaml-introduced type. Deleting it would leave
	// that contract with no named definition anywhere in the codebase --
	// the opposite of what the zero reference count suggests.
	NodeTypeCustom NodeType = "custom"
)

type ArtifactType string

const (
	ArtifactText    ArtifactType = "text"
	ArtifactGherkin ArtifactType = "gherkin"
	ArtifactHTML    ArtifactType = "html"
	ArtifactImage   ArtifactType = "image"
	ArtifactJSON    ArtifactType = "json"
)

// IsFileBackedArtifactType reports whether artType is one of the artifact
// types whose bytes are encoded into artifacts.content (and which may
// therefore be supplied as a file_path on create): html and image. Every
// other type stores its content verbatim as text, which is why file_path and
// a client-supplied metadata.mime_type are rejected for them.
//
// This lives in domain rather than in any one consumer because the same
// "html or image" test is needed by packages that do not import each
// other -- internal/httpserver (artifact create/validate) and
// internal/artifactcontent (Content-Type selection). It used to be
// open-coded in each, so adding a file-backed type meant finding every
// unrelated expression separately (DFLT-00023 D-5). Note this is only the
// "are its bytes file-backed" question; the per-type encode/decode and
// Content-Type switches in internal/artifactcontent are a separate concern
// and are deliberately left alone (DFLT-00023 D-6).
func IsFileBackedArtifactType(artType ArtifactType) bool {
	return artType == ArtifactHTML || artType == ArtifactImage
}

type EdgeCondition string

const (
	EdgeSuccess EdgeCondition = "success"
	EdgeFailure EdgeCondition = "failure"
	EdgeLoop    EdgeCondition = "iteration_loop"
	EdgeAlways  EdgeCondition = "always"
)

type Ticket struct {
	ID             string       `json:"id"`
	ProjectID      string       `json:"project_id"`
	Title          string       `json:"title"`
	Description    string       `json:"description"`
	Status         TicketStatus `json:"status"`
	AutoExecutable bool         `json:"auto_executable"`
	Blocked        bool         `json:"blocked"`
	CreatedAt      string       `json:"created_at"`
	UpdatedAt      string       `json:"updated_at"`
	// RefinedAt is set the moment refine-ticket last overwrote Description
	// with non-empty text (see engine.RefineTicket) -- nil until that has
	// ever happened. Distinct from UpdatedAt, which also moves on every
	// other ticket write (self-assignment toggles, graph execution, ...).
	RefinedAt *string `json:"refined_at,omitempty"`
	// ClosedReason is the free-text reason passed to close-ticket, if any.
	// It is overwritten every time the ticket is closed again (never merged
	// with a prior value) and is left untouched by ReopenTicket, so it stays
	// visible as history after a reopen until the next close overwrites it.
	ClosedReason *string `json:"closed_reason,omitempty"`
	// Assignee is the ticket's only notion of assignment: nil means
	// unassigned, otherwise it holds the display name of whoever last
	// pressed "assign to me" via PATCH /api/tickets/{id}'s "assignee" field --
	// that name is whatever was configured as their own "アプリ設定" MyName
	// (see runtimeconfig.FileConfig's MyName) at the moment they pressed it,
	// captured server-side so every viewer sees the same value regardless of
	// their own local MyName setting.
	//
	// DFLT-00024 first introduced a free-text assignee, replaced by a bare
	// AssignedToMe boolean, which DFLT-00047 in turn replaced with this
	// field because a shared backend (MySQL) makes "assigned_to_me"
	// ambiguous -- every viewer's client rendered that same shared
	// true/false against their *own* local MyName, so another person's
	// assignment displayed (and could be cleared) as if it were the
	// viewer's own. Storing the actual name server-side removes that
	// ambiguity.
	Assignee *string `json:"assignee,omitempty"`
	// GraphExpandedAt is set the moment engine.ExpandGraph first succeeds for
	// this ticket (same nullable-timestamp pattern as RefinedAt) -- nil until
	// that has ever happened. ExpandGraph/CompleteNode/ReopenNodes/
	// UnstickNode/ReopenTicket are not handed the workflow config.Catalog
	// (threading it through would ripple into every caller and test of those
	// methods), so "has the graph been expanded beyond its seed nodes?" can't
	// be answered by checking node counts/types against the catalog's seed
	// list. This column answers it directly instead (DFLT-00046): a non-nil
	// value means the ticket has nodes beyond the seed, which
	// deriveTicketStatus needs to avoid calling a ticket DONE while it only
	// has the plan/plan_review seed sitting at DONE.
	GraphExpandedAt *string `json:"graph_expanded_at,omitempty"`
	// Priority is the ticket's priority (DFLT-00048): always one of
	// TicketPriorityHigh/Medium/Low, DefaultTicketPriority (MEDIUM) when
	// none was given at creation (DFLT-00083). It is always serialized --
	// there is no null/omitted "unset" state.
	Priority TicketPriority `json:"priority"`
	// Labels are the project-scoped labels attached to this ticket
	// (DFLT-00084), sorted by name case-insensitively. The store always
	// fills it with a non-nil slice so it serializes as [] rather than null
	// for a ticket with no labels. The link is by label ID, so renaming or
	// recoloring a label shows up on every ticket that carries it.
	//
	// On store.GraphRepository.CreateTicket's input only each element's ID
	// is read (the labels to attach); Name/Color/etc. are ignored there.
	Labels []Label `json:"labels"`
}

// LabelColor is one of the fixed palette keys a label can use (DFLT-00084).
// Only the key is stored; how each key looks (light/dark theme classes) is
// the Web UI's concern (packages/web/src/labelMeta.ts).
type LabelColor string

const (
	LabelColorGray   LabelColor = "gray"
	LabelColorRed    LabelColor = "red"
	LabelColorOrange LabelColor = "orange"
	LabelColorAmber  LabelColor = "amber"
	LabelColorGreen  LabelColor = "green"
	LabelColorTeal   LabelColor = "teal"
	LabelColorBlue   LabelColor = "blue"
	LabelColorIndigo LabelColor = "indigo"
	LabelColorPurple LabelColor = "purple"
	LabelColorPink   LabelColor = "pink"
)

// LabelColors is the fixed palette in display order.
var LabelColors = []LabelColor{
	LabelColorGray, LabelColorRed, LabelColorOrange, LabelColorAmber, LabelColorGreen,
	LabelColorTeal, LabelColorBlue, LabelColorIndigo, LabelColorPurple, LabelColorPink,
}

// ParseLabelColor validates s against the fixed palette. It is exact-match:
// no trimming or case folding, so the stored key is always one of
// LabelColors verbatim.
func ParseLabelColor(s string) (LabelColor, error) {
	for _, c := range LabelColors {
		if string(c) == s {
			return c, nil
		}
	}
	return "", NewAPIError(ErrCodeInvalidLabelColor, "invalid label color %q: must be one of the fixed palette colors", s)
}

// MaxLabelNameLength is the maximum label name length in runes (not bytes),
// after surrounding whitespace is trimmed.
const MaxLabelNameLength = 50

// NormalizeLabelName trims surrounding whitespace (including full-width
// spaces) and rejects an empty result or one longer than
// MaxLabelNameLength runes. Label creation and renaming both go through it.
func NormalizeLabelName(s string) (string, error) {
	name := strings.TrimSpace(s)
	if name == "" {
		return "", NewAPIError(ErrCodeInvalidLabelName, "label name is required")
	}
	if n := utf8.RuneCountInString(name); n > MaxLabelNameLength {
		return "", NewAPIError(ErrCodeInvalidLabelName, "label name is %d characters long; the maximum is %d", n, MaxLabelNameLength)
	}
	return name, nil
}

// Label is a project-scoped label (DFLT-00084). Name is unique within its
// project, compared case-insensitively.
type Label struct {
	ID        string     `json:"id"`
	ProjectID string     `json:"project_id"`
	Name      string     `json:"name"`
	Color     LabelColor `json:"color"`
	CreatedAt string     `json:"created_at"`
	UpdatedAt string     `json:"updated_at"`
}

// LabelUsage is a label plus how many tickets carry it, as listed by
// GET /api/projects/{id}/labels (the Web UI's delete confirmation shows the
// count).
type LabelUsage struct {
	Label
	TicketCount int `json:"ticket_count"`
}

// Project scopes a set of tickets to one prefix-based ID namespace. Where
// the project lives on disk is not part of it (DFLT-00080): that local path
// differs per team member, so it is kept per environment in
// the home config file's projectPaths (internal/runtimeconfig), not in the
// shared DB. Prefix is immutable once created (see
// internal/project.ResolvePrefix): tickets/nodes already minted under it
// would otherwise disagree with a later rename. TicketSeq/NodeSeq (the
// per-project/per-ticket counters that mint new IDs) are store-internal
// bookkeeping and deliberately not exposed here -- see store.SQLiteRepository.
type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// GraphNode is one step in a ticket's execution graph. GateID/Criteria are
// only populated for NodeTypeReviewGate nodes: GateID names which catalog
// entry produced it, Criteria is the resolved review text frozen at refine
// time (see internal/engine.RefineTicket) so later config edits never change
// what an already-created ticket was reviewed against.
type GraphNode struct {
	ID             string     `json:"id"`
	TicketID       string     `json:"ticket_id"`
	Name           string     `json:"name"`
	Type           NodeType   `json:"type"`
	Status         NodeStatus `json:"status"`
	IterationCount int        `json:"iteration_count"`
	MaxIterations  int        `json:"max_iterations"`
	Assignee       *string    `json:"assignee,omitempty"`
	IsManual       bool       `json:"is_manual"`
	GateID         *string    `json:"gate_id,omitempty"`
	Criteria       *string    `json:"criteria,omitempty"`
	// ConfigID is the id this node had in the workflow catalog/patch that
	// created it (e.g. "plan", "impl", "pci_review"). It lets the engine
	// re-derive the config-id -> DB-id mapping for a ticket already in
	// progress, which is required to attach newly-generated nodes (phase 2
	// of graph construction) to already-existing ones (phase 1 seed nodes).
	ConfigID  *string `json:"config_id,omitempty"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

type GraphEdge struct {
	ID         string        `json:"id"`
	TicketID   string        `json:"ticket_id"`
	FromNodeID string        `json:"from_node_id"`
	ToNodeID   string        `json:"to_node_id"`
	Condition  EdgeCondition `json:"condition"`
	CreatedAt  string        `json:"created_at"`
}

type Artifact struct {
	ID       string       `json:"id"`
	TicketID string       `json:"ticket_id"`
	NodeID   string       `json:"node_id"`
	Name     string       `json:"name"`
	Type     ArtifactType `json:"type"`
	Content  *string      `json:"content,omitempty"`
	FilePath *string      `json:"file_path,omitempty"`
	Metadata *string      `json:"metadata,omitempty"`
	// HasContent reports whether this artifact has bytes stored in the DB's
	// content column, independent of whether Content itself is populated on
	// this particular struct value. Listing endpoints that omit Content for
	// html/image rows to keep payloads small (see
	// store.artifactSummaryCols/ListArtifactsByTicket, DFLT-00006's
	// non-functional review) still set this so callers know whether
	// GET /api/artifacts/{id}/content has anything to serve.
	HasContent bool   `json:"has_content"`
	CreatedAt  string `json:"created_at"`
}

type TicketDetail struct {
	Ticket
	Nodes     []GraphNode `json:"nodes"`
	Edges     []GraphEdge `json:"edges"`
	Artifacts []Artifact  `json:"artifacts"`
}

// TicketGraph is one element of GET /api/tickets (DFLT-00112): a ticket plus
// its execution graph, and deliberately *without* its artifacts.
//
// The Web UI polls that endpoint every 15s. It used to get bare tickets and
// then fetch TicketDetail for each one, which made a poll "1 + N" requests
// and re-transferred every text artifact's whole body (plans, review
// verdicts) even though only an expanded ticket's panel ever reads them.
// Nodes and edges are narrow rows and the collapsed cards do need them
// (progress bar, node chips, approval-gate highlight, the dashboard
// totals), so they moved into the list; artifacts stay behind in
// GET /api/tickets/{id}, which is now fetched only for expanded tickets.
//
// Having no Artifacts field at all -- rather than an empty one -- is what
// keeps the key out of the JSON entirely, so this response can't quietly
// start carrying artifact bodies again.
//
// Nodes/Edges are always serialized as arrays, never null: the UI reads
// ticket.nodes.length unconditionally.
type TicketGraph struct {
	Ticket
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}
