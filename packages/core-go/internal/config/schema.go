// Package config loads and merges the three-tier workflow configuration:
// plugin defaults (embedded) -> user (~/.graph-ops/config.yaml) ->
// project (.graph-ops/workflow.yaml), project taking precedence.
package config

import (
	"fmt"
	"sort"

	"github.com/graph-ops/core-go/internal/modelcap"
)

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
	// node names and the default review gates' names -- see locale.go. Empty
	// means "unset" -- no locale is applied and the English plugin defaults
	// stand.
	//
	// The working language is a personal setting (DFLT-00153): only the user
	// tier's config.yaml is consulted. A team tier's workflow.yaml that sets
	// it is ignored with a WarnTeamLanguageIgnored warning -- LoadWithRoots
	// and the CLI's get-language-settings drop it before resolving. Merge
	// itself still treats the field generically (later non-empty wins) over
	// whatever documents it is handed.
	Language string `yaml:"language,omitempty" json:"language,omitempty"`
	// MaxIterations is the workflow-wide review iteration limit: the maximum
	// number of review rounds (counting the first review) a review loop may
	// run before a failing review blocks the ticket instead of looping back.
	// It must be one of AllowedMaxIterations; nil means "unset, inherit".
	// Later tiers win (see Merge), and the value in effect when a node is
	// created is stored on that node, so changing it later does not affect
	// tickets already in progress.
	MaxIterations *int `yaml:"max_iterations,omitempty" json:"max_iterations,omitempty"`
	// NodeModels assigns a model to node types (DFLT-00375): the key is a
	// node type (built-in or custom), the value one of haiku, sonnet, opus
	// or inherit (the top tier: the run's cap, else the session's own
	// model). A type no tier mentions is inherit. Tiers merge per key, later
	// wins (team beats user beats the plugin default), and a value outside
	// those four makes LoadWithRoots refuse the file (the settings API
	// reports WarnNodeModelInvalid instead). See internal/modelcap for how an
	// assignment and the launch-time cap combine.
	NodeModels map[string]string `yaml:"node_models,omitempty" json:"node_models,omitempty"`
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
	// Document.Language and ResolveLanguage) -- "" if the user tier does not
	// set one (a team tier's language is never used; see Document.Language).
	Language string `json:"language,omitempty"`
	// MaxIterations is the resolved workflow-wide review iteration limit
	// (see Document.MaxIterations); DefaultMaxIterations when no tier sets
	// one.
	MaxIterations int `json:"max_iterations"`
	// NodeModels is the merged node-type -> model assignment (see
	// Document.NodeModels). Use ModelFor to read it: a type missing here is
	// inherit.
	NodeModels map[string]string `json:"node_models"`
	// Warnings lists non-fatal problems found while merging, e.g. a review
	// gate that still sets the retired per-gate max_iterations. It is not
	// part of the catalog's JSON shape: the CLI prints each Message() to
	// stderr, and the settings API returns the structured values in its own
	// top-level "warnings" field.
	Warnings []Warning `json:"-"`
}

// Warning codes. A code is a fixed, machine-readable identifier: the Web UI
// translates it (packages/web's SETTINGS_CATALOG_WARNINGS and the
// settings.reviewGates.warnings.* messages), while the CLI prints the English
// Message() instead.
const (
	// WarnLegacyGateMaxIterations: a review gate (GateID) still sets the
	// retired per-gate max_iterations, which is ignored.
	WarnLegacyGateMaxIterations = "LEGACY_GATE_MAX_ITERATIONS"
	// WarnMaxIterationsOutOfRange: a tier's own top-level max_iterations
	// (Value) is not one of AllowedMaxIterations. LoadWithRoots refuses such
	// a file outright; the settings API reports it with this code instead so
	// the screen can show it and let the user pick a valid value.
	WarnMaxIterationsOutOfRange = "MAX_ITERATIONS_OUT_OF_RANGE"
	// WarnTeamLanguageIgnored: the team tier's workflow.yaml sets language
	// (Language), which is ignored because the working language is a
	// personal setting (DFLT-00153). Only the CLI reports it: the settings
	// API merges the user tier alone, so this code never reaches the Web UI.
	WarnTeamLanguageIgnored = "TEAM_LANGUAGE_IGNORED"
	// WarnNodeModelInvalid: a tier's node_models assigns node type NodeType
	// a value (NodeModel) other than haiku, sonnet, opus or inherit.
	// LoadWithRoots refuses such a file; the settings API reports it with
	// this code instead.
	WarnNodeModelInvalid = "NODE_MODEL_INVALID"
)

// Warning is one non-fatal configuration problem, as structured data so a
// client can localize it: Code says what it is, GateID/Value/Language carry
// the details the message needs.
type Warning struct {
	Code     string `json:"code"`
	GateID   string `json:"gate_id,omitempty"`
	Value    *int   `json:"value,omitempty"`
	Language string `json:"language,omitempty"`
	NodeType string `json:"node_type,omitempty"`
	// NodeModel is the refused node_models value. A pointer so an empty
	// string value is still sent (and shown) rather than omitted.
	NodeModel *string `json:"node_model,omitempty"`
}

// Message is the English, developer-facing text for w -- what the CLI prints
// to stderr. The Web UI never shows it; it translates Code instead.
func (w Warning) Message() string {
	switch w.Code {
	case WarnLegacyGateMaxIterations:
		return fmt.Sprintf("review gate %q: max_iterations is no longer supported per gate and is ignored; set the top-level max_iterations (3/4/5) instead", w.GateID)
	case WarnMaxIterationsOutOfRange:
		if w.Value != nil {
			return fmt.Sprintf("max_iterations must be 3, 4 or 5, got %d", *w.Value)
		}
		return "max_iterations must be 3, 4 or 5"
	case WarnTeamLanguageIgnored:
		return fmt.Sprintf("team workflow.yaml sets language %q, which is ignored: the working language is a personal setting (run the onboarding skill, or set language in your own config.yaml)", w.Language)
	case WarnNodeModelInvalid:
		v := ""
		if w.NodeModel != nil {
			v = *w.NodeModel
		}
		return fmt.Sprintf("node_models: node type %q must be haiku, sonnet, opus or inherit, got %q", w.NodeType, v)
	default:
		return w.Code
	}
}

// Messages returns each warning's Message(), in order.
func Messages(warnings []Warning) []string {
	out := make([]string, 0, len(warnings))
	for _, w := range warnings {
		out = append(out, w.Message())
	}
	return out
}

// MaxIterationsWarnings returns a WarnMaxIterationsOutOfRange warning when
// doc's own top-level max_iterations is set to a value outside
// AllowedMaxIterations (e.g. a hand-edited 7), and nothing otherwise.
func MaxIterationsWarnings(doc Document) []Warning {
	if doc.MaxIterations == nil || ValidateMaxIterations(*doc.MaxIterations) == nil {
		return nil
	}
	v := *doc.MaxIterations
	return []Warning{{Code: WarnMaxIterationsOutOfRange, Value: &v}}
}

// NodeModelWarnings returns one WarnNodeModelInvalid warning per node type
// in doc's node_models whose value is not haiku, sonnet, opus or inherit,
// sorted by node type.
func NodeModelWarnings(doc Document) []Warning {
	types := make([]string, 0, len(doc.NodeModels))
	for t, v := range doc.NodeModels {
		if !modelcap.ValidAssignment(v) {
			types = append(types, t)
		}
	}
	sort.Strings(types)
	out := make([]Warning, 0, len(types))
	for _, t := range types {
		v := doc.NodeModels[t]
		out = append(out, Warning{Code: WarnNodeModelInvalid, NodeType: t, NodeModel: &v})
	}
	return out
}

// ModelFor returns nodeType's model assignment: haiku, sonnet, opus or
// inherit. A type the catalog does not assign -- a custom type no tier
// mentions, say -- is inherit.
func (c Catalog) ModelFor(nodeType string) string {
	if v, ok := c.NodeModels[nodeType]; ok && modelcap.ValidAssignment(v) {
		return v
	}
	return modelcap.Inherit
}

// TeamLanguageIgnoredWarnings returns a WarnTeamLanguageIgnored warning when
// the team tier's document sets language, and nothing otherwise. Callers that
// resolve the language (LoadWithRoots, the CLI's get-language-settings) use
// it so the same message is printed wherever the team value is dropped.
func TeamLanguageIgnoredWarnings(teamDoc Document) []Warning {
	if teamDoc.Language == "" {
		return nil
	}
	return []Warning{{Code: WarnTeamLanguageIgnored, Language: teamDoc.Language}}
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
