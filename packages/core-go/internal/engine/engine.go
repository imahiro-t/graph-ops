// Package engine implements the ticket/graph orchestration logic: building a
// ticket's execution graph from a config.Catalog (optionally patched by an
// LLM-proposed set of extra nodes), walking that graph to find executable
// nodes, and handling pass/fail transitions including iteration loop-back.
package engine

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

type GraphEngine struct {
	repo store.GraphRepository
}

func New(repo store.GraphRepository) *GraphEngine {
	return &GraphEngine{repo: repo}
}

// CreateTicket persists a new ticket under projectID (whose prefix/sequence
// counter mints the ticket's ID -- see store.GraphRepository.CreateTicket).
// A new ticket is never assigned: assignment only ever happens afterward, via
// the Web UI's "assign to me" button (see domain.Ticket.Assignee).
//
// This is a thin wrapper around CreateTicketWithPriority(..., nil) rather
// than the other way around: CreateTicket has 100+ existing call sites
// (mostly tests) that pass exactly these three arguments, so its signature
// is kept frozen and the priority-aware behavior lives in the new method
// instead (DFLT-00059).
func (e *GraphEngine) CreateTicket(projectID, title, description string) (domain.Ticket, error) {
	return e.CreateTicketWithPriority(projectID, title, description, nil)
}

// CreateTicketWithPriority is CreateTicket plus an optional priority set at
// creation time (DFLT-00059): priority == nil creates the ticket with
// domain.DefaultTicketPriority (MEDIUM, DFLT-00083), exactly like
// CreateTicket. A non-nil priority is validated
// here (not just trusted from the caller) so this method is safe to call
// directly -- e.g. from a future call site that doesn't already funnel
// through the CLI/HTTP validation this ticket's other entry points perform.
func (e *GraphEngine) CreateTicketWithPriority(projectID, title, description string, priority *domain.TicketPriority) (domain.Ticket, error) {
	return e.CreateTicketWithOptions(projectID, title, description, CreateTicketOptions{Priority: priority})
}

// CreateTicketOptions are CreateTicketWithOptions' optional settings.
type CreateTicketOptions struct {
	// Priority: nil -> domain.DefaultTicketPriority.
	Priority *domain.TicketPriority
	// LabelNames (DFLT-00084) are resolved against projectID's registered
	// labels (see resolveLabelNames); any unregistered name fails the call
	// before the ticket is created.
	LabelNames []string
	// ParentTicketID (DFLT-00142), when non-nil, makes the new ticket a
	// child of that ticket. The parent must exist (TICKET_NOT_FOUND) and be
	// in projectID (VALIDATION_ERROR); either failure creates nothing.
	ParentTicketID *string
}

// CreateTicketWithOptions is CreateTicketWithPriority plus labels given by
// name (DFLT-00084), following the same "new method, old ones delegate"
// approach DFLT-00059 took so the existing signatures stay frozen.
func (e *GraphEngine) CreateTicketWithOptions(projectID, title, description string, opts CreateTicketOptions) (domain.Ticket, error) {
	value := domain.DefaultTicketPriority
	if opts.Priority != nil {
		parsed, err := domain.ParseTicketPriority(string(*opts.Priority))
		if err != nil {
			return domain.Ticket{}, err
		}
		value = parsed
	}
	var parentID *string
	if opts.ParentTicketID != nil {
		parent, err := e.repo.GetTicket(*opts.ParentTicketID)
		if err != nil {
			return domain.Ticket{}, err
		}
		if parent == nil {
			return domain.Ticket{}, domain.NewAPIError(domain.ErrCodeTicketNotFound, "TICKET_NOT_FOUND: parent ticket %s not found", *opts.ParentTicketID)
		}
		if parent.ProjectID != projectID {
			return domain.Ticket{}, domain.NewAPIError(domain.ErrCodeValidation,
				"VALIDATION_ERROR: parent ticket %s is in project %s, not %s; a child ticket must be in its parent's project", parent.ID, parent.ProjectID, projectID)
		}
		id := parent.ID
		parentID = &id
	}
	var labels []domain.Label
	if len(opts.LabelNames) > 0 {
		ids, err := e.resolveLabelNames(projectID, opts.LabelNames)
		if err != nil {
			return domain.Ticket{}, err
		}
		for _, id := range ids {
			labels = append(labels, domain.Label{ID: id})
		}
	}
	return e.repo.CreateTicket(projectID, domain.Ticket{
		Title:          title,
		Description:    description,
		Status:         domain.TicketTODO,
		AutoExecutable: true,
		Blocked:        false,
		Priority:       value,
		Labels:         labels,
		ParentTicketID: parentID,
	})
}

// GetTicketDetailWithFamily is the repository's GetTicketDetail plus the
// ticket's parent and children (DFLT-00142) -- what get-ticket and
// GET /api/tickets/{id} return. It returns nil, nil for a missing ticket,
// like GetTicketDetail. Children come from store.ListChildTickets (one
// indexed query on SQLite/MySQL, a filtered project listing on the HTTP
// data source). A parent that can no longer be read (only possible on a
// backend without ON DELETE SET NULL) is reported as no parent rather than
// as an error.
func (e *GraphEngine) GetTicketDetailWithFamily(id string) (*domain.TicketDetailWithFamily, error) {
	detail, err := e.repo.GetTicketDetail(id)
	if err != nil || detail == nil {
		return nil, err
	}
	out := &domain.TicketDetailWithFamily{TicketDetail: *detail, Children: []domain.TicketRef{}}
	if pid := detail.ParentTicketID; pid != nil {
		parent, err := e.repo.GetTicket(*pid)
		if err != nil {
			return nil, err
		}
		if parent != nil {
			out.Parent = &domain.TicketRef{ID: parent.ID, Title: parent.Title, Status: parent.Status}
		}
	}
	children, err := store.ListChildTickets(e.repo, detail.Ticket)
	if err != nil {
		return nil, err
	}
	for _, c := range children {
		out.Children = append(out.Children, domain.TicketRef{ID: c.ID, Title: c.Title, Status: c.Status})
	}
	return out, nil
}

// resolveLabelNames maps label names to the IDs of projectID's registered
// labels (DFLT-00084), for the CLI's --label flags. Each name is trimmed and
// matched case-insensitively; repeated names collapse to one ID. If any name
// matches nothing, it returns LABEL_NOT_FOUND naming every unmatched name
// and every label the project does have, and the caller writes nothing.
// `graph-engine list-labels` (DFLT-00138) is the proper way to look the names
// up beforehand; the list in the message is a convenience for a caller that
// guessed wrong, and it points at `graph-engine create-label` when the
// project has no labels at all.
func (e *GraphEngine) resolveLabelNames(projectID string, names []string) ([]string, error) {
	registered, err := e.repo.ListLabelsByProject(projectID)
	if err != nil {
		return nil, err
	}
	var ids, missing []string
	seen := map[string]bool{}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		var match string
		for _, l := range registered {
			if strings.EqualFold(l.Name, name) {
				match = l.ID
				break
			}
		}
		if match == "" {
			missing = append(missing, fmt.Sprintf("%q", name))
			continue
		}
		if !seen[match] {
			seen[match] = true
			ids = append(ids, match)
		}
	}
	if len(missing) > 0 {
		available := make([]string, 0, len(registered))
		for _, l := range registered {
			available = append(available, fmt.Sprintf("%q", l.Name))
		}
		list := strings.Join(available, ", ")
		if list == "" {
			list = `(none; register labels with "graph-engine create-label" or in the Web UI settings)`
		}
		return nil, domain.NewAPIError(domain.ErrCodeLabelNotFound,
			"LABEL_NOT_FOUND: label(s) %s not registered in project %s; registered labels: %s",
			strings.Join(missing, ", "), projectID, list)
	}
	return ids, nil
}

// CreateLabel registers a new label in projectID for `graph-engine
// create-label` (DFLT-00138). DFLT-00084 kept label management in the Web UI
// only; DFLT-00138 lifts that for listing and creating so a ticket skill can
// add a missing label (with the user's approval) without a trip to the
// settings. Renaming and deleting stay Web UI only.
//
// A non-empty color is passed to the store untouched. With color empty, the
// project's existing labels are listed and domain.PickLabelColor chooses one
// (the first unused palette color, else the least used); a missing project
// fails right there with PROJECT_NOT_FOUND. Either way every check -- name
// normalization and length, the palette, the project's existence and the
// case-insensitive duplicate-name rule -- is the store's CreateLabel, run in
// its one transaction, so a rejected label writes nothing and all three
// backends (sqlite, MySQL, HTTP data source) behave alike without any change
// to the store interface or the HTTP data source protocol.
//
// Two concurrent color-less calls in the same project may both see a color
// as unused and pick it; that only duplicates a color, which is allowed, so
// no lock is taken. Duplicate names are still stopped by the store.
func (e *GraphEngine) CreateLabel(projectID, name, color string) (domain.Label, error) {
	if color == "" {
		existing, err := e.repo.ListLabelsByProject(projectID)
		if err != nil {
			return domain.Label{}, err
		}
		used := make([]domain.LabelColor, len(existing))
		for i, l := range existing {
			used[i] = l.Color
		}
		color = string(domain.PickLabelColor(used))
	}
	return e.repo.CreateLabel(projectID, name, color)
}

// LabelChange describes what RefineTicketWithLabels does to a ticket's
// labels (DFLT-00084), shaped like PriorityChange: the zero value
// (NoLabelChange()) leaves them untouched, SetLabelsByName replaces them
// with exactly the named set.
type LabelChange struct {
	names []string
	set   bool
}

// NoLabelChange leaves the ticket's labels untouched.
func NoLabelChange() LabelChange { return LabelChange{} }

// SetLabelsByName replaces the ticket's labels with the named ones, resolved
// against the ticket's project (see resolveLabelNames).
func SetLabelsByName(names []string) LabelChange {
	return LabelChange{names: append([]string(nil), names...), set: true}
}

// PriorityChange describes what RefineTicket should do to a ticket's
// priority, alongside its description update (DFLT-00059). The zero value
// (also NoPriorityChange()) leaves the stored priority untouched;
// SetPriority(p) sets it to p. There is no way to clear a priority
// (DFLT-00083): a ticket's priority is always one of HIGH/MEDIUM/LOW.
//
// It is a small named type with constructors rather than a bare
// *domain.TicketPriority so RefineTicket's call sites (the CLI,
// handleRefine, and several engine tests) say "no change" explicitly.
type PriorityChange struct {
	value *domain.TicketPriority
}

// NoPriorityChange leaves the ticket's stored priority untouched. It is the
// zero value of PriorityChange; this constructor exists only so call sites
// can say so explicitly.
func NoPriorityChange() PriorityChange { return PriorityChange{} }

// SetPriority sets the ticket's priority to p. Callers are expected to have
// already validated p (e.g. via domain.ParseTicketPriority) the same way
// every other write path in this codebase validates at its entry point
// before handing a typed value inward.
func SetPriority(p domain.TicketPriority) PriorityChange {
	return PriorityChange{value: &p}
}

// RefineTicket is what `refine-ticket` now does: it no longer builds any
// graph (that's process-ticket's job, see EnsureGraphStarted /
// maybeExpandGraph below). It replaces the ticket's description outright
// with the given text -- the skill is expected to have worked out the
// completion criteria and background/rationale ("why") together with the
// user and folded them into one coherent, updated description (incorporating
// whatever from the original create-ticket text still applies), not appended
// as a separate afterthought block -- and marks the ticket REFINED. An empty
// description leaves the ticket's existing description untouched; only its
// status changes to REFINED. A non-empty description also stamps
// domain.Ticket.RefinedAt with the current time, so the Web UI can show when
// the description was last overwritten by a refine.
//
// priority (DFLT-00059) independently controls the ticket's priority: see
// PriorityChange's doc comment. It is applied in the same UpdateTicket call
// as the description/status change below, so refining a ticket's
// description and adjusting its priority in the same `refine-ticket`
// invocation costs no extra DB round trip.
//
// A CLOSED ticket is rejected outright (DFLT-00043): unlike syncTicketStatus,
// which only runs as a side effect of node completion, RefineTicket writes
// ticket.Status directly and unconditionally, so without this check it would
// be a silent backdoor out of CLOSED that bypasses ReopenTicket entirely.
func (e *GraphEngine) RefineTicket(ticketID string, description string, priority PriorityChange) (*domain.Ticket, error) {
	return e.RefineTicketWithLabels(ticketID, description, priority, NoLabelChange())
}

// RefineTicketWithLabels is RefineTicket plus a label change (DFLT-00084).
// The CLOSED check runs first, then label names are resolved; an unresolved
// name fails before anything is written, so the description, priority,
// status and refined_at all stay as they were. Otherwise the labels are
// replaced in the same UpdateTicket call as everything else.
func (e *GraphEngine) RefineTicketWithLabels(ticketID string, description string, priority PriorityChange, labels LabelChange) (*domain.Ticket, error) {
	ticket, err := e.repo.GetTicket(ticketID)
	if err != nil {
		return nil, err
	}
	if ticket == nil {
		return nil, fmt.Errorf("ticket %s not found", ticketID)
	}
	if ticket.Status == domain.TicketClosed {
		return nil, fmt.Errorf("ticket %s is CLOSED; reopen it first with reopen-ticket", ticketID)
	}

	patch := store.TicketPatch{}
	if labels.set {
		ids, err := e.resolveLabelNames(ticket.ProjectID, labels.names)
		if err != nil {
			return nil, err
		}
		if ids == nil {
			ids = []string{}
		}
		patch.LabelIDs = &ids
	}

	newDescription := ticket.Description
	if description != "" {
		newDescription = description
		now := time.Now().UTC().Format(time.RFC3339Nano)
		patch.RefinedAt = &now
	}
	refined := domain.TicketRefined
	patch.Description = &newDescription
	patch.Status = &refined
	patch.Priority = priority.value
	updated, err := e.repo.UpdateTicket(ticketID, patch)
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// persistPlan creates DB rows for whichever of planned's nodes aren't already
// represented in dbIDByConfigID (keyed by plannedNode.ConfigID), then wires
// up their depends_on/loop_back_to edges. dbIDByConfigID is both read and
// extended in place, so callers can seed it with already-existing nodes
// (e.g. the seed nodes when expanding the graph in maybeExpandGraph) to
// attach new nodes to them without recreating or duplicating anything.
func (e *GraphEngine) persistPlan(ticketID string, planned []plannedNode, dbIDByConfigID map[string]string) error {
	preExisting := make(map[string]bool, len(dbIDByConfigID))
	for id := range dbIDByConfigID {
		preExisting[id] = true
	}

	for _, p := range planned {
		if preExisting[p.ConfigID] {
			continue
		}
		configID := p.ConfigID
		created, err := e.repo.CreateNode(domain.GraphNode{
			TicketID:      ticketID,
			Name:          p.Name,
			Type:          domain.NodeType(p.Type),
			Status:        domain.NodeTODO,
			MaxIterations: p.MaxIterations,
			IsManual:      p.IsManual,
			GateID:        p.GateID,
			Criteria:      p.Criteria,
			ConfigID:      &configID,
		})
		if err != nil {
			return fmt.Errorf("creating node %s: %w", p.ConfigID, err)
		}
		dbIDByConfigID[p.ConfigID] = created.ID
	}

	for _, p := range planned {
		if preExisting[p.ConfigID] {
			continue // this node's edges were already created in an earlier phase
		}
		toID := dbIDByConfigID[p.ConfigID]
		for _, dep := range p.DependsOn {
			fromID, ok := dbIDByConfigID[dep]
			if !ok {
				return fmt.Errorf("node %q depends_on %q which is outside this plan", p.ConfigID, dep)
			}
			if _, err := e.repo.CreateEdge(domain.GraphEdge{
				ID: newEdgeID(), TicketID: ticketID, FromNodeID: fromID, ToNodeID: toID, Condition: domain.EdgeSuccess,
			}); err != nil {
				return err
			}
		}
		if p.LoopBackTo != "" {
			targetID, ok := dbIDByConfigID[p.LoopBackTo]
			if !ok {
				return fmt.Errorf("node %q loop_back_to %q which is outside this plan", p.ConfigID, p.LoopBackTo)
			}
			if _, err := e.repo.CreateEdge(domain.GraphEdge{
				ID: newEdgeID(), TicketID: ticketID, FromNodeID: toID, ToNodeID: targetID, Condition: domain.EdgeLoop,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// seedIDSet indexes catalog.Seed for membership tests. Both graph-building
// entry points need it: EnsureGraphStarted to pick the seed subset out of the
// catalog, ExpandGraph to tell already-persisted seed nodes apart from
// evidence the graph was expanded before.
func seedIDSet(catalog config.Catalog) map[string]bool {
	set := make(map[string]bool, len(catalog.Seed))
	for _, id := range catalog.Seed {
		set[id] = true
	}
	return set
}

// blockTicket flags a ticket as blocked, stopping automatic execution until a
// human (or process-ticket's triage, via ReopenNodes) intervenes. It also
// resyncs the ticket's status: both of CompleteNode's callers of blockTicket
// (an approval_gate rejection, or an iteration_loop exceeding max_iterations)
// otherwise leave the ticket's status column holding whatever
// syncTicketStatus last computed -- stale the moment the node they just
// touched (now REJECTED or AWAITING FIX) would change that computation, e.g.
// a rejected approval_gate must drop out of IN REVIEW into IN PROGRESS
// (DFLT-00046).
func (e *GraphEngine) blockTicket(ticketID string) error {
	blocked := true
	if _, err := e.repo.UpdateTicket(ticketID, store.TicketPatch{Blocked: &blocked}); err != nil {
		return err
	}
	return e.syncTicketStatus(ticketID)
}

// EnsureGraphStarted creates the catalog's seed nodes (catalog.Seed, e.g.
// "plan" + "plan_review") the first time process-ticket asks for a ticket's
// executable nodes. It is a no-op once any node exists for the ticket, so
// callers can call it unconditionally on every GetExecutableNodes call.
func (e *GraphEngine) EnsureGraphStarted(ticketID string, catalog config.Catalog) error {
	ticket, err := e.repo.GetTicket(ticketID)
	if err != nil {
		return err
	}
	if ticket == nil {
		return fmt.Errorf("ticket %s not found", ticketID)
	}

	existing, err := e.repo.ListNodesByTicket(ticketID)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}

	seedSet := seedIDSet(catalog)
	var seedDefs []config.NodeDef
	for _, n := range catalog.EnabledNodes() {
		if seedSet[n.ID] {
			seedDefs = append(seedDefs, n)
		}
	}
	if len(seedDefs) == 0 {
		return fmt.Errorf("workflow catalog's seed list %v matches no enabled node", catalog.Seed)
	}

	planned, err := buildPlanFromNodeDefs(seedDefs, catalog.EnabledReviewGates(), nil, nil, catalog.MaxIterations)
	if err != nil {
		return fmt.Errorf("invalid seed plan: %w", err)
	}
	return e.persistPlan(ticketID, planned, map[string]string{})
}

// ExpandGraph builds the rest of a ticket's graph (everything beyond the
// plan/plan_review seed) and attaches it to the existing seed nodes. It is
// an explicit, skill-invoked step -- deciding what belongs in the graph
// (does this ticket need an implementation + review cluster? a Gherkin
// testing cluster? is it investigation-only?) requires understanding the
// ticket's content, which only the LLM driving process-ticket can judge.
//
// When patch is nil, it falls back to the catalog's full default template
// (buildPlan with no patch) -- the standard implementation+Gherkin flow.
// When patch is given, it is used EXCLUSIVELY: the catalog's own `nodes`
// list is ignored entirely, and the patch's ExtraNodes must describe the
// complete non-seed graph (including a `release` node wired to whatever
// ends up last). Review gates are still resolved from the catalog via
// gate_ref, so a patch can reuse e.g. "code_review"'s criteria without
// re-specifying it.
func (e *GraphEngine) ExpandGraph(ticketID string, catalog config.Catalog, patch *Patch) error {
	nodes, err := e.repo.ListNodesByTicket(ticketID)
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		return fmt.Errorf("ticket %s has no nodes yet; call get-executable first to seed the graph", ticketID)
	}

	seedSet := seedIDSet(catalog)
	if len(seedSet) == 0 {
		return fmt.Errorf("workflow catalog has an empty seed list")
	}

	dbIDByConfigID := make(map[string]string, len(nodes))
	seedDone := 0
	for _, n := range nodes {
		if n.ConfigID == nil {
			continue
		}
		dbIDByConfigID[*n.ConfigID] = n.ID
		if seedSet[*n.ConfigID] {
			if n.Status == domain.NodeDone {
				seedDone++
			}
		} else {
			return fmt.Errorf("ticket %s's graph has already been expanded", ticketID)
		}
	}
	if seedDone < len(seedSet) {
		return fmt.Errorf("not all seed nodes are DONE yet (%d/%d)", seedDone, len(seedSet))
	}

	var planned []plannedNode
	if patch != nil {
		planned, err = buildPlanFromNodeDefs(nil, catalog.EnabledReviewGates(), patch, seedSet, catalog.MaxIterations)
	} else {
		planned, err = buildPlan(catalog, nil)
	}
	if err != nil {
		return fmt.Errorf("invalid expansion plan: %w", err)
	}
	if err := e.persistPlan(ticketID, planned, dbIDByConfigID); err != nil {
		return err
	}
	// Stamp GraphExpandedAt the moment expansion succeeds (same
	// nullable-timestamp pattern as RefinedAt, see domain.Ticket's doc
	// comment) -- the seed/non-seed distinction deriveTicketStatus needs to
	// tell "only the plan/plan_review seed is DONE" apart from "the expanded
	// graph is DONE" (DFLT-00046), without handing catalog through to every
	// method that might need it.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := e.repo.UpdateTicket(ticketID, store.TicketPatch{GraphExpandedAt: &now}); err != nil {
		return err
	}
	// The ticket's status column still holds whatever syncTicketStatus last
	// computed for the seed alone (typically DONE, since expansion only ever
	// runs once every seed node is DONE) -- recompute it now that fresh TODO
	// nodes exist, rather than leaving it stale until some later
	// GetExecutableNodes/CompleteNode call happens to resync it.
	return e.syncTicketStatus(ticketID)
}

// allNonLoopPrereqsDone reports whether every non-iteration_loop edge feeding
// into nodeID comes from a DONE node -- i.e. whether nodeID has been
// "reached" by the graph's forward execution. This is the prerequisite check
// GetExecutableNodes uses to decide what's executable, factored out so
// deriveTicketStatus can reuse the exact same definition of "reached" to spot
// a pending approval_gate (DFLT-00046) -- matching the Web UI's own
// isNodeReached (packages/web/src/components/TicketItem.tsx), which
// implements this same check independently for the "pending approval"
// blink.
func allNonLoopPrereqsDone(nodeID string, byID map[string]domain.GraphNode, edges []domain.GraphEdge) bool {
	for _, edge := range edges {
		if edge.ToNodeID != nodeID || edge.Condition == domain.EdgeLoop {
			continue
		}
		from, ok := byID[edge.FromNodeID]
		if !ok || from.Status != domain.NodeDone {
			return false
		}
	}
	return true
}

// claimableExclusions lists the statuses a node must NOT be in to be claimed:
// DONE has already produced its result, and IN PROGRESS / IN REVIEW mean
// somebody is working on it right now. Everything else (TODO, AWAITING FIX,
// REJECTED) is fair game -- unchanged from before the claim became a CAS, and
// deliberately so: GetExecutableNodes passes this same list as the CAS's
// excluded set, so the pre-filter it drives and the condition the database
// enforces are one definition rather than two that can drift apart.
var claimableExclusions = []domain.NodeStatus{domain.NodeDone, domain.NodeInProgress, domain.NodeInReview}

func isClaimed(status domain.NodeStatus) bool {
	for _, s := range claimableExclusions {
		if status == s {
			return true
		}
	}
	return false
}

// GetExecutableNodes returns the nodes whose non-loop prerequisites are all
// DONE, excluding manual nodes (which require human action) and nodes that
// are already in progress, in review, or done. Both TODO and AWAITING FIX
// nodes (a reviewer that looped back and is waiting on its target's rework --
// see domain.NodeAwaitingFix) are eligible; the exclusion below is a deny-
// list, so no extra condition is needed for the latter. It seeds the ticket's
// graph on first call (see EnsureGraphStarted) if it doesn't exist yet.
//
// Despite the read-only-sounding name, this method writes: each node it hands
// back is first flipped from TODO (or AWAITING FIX) to IN PROGRESS (or IN
// REVIEW for `review`/`review_gate` nodes) before being returned. Without
// this, a node sat at TODO for its entire execution and only ever jumped
// straight to DONE (DFLT-00009) -- both the node's own status and,
// transitively, the ticket's IN PROGRESS status (which deriveTicketStatus
// derives from node statuses) never appeared. Writing the status here, at the
// point a node is actually handed out for execution, also closes a
// duplicate-dispatch hole: the status-based exclusion above
// (NodeDone/NodeInProgress/NodeInReview) only works once a claimed node's
// status reflects that it's been claimed, so a second GetExecutableNodes call
// while the first claim is still running no longer hands out the same node
// again.
//
// Note that a `review`/`review_gate` node claimed IN REVIEW here does not put
// the ticket itself into TicketInReview -- deriveTicketStatus reserves that
// for a pending human approval_gate (DFLT-00046); a ticket with a
// review/review_gate node running stays IN PROGRESS.
//
// It either hands a claimed node back or releases it (DFLT-00136). If the
// call fails after claiming -- a later node's ClaimNode or the closing
// syncTicketStatus erroring -- it returns the nodes it claimed to their
// pre-claim status before returning the error, so no node is left looking
// "in progress" with nobody running it; the caller can simply retry. Only if
// a release itself fails does the error name nodes left claimed, for
// unstick-node. See releaseClaims.
func (e *GraphEngine) GetExecutableNodes(ticketID string, catalog config.Catalog) ([]domain.GraphNode, error) {
	// Checked before EnsureGraphStarted (which seeds the graph on first
	// call): a CLOSED ticket must never gain nodes just because something
	// polled it, and must never be handed nodes to execute (DFLT-00043).
	ticket, err := e.repo.GetTicket(ticketID)
	if err != nil {
		return nil, err
	}
	if ticket == nil {
		return nil, fmt.Errorf("ticket %s not found", ticketID)
	}
	if ticket.Status == domain.TicketClosed {
		return []domain.GraphNode{}, nil
	}

	if err := e.EnsureGraphStarted(ticketID, catalog); err != nil {
		return nil, err
	}

	detail, err := e.repo.GetTicketDetail(ticketID)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, fmt.Errorf("ticket %s not found", ticketID)
	}
	if detail.Blocked || !detail.AutoExecutable {
		return []domain.GraphNode{}, nil
	}

	byID := make(map[string]domain.GraphNode, len(detail.Nodes))
	for _, n := range detail.Nodes {
		byID[n.ID] = n
	}

	executable := []domain.GraphNode{}
	// claims mirrors executable with what releaseClaims needs to undo it.
	var claims []nodeClaim
	for _, n := range detail.Nodes {
		if isClaimed(n.Status) {
			continue
		}
		if n.IsManual {
			continue
		}
		if !allNonLoopPrereqsDone(n.ID, byID, detail.Edges) {
			continue
		}

		claimedStatus := domain.NodeInProgress
		if n.Type == domain.NodeTypeReview || n.Type == domain.NodeTypeReviewGate {
			claimedStatus = domain.NodeInReview
		}
		// ClaimNode, not UpdateNode: the status check above and the write
		// that acts on it have to be one step. The statuses read into
		// `detail` are a snapshot, and process-ticket runs several
		// subagents that call this method at the same moment -- with a
		// read here and a write there, two of them could both see the
		// same node at TODO and both be handed it (CHK-01). The excluded
		// set is exactly the skip condition above, so the range of
		// claimable statuses is unchanged.
		claimed, err := e.repo.ClaimNode(n.ID, claimedStatus, claimableExclusions)
		if err != nil {
			// This node itself is not released: the failure may have come
			// after the write (claimNodeCAS reads the row back), but it may
			// equally have come before it while somebody else claimed the
			// node, and releasing that claim would hand their node out a
			// second time. Its ID goes in the message instead, so a human
			// can check it with get-ticket.
			cause := fmt.Errorf("claiming node %s: %w (if it now shows IN PROGRESS/IN REVIEW and no worker was handed it, run unstick-node on it)", n.ID, err)
			return nil, e.releaseClaims(claims, cause)
		}
		// Somebody else got there first (or the node has since been
		// deleted). Leave it out of this call's result and carry on --
		// losing a race is how parallel execution is supposed to look,
		// not an error to report to the caller.
		if claimed == nil {
			continue
		}
		claims = append(claims, nodeClaim{id: n.ID, from: n.Status, to: claimedStatus})
		executable = append(executable, *claimed)
	}

	// Always resync, not just when something was actually claimed: a manual
	// node (approval_gate, or any is_manual custom type) never appears in
	// `executable` above, so a ticket that becomes newly blocked on one --
	// e.g. right after ExpandGraph adds it as the only next step, with no
	// other node left to claim -- would otherwise keep whatever stale status
	// (often DONE, from when only the seed existed) syncTicketStatus last
	// computed, silently hiding a ticket that's actually waiting on a human.
	//
	// A failure here hands back no nodes, so the claims above are released
	// first: returned or released, never left claimed with no worker. The
	// ticket row needs no compensating write -- the failed UpdateTicket is
	// the only write syncTicketStatus makes, and with the claims undone the
	// derived status is back to what it was before this call.
	if err := e.syncTicketStatus(ticketID); err != nil {
		return nil, e.releaseClaims(claims, fmt.Errorf("syncing ticket %s status: %w", ticketID, err))
	}
	return executable, nil
}

// nodeClaim records one claim GetExecutableNodes made, so it can be undone if
// the call fails before handing the node back: the node's status before the
// claim (from the detail snapshot) and the status the claim set.
type nodeClaim struct {
	id   string
	from domain.NodeStatus
	to   domain.NodeStatus
}

// releaseClaims is GetExecutableNodes' compensation (DFLT-00136): it returns
// every node in claims to the status it had before this call claimed it, then
// returns cause -- augmented, if any release failed, with the IDs that could
// not be released so they can be fixed with unstick-node. The result always
// wraps cause, so errors.Is/As still reach the original failure.
//
// A release is itself a ClaimNode CAS whose excluded set is every status
// except the one this call claimed the node to. It therefore only moves a
// node that still sits at exactly that status; a node somebody has since
// moved on -- completed to DONE, rewound to TODO by a loop-back -- fails the
// condition and is left alone ((nil, nil), which counts as released). The
// CAS's contract, newStatus in excluded, holds because a claimable status
// (TODO, AWAITING FIX, REJECTED) is never a claimed one (IN PROGRESS,
// IN REVIEW).
//
// The check is on status alone, so it has an ABA window: were the node
// rewound and then claimed again by another process to the same status in
// the milliseconds before the release, the release would undo that claim
// too. The cost is the node being handed out once more -- the same kind of
// cost DFLT-00119 accepted -- and far less than a node stuck claimed. On the
// HTTP backend ClaimNode is a GET then a PATCH, and the release inherits that
// same non-atomicity.
//
// A failed release does not stop the others: every node that can be
// released is.
func (e *GraphEngine) releaseClaims(claims []nodeClaim, cause error) error {
	var stuck []string
	var releaseErrs []string
	for _, c := range claims {
		excluded := make([]domain.NodeStatus, 0, len(domain.AllNodeStatuses()))
		for _, s := range domain.AllNodeStatuses() {
			if s != c.to {
				excluded = append(excluded, s)
			}
		}
		if _, err := e.repo.ClaimNode(c.id, c.from, excluded); err != nil {
			stuck = append(stuck, c.id)
			releaseErrs = append(releaseErrs, fmt.Sprintf("%s: %v", c.id, err))
		}
	}
	if len(stuck) == 0 {
		return cause
	}
	return fmt.Errorf("%w; additionally failed to release claimed node(s) %s -- they remain IN PROGRESS/IN REVIEW with no worker; run unstick-node on each: %s",
		cause, strings.Join(stuck, ", "), strings.Join(releaseErrs, "; "))
}

// reachableVia walks adjacency breadth-first from `from` and returns every id
// it can reach, `from` itself included.
func reachableVia(from string, adjacency map[string][]string) map[string]bool {
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, next := range adjacency[id] {
			if seen[next] {
				continue
			}
			seen[next] = true
			queue = append(queue, next)
		}
	}
	return seen
}

// successorsBySuccessEdge builds the `success`-edge adjacency list the rewind
// and its "is the failing node downstream of the target" question both walk.
// iteration_loop edges are deliberately left out: they point backward, and
// following them would make every loop target reachable from its own
// reviewers.
func successorsBySuccessEdge(edges []domain.GraphEdge) map[string][]string {
	successors := make(map[string][]string, len(edges))
	for _, edge := range edges {
		if edge.Condition != domain.EdgeSuccess {
			continue
		}
		successors[edge.FromNodeID] = append(successors[edge.FromNodeID], edge.ToNodeID)
	}
	return successors
}

// loopBackRewindSet returns the ids of the nodes a loop-back must rewind to
// TODO alongside its target: everything reachable from the loop target by
// `success` edges -- its full forward closure, the same rule ReopenNodes
// applies -- that is not already TODO, with the target and the failing node
// themselves excluded, since CompleteNode writes each of those itself (TODO
// with, usually, a bumped iteration count, and AWAITING FIX, respectively).
//
// Why the whole forward closure. Once the target is going to be redone, every
// result downstream of it was produced against output that no longer exists.
// DFLT-00101 narrowed this to the nodes on a path from the target to the
// failing node, which left two holes that DFLT-00119 closes:
//
//   - Sibling gates kept their verdicts. The default workflow hangs four
//     review_gates off `impl` in parallel, each looping back to `impl`. A
//     `qa_review` failure does not reach `code_review` by success edges, so
//     `code_review` stayed DONE -- and the rewritten implementation then
//     sailed past a gate that had never seen it. Observed in DFLT-00100,
//     where the ticket's most consequential change reached release approval
//     without ever passing code review. The same applied to `gherkin_test`
//     whenever the node that failed was a sibling rather than `test_review`.
//   - Work past the failing node kept its DONE status. It is rarer (nothing
//     downstream of a reviewer that just rejected is usually DONE yet), but
//     nothing prevented it -- a parallel branch can finish while another
//     rejects -- and it was judged against the same superseded output.
//
// Nodes that are not TODO are rewound whatever state they are in, including
// IN PROGRESS and IN REVIEW. DFLT-00101 excluded those to avoid racing their
// worker; DFLT-00119 reverses that, because the race is the lesser problem:
// such a worker is judging the output the target is about to replace, so its
// verdict must not be recorded. checkCompletable is what actually discards it
// -- an automatic node rewound to TODO refuses the late CompleteNode call
// outright, writing nothing (see there). The cost is that the rewound node is
// handed out again by the next GetExecutableNodes while the old worker may
// still be running, so the same gate can briefly be worked twice; that is
// accepted (DFLT-00119 completion criterion 2) rather than prevented, and
// UnstickNode is no longer needed to free a claim a rewind swept up.
//
// TODO nodes are left out only because they are already where a rewind would
// put them; the rule is "reset everything in the closure", not "reset the
// completed ones".
//
// A DONE approval_gate in the closure *is* rewound, despite being a manual
// node: its approval was given for output the target is about to redo, so
// leaving it DONE would wave the reworked result past a stale human decision.
// The deliberate consequence is that such a ticket stops for a fresh approval
// after the rework (GetExecutableNodes never hands out manual nodes).
//
// Rewound nodes keep their own iteration_count -- only the loop target's
// moves. See CompleteNode's loop-back branch for why, and for when even the
// target's does not move.
//
// Ids come back sorted, purely so the resulting writes are in a deterministic
// order rather than Go's randomized map order.
func loopBackRewindSet(targetID, failedNodeID string, byID map[string]domain.GraphNode, edges []domain.GraphEdge) []string {
	downstreamOfTarget := reachableVia(targetID, successorsBySuccessEdge(edges))

	ids := make([]string, 0, len(downstreamOfTarget))
	for id := range downstreamOfTarget {
		if id == targetID || id == failedNodeID {
			continue
		}
		if n, ok := byID[id]; ok && n.Status != domain.NodeTODO {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// loopBackRewindsFailedNode reports whether the node that just failed sits in
// the loop target's `success`-edge forward closure -- i.e. whether the very
// loop-back it is triggering would have rewound it, had CompleteNode not
// marked it AWAITING FIX instead.
//
// This is what tells a "vertical" loop-back (the default workflow's gates and
// reviews, all downstream of `impl`) apart from a "sideways" one (a
// loop_back_to pointing at a node on an unrelated branch). CompleteNode needs
// the distinction to decide whether a failure counts as a fresh iteration of
// the target -- see there.
func loopBackRewindsFailedNode(targetID, failedNodeID string, edges []domain.GraphEdge) bool {
	if targetID == failedNodeID {
		return false
	}
	return reachableVia(targetID, successorsBySuccessEdge(edges))[failedNodeID]
}

// checkCompletable decides whether a node may be completed at all, from its
// own status and its ticket's. It returns an INVALID_NODE_STATE *domain.APIError
// when it may not, and is called by CompleteNode before that method writes
// anything (DFLT-00102 / BUG-04).
//
// Until this existed, CompleteNode never looked at the node it was closing.
// A DONE node could be completed a second time with passed=false, blocking a
// ticket whose work was finished; a TODO node nobody had run could be marked
// DONE; and either way the artifacts of that bogus call were already on the
// node by the time anyone noticed. The record then disagreed with what had
// actually happened, with nothing left to show where it went wrong. The rule
// was previously carried by a warning in process-ticket's SKILL.md ("don't
// call complete-node if the Web UI already handled it"), which is not a rule
// at all -- anyone completing a node by hand, or two agents racing, walked
// straight past it.
//
// What may complete:
//
//   - an automatic node (is_manual=false) that is IN PROGRESS or IN REVIEW,
//     i.e. one get-executable actually handed out;
//   - a manual node (approval_gate, release, any is_manual custom type) in
//     those two statuses *or* still at TODO. Manual nodes are never claimed --
//     GetExecutableNodes skips them and a human judges them where they sit --
//     so TODO is their normal state at the moment of approval, and the Web UI
//     offers approve/reject on exactly the TODO approval_gates. Holding them
//     to the automatic rule would break the approval flow outright.
//
// Everything else is refused: DONE, REJECTED and AWAITING FIX have all had
// their turn, TODO on an automatic node was never claimed, and an unknown
// status is refused rather than guessed at. A CLOSED ticket refuses
// regardless of node status -- a closed ticket takes no more work (the same
// line GetExecutableNodes and syncTicketStatus already hold).
//
// "TODO on an automatic node was never claimed" carries a second meaning
// since DFLT-00119, and the message for that case says so: a loop-back now
// rewinds the loop target's whole forward closure, IN PROGRESS/IN REVIEW
// nodes included (see loopBackRewindSet), so a gate that was mid-judgment
// when a sibling gate sent the work back is put straight to TODO. Its
// subagent finishes and calls CompleteNode on output that has since been
// superseded, and this refusal is what keeps that verdict off the record --
// the whole point of rewinding it. Callers (process-ticket) must treat that
// refusal as routine: go back to get-executable and redo the node when it is
// handed out again, rather than reporting a failure. What is refused is the
// verdict, not the write-up behind it: agents save that with add-artifact
// before calling here, and neither that path nor this one looks at the node's
// status, so a superseded review stays on the node beside whatever the re-run
// produces (artifacts are only ever appended; the most recent one is the
// current verdict). Nothing records the refused call as a verdict, so a gate
// re-run after a rewind legitimately raises the same findings again.
//
// Two things it deliberately does not check: the ticket's `blocked` flag and
// whether the node's prerequisites are DONE. Parallel branches make both
// wrong -- one branch's gate rejection blocks the ticket while a sibling node
// is still legitimately running, and refusing that sibling's completion would
// throw away work that was correctly done and was already in flight when the
// block landed.
func checkCompletable(node *domain.GraphNode, detail *domain.TicketDetail) error {
	const recovery = "use get-executable to hand out an automatic node still at TODO, unstick-node to release a node stuck at IN PROGRESS/IN REVIEW, or reopen-nodes to redo a completed one"

	if detail.Status == domain.TicketClosed {
		return domain.NewAPIError(domain.ErrCodeInvalidNodeState,
			"node %s belongs to ticket %s, which is CLOSED; a closed ticket's nodes cannot be completed", node.ID, detail.ID)
	}
	if node.Status == domain.NodeInProgress || node.Status == domain.NodeInReview {
		return nil
	}
	if node.IsManual && node.Status == domain.NodeTODO {
		return nil
	}
	if node.Status == domain.NodeTODO {
		return domain.NewAPIError(domain.ErrCodeInvalidNodeState,
			"node %s is TODO, not IN PROGRESS or IN REVIEW; only a node in one of those states can be completed. "+
				"An automatic node is at TODO either because get-executable never handed it out, or because a loop-back rewound it "+
				"while this run was still working -- in that case the output this verdict judged has already been redone, so the "+
				"verdict is deliberately not recorded. Go back to get-executable and redo the node when it is handed out again (%s)",
			node.ID, recovery)
	}
	allowed := "IN PROGRESS or IN REVIEW"
	if node.IsManual {
		allowed = "TODO, IN PROGRESS or IN REVIEW"
	}
	return domain.NewAPIError(domain.ErrCodeInvalidNodeState,
		"node %s is %s, not %s; only a node in one of those states can be completed (%s)", node.ID, node.Status, allowed, recovery)
}

// CompleteNodeResult mirrors the TS engine's { nextStatus, loopedBack } shape.
type CompleteNodeResult struct {
	NextStatus string `json:"nextStatus"`
	LoopedBack bool   `json:"loopedBack"`
}

// CompleteNode records a node's pass/fail outcome (plus any produced
// artifacts) and advances the graph: on pass the node is marked DONE and the
// ticket status is resynced; on fail it walks the node's iteration_loop edge
// back to the loop target, resetting that target to TODO, resetting the
// target's whole `success`-edge forward closure to TODO as well (without
// touching their iteration counts -- see loopBackRewindSet for which nodes
// those are and why), and marking the failing node itself AWAITING FIX
// (NextStatus "AWAITING FIX", DFLT-00042), or blocks the ticket once the
// review round limit is reached -- a failure in round max_iterations of the
// loop target (round = iteration_count + 1) blocks instead of looping back,
// so a limit of N allows at most N review rounds -- or if there's no loop
// edge to take at all. The
// one exception is NodeTypeApprovalGate: a "reject" there (passed=false)
// always blocks the ticket immediately, regardless of any iteration_loop edge
// -- see the dedicated branch below.
//
// The loop target's iteration_count is bumped once per round of rework, not
// once per failure: several review gates hanging off the same target and
// rejecting for the same defect spend one iteration between them
// (DFLT-00119). The loop-back branch below spells out how a round is
// identified and why a sideways loop_back_to is counted differently.
//
// The node's current status is checked first, by checkCompletable, and a node
// that may not be completed from where it stands is refused with an
// INVALID_NODE_STATE *domain.APIError before this method writes anything at
// all -- artifacts included (DFLT-00102 / BUG-04). See checkCompletable for
// which statuses pass and why manual nodes are allowed to complete from TODO.
//
// artifacts is persisted as-is via e.repo.CreateArtifact, with no validation
// of its own -- this package is deliberately DB-/HTTP-independent pure graph
// logic (see the package doc comment) and has none of the machinery
// (ArtifactsDir sandboxing, report-template checks, image magic-byte
// validation, ...) that validation needs. Security review art-cdbe6a11 (6th
// security review, DFLT-00006) found that httpserver's handleCompleteNode
// used to hand its request body's artifacts straight through to here
// unvalidated, which this method then persisted unchecked -- a full
// stored-XSS bypass (forged image content + metadata.mime_type). The fix
// lives entirely on the caller's side: httpserver.Server.handleCompleteNode
// now routes every element of artifacts through the same
// prepareArtifactForCreate choke point handleCreateArtifact uses before
// ever calling this method. Every caller of CompleteNode must do the same --
// this method has no way to enforce that itself, since it doesn't know
// (and shouldn't need to know) what a "valid" artifact looks like.
func (e *GraphEngine) CompleteNode(nodeID string, passed bool, artifacts []domain.Artifact) (CompleteNodeResult, error) {
	node, err := e.repo.GetNode(nodeID)
	if err != nil {
		return CompleteNodeResult{}, err
	}
	if node == nil {
		return CompleteNodeResult{}, domain.NewAPIError(domain.ErrCodeNodeNotFound, "node %s not found", nodeID)
	}
	detail, err := e.repo.GetTicketDetail(node.TicketID)
	if err != nil {
		return CompleteNodeResult{}, err
	}
	if detail == nil {
		return CompleteNodeResult{}, fmt.Errorf("ticket %s not found", node.TicketID)
	}
	// Before the artifact loop below, not after it: a rejected completion
	// has to leave the graph exactly as it found it, and this check used to
	// not exist at all, so a call that should never have been accepted still
	// wrote its artifacts onto the node (BUG-04). Everything above this point
	// is a read.
	if err := checkCompletable(node, detail); err != nil {
		return CompleteNodeResult{}, err
	}

	for _, art := range artifacts {
		art.ID = newArtifactID()
		art.TicketID = node.TicketID
		art.NodeID = node.ID
		if _, err := e.repo.CreateArtifact(art); err != nil {
			return CompleteNodeResult{}, err
		}
	}

	if passed {
		done := domain.NodeDone
		if _, err := e.repo.UpdateNode(node.ID, store.NodePatch{Status: &done}); err != nil {
			return CompleteNodeResult{}, err
		}
		if err := e.syncTicketStatus(node.TicketID); err != nil {
			return CompleteNodeResult{}, err
		}
		return CompleteNodeResult{NextStatus: "DONE", LoopedBack: false}, nil
	}

	// approval_gate's rejection contract is unconditional: a human "reject"
	// must always stop the whole ticket, never loop back for another
	// automatic attempt (there is nothing automatic to retry -- the only way
	// forward is another human decision). Handling this before the
	// loop-edge search below means a workflow/patch author accidentally
	// wiring an iteration_loop edge out of an approval_gate node (as they
	// would for a review/review_gate) can never silently turn a rejection
	// into a retry loop.
	//
	// The node itself is marked NodeRejected (not left at NodeTODO) so a
	// gate nobody has judged yet and one that was explicitly rejected and is
	// now waiting on further triage are distinguishable from status alone
	// (DFLT-00016) -- callers are expected to have already persisted the
	// free-text rejection reason as a "rejection_reason" text artifact via
	// the `artifacts` parameter above (see cmdCompleteNode's --reason flag
	// and handleCompleteNode's inline `artifacts` array), so ReopenNodes'
	// caller (process-ticket) can read it back off this node. NextStatus
	// "REJECTED" (previously "BLOCKED") is a deliberate breaking change to
	// this method's result contract for approval_gate rejections only --
	// see plan art-5f8847a4 section 2.3(b); TicketItem.tsx does not branch
	// on this string (confirmed during planning), so the only callers
	// affected are this package's own tests.
	if node.Type == domain.NodeTypeApprovalGate {
		rejected := domain.NodeRejected
		if _, err := e.repo.UpdateNode(node.ID, store.NodePatch{Status: &rejected}); err != nil {
			return CompleteNodeResult{}, err
		}
		if err := e.blockTicket(node.TicketID); err != nil {
			return CompleteNodeResult{}, err
		}
		return CompleteNodeResult{NextStatus: "REJECTED", LoopedBack: false}, nil
	}

	for _, edge := range detail.Edges {
		if edge.FromNodeID != node.ID || edge.Condition != domain.EdgeLoop {
			continue
		}
		target, err := e.repo.GetNode(edge.ToNodeID)
		if err != nil {
			return CompleteNodeResult{}, err
		}
		if target == nil {
			continue
		}
		// Does this failure start a new round of rework, or is it one more
		// verdict inside a round some other node has already sent back?
		// (DFLT-00119 completion criterion 3.) With four review gates hanging
		// off `impl` in parallel, one defect used to cost four iterations --
		// the default budget of 3 meant such a ticket could block before the
		// implementation had been redone even once (observed in DFLT-00103).
		//
		// A round is "already counted" when the target is sitting at TODO
		// *and* this failing node is one the rewind of that round swept up:
		// it cannot have run again since, because it is downstream of a
		// target that has not been redone, so its verdict belongs to the
		// round already on the counter.
		//
		// The second half of that condition is what keeps the iteration
		// ceiling a ceiling. A sideways loop_back_to -- a failing node that
		// is NOT in the target's forward closure -- can be handed out and
		// fail again and again without the target ever running (its own
		// prerequisites are unrelated to the target, so allNonLoopPrereqsDone
		// keeps saying yes). Were those failures free, that loop would never
		// reach max_iterations and never block. Each one therefore counts,
		// exactly as it did before DFLT-00119.
		rewindsFailedNode := loopBackRewindsFailedNode(target.ID, node.ID, detail.Edges)
		newIteration := !(rewindsFailedNode && target.Status == domain.NodeTODO)

		// The budget is only spent, and so only checked, by a failure that
		// opens a new round. Checking it on a failure that counts nothing is
		// how the parallel gates used to block a ticket with the second
		// verdict of the first round.
		//
		// max_iterations is a limit on review ROUNDS, counting the first
		// review (DFLT-00140): the round being judged is IterationCount+1,
		// and a failure in the last allowed round blocks rather than opening
		// round N+1. (The comparison used to be `>`, which let a limit of 3
		// run a fourth review.)
		if newIteration && target.IterationCount+1 >= target.MaxIterations {
			if err := e.blockTicket(node.TicketID); err != nil {
				return CompleteNodeResult{}, err
			}
			return CompleteNodeResult{NextStatus: "BLOCKED", LoopedBack: false}, nil
		}
		// Computed before the first write and only once the budget check
		// above has passed: a loop-back that blocks the ticket must leave the
		// graph byte-for-byte as it found it, never half-rewound
		// (DFLT-00101 completion criterion 2 -- no partial application, the
		// same guarantee ReopenNodes gives).
		byID := make(map[string]domain.GraphNode, len(detail.Nodes))
		for _, n := range detail.Nodes {
			byID[n.ID] = n
		}
		rewind := loopBackRewindSet(target.ID, node.ID, byID, detail.Edges)

		todo := domain.NodeTODO
		switch {
		case !newIteration:
			// Nothing to write: the target is already TODO and this round's
			// iteration is already on its counter.
		case rewindsFailedNode:
			// ClaimNode, not UpdateNode, for the same reason
			// GetExecutableNodes uses it: `target` is a snapshot, and the
			// parallel gates this case is about can call CompleteNode at the
			// same moment. Read-then-write would let two of them both see the
			// target at DONE and both bump the count -- the very double
			// counting above. The CAS makes "move the target out of a
			// non-TODO status" the thing exactly one caller can win, and
			// losing it means a sibling opened the round first, so this
			// failure counts nothing after all. (HTTPRepository cannot
			// express a CAS and composes a GET and a PATCH instead, so
			// against that backend this race is not closed at all. Do not
			// read "narrow" into it there: the window is two network round
			// trips wide -- milliseconds to hundreds of milliseconds against
			// the single UPDATE this relies on -- and gates that finish
			// together can still each count a round. See GraphRepository.
			// ClaimNode, and the known limitation in
			// docs/release-notes/v0.7.0.md.)
			//
			// The count written is read back by the claim itself rather than
			// taken from the snapshot, so it cannot revert a bump that landed
			// in between. The budget was still checked against the snapshot:
			// for the count to have moved since, the target must have been
			// redone in between, which means the check is simply one round
			// late -- the next failure blocks.
			claimed, err := e.repo.ClaimNode(target.ID, todo, []domain.NodeStatus{todo})
			if err != nil {
				return CompleteNodeResult{}, err
			}
			if claimed == nil {
				// A sibling opened this round between the read above and
				// this write. Its bump is the round's; this failure adds
				// nothing and has nothing left to write on the target.
				break
			}
			nextIteration := claimed.IterationCount + 1
			if _, err := e.repo.UpdateNode(target.ID, store.NodePatch{IterationCount: &nextIteration}); err != nil {
				return CompleteNodeResult{}, err
			}
		default:
			// Sideways loop-back: this failure always opens a round of its
			// own (see above), so there is no claim to race for -- the target
			// may legitimately already be TODO and still owe a bump.
			nextIteration := target.IterationCount + 1
			if _, err := e.repo.UpdateNode(target.ID, store.NodePatch{Status: &todo, IterationCount: &nextIteration}); err != nil {
				return CompleteNodeResult{}, err
			}
		}
		// Status only, no IterationCount in the patch: the loop target's
		// count is the loop's counter ("how many times has the target been
		// redone"), so bumping the nodes swept along with it would burn the
		// budget of whichever of them happens to be a loop target of its own
		// and shrink the retries left for no reason.
		for _, id := range rewind {
			if _, err := e.repo.UpdateNode(id, store.NodePatch{Status: &todo}); err != nil {
				return CompleteNodeResult{}, err
			}
		}
		// The failing node itself is marked NodeAwaitingFix rather than reset
		// to NodeTODO, so "sent back, waiting on the loop target's rework" is
		// distinguishable from "never run" by status alone (DFLT-00042).
		// Deliberately not branched on node type: any node that reaches this
		// loop-back branch has already judged its target's output.
		awaitingFix := domain.NodeAwaitingFix
		if _, err := e.repo.UpdateNode(node.ID, store.NodePatch{Status: &awaitingFix}); err != nil {
			return CompleteNodeResult{}, err
		}
		// Resync (this branch did not use to, because it only ever moved one
		// DONE node back to TODO): the rewind can now take several DONE nodes
		// out at once, and a manual approval_gate among them turns the ticket
		// into one waiting on a human again, which deriveTicketStatus reports
		// as IN REVIEW. Every other status-changing path -- the passing
		// branch, blockTicket, ReopenNodes, UnstickNode -- already resyncs.
		if err := e.syncTicketStatus(node.TicketID); err != nil {
			return CompleteNodeResult{}, err
		}
		return CompleteNodeResult{NextStatus: string(domain.NodeAwaitingFix), LoopedBack: true}, nil
	}

	if err := e.blockTicket(node.TicketID); err != nil {
		return CompleteNodeResult{}, err
	}
	return CompleteNodeResult{NextStatus: "BLOCKED", LoopedBack: false}, nil
}

// ReopenNodes is the mechanical primitive behind rejection triage
// (DFLT-00016): deciding WHICH already-completed nodes a rejection's free-
// text reason implicates is a judgment call this package deliberately does
// not make (see this package's doc comment and plan art-5f8847a4 section
// 1.6 -- the engine is a deterministic state machine, process-ticket/the LLM
// supplies the judgment). ReopenNodes only applies a judgment already made:
// given the root node ids process-ticket picked, it resets them (and
// whatever downstream work already ran off them) back to TODO and clears
// the ticket's blocked flag, so execution can resume.
//
// Preconditions, checked up front so a bad call leaves nothing written:
//
//   - the ticket must currently be Blocked. ReopenNodes exists to recover
//     from exactly that state (an approval_gate rejection or an
//     iteration_loop exceeding max_iterations); calling it on a healthy
//     ticket would race whatever is currently executing.
//
//   - every id in nodeIDs must belong to this ticket and currently be DONE,
//     REJECTED, TODO or AWAITING FIX. DONE/REJECTED are the original pair (an
//     approval_gate can itself be a root -- rejecting it is often the reason
//     the ticket is blocked in the first place, and re-approving it requires
//     it to be TODO again, not stuck at REJECTED forever). TODO and AWAITING
//     FIX were added by DFLT-00119, because after that ticket a loop target
//     is at TODO, not DONE, when an iteration limit blocks the ticket, and
//     reopen-nodes used to refuse exactly the node the recovery names: with
//     grant-iterations only raising the ceiling and GetExecutableNodes
//     returning nothing while `blocked` is set, a ticket in that state could
//     not be recovered from the CLI at all, only by editing statuses in the
//     Web UI.
//
//     IN PROGRESS and IN REVIEW stay out: something may still be working
//     them, and deciding a claim is stale is UnstickNode's judgment call to
//     be asked for explicitly, not one this method makes on its own.
//
// Forward closure: nodeIDs is only the root set process-ticket identified;
// anything reachable from it by a `success` edge that is itself reopenable is
// included too -- leaving a downstream node's stale DONE status/artifacts in
// place while its input gets redone upstream would leave the graph
// inconsistent. iteration_loop edges are never followed (they point backward,
// not forward, and don't participate in this sweep).
//
// Widening `reopenable` widened this sweep as well, deliberately: a node the
// predicate rejects is not merely left out of the set, it stops the walk down
// that branch. Before DFLT-00119 a TODO node ended the branch there, so DONE
// work sitting behind one (a rewound gate with a finished report past it, say)
// was missed and kept a stale status while its input was redone. Now the walk
// passes through TODO/AWAITING FIX nodes and collects that work too. The
// visible consequences: more nodes reset than before from the same roots, and
// a call that used to succeed can now hit the budget check below via a node it
// did not previously reach.
//
// Iteration budget: a collected node has IterationCount bumped by one as it's
// reset only if it had actually produced something to throw away -- DONE or
// REJECTED. TODO and AWAITING FIX nodes never ran (AWAITING FIX is a reviewer
// waiting on its target's rework), so reopening them costs no attempt, and
// billing one would push the recovery this method exists for back towards the
// very limit it is recovering from. Only the nodes that are bumped are
// budget-checked. If any of them would exceed MaxIterations, this method
// writes nothing at all and returns an error -- process-ticket must not have
// to reason about a partially-applied reopen leaving the graph half-reset.
//
// For a loop target (the `to` of some iteration_loop edge) the check uses the
// same round meaning CompleteNode does (DFLT-00140): max_iterations is the
// number of review rounds, and reopening it starts round IterationCount+2,
// so it is refused once IterationCount+1 >= MaxIterations. Without this,
// right after a round-limit block (IterationCount == MaxIterations-1) a bare
// reopen would start round N+1 with no grant at all, and grant-iterations
// would make no difference. Every other node keeps the plain "would the
// bumped count exceed MaxIterations" check.
func (e *GraphEngine) ReopenNodes(ticketID string, nodeIDs []string) (domain.TicketDetail, error) {
	if len(nodeIDs) == 0 {
		return domain.TicketDetail{}, fmt.Errorf("no node ids given to reopen")
	}

	detail, err := e.repo.GetTicketDetail(ticketID)
	if err != nil {
		return domain.TicketDetail{}, err
	}
	if detail == nil {
		return domain.TicketDetail{}, fmt.Errorf("ticket %s not found", ticketID)
	}
	if !detail.Blocked {
		return domain.TicketDetail{}, fmt.Errorf("ticket %s is not blocked; reopen-nodes only applies after a rejection or an iteration-limit block", ticketID)
	}

	byID := make(map[string]domain.GraphNode, len(detail.Nodes))
	for _, n := range detail.Nodes {
		byID[n.ID] = n
	}
	reopenable := func(status domain.NodeStatus) bool {
		switch status {
		case domain.NodeDone, domain.NodeRejected, domain.NodeTODO, domain.NodeAwaitingFix:
			return true
		}
		return false
	}
	// Which of the reopenable statuses spend an attempt when reset: see the
	// doc comment's "Iteration budget".
	spendsIteration := func(status domain.NodeStatus) bool {
		return status == domain.NodeDone || status == domain.NodeRejected
	}

	toReset := make(map[string]bool, len(nodeIDs))
	queue := make([]string, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		n, ok := byID[id]
		if !ok {
			return domain.TicketDetail{}, fmt.Errorf("node %s does not belong to ticket %s", id, ticketID)
		}
		if !reopenable(n.Status) {
			return domain.TicketDetail{}, fmt.Errorf("node %s is %s, not DONE, REJECTED, TODO or AWAITING FIX; a node that is being worked right now cannot be reopened -- use unstick-node to release it first", id, n.Status)
		}
		if !toReset[id] {
			toReset[id] = true
			queue = append(queue, id)
		}
	}

	successors := make(map[string][]string, len(detail.Edges))
	loopTargets := make(map[string]bool)
	for _, edge := range detail.Edges {
		switch edge.Condition {
		case domain.EdgeSuccess:
			successors[edge.FromNodeID] = append(successors[edge.FromNodeID], edge.ToNodeID)
		case domain.EdgeLoop:
			loopTargets[edge.ToNodeID] = true
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, succ := range successors[id] {
			if toReset[succ] {
				continue
			}
			n, ok := byID[succ]
			if !ok || !reopenable(n.Status) {
				continue
			}
			toReset[succ] = true
			queue = append(queue, succ)
		}
	}

	// Validate every collected node's iteration budget before writing
	// anything (see doc comment: no partial application). Only the nodes that
	// are actually going to be bumped are checked -- a TODO or AWAITING FIX
	// node spends no attempt, so its budget cannot be the thing that stops
	// the recovery.
	for id := range toReset {
		n := byID[id]
		if !spendsIteration(n.Status) {
			continue
		}
		if loopTargets[id] {
			if n.IterationCount+1 >= n.MaxIterations {
				return domain.TicketDetail{}, fmt.Errorf("node %s (%s) has used all %d review rounds of its max_iterations; reopening it would start round %d -- raise the limit with grant-iterations first", id, n.Name, n.MaxIterations, n.IterationCount+2)
			}
			continue
		}
		if n.IterationCount+1 > n.MaxIterations {
			return domain.TicketDetail{}, fmt.Errorf("node %s (%s) would exceed max_iterations (%d) if reopened; cannot auto-retry further", id, n.Name, n.MaxIterations)
		}
	}

	todo := domain.NodeTODO
	for id := range toReset {
		n := byID[id]
		patch := store.NodePatch{Status: &todo}
		if spendsIteration(n.Status) {
			nextIteration := n.IterationCount + 1
			patch.IterationCount = &nextIteration
		}
		if _, err := e.repo.UpdateNode(id, patch); err != nil {
			return domain.TicketDetail{}, err
		}
	}

	unblocked := false
	if _, err := e.repo.UpdateTicket(ticketID, store.TicketPatch{Blocked: &unblocked}); err != nil {
		return domain.TicketDetail{}, err
	}
	if err := e.syncTicketStatus(ticketID); err != nil {
		return domain.TicketDetail{}, err
	}

	updated, err := e.repo.GetTicketDetail(ticketID)
	if err != nil {
		return domain.TicketDetail{}, err
	}
	if updated == nil {
		return domain.TicketDetail{}, fmt.Errorf("ticket %s not found after reopen", ticketID)
	}
	return *updated, nil
}

// maxIterationsGrantPerCall bounds GrantIterations' `extra` argument. The
// point is not that 10 is a meaningful ceiling -- the command can be run
// again -- but that a slipped digit (`--extra 1000000`) can't quietly turn
// max_iterations into "unlimited retries" and remove the safety valve
// altogether. Each grant is meant to be a deliberate human decision, so
// needing a second one is the intended cost of going higher.
const maxIterationsGrantPerCall = 10

// GrantIterations raises the max_iterations budget of the given nodes by
// `extra`, leaving their status and iteration_count alone. It is the missing
// first step of recovering a ticket that an iteration limit blocked
// (DFLT-00101 / BUG-14): at that point the loop target has
// iteration_count + 1 == max_iterations (a review failed in the last allowed
// round, DFLT-00140), so ReopenNodes -- which refuses to reopen a loop target
// whose next round would pass its max_iterations, and writes nothing if any
// node fails its check -- always fails. Granting k extra lets exactly k more
// rounds run; GetReviewCriteria judges every round past the original limit
// at the Final tier (the tier limit is the review node's own
// max_iterations, which grants to the loop target leave alone). The
// sanctioned recovery is therefore:
//
//	grant-iterations <ticketId> <loop target>   (raise the budget)
//	reopen-nodes <ticketId> <loop target>       (now succeeds; unblocks the ticket)
//
// The loop target may be DONE (the block came from a sideways loop-back, or
// from a reopen that ran out of budget) or TODO (it was already rewound by an
// earlier failure of the same round, which is the usual shape after
// DFLT-00119). ReopenNodes accepts both; a TODO target is reset without
// spending an attempt.
//
// A third step is needed only for a node left IN PROGRESS/IN REVIEW, which
// ReopenNodes will not touch:
//
//	unstick-node <that node>
//
// Since DFLT-00119 the failing reviewer is not normally one of them -- a
// loop-back that actually rewinds puts the whole forward closure, claims
// included, back to TODO. It stays claimed only when the loop-back never
// happened, i.e. when the budget check blocked the ticket before any write.
//
// Deliberately NOT a reset of iteration_count: the count is the ticket's
// record of how many automatic attempts the loop has already consumed, and
// erasing it would erase the evidence that a human had to step in. Raising
// the ceiling instead keeps that history and still leaves a ceiling.
//
// Deliberately NOT gated on the ticket being Blocked, unlike ReopenNodes: this
// writes no status at all, so there is no running work for it to race, and
// raising a budget before a loop runs out of it (e.g. on a ticket already
// known to need more rounds) is a legitimate use.
//
// Every id is validated -- non-empty, belongs to ticketID -- along with
// `extra` (1..maxIterationsGrantPerCall) before anything is written, so a bad
// call leaves the graph untouched, exactly as ReopenNodes promises. Duplicate
// ids collapse to a single grant rather than stacking. Returns the updated
// nodes in ascending id order.
func (e *GraphEngine) GrantIterations(ticketID string, nodeIDs []string, extra int) ([]domain.GraphNode, error) {
	if extra < 1 {
		return nil, fmt.Errorf("extra iterations must be at least 1, got %d", extra)
	}
	if extra > maxIterationsGrantPerCall {
		return nil, fmt.Errorf("extra iterations must be at most %d per call, got %d; run grant-iterations again if more are genuinely needed", maxIterationsGrantPerCall, extra)
	}
	if len(nodeIDs) == 0 {
		return nil, fmt.Errorf("no node ids given to grant iterations to")
	}

	detail, err := e.repo.GetTicketDetail(ticketID)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, fmt.Errorf("ticket %s not found", ticketID)
	}
	byID := make(map[string]domain.GraphNode, len(detail.Nodes))
	for _, n := range detail.Nodes {
		byID[n.ID] = n
	}

	targets := make(map[string]bool, len(nodeIDs))
	for _, raw := range nodeIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil, fmt.Errorf("empty node id given to grant iterations to")
		}
		if _, ok := byID[id]; !ok {
			return nil, fmt.Errorf("node %s does not belong to ticket %s", id, ticketID)
		}
		targets[id] = true
	}

	ids := make([]string, 0, len(targets))
	for id := range targets {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	updated := make([]domain.GraphNode, 0, len(ids))
	for _, id := range ids {
		newMax := byID[id].MaxIterations + extra
		n, err := e.repo.UpdateNode(id, store.NodePatch{MaxIterations: &newMax})
		if err != nil {
			return nil, err
		}
		updated = append(updated, n)
	}
	return updated, nil
}

// UnstickNode resets a single node that's stuck at IN PROGRESS or IN REVIEW
// back to TODO, without touching its iteration count or requiring the
// ticket to be Blocked.
//
// This exists for a different failure mode than ReopenNodes: GetExecutableNodes
// claims (flips to IN PROGRESS/IN REVIEW) every node it returns in one call --
// deliberately, to prevent double-dispatch (DFLT-00009). But a node's claim
// and its actual execution are two separate steps a caller performs, and
// nothing enforces that the second one happens. If get-executable is called
// by anything other than the one place actually about to dispatch a worker
// for every node it returns -- e.g. a subagent polling it for unrelated
// context while working a different node in the same batch -- a node can be
// claimed and then never worked, silently going quiet: it is excluded from
// every future GetExecutableNodes call (its status is no longer TODO), yet
// the ticket never becomes Blocked (nothing failed; nothing hit an
// iteration limit), so ReopenNodes' precondition is never met either. There
// is then no sanctioned way to make forward progress on that node short of
// bypassing get-executable and calling CompleteNode on it directly.
// (Observed in practice: DFLT-00020's Test Result Review node was claimed
// this way and sat IN REVIEW, unreachable via get-executable, from before
// its own Implementation loop-back target was even fixed.)
//
// Like ReopenNodes, this is a mechanical primitive: deciding that a given
// node's claim is actually stale (nothing is currently working it) is a
// judgment call left to the caller (process-ticket), not something this
// method can verify on its own -- there is no lease/heartbeat tracking a
// claim's owner. Calling this on a node a live subagent is still working
// races that subagent's own eventual CompleteNode call, exactly as
// ReopenNodes' doc comment warns for its own, narrower precondition. No
// iteration_count bump: unlike a loop-back or a rejection, no actual
// attempt at this node happened, so nothing should count against its
// max_iterations budget.
func (e *GraphEngine) UnstickNode(nodeID string) (domain.GraphNode, error) {
	node, err := e.repo.GetNode(nodeID)
	if err != nil {
		return domain.GraphNode{}, err
	}
	if node == nil {
		return domain.GraphNode{}, fmt.Errorf("node %s not found", nodeID)
	}
	if node.Status != domain.NodeInProgress && node.Status != domain.NodeInReview {
		return domain.GraphNode{}, fmt.Errorf("node %s is %s, not IN PROGRESS or IN REVIEW; only a claimed-but-unworked node can be unstuck", nodeID, node.Status)
	}

	todo := domain.NodeTODO
	updated, err := e.repo.UpdateNode(nodeID, store.NodePatch{Status: &todo})
	if err != nil {
		return domain.GraphNode{}, err
	}
	if err := e.syncTicketStatus(node.TicketID); err != nil {
		return domain.GraphNode{}, err
	}
	return updated, nil
}

// UpdateNode applies patch to nodeID and then brings the owning ticket's
// status back in line with its nodes, exactly as CompleteNode, ReopenNodes
// and UnstickNode do (DFLT-00103 / BUG-05).
//
// It exists because PATCH /api/nodes/{id} used to call repo.UpdateNode
// directly, which is the one node-mutating path in the codebase that skipped
// syncTicketStatus: a PATCH that moved the last node to DONE left the ticket
// sitting at IN PROGRESS forever, and nothing but another mutation through a
// different path would ever fix it. Routing that handler through the engine
// rather than teaching it to call a second repo method keeps "a node changed,
// so re-derive the ticket" a rule of the engine, not something each caller
// has to remember.
//
// A CLOSED ticket is not resurrected and its closed_reason is not touched:
// syncTicketStatus returns early for one (DFLT-00043), and nothing here
// writes to the ticket other than through it.
func (e *GraphEngine) UpdateNode(nodeID string, patch store.NodePatch) (domain.GraphNode, error) {
	node, err := e.repo.GetNode(nodeID)
	if err != nil {
		return domain.GraphNode{}, err
	}
	if node == nil {
		return domain.GraphNode{}, domain.NewAPIError(domain.ErrCodeNodeNotFound, "node not found: %s", nodeID)
	}
	updated, err := e.repo.UpdateNode(nodeID, patch)
	if err != nil {
		return domain.GraphNode{}, err
	}
	if err := e.syncTicketStatus(node.TicketID); err != nil {
		return domain.GraphNode{}, err
	}
	return updated, nil
}

// deriveTicketStatus computes the ticket status implied by detail's ticket/
// node state (DONE > IN RELEASE > IN REVIEW > IN PROGRESS, in that
// precedence), with ok=false when none of those apply (e.g. no nodes, or
// every node still TODO) -- callers decide what to do in that case
// themselves, since it means something different in each: syncTicketStatus
// simply leaves the ticket's status untouched, while ReopenTicket
// (DFLT-00043) falls back to REFINED/TODO based on whether the ticket has
// ever been refined.
//
// IN REVIEW (DFLT-00046) means a human is waiting to approve/reject, not a
// review/review_gate node running automatically: it is reserved for a
// *reached* approval_gate still sitting at TODO (GateID aside, the same
// "reached" check GetExecutableNodes uses to decide what's executable --
// see allNonLoopPrereqsDone), matching the Web UI's own
// pendingApprovalNodeIds (packages/web/src/components/TicketItem.tsx) so the
// ticket list's "awaiting approval" blink and this status agree. A
// review/review_gate node IN REVIEW, or a REJECTED approval_gate, both fall
// through to the IN PROGRESS catch-all instead.
//
// DONE additionally requires detail.GraphExpandedAt to be set: the seed
// (plan/plan_review) being DONE, before ExpandGraph has ever run, must not
// read as the whole ticket being done (DFLT-00046) -- see domain.Ticket's
// doc comment on GraphExpandedAt for why this can't just check node
// counts/types against the catalog's seed list.
func deriveTicketStatus(detail domain.TicketDetail) (domain.TicketStatus, bool) {
	byID := make(map[string]domain.GraphNode, len(detail.Nodes))
	for _, n := range detail.Nodes {
		byID[n.ID] = n
	}

	allDone := len(detail.Nodes) > 0 && detail.GraphExpandedAt != nil
	releaseInProgress := false
	anyPendingApproval := false
	anyInProgressOrDone := false
	for _, n := range detail.Nodes {
		if n.Status != domain.NodeDone {
			allDone = false
		}
		if n.Type == domain.NodeTypeRelease && n.Status == domain.NodeInProgress {
			releaseInProgress = true
		}
		if n.Type == domain.NodeTypeApprovalGate && n.Status == domain.NodeTODO &&
			allNonLoopPrereqsDone(n.ID, byID, detail.Edges) {
			anyPendingApproval = true
		}
		if n.Status == domain.NodeInProgress || n.Status == domain.NodeDone ||
			n.Status == domain.NodeInReview || n.Status == domain.NodeRejected {
			anyInProgressOrDone = true
		}
	}

	switch {
	case allDone:
		return domain.TicketDone, true
	case releaseInProgress:
		return domain.TicketInRelease, true
	case anyPendingApproval:
		return domain.TicketInReview, true
	case anyInProgressOrDone:
		return domain.TicketInProgress, true
	default:
		return "", false
	}
}

func (e *GraphEngine) syncTicketStatus(ticketID string) error {
	detail, err := e.repo.GetTicketDetail(ticketID)
	if err != nil || detail == nil {
		return err
	}
	// A CLOSED ticket is withdrawn, not merely idle: complete-node and every
	// other caller of syncTicketStatus must never resurrect it into
	// TODO/IN PROGRESS/.../DONE just because a node it no longer cares about
	// finished. Only ReopenTicket may move it out of CLOSED (DFLT-00043).
	if detail.Status == domain.TicketClosed {
		return nil
	}

	newStatus, ok := deriveTicketStatus(*detail)
	if !ok {
		return nil
	}
	// Nothing to sync when the derived status already matches what's stored:
	// skip the write entirely (DFLT-00100). This is the same "don't take a
	// write lock when there is nothing to write" fix the ticket applies to
	// Init's backfill, and it matters for the same reason -- a read-only
	// command must not become a contender for the write lock. get-executable
	// runs syncTicketStatus on every invocation, so without this guard
	// *every* read of the executable set rewrote the ticket row and took
	// the write lock to do it. When DFLT-00100 added this guard, that write
	// was a deferred read-then-write transaction on SQLite, which failed
	// outright under contention, so the guard was the workaround. Since
	// DFLT-00136 SQLite transactions begin IMMEDIATE and wait out the lock
	// under busy_timeout instead, so the guard is no longer what keeps these
	// commands from failing; it stays as the optimization it also is -- no
	// write, no lock, no waiting.
	//
	// Deliberate behaviour change: a ticket whose derived status is unchanged
	// no longer gets its updated_at bumped by complete-node/get-executable.
	// updated_at now means "something about this ticket actually changed",
	// which is what a caller reading it would expect anyway.
	if detail.Status == newStatus {
		return nil
	}
	_, err = e.repo.UpdateTicket(ticketID, store.TicketPatch{Status: &newStatus})
	return err
}

// CloseTicket withdraws ticketID without marking it complete: it sets status
// to CLOSED and stores reason (overwriting whatever reason a previous close
// left, even to empty), regardless of the ticket's current status or its
// nodes' statuses -- a ticket with nodes IN PROGRESS/IN REVIEW can be closed
// just as freely as one at TODO or DONE (DFLT-00043). Node/Blocked state is
// deliberately left untouched: GetExecutableNodes' own CLOSED check is what
// keeps a closed ticket from being executed further, not any change to its
// nodes here.
func (e *GraphEngine) CloseTicket(ticketID string, reason string) (*domain.Ticket, error) {
	ticket, err := e.repo.GetTicket(ticketID)
	if err != nil {
		return nil, err
	}
	if ticket == nil {
		return nil, fmt.Errorf("ticket %s not found", ticketID)
	}
	closed := domain.TicketClosed
	updated, err := e.repo.UpdateTicket(ticketID, store.TicketPatch{Status: &closed, ClosedReason: &reason})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// ReopenTicket moves a CLOSED ticket back into the normal status flow. The
// new status follows the same derivation syncTicketStatus uses for every
// other ticket (deriveTicketStatus); if that yields nothing -- no
// nodes at all, or every node still at TODO -- it falls back to REFINED when
// the ticket has ever been refined (RefinedAt set) or TODO otherwise
// (DFLT-00043). ClosedReason is left as-is: it stays visible as history until
// the ticket is closed again. Reopening a ticket that isn't CLOSED is
// rejected -- there is nothing to "reopen".
func (e *GraphEngine) ReopenTicket(ticketID string) (*domain.Ticket, error) {
	detail, err := e.repo.GetTicketDetail(ticketID)
	if err != nil {
		return nil, err
	}
	if detail == nil {
		return nil, fmt.Errorf("ticket %s not found", ticketID)
	}
	if detail.Status != domain.TicketClosed {
		return nil, fmt.Errorf("ticket %s is %s, not CLOSED; nothing to reopen", ticketID, detail.Status)
	}

	newStatus, ok := deriveTicketStatus(*detail)
	if !ok {
		if detail.RefinedAt != nil {
			newStatus = domain.TicketRefined
		} else {
			newStatus = domain.TicketTODO
		}
	}
	updated, err := e.repo.UpdateTicket(ticketID, store.TicketPatch{Status: &newStatus})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// ReviewTier is the convergence tier a review round is judged at.
type ReviewTier string

const (
	// TierNormal: anything that needs fixing, minor points included, fails.
	TierNormal ReviewTier = "Normal"
	// TierImportant: only correctness bugs, unmet completion criteria,
	// security problems and regressions fail; minor/style points are
	// carried over.
	TierImportant ReviewTier = "Important"
	// TierFinal: only critical bugs, security vulnerabilities, data
	// corruption and unmet completion criteria fail.
	TierFinal ReviewTier = "Final"
)

// ReviewRound locates where a review node stands in its loop. The loop
// target is the `to` of the first iteration_loop edge leaving node:
//
//   - round is the review round being judged, counting the first review:
//     target.IterationCount + 1 (the target's count is how many times it has
//     been redone).
//   - ceiling is target.MaxIterations -- the limit stored when the graph was
//     built plus whatever grant-iterations added.
//   - tierLimit is node.MaxIterations, the review node's own stored limit.
//     Grants go to the loop target, so this stays at the original N and every
//     granted round past it lands on the Final tier.
//
// A review with no loop target is always round 1 of node.MaxIterations.
func ReviewRound(node domain.GraphNode, detail domain.TicketDetail) (round, ceiling, tierLimit int) {
	tierLimit = node.MaxIterations
	round, ceiling = 1, node.MaxIterations
	for _, edge := range detail.Edges {
		if edge.FromNodeID != node.ID || edge.Condition != domain.EdgeLoop {
			continue
		}
		for _, n := range detail.Nodes {
			if n.ID == edge.ToNodeID {
				return n.IterationCount + 1, n.MaxIterations, tierLimit
			}
		}
	}
	return round, ceiling, tierLimit
}

// ReviewTierFor maps a review round to its tier under tierLimit:
//
//	limit 3: Normal / Important / Final
//	limit 4: Normal, Normal / Important / Final
//	limit 5: Normal, Normal / Important, Important / Final
//
// Round tierLimit and every round past it are Final. Before that the first
// round (limit <= 3) or first two rounds (limit >= 4) are Normal and the rest
// Important, so legacy limits degrade sensibly: 1 is always Final, 2 is
// Normal then Final, 6+ is two Normal rounds, Important rounds, then Final.
func ReviewTierFor(round, tierLimit int) ReviewTier {
	if round >= tierLimit {
		return TierFinal
	}
	normalRounds := 2
	if tierLimit <= 3 {
		normalRounds = 1
	}
	if round <= normalRounds {
		return TierNormal
	}
	return TierImportant
}

var reviewTierDefinitions = map[ReviewTier]string{
	TierNormal:    "Normal tier: fail the review if anything needs fixing, minor points included.",
	TierImportant: "Important tier: fail the review only for correctness bugs, unmet completion criteria, security problems, or regressions. Record minor and stylistic points as carry-over items instead of failing the review for them.",
	TierFinal:     "Final tier: fail the review only for critical bugs, security vulnerabilities, data corruption, or unmet completion criteria.",
}

const reviewNeverRelaxedText = `Never relaxed, at any tier:
- A bug, regression, or security problem newly introduced by the changes made since the previous round is judged as strictly as at the Normal tier.
- A serious issue flagged in a previous round that is still not fixed fails the review.`

// reviewPreviousRoundText is appended from round 2 on. Its second paragraph
// covers a round 2+ review that has no earlier review of its own: the round
// comes from the loop target's iteration_count, so a parallel gate rewound by
// a sibling before its verdict was recorded, a later review (test results,
// report) whose loop target was already redone for other reviews, or a round
// reopened after an approval was rejected all start at round 2 or more with
// nothing of their own to compare against.
const reviewPreviousRoundText = `This is not the first round: before judging, fetch your own previous review result (the latest review artifact on this node, from get-ticket) and the changes made since that review (for code, git log / git diff of the commits after that review was written; for a document, the diff between the loop target's latest artifact and the one you reviewed last time), and check both against the rules above.

If this node has no previous review of its own (for example a parallel gate rewound by a sibling gate before its verdict was recorded, a later review whose loop target was already redone for other reviews, or a round reopened after an approval was rejected): judge the whole output under review at this round's tier, and take the diff base from the loop target's previous round instead (for code, the commits made since the loop target's previous output -- e.g. after its previous implementation notes were saved; for a document, the diff between the loop target's latest artifact and its previous one). A draft this node saved in a round whose verdict was refused is not a previous review, though its findings may serve as a checklist. The never-relaxed rules still apply in full to everything that diff introduced.`

const reviewCarryOverText = "Record carry-over items (points that do not fail the review) under the last heading of the review template. Do not turn them into a conditional approval that requires code changes -- there is no path back to the implementation node from an approval; pass with the unconditional verdict instead."

// GetReviewCriteria returns what a review/review_gate node is judged
// against. review and review_gate are treated alike: a gate's frozen
// gate-specific criteria (set when the graph was built, see buildPlan) come
// first when present, then the round/tier line, the tier's definition, the
// rules no tier relaxes, (from round 2 on) the instruction to fetch the
// previous review and the changes since, and the carry-over rule. The text is
// fixed English on purpose: the engine is language-independent, and the
// language deliverables are written in comes from the onboarding
// instructions instead.
func GetReviewCriteria(node domain.GraphNode, detail domain.TicketDetail) string {
	round, ceiling, tierLimit := ReviewRound(node, detail)
	tier := ReviewTierFor(round, tierLimit)

	parts := make([]string, 0, 6)
	if node.Criteria != nil && *node.Criteria != "" {
		parts = append(parts, *node.Criteria)
	}
	parts = append(parts,
		fmt.Sprintf("Review round: %d / %d — tier: %s", round, ceiling, tier),
		reviewTierDefinitions[tier],
		reviewNeverRelaxedText,
	)
	if round >= 2 {
		parts = append(parts, reviewPreviousRoundText)
	}
	parts = append(parts, reviewCarryOverText)
	return strings.Join(parts, "\n\n")
}
