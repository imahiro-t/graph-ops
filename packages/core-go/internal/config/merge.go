package config

import (
	"fmt"
	"sort"
)

// mergeReviewGates layers overlay on top of base, keyed by gate id. A field
// left zero-valued in overlay does not clobber the inherited value; the one
// exception is AdditionalCriteria, which is *appended* to the inherited
// Criteria (of either layer) rather than replacing it.
func mergeReviewGates(base, overlay map[string]ReviewGateDef) map[string]ReviewGateDef {
	merged := make(map[string]ReviewGateDef, len(base)+len(overlay))
	for id, g := range base {
		merged[id] = g
	}
	for id, ov := range overlay {
		cur, exists := merged[id]
		if !exists {
			merged[id] = ov
			continue
		}
		if ov.Name != "" {
			cur.Name = ov.Name
		}
		if ov.Criteria != "" {
			cur.Criteria = ov.Criteria
		}
		if ov.AdditionalCriteria != "" {
			if cur.Criteria != "" {
				cur.Criteria = cur.Criteria + "\n" + ov.AdditionalCriteria
			} else {
				cur.Criteria = ov.AdditionalCriteria
			}
		}
		if ov.Enabled != nil {
			cur.Enabled = ov.Enabled
		}
		merged[id] = cur
	}
	return merged
}

// Merge combines documents in increasing priority order (e.g. default, user,
// project) into a single Catalog.
//
// ReviewGates merge across every layer (a user/team/project doc can add,
// override, or disable a gate). Workflow.Nodes/Workflow.Seed do not: the
// skeleton graph (plan -> plan_review -> plan_approval -> ... ->
// release_approval -> release) and its seed are fixed by the plugin default
// alone and always come from docs[0] (every caller -- Load/LoadWithRoots and
// handleGetSettingsCatalog/handlePutSettingsCatalog -- passes the plugin
// default first). handlePutSettingsCatalog additionally rejects a submitted
// document that sets either field, so there is no path -- API or hand-edited
// user/team file -- through which a lower-priority layer's workflow.nodes/
// seed can take effect.
//
// Language is a third kind of field, distinct from both of the above: like
// Workflow.Seed it is "last non-empty wins" rather than per-key merged like
// ReviewGates, but unlike Workflow.Nodes/Seed every layer is eligible to set
// it (docs[0], the plugin default, never has one -- see DefaultDocument), so
// a later, higher-priority document's Language always overrides an earlier
// one's. This is what lets user- and team-tier config.yaml/workflow.yaml
// pick the display-name locale (see locale.go's ResolveLanguage) without
// touching the locked workflow skeleton itself.
//
// MaxIterations is a fourth kind: a single workflow-wide value resolved
// "last non-nil wins" across every layer (so team beats user beats the
// plugin default), falling back to DefaultMaxIterations when no layer sets
// it. The retired per-gate ReviewGateDef.LegacyMaxIterations is not merged
// at all: each layer's gate that still sets it only adds an entry to
// Catalog.Warnings, and the value itself is ignored.
func Merge(docs ...Document) Catalog {
	var gates map[string]ReviewGateDef
	var language string
	maxIterations := DefaultMaxIterations
	var warnings []string
	for _, d := range docs {
		gates = mergeReviewGates(gates, d.ReviewGates)
		if d.Language != "" {
			language = d.Language
		}
		if d.MaxIterations != nil {
			maxIterations = *d.MaxIterations
		}
		warnings = append(warnings, LegacyMaxIterationsWarnings(d)...)
	}
	var nodes []NodeDef
	var seed []string
	if len(docs) > 0 {
		nodes = docs[0].Workflow.Nodes
		seed = docs[0].Workflow.Seed
	}
	return Catalog{ReviewGates: gates, Nodes: nodes, Seed: seed, Language: language, MaxIterations: maxIterations, Warnings: warnings}
}

// LegacyMaxIterationsWarnings returns one warning per review gate in doc that
// still sets the retired per-gate max_iterations, sorted by gate id.
func LegacyMaxIterationsWarnings(doc Document) []string {
	ids := make([]string, 0, len(doc.ReviewGates))
	for id, g := range doc.ReviewGates {
		if g.LegacyMaxIterations != nil {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, fmt.Sprintf("review gate %q: max_iterations is no longer supported per gate and is ignored; set the top-level max_iterations (3/4/5) instead", id))
	}
	return out
}
