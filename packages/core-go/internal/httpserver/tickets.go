package httpserver

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/graph-ops/core-go/internal/artifactcontent"
	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// nullableString decodes an optional, clearable JSON string field so PATCH
// handlers can distinguish three states a plain (or even doubly-indirected)
// *string cannot: the key absent (Present == false: leave the stored value
// unchanged), `"field": null` (Present == true, Value == nil: explicitly
// clear it), and `"field": "value"` (Present == true, Value != nil: set it).
// encoding/json only calls UnmarshalJSON when the key exists in the object at
// all -- unlike a bare **string, whose outer pointer ends up nil for both
// "absent" and "explicit null", giving no way to tell them apart. Any other
// JSON value (a number, an object, ...) is a decode error, i.e. a 400.
//
// The type is generic rather than tied to one field: handleUpdateNode's node
// assignee and, since DFLT-00047, handleUpdateTicket's ticket assignee both
// use it. (Tickets first used a plain *string here for a free-text assignee,
// which DFLT-00024 removed in favor of a plain boolean; DFLT-00047 replaced
// that boolean with a real, clearable name again -- see domain.Ticket's
// Assignee doc comment for why.)
type nullableString struct {
	Present bool
	Value   *string
}

func (n *nullableString) UnmarshalJSON(data []byte) error {
	n.Present = true
	if string(data) == "null" {
		n.Value = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	n.Value = &s
	return nil
}

// handleListTickets defaults to the currently-selected project (completion
// criterion: "switching projects shows only that project's ticket list"):
// pass ?project_id=<id> to list a specific project's tickets
// explicitly, or ?all=true to bypass project scoping entirely and list
// every ticket across every project.
func (s *Server) handleListTickets(w http.ResponseWriter, r *http.Request) {
	var tickets []domain.Ticket
	var err error
	switch {
	case r.URL.Query().Get("all") == "true":
		tickets, err = s.repo.ListTickets()
	default:
		projectID := r.URL.Query().Get("project_id")
		if projectID == "" {
			projectID, err = s.repo.GetCurrentProjectID()
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
		}
		if projectID == "" {
			tickets = []domain.Ticket{}
		} else {
			tickets, err = s.repo.ListTicketsByProject(projectID)
		}
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, tickets)
}

// handleCreateTicket defaults to the currently-selected project when
// project_id is omitted from the body; if there is neither an explicit
// project_id nor a current project, the request fails with
// ErrCodeNoCurrentProject rather than silently picking one.
//
// There is no assignee field here: a new ticket is always created unassigned
// (see engine.CreateTicket), regardless of whether a caller's body includes
// one -- encoding/json ignores unknown keys, exactly as for any other field
// this handler doesn't know. Assignment only ever happens afterward, via
// PATCH /api/tickets/{id}'s "assignee" field.
//
// Priority (DFLT-00059) is a plain optional *string, unlike PATCH's
// nullableString: at creation time there is no existing value to leave
// unchanged or clear, so the only two states that matter are "not given"
// and "a level". "Not given" -- the key omitted, or an explicit
// `"priority": null`, which decodes to the same nil -- creates the ticket
// with domain.DefaultTicketPriority (MEDIUM, DFLT-00083), applied by the
// engine. Any other value must be HIGH/MEDIUM/LOW, or the request is a 400
// and no ticket is created.
func (s *Server) handleCreateTicket(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title       string  `json:"title"`
		Description string  `json:"description"`
		ProjectID   string  `json:"project_id"`
		Priority    *string `json:"priority"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.Title == "" {
		writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeTitleRequired, "title is required"))
		return
	}

	var priority *domain.TicketPriority
	if body.Priority != nil {
		parsed, err := domain.ParseTicketPriority(*body.Priority)
		if err != nil {
			writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation, "%s", err))
			return
		}
		priority = &parsed
	}

	projectID := body.ProjectID
	if projectID == "" {
		var err error
		projectID, err = s.repo.GetCurrentProjectID()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	if projectID == "" {
		writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeNoCurrentProject,
			"no current project selected; create or select a project first"))
		return
	}

	ticket, err := s.engine.CreateTicketWithPriority(projectID, body.Title, body.Description, priority)
	if err != nil {
		writeError(w, statusForError(err, http.StatusInternalServerError), err)
		return
	}
	writeJSON(w, http.StatusCreated, ticket)
}

func (s *Server) handleGetTicket(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	detail, err := s.repo.GetTicketDetail(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if detail == nil {
		writeError(w, http.StatusNotFound, domain.NewAPIError(domain.ErrCodeTicketNotFound, "ticket not found: %s", id))
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleUpdateTicket applies a partial update.
//
// Every field is validated before s.repo.UpdateTicket is called, never after
// and never inside the store (DFLT-00103 / BUG-05). That ordering is what
// makes "a rejected PATCH changes nothing" structural rather than a property
// each new field has to be tested for: at the point the first write happens,
// the whole body has already been accepted.
func (s *Server) handleUpdateTicket(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		// Title is *string, not a plain string, so "key absent" (leave it
		// alone) stays distinguishable from `"title": ""` -- which is
		// rejected below, since a ticket with no title is unreadable in
		// every list the UI renders.
		Title       *string `json:"title"`
		Description *string `json:"description"`
		// Status arrives as a *string and goes through
		// domain.ParseTicketStatus; typed as *domain.TicketStatus it would
		// be a cast, not a check, and any string at all would reach the
		// column.
		Status         *string `json:"status"`
		AutoExecutable *bool   `json:"auto_executable"`
		Blocked        *bool   `json:"blocked"`
		// Assignee: see nullableString's doc comment for why this isn't a
		// plain *string, and domain.Ticket.Assignee's doc comment for what
		// it means -- driven by the Web UI's "assign to me"/"unassign"
		// buttons, the ticket's only form of assignment.
		Assignee nullableString `json:"assignee"`
		// Priority: same nullableString mechanism as Assignee, so a caller
		// can distinguish "leave unchanged" (key absent) from "set"
		// (`"priority": "HIGH"`). An explicit `"priority": null` is a 400
		// (DFLT-00083): a priority can't be cleared, see
		// domain.Ticket.Priority's doc comment.
		Priority nullableString `json:"priority"`
		// LabelIDs (DFLT-00084): absent leaves labels unchanged, an array
		// replaces them (duplicates collapsed, [] removes all), null is a
		// 400. Every ID must be a label of the ticket's project, otherwise
		// the whole PATCH is a 400 LABEL_NOT_FOUND and nothing is written.
		LabelIDs nullableStringSlice `json:"label_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if body.Title != nil && strings.TrimSpace(*body.Title) == "" {
		writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeTitleRequired,
			"title cannot be empty"))
		return
	}

	patch := store.TicketPatch{
		Title: body.Title, Description: body.Description,
		AutoExecutable: body.AutoExecutable, Blocked: body.Blocked,
	}
	if body.Status != nil {
		status, err := domain.ParseTicketStatus(*body.Status)
		if err != nil {
			writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation, "%s", err))
			return
		}
		// CLOSED is a real status but not one this endpoint may set: closing
		// records a reason alongside it (engine.CloseTicket, DFLT-00043), and
		// a bare status write would leave a ticket closed with no reason
		// while bypassing the one code path that owns that transition.
		// Reopening is the same story in reverse (engine.ReopenTicket).
		if status == domain.TicketClosed {
			writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation,
				"status cannot be set to %q here: use POST /api/tickets/{id}/close, which records a reason",
				domain.TicketClosed))
			return
		}
		patch.Status = &status
	}
	if body.LabelIDs.Present {
		if body.LabelIDs.Null {
			writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation,
				"label_ids cannot be null: send [] to remove every label"))
			return
		}
		ids := body.LabelIDs.Value
		patch.LabelIDs = &ids
	}
	if body.Assignee.Present {
		patch.Assignee = &body.Assignee.Value
	}
	if body.Priority.Present {
		if body.Priority.Value == nil {
			writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation,
				"priority cannot be cleared: must be one of %q, %q, %q",
				domain.TicketPriorityHigh, domain.TicketPriorityMedium, domain.TicketPriorityLow))
			return
		}
		priority, err := domain.ParseTicketPriority(*body.Priority.Value)
		if err != nil {
			writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation, "%s", err))
			return
		}
		patch.Priority = &priority
	}

	// Errors go through statusForTicketUpdateError: a missing ticket is 404
	// TICKET_NOT_FOUND, an invalid label_ids entry 400, anything
	// unclassified still 500.
	updated, err := s.repo.UpdateTicket(id, patch)
	if err != nil {
		writeError(w, statusForTicketUpdateError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteTicket(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.repo.DeleteTicket(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleRefine(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Description string `json:"description"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	// Priority is not exposed by this HTTP endpoint (DFLT-00059 handles it via
	// the CLI's refine-ticket --priority flag only) -- NoPriorityChange()
	// leaves the ticket's stored priority untouched.
	ticket, err := s.engine.RefineTicket(id, body.Description, engine.NoPriorityChange())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, ticket)
}

// handleCloseTicket withdraws the ticket (DFLT-00043) regardless of its
// current status or its nodes' statuses -- see engine.CloseTicket. The
// request body (and therefore "reason") is entirely optional, matching
// handleRefine's r.ContentLength check above: a bodyless POST closes with an
// empty reason rather than failing to decode.
func (s *Server) handleCloseTicket(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	ticket, err := s.engine.CloseTicket(id, body.Reason)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, ticket)
}

// handleReopenTicket moves a CLOSED ticket back into the normal status flow
// -- see engine.ReopenTicket. Reopening a ticket that isn't CLOSED is
// rejected by the engine call and surfaced here as 400, the same way every
// other engine-validated mutation in this file is.
func (s *Server) handleReopenTicket(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ticket, err := s.engine.ReopenTicket(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, ticket)
}

// handleCompleteNode accepts an optional "artifacts" array inline in the
// same request as the pass/fail verdict, as a convenience so a caller
// doesn't have to make N separate POST .../artifacts calls before
// completing a node. Security review art-cdbe6a11 (6th security review,
// DFLT-00006) found that this convenience array used to skip every
// validation handleCreateArtifact enforces (report-template checks,
// metadata.mime_type rejection, file_path rejection, magic-byte checked
// encoding) by handing domain.Artifact values straight to
// engine.CompleteNode, which persisted them via repo.CreateArtifact
// unchecked -- a full stored-XSS bypass via
// type:"image"+forged metadata.mime_type. Each entry in body.Artifacts is
// now routed through the same prepareArtifactForCreate choke point
// handleCreateArtifact uses, so the two request shapes (a standalone POST
// .../artifacts call vs. artifacts embedded in POST .../complete) can never
// again drift apart on what they allow through.
//
// DFLT-00016's rejection-reason convention rides this same mechanism rather
// than adding a schema/request field: a caller rejecting an approval_gate
// node (passed=false) is expected to include an artifact named
// "rejection_reason" (type "text", content the free-text reason) in this
// same request -- see internal/config/defaults/node-types/approval_gate.md
// and the CLI's `complete-node --reason` flag, which builds exactly this
// shape. There is no server-side enforcement that a rejected approval_gate
// actually includes one (see approval_gate.md and TicketItem.tsx for where
// that's enforced instead); this endpoint only guarantees that if one is
// sent, it goes through the same validation as any other artifact.
func (s *Server) handleCompleteNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Passed    *bool `json:"passed"`
		Artifacts []struct {
			Name     string              `json:"name"`
			Type     domain.ArtifactType `json:"type"`
			Content  *string             `json:"content"`
			FilePath *string             `json:"file_path"`
			Metadata *string             `json:"metadata"`
		} `json:"artifacts"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	passed := true
	if body.Passed != nil {
		passed = *body.Passed
	}

	// The node is only resolved here -- rather than unconditionally -- when
	// there's at least one artifact to validate/prepare (report-template
	// enforcement needs node.Type; every other artifact-creating check
	// needs only s.cfg, not the node). The overwhelmingly common call shape
	// (no inline artifacts -- most nodes save artifacts via a separate POST
	// .../artifacts beforehand and call complete-node with none) skips a
	// lookup engine.CompleteNode will redundantly perform again anyway.
	var node *domain.GraphNode
	if len(body.Artifacts) > 0 {
		var err error
		node, err = s.repo.GetNode(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if node == nil {
			writeError(w, http.StatusNotFound, domain.NewAPIError(domain.ErrCodeNodeNotFound, "node not found: %s", id))
			return
		}
	}

	artifacts := make([]domain.Artifact, 0, len(body.Artifacts))
	for _, a := range body.Artifacts {
		prepared, err := s.prepareArtifactForCreate(node, artifactInput{
			Name: a.Name, Type: a.Type, Content: a.Content, FilePath: a.FilePath, Metadata: a.Metadata,
		})
		if err != nil {
			writeError(w, statusForError(err, http.StatusBadRequest), err)
			return
		}
		artifacts = append(artifacts, prepared)
	}

	result, err := s.engine.CompleteNode(id, passed, artifacts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// nodePatchWithdrawnFields names, for the error message, the PATCH
// /api/nodes/{id} fields DFLT-00103 withdrew. They are refused explicitly
// rather than silently ignored as unknown JSON keys would be, so a caller
// that was setting one gets told instead of watching its write vanish.
//
// Why each went:
//
//   - name / type: a node's identity comes from the workflow catalog the
//     graph was expanded from, not from whoever last sent a PATCH. `type` in
//     particular cannot be validated the way `status` can -- domain.NodeType
//     is deliberately open, so that a workflow.yaml may introduce its own
//     types -- which left "accept any string" as the only alternative to
//     removing it, and any string is exactly what BUG-05 was about.
//   - iteration_count: the engine maintains it as review gates loop
//     (CompleteNode). A value written from outside is not a smaller version
//     of that bookkeeping, it is a corruption of it.
//
// No caller was found for any of the three: the Web UI never issues this
// PATCH at all (it completes nodes through POST /api/nodes/{id}/complete),
// and the CLI goes through the engine rather than HTTP.
const nodePatchWithdrawnFields = "name, type, iteration_count"

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		// Status arrives as a *string and is parsed, for the same reason as
		// on handleUpdateTicket: a *domain.NodeStatus field would make the
		// decoder accept any string as a status.
		Status        *string `json:"status"`
		MaxIterations *int    `json:"max_iterations"`
		// Assignee: see nullableString's doc comment for why this isn't a
		// plain *string.
		Assignee nullableString `json:"assignee"`
		IsManual *bool          `json:"is_manual"`

		// Withdrawn fields, kept only to be refused -- see
		// nodePatchWithdrawnFields. json.RawMessage records "the key was
		// present" without committing to a type, so even a well-formed value
		// is reported rather than applied.
		Name           json.RawMessage `json:"name"`
		Type           json.RawMessage `json:"type"`
		IterationCount json.RawMessage `json:"iteration_count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// Ordered, not a map, so the field named in the message is the same one
	// on every run for a body that carries several of them.
	for _, sent := range []struct {
		field string
		raw   json.RawMessage
	}{
		{"name", body.Name}, {"type", body.Type}, {"iteration_count", body.IterationCount},
	} {
		if sent.raw != nil {
			writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation,
				"%q can no longer be set through PATCH /api/nodes/{id} (withdrawn fields: %s)",
				sent.field, nodePatchWithdrawnFields))
			return
		}
	}

	patch := store.NodePatch{MaxIterations: body.MaxIterations, IsManual: body.IsManual}
	if body.Status != nil {
		status, err := domain.ParseNodeStatus(*body.Status)
		if err != nil {
			writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation, "%s", err))
			return
		}
		patch.Status = &status
	}
	if body.MaxIterations != nil && *body.MaxIterations < 1 {
		writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeInvalidMaxIterations,
			"max_iterations must be 1 or greater, got %d", *body.MaxIterations))
		return
	}
	if body.Assignee.Present {
		patch.Assignee = &body.Assignee.Value
	}

	// Through the engine, not s.repo, so the owning ticket's status is
	// re-derived from its nodes the way it is for every other node mutation
	// -- see engine.UpdateNode (DFLT-00103 / BUG-05). A node this server
	// doesn't have comes back as NODE_NOT_FOUND and is answered 404 by
	// statusForError, the same as before.
	updated, err := s.engine.UpdateNode(id, patch)
	if err != nil {
		writeError(w, statusForError(err, http.StatusBadRequest), err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleCreateArtifact resolves body.NodeID through GetNode first (rather
// than trusting it verbatim) so the artifact is always attached to a node
// that actually exists: node.ID/node.TicketID (the canonical IDs from the
// looked-up row) are what actually get stored, never the caller-supplied
// value directly -- otherwise the INSERT could violate the artifacts->nodes
// foreign key or attach the artifact to nothing resolvable. node.TicketID is
// used in preference to the {id} path value for the same reason.
func (s *Server) handleCreateArtifact(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NodeID   string              `json:"node_id"`
		Name     string              `json:"name"`
		Type     domain.ArtifactType `json:"type"`
		Content  *string             `json:"content"`
		FilePath *string             `json:"file_path"`
		Metadata *string             `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	node, err := s.repo.GetNode(body.NodeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if node == nil {
		writeError(w, http.StatusNotFound, domain.NewAPIError(domain.ErrCodeNodeNotFound, "node not found: %s", body.NodeID))
		return
	}

	prepared, err := s.prepareArtifactForCreate(node, artifactInput{
		Name: body.Name, Type: body.Type, Content: body.Content, FilePath: body.FilePath, Metadata: body.Metadata,
	})
	if err != nil {
		writeError(w, statusForError(err, http.StatusBadRequest), err)
		return
	}
	prepared.ID = engine.NewArtifactID()
	prepared.TicketID = node.TicketID
	prepared.NodeID = node.ID

	artifact, err := s.repo.CreateArtifact(prepared)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, artifact)
}

// artifactInput is the caller-supplied shape for a to-be-created artifact,
// shared by handleCreateArtifact's JSON body and handleCompleteNode's
// inline "artifacts" array -- the two request shapes decode independently
// (each matches its own endpoint's wire format) but both funnel into this
// same struct before validation, so prepareArtifactForCreate only has one
// shape to validate against.
type artifactInput struct {
	Name     string
	Type     domain.ArtifactType
	Content  *string
	FilePath *string
	Metadata *string
}

// prepareArtifactForCreate is the single validation/normalization choke
// point every artifact-creating write path must pass through before
// repo.CreateArtifact is ever called. Security review art-cdbe6a11 (6th
// security review, DFLT-00006) found that handleCompleteNode's inline
// "artifacts" array bypassed all of this -- handing values straight to
// engine.CompleteNode, which persisted them via repo.CreateArtifact with
// none of handleCreateArtifact's checks (report-template enforcement,
// metadata.mime_type rejection for non-html/image, file_path rejection for
// non-html/image, and EncodeFile/EncodeInline's magic-byte check plus
// server-side mime_type override for html/image) -- letting a POST to
// /api/nodes/{id}/complete plant an "image" artifact with a forged
// metadata.mime_type that artifactcontent.Payload would later trust
// verbatim: a full stored-XSS bypass of every prior fix in this class (see
// arts 9e220d80, 0e621b48, 3d521dc0, 65fe221a). Both handleCreateArtifact
// and handleCompleteNode now call this before ever touching s.repo, so the
// two entry points can no longer drift apart on what they allow through.
//
// node is the resolved target node (used only for report-template
// enforcement, which needs node.Type; it may be nil when in.Type can never
// be "html" for a report node in the caller's context, but every current
// caller always resolves and passes it). The returned Artifact carries
// Name/Type/Content/FilePath/Metadata only -- ID/TicketID/NodeID are still
// the caller's responsibility to stamp (handleCreateArtifact stamps them
// itself before calling repo.CreateArtifact directly; engine.CompleteNode
// stamps them the same way it always has for its own artifacts parameter).
func (s *Server) prepareArtifactForCreate(node *domain.GraphNode, in artifactInput) (domain.Artifact, error) {
	// Same fixed-report-format enforcement as the CLI's `add-artifact`
	// (see cmd/graph-engine's validateReportArtifactIfNeeded): only applies
	// to "report" node HTML artifacts. Both inline content and a file_path
	// are checked (see validateReportArtifact) so neither submission style
	// can silently bypass the fixed-template contract.
	if in.Type == domain.ArtifactHTML {
		if err := s.validateReportArtifact(node, in.Content, in.FilePath); err != nil {
			return domain.Artifact{}, err
		}
	}

	// Security review art-3d521dc0 (4th security review, DFLT-00006):
	// text/gherkin/json artifacts don't go through EncodeFile/EncodeInline
	// below (only html/image do), so nothing ever recomputes their metadata
	// server-side -- in.Metadata would otherwise pass straight through to
	// the DB verbatim. artifactcontent.Payload now refuses to honor
	// meta.MimeType for any type other than html/image (so this alone can no
	// longer flip the served Content-Type), but rejecting it here too means a
	// caller learns their request was invalid instead of silently getting an
	// artifact whose metadata doesn't do what they asked. html/image are
	// exempt because EncodeFile/EncodeInline unconditionally overwrite
	// whatever mime_type the client sent with a server-detected value (see
	// below) -- so a client-claimed mime_type never survives for them either,
	// it just doesn't need a separate rejection since it's discarded either
	// way.
	if !domain.IsFileBackedArtifactType(in.Type) && artifactcontent.DecodeMetadata(in.Metadata).MimeType != "" {
		return domain.Artifact{}, domain.NewAPIError(domain.ErrCodeValidation,
			"metadata.mime_type is not allowed for artifact type %q; only html/image artifacts may specify a mime type, and even then it is overwritten with a server-detected value", in.Type)
	}

	// Security review art-65fe221a (5th security review, DFLT-00006): the
	// switch below only ever reads file_path into stored Content for
	// html/image (matching add-artifact's CLI behavior -- see cmdAddArtifact,
	// which never sets FilePath at all for any other type). Without this
	// check, a caller could still submit type:"text"/"gherkin"/"json" with
	// only file_path and no content, permanently depending on a local
	// file_path instead of having its bytes in the DB -- defeating the
	// DB-only storage contract DFLT-00006 exists for in the first place. It
	// is rejected outright here rather than made safe to serve some other
	// way.
	if !domain.IsFileBackedArtifactType(in.Type) && in.FilePath != nil && *in.FilePath != "" {
		return domain.Artifact{}, domain.NewAPIError(domain.ErrCodeValidation,
			"file_path is not allowed for artifact type %q; only html/image artifacts may reference a file (and even then its bytes are read into the DB) -- pass the content directly in \"content\" instead", in.Type)
	}

	// html/image artifacts must carry their actual bytes in the DB, not only
	// a file_path string pointing at wherever the file happened to be read
	// from -- otherwise a remote/shared DB (DFLT-00006) can't preview them
	// from any other machine. When the caller sent file_path without inline
	// content, read the file (through the same sandbox as validation above)
	// and convert it into DB-ready Content+Metadata. FilePath itself is
	// still stored as-is afterward, but only as informational provenance --
	// see artifactcontent.Resolve's doc comment; GET
	// /api/artifacts/{id}/content never reads it back to serve content.
	content, metadata := in.Content, in.Metadata
	switch {
	case domain.IsFileBackedArtifactType(in.Type) &&
		(in.Content == nil || *in.Content == "") && in.FilePath != nil && *in.FilePath != "":
		safePath, err := s.safeArtifactPath(*in.FilePath)
		if err != nil {
			return domain.Artifact{}, err
		}
		data, err := os.ReadFile(safePath)
		if err != nil {
			return domain.Artifact{}, fmt.Errorf("reading artifact file: %w", err)
		}
		encoded, meta, err := artifactcontent.EncodeFile(in.Type, filepath.Base(safePath), data)
		if err != nil {
			return domain.Artifact{}, err
		}
		content = &encoded
		metadata = artifactcontent.EncodeMetadata(meta)

	// A caller can also submit html/image bytes directly in "content"
	// instead of via file_path. Security Review art-0e621b48: this branch
	// used to fall straight through and store in.Content/in.Metadata
	// completely unchecked -- including metadata.mime_type, a value the
	// *client* chooses. An attacker could submit type:"image", content:
	// arbitrary (non-image) bytes, and metadata: {"mime_type":"text/html"};
	// GET /api/artifacts/{id}/content is unauthenticated and would echo
	// that claimed Content-Type verbatim, serving attacker-controlled bytes
	// as HTML -- stored XSS via content-type confusion. Routing this path
	// through EncodeInline (the same function add-artifact's CLI path
	// already uses for inline content, see artifactcontent.Resolve) closes
	// the hole for both artifact types: image content is base64-decoded and
	// must pass validateImageBytes' magic-byte check, and for both html and
	// image the stored mime_type is always the server-detected/fixed value,
	// never the client's claim.
	case domain.IsFileBackedArtifactType(in.Type) && in.Content != nil && *in.Content != "":
		encoded, meta, err := artifactcontent.EncodeInline(in.Type, *in.Content)
		if err != nil {
			return domain.Artifact{}, err
		}
		content = &encoded
		metadata = artifactcontent.EncodeMetadata(meta)
	}

	return domain.Artifact{
		Name:     in.Name,
		Type:     in.Type,
		Content:  content,
		FilePath: in.FilePath,
		Metadata: metadata,
	}, nil
}

// validateReportArtifact enforces the fixed report HTML format for "report"
// node artifacts, whether the HTML arrives as inline content or via a
// file_path (see Security review node-47c93a46 / QA review node-5ff1dd13:
// the earlier version only checked file_path, silently skipping validation
// for inline content submitted over the HTTP API). A file path that can't be
// resolved/read at all is treated as "nothing to validate yet" (not an
// error) rather than blocking artifact creation on infra/path issues
// unrelated to report formatting. node is the already-resolved node (see
// handleCreateArtifact), not re-looked-up here.
func (s *Server) validateReportArtifact(node *domain.GraphNode, content, filePath *string) error {
	if node.Type != domain.NodeTypeReport {
		return nil
	}
	if filePath != nil && *filePath != "" {
		safePath, err := s.safeArtifactPath(*filePath)
		if err != nil {
			// Unlike a plain missing/unreadable file (below), a file_path
			// that fails the sandbox check is always rejected outright
			// (400), never silently skipped -- silently skipping here would
			// let a caller learn nothing was validated while still getting
			// the artifact created, defeating the point of the check.
			return err
		}
		raw, err := os.ReadFile(safePath)
		if err != nil {
			return nil
		}
		return config.ValidateReportHTML(string(raw))
	}
	if content != nil && *content != "" {
		return config.ValidateReportHTML(*content)
	}
	return nil
}

// safeArtifactPath resolves a client-supplied file_path strictly within
// s.cfg.ArtifactsDir, returning the cleaned absolute path to read, or an
// error if it escapes that directory. This HTTP endpoint has no
// authentication and the server sets a wildcard CORS policy (withCORS in
// server.go), so any web page a user's browser visits can reach it -- a
// client-supplied path must never be handed to os.ReadFile unsandboxed.
// Concretely:
//   - an absolute file_path is rejected outright rather than being read
//     directly off the local filesystem (this was the arbitrary-file-read
//     oracle flagged as blocking in Security Review node-47c93a46 finding #1:
//     it let a caller distinguish "file exists and is readable" from "file
//     missing" for any absolute path on the machine, e.g. ~/.ssh/id_rsa);
//   - the (relative) path is joined under ArtifactsDir, filepath.Clean'd,
//     and re-verified to still resolve inside ArtifactsDir, so "../"-style
//     traversal in a relative path can't escape the sandbox either.
func (s *Server) safeArtifactPath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("file_path must be relative to the artifacts directory")
	}
	root, err := filepath.Abs(s.cfg.ArtifactsDir)
	if err != nil {
		return "", err
	}
	cleaned := filepath.Clean(filepath.Join(root, path))
	if cleaned != root && !strings.HasPrefix(cleaned, root+string(filepath.Separator)) {
		return "", fmt.Errorf("file_path escapes the artifacts directory")
	}
	return cleaned, nil
}

// handleGetArtifactContent serves an artifact's raw bytes (its html source,
// its image data, ...) with an appropriate Content-Type, decoded from
// artifacts.content via artifactcontent.Payload -- the DB-only preview path
// DFLT-00006 requires: it never depends on the local filesystem of whichever
// machine created the artifact, so it works the same from any machine that
// can reach this API server, which is the whole point of "must always be
// placed in the DB" for a team sharing one remote DB. (It is also the only
// way to read an artifact's bytes over HTTP: DFLT-00103 removed the
// filesystem-backed route that used to serve the artifacts directory
// directly, on the app's own origin and with no sandbox.)
//
// The response is also isolated from the app's own origin: see the
// Content-Security-Policy / X-Content-Type-Options comment in
// writeArtifactContent for why, since agent-authored HTML served from here
// would otherwise render as a same-origin, unauthenticated-yet-privileged
// top-level document when opened in a new tab.
func (s *Server) handleGetArtifactContent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	artifact, err := s.repo.GetArtifact(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if artifact == nil {
		writeError(w, http.StatusNotFound, domain.NewAPIError(domain.ErrCodeArtifactNotFound, "artifact not found: %s", id))
		return
	}

	meta := artifactcontent.DecodeMetadata(artifact.Metadata)
	// ?download=1 (the Web UI's per-artifact "Download" button) asks for the
	// same bytes/Content-Type this endpoint already serves for inline
	// preview, but forces a Save-As download (Content-Disposition:
	// attachment) with a guaranteed filename -- inline preview only sets a
	// filename when meta.OriginalFilename happens to be recorded, which is
	// fine for a browser rendering the response in place but leaves a
	// forced download with no name (or the browser's own, useless,
	// id-shaped default) when it's empty (text/gherkin/json artifacts, and
	// any html/image row created from inline content rather than a file).
	download := r.URL.Query().Get("download") != ""

	if artifact.Content != nil && *artifact.Content != "" {
		data, contentType, err := artifactcontent.Payload(artifact.Type, *artifact.Content, meta)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		filename := meta.OriginalFilename
		if download {
			filename = artifactDownloadFilename(artifact, meta, contentType)
		}
		writeArtifactContent(w, data, contentType, filename, download)
		return
	}

	writeError(w, http.StatusNotFound, domain.NewAPIError(domain.ErrCodeArtifactNotFound, "artifact %s has no content", id))
}

func writeArtifactContent(w http.ResponseWriter, data []byte, contentType, filename string, attachment bool) {
	w.Header().Set("Content-Type", contentType)
	// Content-Security-Policy: sandbox allow-scripts forces the response to
	// render with an opaque ("null") origin whenever a browser loads it as a
	// top-level document -- notably via the Web UI's "open in new tab" link,
	// which (unlike the sandboxed <iframe> used for inline preview) hits this
	// endpoint directly with no isolation of its own. An opaque origin can't
	// script the real app origin, and isLoopbackOrigin (server.go) already
	// rejects any non-http(s) Origin header, so it also makes our existing
	// CORS/CSRF checks reject requests this response's script could send back
	// -- without those checks needing to change. X-Content-Type-Options:
	// nosniff additionally stops a browser from MIME-sniffing a non-HTML
	// artifact into an HTML document despite the Content-Type set above.
	// Applied unconditionally to every artifact type (including images and
	// download=1 attachments) rather than branching on type: a browser never
	// executes an image as script, so the header is harmless there, and a
	// single unconditional call can't be forgotten the next time an artifact
	// type is added.
	//
	// frame-ancestors 'self' rides in the same header, replacing the
	// frame-ancestors 'none' withSecurityHeaders set for every other
	// response (DFLT-00103). This endpoint is the one documented exception
	// to that rule, because the UI previews an artifact by pointing a
	// sandboxed <iframe> at it -- both inline on the ticket screen and on
	// the /artifacts/{id}/preview page (packages/web/src/components/
	// TicketItem.tsx, ArtifactPreviewPage.tsx). 'self' is what keeps that
	// working while still refusing a foreign page's frame. It has to be one
	// header with both directives: two Content-Security-Policy headers are
	// intersected by the browser, so leaving the middleware's 'none' in
	// place alongside would forbid every ancestor and break the preview.
	//
	// X-Frame-Options has no per-origin form ('self' has no equivalent
	// beyond SAMEORIGIN), so the middleware's DENY is downgraded to
	// SAMEORIGIN here rather than dropped: the app's own origin is exactly
	// what SAMEORIGIN allows, and leaving the header off entirely would give
	// a browser that predates CSP frame-ancestors no answer at all.
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts; frame-ancestors 'self'")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if filename != "" {
		if attachment {
			w.Header().Set("Content-Disposition", contentDispositionAttachment(filename))
		} else {
			w.Header().Set("Content-Disposition", contentDispositionInline(filename))
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// dispositionQuoteStripper removes the two characters that could otherwise
// let a filename escape a Content-Disposition quoted-string. A Replacer is
// safe for concurrent use, so one package-level value serves every request.
var dispositionQuoteStripper = strings.NewReplacer(`"`, "", `\`, "")

// contentDisposition builds a Content-Disposition header value (disposition
// being "inline" or "attachment") for filename, which is untrusted: it is
// metadata.OriginalFilename, derived from filepath.Base() of whatever a
// caller supplied as file_path -- see artifactcontent.EncodeFile. Building
// `inline; filename="` + filename + `"` directly, as an earlier version of
// this handler did, let a filename containing a double quote break out of the
// quoted-string and inject additional header parameters/directives (flagged
// in Security Review art-9e220d80 finding #3). Go's net/http already refuses
// to write raw CR/LF in a header value, so this only needs to handle quotes
// and backslashes:
//   - the quoted-string form strips '"' and '\' so the value can never
//     escape its own quotes;
//   - filename* (RFC 5987/6266) carries the original bytes percent-encoded,
//     so non-ASCII/stripped characters are not silently lost for clients
//     that support it.
func contentDisposition(disposition, filename string) string {
	safe := dispositionQuoteStripper.Replace(filename)
	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, disposition, safe, url.PathEscape(filename))
}

// contentDispositionInline offers the bytes for the browser to render in
// place (an artifact preview).
func contentDispositionInline(filename string) string {
	return contentDisposition("inline", filename)
}

// contentDispositionAttachment forces a Save As dialog instead -- ?download=1
// on a single artifact, and every entry's implicit "save this" intent inside
// a zip's Content-Disposition.
func contentDispositionAttachment(filename string) string {
	return contentDisposition("attachment", filename)
}

// filenameComponentReplacer maps every character that is illegal (or just
// awkward) in a filename on some common filesystem to "_". Like
// dispositionQuoteStripper, one package-level Replacer serves every request.
var filenameComponentReplacer = strings.NewReplacer(
	"/", "_", `\`, "_", ":", "_", "*", "_", "?", "_",
	`"`, "_", "<", "_", ">", "_", "|", "_",
)

// sanitizeFilenameComponent makes name safe to use as a single path segment
// in a Content-Disposition filename or a zip entry name: strips characters
// that are illegal (or just awkward -- a literal "/" would otherwise be
// misread as an extra directory level) in a filename on any common
// filesystem, then trims stray leading/trailing dots and spaces (Windows
// rejects trailing dots/spaces outright). Falls back to "artifact" rather
// than emitting an empty path segment.
func sanitizeFilenameComponent(name string) string {
	name = strings.Trim(filenameComponentReplacer.Replace(name), ". ")
	if name == "" {
		return "artifact"
	}
	return name
}

// artifactExtension picks the file extension a downloaded artifact should
// carry when it has no recorded original filename to borrow one from
// (meta.OriginalFilename -- see artifactDownloadFilename): a fixed
// extension per artType for the types this package always renders the same
// way, and a contentType-based guess for images, whose actual format
// (PNG/JPEG/GIF/BMP/WebP) varies per row -- see
// artifactcontent.validateImageBytes for the formats accepted at all.
func artifactExtension(artType domain.ArtifactType, contentType string) string {
	switch artType {
	case domain.ArtifactHTML:
		return ".html"
	case domain.ArtifactGherkin:
		return ".feature"
	case domain.ArtifactJSON:
		return ".json"
	case domain.ArtifactText:
		return ".md"
	case domain.ArtifactImage:
		switch {
		case strings.Contains(contentType, "png"):
			return ".png"
		case strings.Contains(contentType, "jpeg"), strings.Contains(contentType, "jpg"):
			return ".jpg"
		case strings.Contains(contentType, "gif"):
			return ".gif"
		case strings.Contains(contentType, "bmp"):
			return ".bmp"
		case strings.Contains(contentType, "webp"):
			return ".webp"
		default:
			return ".png"
		}
	default:
		return ".txt"
	}
}

// artifactDownloadFilename is the single place that decides the filename a
// downloaded artifact is offered as, shared by handleGetArtifactContent's
// ?download=1 and handleDownloadTicketArtifacts' zip entries so the two
// download paths can never disagree about it: the artifact's original
// upload filename when one was recorded (an html/image row created from a
// file -- see artifactcontent.Metadata), otherwise the artifact's own name
// with an extension appropriate to its type/content appended.
func artifactDownloadFilename(a *domain.Artifact, meta artifactcontent.Metadata, contentType string) string {
	if meta.OriginalFilename != "" {
		return sanitizeFilenameComponent(filepath.Base(meta.OriginalFilename))
	}
	name := sanitizeFilenameComponent(a.Name)
	ext := artifactExtension(a.Type, contentType)
	if strings.HasSuffix(strings.ToLower(name), ext) {
		return name
	}
	return name + ext
}

// handleDownloadTicketArtifacts serves every artifact currently attached to
// a ticket as a single zip -- the "download all" counterpart to
// handleGetArtifactContent's ?download=1 (one artifact at a time), so a
// user reviewing a ticket's deliverables doesn't have to click "Download"
// once per artifact.
//
// detail.Artifacts (ListArtifactsByTicket) omits `content` for html/image
// rows to keep that response small (see artifactSummaryCols), so each entry
// is re-fetched in full via GetArtifact before being written into the
// archive -- the same thing handleGetArtifactContent does for a single
// artifact. Entries are grouped one directory per node (by node name) so
// artifacts from different nodes -- or different loop-back passes reusing
// the same artifact name -- can't collide; a name collision within the same
// directory gets a numeric suffix rather than silently overwriting the
// earlier entry in the zip. An artifact whose bytes can't be produced (a
// decode error) is skipped rather than failing the whole download --
// headers are already committed by the time entries are streamed, so there
// is no way to report a per-entry failure except omitting it.
func (s *Server) handleDownloadTicketArtifacts(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	detail, err := s.repo.GetTicketDetail(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if detail == nil {
		writeError(w, http.StatusNotFound, domain.NewAPIError(domain.ErrCodeTicketNotFound, "ticket not found: %s", id))
		return
	}

	nodeNameByID := make(map[string]string, len(detail.Nodes))
	for _, n := range detail.Nodes {
		nodeNameByID[n.ID] = n.Name
	}

	zipName := sanitizeFilenameComponent(detail.ID+"-"+detail.Title) + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDispositionAttachment(zipName))
	w.WriteHeader(http.StatusOK)

	zw := zip.NewWriter(w)
	defer zw.Close()

	entryCount := make(map[string]int)
	for _, summary := range detail.Artifacts {
		full, err := s.repo.GetArtifact(summary.ID)
		if err != nil || full == nil {
			continue
		}
		meta := artifactcontent.DecodeMetadata(full.Metadata)

		if full.Content == nil || *full.Content == "" {
			continue
		}
		data, contentType, err := artifactcontent.Payload(full.Type, *full.Content, meta)
		if err != nil {
			continue
		}

		dir := sanitizeFilenameComponent(nodeNameByID[full.NodeID])
		filename := artifactDownloadFilename(full, meta, contentType)
		key := dir + "/" + filename
		n := entryCount[key]
		entryCount[key] = n + 1
		if n > 0 {
			ext := filepath.Ext(filename)
			filename = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(filename, ext), n+1, ext)
		}

		entry, err := zw.Create(dir + "/" + filename)
		if err != nil {
			continue
		}
		_, _ = entry.Write(data)
	}
}
