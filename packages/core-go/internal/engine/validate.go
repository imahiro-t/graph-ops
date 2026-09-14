package engine

import "github.com/graph-ops/core-go/internal/config"

// ValidateCatalog checks that catalog's enabled nodes form a well-formed
// workflow definition -- no duplicate ids, no depends_on/loop_back_to
// reference to an unknown node, no cycle in the depends_on edges, and every
// review_gate node references a known (enabled) review gate -- without
// touching the database. It's the settings UI's pre-save guardrail (see the
// execution plan's "4.5 バリデーション方針"): PUT /api/settings/catalog runs
// this against the candidate merged catalog before writing anything to disk,
// so a broken workflow.yaml/config.yaml edit is rejected instead of silently
// corrupting graph construction for every future ticket.
//
// This deliberately reuses buildPlan (the exact same validation
// GetExecutableNodes/ExpandGraph rely on at graph-construction time) rather
// than re-implementing a parallel check, so the settings UI and the actual
// graph engine can never disagree about what counts as "valid".
func ValidateCatalog(catalog config.Catalog) error {
	_, err := buildPlan(catalog, nil)
	return err
}
