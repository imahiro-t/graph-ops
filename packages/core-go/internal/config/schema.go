// Package config loads and merges the three-tier workflow configuration:
// plugin defaults (embedded) -> user (~/.graph-ops/config.yaml) ->
// project (.graph-ops/workflow.yaml), project taking precedence.
package config

import "fmt"

// ReviewGateDef describes one reusable review perspective. AdditionalCriteria
// is appended to (not replacing) whatever Criteria was inherited from a
// lower-priority layer; Criteria, when set, replaces it outright.
type ReviewGateDef struct {
	Name               string `yaml:"name,omitempty" json:"name,omitempty"`
	Criteria           string `yaml:"criteria,omitempty" json:"criteria,omitempty"`
	AdditionalCriteria string `yaml:"additional_criteria,omitempty" json:"additional_criteria,omitempty"`
	// LegacyMaxIterations is the retired per-gate max_iterations. It is
	// kept only so an old config file that still sets it can be detected
	// and warned about (see Merge's Warnings): it no longer affects
	// anything, is never serialized to JSON (so the settings API neither
	// shows nor accepts it), and SaveDocumentAt drops it before writing.
	// The iteration limit is now the workflow-wide Document.MaxIterations.
	LegacyMaxIterations *int  `yaml:"max_iterations,omitempty" json:"-"`
	Enabled             *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}

// NodeDef is one node in the workflow template. Gate references a
// ReviewGateDef by id and only applies to type: review_gate nodes.
type NodeDef struct {
	ID         string   `yaml:"id" json:"id"`
	Name       string   `yaml:"name,omitempty" json:"name,omitempty"`
	Type       string   `yaml:"type" json:"type"`
	Gate       string   `yaml:"gate,omitempty" json:"gate,omitempty"`
	DependsOn  []string `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
	LoopBackTo string   `yaml:"loop_back_to,omitempty" json:"loop_back_to,omitempty"`
	IsManual   bool     `yaml:"is_manual,omitempty" json:"is_manual,omitempty"`
	Enabled    *bool    `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}

type WorkflowDef struct {
	Nodes []NodeDef `yaml:"nodes,omitempty" json:"nodes,omitempty"`
	// Seed lists the node ids created immediately when process-ticket starts
	// (before the rest of the graph exists). Once every seed node is DONE,
	// the engine generates the remaining catalog nodes and attaches them to
	// the seed nodes. A layer that sets this replaces the inherited list
	// outright (no per-item merging, unlike Nodes/ReviewGates).
	Seed []string `yaml:"seed,omitempty" json:"seed,omitempty"`
}

// Document is the shape of one config layer (default/user/project file).
type Document struct {
	Version     int                      `yaml:"version" json:"version"`
	ReviewGates map[string]ReviewGateDef `yaml:"review_gates,omitempty" json:"review_gates,omitempty"`
	Workflow    WorkflowDef              `yaml:"workflow,omitempty" json:"workflow,omitempty"`
	// Language is a short locale code (e.g. "ja") selecting which
	// defaults/locales/<code>.yaml localizes the fixed workflow skeleton's
	// node names and the default review gates' names -- see locale.go. Unlike
	// Workflow.Nodes/Seed, this is a normal merge-across-tiers field (either
	// tier may set it, later tier wins; see Merge's doc comment) rather than
	// locked to the plugin default. Empty means "unset" -- no locale is
	// applied and the English plugin defaults stand.
	Language string `yaml:"language,omitempty" json:"language,omitempty"`
	// MaxIterations is the workflow-wide review iteration limit: the maximum
	// number of review rounds (counting the first review) a review loop may
	// run before a failing review blocks the ticket instead of looping back.
	// It must be one of AllowedMaxIterations; nil means "unset, inherit".
	// Later tiers win (see Merge), and the value in effect when a node is
	// created is stored on that node, so changing it later does not affect
	// tickets already in progress.
	MaxIterations *int `yaml:"max_iterations,omitempty" json:"max_iterations,omitempty"`
}

// DefaultMaxIterations is the workflow-wide review iteration limit used when
// no tier sets Document.MaxIterations.
const DefaultMaxIterations = 3

// AllowedMaxIterations lists the values Document.MaxIterations may take.
var AllowedMaxIterations = []int{3, 4, 5}

// ValidateMaxIterations reports whether v is one of AllowedMaxIterations.
func ValidateMaxIterations(v int) error {
	for _, a := range AllowedMaxIterations {
		if v == a {
			return nil
		}
	}
	return fmt.Errorf("max_iterations must be 3, 4 or 5, got %d", v)
}

// Catalog is the fully merged, ready-to-use result of all three layers.
type Catalog struct {
	ReviewGates map[string]ReviewGateDef `json:"review_gates"`
	Nodes       []NodeDef                `json:"nodes"`
	Seed        []string                 `json:"seed"`
	// Language is the resolved locale code that produced this Catalog (see
	// Document.Language and ResolveLanguage) -- "" if none of the merged
	// tiers set one.
	Language string `json:"language,omitempty"`
	// MaxIterations is the resolved workflow-wide review iteration limit
	// (see Document.MaxIterations); DefaultMaxIterations when no tier sets
	// one.
	MaxIterations int `json:"max_iterations"`
	// Warnings lists non-fatal problems found while merging, e.g. a review
	// gate that still sets the retired per-gate max_iterations.
	Warnings []string `json:"warnings,omitempty"`
}

// EnabledReviewGates returns the catalog's review gates with Enabled=false
// entries removed.
func (c Catalog) EnabledReviewGates() map[string]ReviewGateDef {
	out := make(map[string]ReviewGateDef, len(c.ReviewGates))
	for id, g := range c.ReviewGates {
		if g.Enabled != nil && !*g.Enabled {
			continue
		}
		out[id] = g
	}
	return out
}

// EnabledNodes returns the catalog's node definitions with Enabled=false
// entries removed, preserving declaration order.
func (c Catalog) EnabledNodes() []NodeDef {
	out := make([]NodeDef, 0, len(c.Nodes))
	for _, n := range c.Nodes {
		if n.Enabled != nil && !*n.Enabled {
			continue
		}
		out = append(out, n)
	}
	return out
}
