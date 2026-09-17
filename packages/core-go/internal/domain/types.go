// Package domain holds the core entities persisted by the graph engine.
//
// Timestamps are plain RFC3339 strings (not time.Time): this matches the
// original TS contract (new Date().toISOString()) that the web UI and JSON
// API consumers already expect, and sidesteps database/sql's driver-specific
// handling of time.Time scan destinations.
package domain

import "fmt"

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
	// once its non-loop prerequisites are all DONE again -- in a graph where
	// the loop target is not a direct prerequisite (e.g. test_review looping
	// back to impl while depending on gherkin_test), that can be the very
	// next call, so the status may only be visible briefly. It participates
	// in no syncTicketStatus branch (see engine.go), so it changes no ticket-
	// status derivation; like NodeRejected, the column has no CHECK
	// constraint, so no migration is needed to store the new value.
	NodeAwaitingFix NodeStatus = "AWAITING FIX"
)

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
	// that name is whatever was configured as their own "全体設定" MyName
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
}

// Project scopes a set of tickets to one prefix-based ID namespace. Where
// the project lives on disk is not part of it (DFLT-00080): that local path
// differs per team member, so it is kept per environment in
// graph-config.json's projectPaths (internal/runtimeconfig), not in the
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
