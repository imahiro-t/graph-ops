package engine

// ExtraGateDef defines a brand-new, ticket-specific review gate inline in a
// refine-ticket patch (as opposed to GateRef, which reuses an existing
// catalog entry).
type ExtraGateDef struct {
	Name          string `json:"name"`
	Criteria      string `json:"criteria"`
	MaxIterations *int   `json:"max_iterations,omitempty"`
}

// ExtraNode is one LLM-proposed addition to the base workflow template,
// submitted via `refine-ticket --patch`. Exactly one of GateRef/Gate should
// be set when Type is "review_gate": GateRef reuses an existing catalog
// gate by id, Gate defines a new one scoped to this ticket only.
type ExtraNode struct {
	ID         string        `json:"id"`
	Name       string        `json:"name,omitempty"`
	Type       string        `json:"type"`
	GateRef    string        `json:"gate_ref,omitempty"`
	Gate       *ExtraGateDef `json:"gate,omitempty"`
	DependsOn  []string      `json:"depends_on,omitempty"`
	LoopBackTo string        `json:"loop_back_to,omitempty"`
	IsManual   bool          `json:"is_manual,omitempty"`
}

// Patch is the top-level shape read from `refine-ticket --patch <file|->`.
type Patch struct {
	ExtraNodes []ExtraNode `json:"extra_nodes"`
}
