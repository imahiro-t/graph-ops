// Package store persists domain entities to SQLite via database/sql, using
// the pure-Go modernc.org/sqlite driver so the binary needs no C toolchain
// and cross-compiles cleanly to Windows/macOS/Linux.
package store

import "github.com/graph-ops/core-go/internal/domain"

// TicketPatch carries optional field updates for UpdateTicket; nil fields are
// left unchanged.
//
// Assignee is a double pointer for the same reason as NodePatch.Assignee
// below: nil leaves the stored value unchanged, a non-nil Assignee pointing
// at a nil *string explicitly clears it, and a non-nil Assignee pointing at
// a non-nil *string sets it. This lets PATCH /api/tickets/{id} clear
// assignee without disturbing a PATCH that merely omits the field.
// Priority is a single pointer (DFLT-00083): nil leaves the stored value
// unchanged and a non-nil Priority sets it. A priority can't be cleared --
// there is no unset state to clear it to.
type TicketPatch struct {
	Title           *string
	Description     *string
	Status          *domain.TicketStatus
	AutoExecutable  *bool
	Blocked         *bool
	RefinedAt       *string
	ClosedReason    *string
	Assignee        **string
	GraphExpandedAt *string
	Priority        *domain.TicketPriority
}

// NodePatch carries optional field updates for UpdateNode; nil fields are
// left unchanged.
//
// Assignee is a double pointer so callers can distinguish three states, not
// just two: a nil Assignee leaves the stored value unchanged; a non-nil
// Assignee pointing at a nil *string explicitly clears it (stores NULL); a
// non-nil Assignee pointing at a non-nil *string sets it to that value. A
// single pointer can't express "explicitly clear" separately from "leave
// unchanged", which matters here because PATCH /api/nodes/{id} must be able
// to set assignee to null without disturbing every PATCH that merely omits
// the assignee field.
type NodePatch struct {
	Name           *string
	Type           *domain.NodeType
	Status         *domain.NodeStatus
	IterationCount *int
	MaxIterations  *int
	Assignee       **string
	IsManual       *bool
	GateID         *string
	Criteria       *string
}

// ProjectPatch carries optional field updates for UpdateProject; nil fields
// are left unchanged. Prefix is deliberately not patchable (see
// domain.Project's doc comment): it is fixed at creation time.
type ProjectPatch struct {
	Name *string
}

type GraphRepository interface {
	// Init applies the schema.
	Init() error

	// CreateTicket mints a new ID (<project's prefix>-<seq:05d>) from
	// projectID's own counter and ignores t.ID/t.ProjectID.
	CreateTicket(projectID string, t domain.Ticket) (domain.Ticket, error)
	GetTicket(id string) (*domain.Ticket, error)
	GetTicketDetail(id string) (*domain.TicketDetail, error)
	// ListTickets returns every ticket across all projects (used by the
	// explicit ?all=true escape hatch); ListTicketsByProject is what backs
	// the default, project-scoped listing.
	ListTickets() ([]domain.Ticket, error)
	ListTicketsByProject(projectID string) ([]domain.Ticket, error)
	UpdateTicket(id string, patch TicketPatch) (domain.Ticket, error)
	DeleteTicket(id string) error

	// CreateNode mints a new ID (<ticket's ID>-<seq:02d>, a ticket is capped
	// at 99 nodes) from n.TicketID's own counter and ignores n.ID.
	CreateNode(n domain.GraphNode) (domain.GraphNode, error)
	GetNode(id string) (*domain.GraphNode, error)
	ListNodesByTicket(ticketID string) ([]domain.GraphNode, error)
	UpdateNode(id string, patch NodePatch) (domain.GraphNode, error)
	DeleteNode(id string) error

	CreateEdge(e domain.GraphEdge) (domain.GraphEdge, error)
	ListEdgesByTicket(ticketID string) ([]domain.GraphEdge, error)
	ClearEdgesByTicket(ticketID string) error

	CreateArtifact(a domain.Artifact) (domain.Artifact, error)
	GetArtifact(id string) (*domain.Artifact, error)
	ListArtifactsByTicket(ticketID string) ([]domain.Artifact, error)
	ListArtifactsByNode(nodeID string) ([]domain.Artifact, error)

	// CreateProject resolves prefix (validating it if explicit, deriving
	// and de-duplicating one from name if empty -- see
	// internal/project.ResolvePrefix) and persists a new Project. A
	// project's local path is not stored in the DB (DFLT-00080): it is a
	// per-environment setting in graph-config.json's projectPaths (see
	// internal/runtimeconfig).
	CreateProject(name, prefix string) (domain.Project, error)
	GetProject(id string) (*domain.Project, error)
	ListProjects() ([]domain.Project, error)
	UpdateProject(id string, patch ProjectPatch) (domain.Project, error)
	// DeleteProject deletes the project and every ticket that belongs to it
	// (cascading, in turn, to their nodes/edges/artifacts -- see schemaDDL's
	// ON DELETE CASCADE chain), and clears the "current project" pointer if
	// it referenced this project. A no-op (not an error) if the project
	// doesn't exist.
	DeleteProject(id string) error

	// GetCurrentProjectID returns "" (not an error) when no project has
	// ever been selected.
	GetCurrentProjectID() (string, error)
	SetCurrentProjectID(projectID string) error
}
