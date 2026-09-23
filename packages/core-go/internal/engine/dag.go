package engine

import (
	"fmt"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
)

// plannedNode is one fully-resolved node in a workflow plan: config-level
// references (Gate, DependsOn ids by config id, not DB id) have already been
// resolved to concrete Criteria text, but nothing has been persisted yet.
// MaxIterations is the workflow-wide limit in effect when the plan was built
// (catalog.MaxIterations), stored on every node so a later settings change
// never affects a ticket already in progress.
type plannedNode struct {
	ConfigID      string
	Name          string
	Type          string
	GateID        *string
	Criteria      *string
	MaxIterations int
	DependsOn     []string
	LoopBackTo    string
	IsManual      bool
}

// buildPlan resolves the catalog's full base workflow plus an optional
// LLM-proposed patch into a validated, ordered list of plannedNode.
func buildPlan(catalog config.Catalog, patch *Patch) ([]plannedNode, error) {
	return buildPlanFromNodeDefs(catalog.EnabledNodes(), catalog.EnabledReviewGates(), patch, nil, catalog.MaxIterations)
}

// buildPlanFromNodeDefs is buildPlan's underlying implementation, taking an
// explicit node-def list rather than always pulling the catalog's full set.
// This is what lets EnsureGraphStarted build a plan from just the seed
// subset while ExpandGraph builds one from the full catalog (or a
// skill-authored patch), reusing the same validation. It never touches the
// database: the DAG shape (duplicate ids, dangling references, cycles in the
// non-loop-back edges) is checked entirely in memory so a bad definition is
// rejected before anything is written.
//
// externalIDs names node ids that exist outside this batch (already
// persisted in an earlier phase, e.g. the seed nodes when ExpandGraph runs a
// patch-only plan) -- a depends_on/loop_back_to referencing one of these is
// valid even though it isn't part of nodeDefs/patch, and it must NOT count
// against this batch's own topological ordering (see detectCycles).
//
// maxIterations is the workflow-wide review iteration limit
// (config.Catalog.MaxIterations) and is stored on every planned node, not
// just on loop-back targets: a patch-only expansion may loop back to a seed
// node persisted earlier (so "is this a loop target?" is not always knowable
// here), and a review node's own value is what GetReviewCriteria uses as the
// tier limit. A value below 1 (a zero Catalog built by hand in a test) falls
// back to config.DefaultMaxIterations.
func buildPlanFromNodeDefs(nodeDefs []config.NodeDef, gates map[string]config.ReviewGateDef, patch *Patch, externalIDs map[string]bool, maxIterations int) ([]plannedNode, error) {
	if maxIterations < 1 {
		maxIterations = config.DefaultMaxIterations
	}
	var planned []plannedNode
	seen := make(map[string]bool)

	addPlanned := func(id, name, typ, gateRef string, inlineGate *ExtraGateDef, dependsOn []string, loopBackTo string, isManual bool) error {
		if id == "" {
			return fmt.Errorf("node is missing an id")
		}
		if seen[id] {
			return fmt.Errorf("duplicate node id %q", id)
		}
		seen[id] = true

		pn := plannedNode{
			ConfigID:      id,
			Name:          name,
			Type:          typ,
			MaxIterations: maxIterations,
			DependsOn:     dependsOn,
			LoopBackTo:    loopBackTo,
			IsManual:      isManual,
		}

		if typ == string(domain.NodeTypeApprovalGate) {
			// A human must always explicitly approve/reject this node --
			// unlike review_gate (an automated pass/fail judgment), there is
			// no automated path through it. Forcing IsManual here (rather
			// than merely documenting "set is_manual: true" for workflow
			// authors) guarantees GetExecutableNodes' IsManual exclusion
			// applies even if a YAML/patch definition omits or misconfigures
			// is_manual.
			pn.IsManual = true
		}

		if typ == string(domain.NodeTypeReviewGate) {
			switch {
			case inlineGate != nil:
				gid := id
				pn.GateID = &gid
				criteria := inlineGate.Criteria
				pn.Criteria = &criteria
				if pn.Name == "" {
					pn.Name = inlineGate.Name
				}
			case gateRef != "":
				gate, ok := gates[gateRef]
				if !ok {
					return fmt.Errorf("node %q references unknown review gate %q", id, gateRef)
				}
				gidCopy := gateRef
				pn.GateID = &gidCopy
				criteria := gate.Criteria
				pn.Criteria = &criteria
				if pn.Name == "" {
					pn.Name = gate.Name
				}
			default:
				return fmt.Errorf("review_gate node %q must set a gate reference or an inline gate definition", id)
			}
		}

		planned = append(planned, pn)
		return nil
	}

	for _, n := range nodeDefs {
		if err := addPlanned(n.ID, n.Name, n.Type, n.Gate, nil, n.DependsOn, n.LoopBackTo, n.IsManual); err != nil {
			return nil, err
		}
	}

	if patch != nil {
		for _, e := range patch.ExtraNodes {
			if err := addPlanned(e.ID, e.Name, e.Type, e.GateRef, e.Gate, e.DependsOn, e.LoopBackTo, e.IsManual); err != nil {
				return nil, err
			}
		}
	}

	if err := validateReferences(planned, seen, externalIDs); err != nil {
		return nil, err
	}
	if err := detectCycles(planned, externalIDs); err != nil {
		return nil, err
	}

	return planned, nil
}

func validateReferences(planned []plannedNode, ids map[string]bool, externalIDs map[string]bool) error {
	valid := func(id string) bool { return ids[id] || externalIDs[id] }
	for _, n := range planned {
		for _, dep := range n.DependsOn {
			if !valid(dep) {
				return fmt.Errorf("node %q depends_on unknown node %q", n.ConfigID, dep)
			}
		}
		if n.LoopBackTo != "" && !valid(n.LoopBackTo) {
			return fmt.Errorf("node %q loop_back_to unknown node %q", n.ConfigID, n.LoopBackTo)
		}
	}
	return nil
}

// detectCycles runs Kahn's algorithm over the depends_on edges only
// (loop_back_to edges are intentionally backward and excluded: they express
// "on failure, go back to X", not a forward prerequisite). Dependencies on
// externalIDs are excluded from the in-degree count entirely: those nodes
// were already persisted (and completed) in an earlier phase, so they can
// never participate in a cycle within this batch, and counting them would
// leave the depending node's in-degree permanently non-zero (nothing in this
// batch ever "resolves" an external id), falsely reporting a cycle.
func detectCycles(planned []plannedNode, externalIDs map[string]bool) error {
	inDegree := make(map[string]int, len(planned))
	successors := make(map[string][]string, len(planned))
	for _, n := range planned {
		if _, ok := inDegree[n.ConfigID]; !ok {
			inDegree[n.ConfigID] = 0
		}
		for _, dep := range n.DependsOn {
			if externalIDs[dep] {
				continue
			}
			inDegree[n.ConfigID]++
			successors[dep] = append(successors[dep], n.ConfigID)
		}
	}

	queue := make([]string, 0, len(planned))
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, succ := range successors[id] {
			inDegree[succ]--
			if inDegree[succ] == 0 {
				queue = append(queue, succ)
			}
		}
	}

	if visited != len(planned) {
		return fmt.Errorf("workflow graph contains a cycle in depends_on edges (%d of %d nodes are unreachable by topological order)", len(planned)-visited, len(planned))
	}
	return nil
}
