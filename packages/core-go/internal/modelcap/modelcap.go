// Package modelcap decides which model a graph node's subagent runs on
// (DFLT-00375): each node type has an assignment (the workflow catalog's
// node_models -- haiku, sonnet, opus, or inherit), a run may carry an
// explicit model cap chosen at launch, and the result is
// min(assignment, cap) -- with one twist that keeps today's behavior when
// nothing is capped: a result that is the calling session's own model family
// is returned as "" ("do not pass a model"), so the subagent inherits the
// session's exact model, version and variant (e.g. opus[1m]) included,
// instead of being switched to whatever the bare alias points at.
//
// Models are the three aliases both Claude Code's Agent tool and
// `claude --model` accept, ranked haiku < sonnet < opus. Other aliases a
// given environment may offer cannot be ranked against these and are
// rejected by Parse; a session whose model cannot be classified is treated
// as "unknown" (see Family), which degrades to "no cap" rather than failing.
package modelcap

import (
	"fmt"
	"strings"
)

// Model is one rankable model family alias. The zero value ("") means
// "none / unknown".
type Model string

const (
	Haiku  Model = "haiku"
	Sonnet Model = "sonnet"
	Opus   Model = "opus"
)

// Inherit is the node_models assignment meaning "the top tier": the node
// runs on the effective cap itself (the explicit cap, else the session's own
// model). It is an assignment value only -- never a cap (see Parse).
const Inherit = "inherit"

// Models lists the accepted model aliases, lowest first.
var Models = []Model{Haiku, Sonnet, Opus}

func (m Model) rank() int {
	switch m {
	case Haiku:
		return 1
	case Sonnet:
		return 2
	case Opus:
		return 3
	}
	return 0
}

// Parse accepts haiku, sonnet or opus (case-insensitive, surrounding spaces
// ignored) and rejects everything else -- inherit and "" included: a cap is
// either one of the three models or not given at all.
func Parse(s string) (Model, error) {
	m := Model(strings.ToLower(strings.TrimSpace(s)))
	if m.rank() == 0 {
		return "", fmt.Errorf("model must be haiku, sonnet or opus, got %q", s)
	}
	return m, nil
}

// ValidAssignment reports whether s is a valid node_models value: one of
// the three models or inherit (exact lowercase spelling -- it is a config
// file value, not user input to be forgiving about).
func ValidAssignment(s string) bool {
	return s == Inherit || Model(s).rank() > 0
}

// Family extracts the model family from a Claude Code model ID or alias:
// "claude-opus-4-5", "claude-sonnet-4-5-20250929", "us.anthropic.claude-haiku-...",
// "opus", "opus[1m]" and the like. It returns "" (unknown) when no family can
// be found -- e.g. an alias this package cannot rank. Unknown is the safe
// direction on purpose: Resolve then treats the session as uncapped, so
// top-tier nodes keep inheriting the session's model and only nodes
// explicitly assigned below the top get a model. If Claude model IDs ever
// change shape, the failure mode is "no cap", never a wrong cap.
func Family(s string) Model {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexByte(s, '['); i >= 0 {
		s = s[:i]
	}
	if m := Model(s); m.rank() > 0 {
		return m
	}
	if !strings.Contains(s, "claude") {
		return ""
	}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == '.' || r == ':' || r == '/' || r == '_' || r == '@'
	}) {
		if m := Model(part); m.rank() > 0 {
			return m
		}
	}
	return ""
}

// Resolve returns the model to pass as the Agent tool's model when starting
// the subagent of a node whose assignment is assigned (a node_models value;
// anything not a model is treated as inherit), given the explicit cap
// ("" = none) and the session's own model family ("" = unknown). "" means
// "do not pass a model".
//
//  1. The effective cap is cap when given, else session, else none.
//  2. The candidate is the effective cap for inherit (none -> ""), else
//     min(assigned, effective cap), or assigned when there is no cap.
//  3. A candidate equal to session becomes "": the subagent inherits the
//     session's own model unchanged (version and variant included).
func Resolve(assigned string, cap, session Model) Model {
	effective := cap
	if effective.rank() == 0 {
		effective = session
	}
	var candidate Model
	a := Model(assigned)
	switch {
	case a.rank() == 0: // inherit (or anything that is not a model)
		if effective.rank() > 0 {
			candidate = effective
		}
	case effective.rank() > 0 && effective.rank() < a.rank():
		candidate = effective
	default:
		candidate = a
	}
	if candidate != "" && candidate == session {
		return ""
	}
	return candidate
}
