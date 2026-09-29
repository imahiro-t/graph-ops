package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/identity"
)

// This file holds the CLI side of processing sessions and node claims
// (DFLT-00327): begin-session, the --session flag the node commands take,
// and get-executable's output type that carries the claim token.

// sessionWarnWriter is where session and claim warnings go (stderr; a
// variable so tests can capture it).
var sessionWarnWriter io.Writer = os.Stderr

func emitSessionWarnings(warnings []string) {
	for _, w := range warnings {
		fmt.Fprintln(sessionWarnWriter, "graph-engine: warning: "+w)
	}
}

// takeFlagValue removes every "<flag> <value>" pair from args and returns
// the last value ("" when the flag is absent) with the remaining arguments
// in order. A flag without a value is an error.
func takeFlagValue(args []string, flag, usage string) (string, []string, error) {
	var value string
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] != flag {
			rest = append(rest, args[i])
			continue
		}
		if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
			return "", nil, fmt.Errorf("%s: %s requires a value", usage, flag)
		}
		value = args[i+1]
		i++
	}
	return value, rest, nil
}

// touchSession records a heartbeat of the caller's processing session
// before a command's own work, warning (never failing) when it cannot.
func touchSession(eng *engine.GraphEngine, sessionID string) {
	if sessionID == "" {
		return
	}
	emitSessionWarnings(eng.TouchSession(sessionID))
}

// claimedNodeView is a node as get-executable prints it: the node plus the
// token of the claim this call made on it. domain.GraphNode never
// serializes its token (so no other output can leak it); this is the one
// output that hands it out -- to the caller that took the node, which
// passes it back with `complete-node --claim`.
type claimedNodeView struct {
	domain.GraphNode
	ClaimToken string `json:"claim_token,omitempty"`
}

func claimedNodeViews(nodes []domain.GraphNode) []claimedNodeView {
	out := make([]claimedNodeView, 0, len(nodes))
	for _, n := range nodes {
		v := claimedNodeView{GraphNode: n}
		if n.ClaimToken != nil {
			v.ClaimToken = *n.ClaimToken
		}
		out = append(out, v)
	}
	return out
}

// cmdBeginSession implements `begin-session <ticketId> [--run <runId>]`:
// it starts a processing session of the ticket and prints
// {"session_id", "lease_minutes", "sessions_supported", "others", "same_run"}.
// When somebody else is processing the ticket ("others"), it says so on
// stderr -- and still exits 0: it warns, it never blocks.
func cmdBeginSession(eng *engine.GraphEngine, rc runtimeConfig, args []string) error {
	const usage = `usage: graph-engine begin-session <ticketId> [--run <runId>]`
	runID, rest, err := takeFlagValue(args, "--run", usage)
	if err != nil {
		return err
	}
	if len(rest) != 1 || strings.HasPrefix(rest[0], "-") {
		return fmt.Errorf(usage)
	}
	actor, err := identity.Resolve(rc.HomeDir)
	if err != nil {
		// Without a machine ID the session still works; only "the same
		// machine" cannot be told.
		name, fallback := identity.DisplayName(rc.HomeDir)
		actor = identity.Actor{Name: name, NameIsFallback: fallback}
		emitSessionWarnings([]string{fmt.Sprintf("this machine's ID could not be read (%v); the session is begun without it", err)})
	}
	res, err := eng.BeginSession(rest[0], runID, actor)
	if err != nil {
		return err
	}
	emitSessionWarnings(res.Warnings)
	if len(res.Others) > 0 {
		emitSessionWarnings([]string{othersWarning(rest[0], res.Others, time.Now())})
	}
	return printJSON(res)
}

// othersWarning is begin-session's warning that other sessions are
// processing the ticket, naming each: who, which nodes, how long ago it was
// last heard from.
func othersWarning(ticketID string, others []engine.SessionPeer, now time.Time) string {
	parts := make([]string, 0, len(others))
	for _, o := range others {
		name := o.Name
		if name == "" {
			name = "an unnamed member"
		} else if o.NameIsFallback {
			name += " (name not set)"
		}
		if o.SameMachine {
			name += " (another session on this machine)"
		}
		var detail []string
		if len(o.NodeIDs) > 0 {
			detail = append(detail, "nodes "+strings.Join(o.NodeIDs, ", "))
		} else {
			detail = append(detail, "no node claimed right now")
		}
		detail = append(detail, "last heartbeat "+agoText(o.Heartbeat, now))
		detail = append(detail, "session "+o.SessionID)
		parts = append(parts, name+" ("+strings.Join(detail, "; ")+")")
	}
	return fmt.Sprintf("ticket %s is being processed by another session: %s. Carrying on is allowed, but do not unstick or redo the nodes they hold", ticketID, strings.Join(parts, "; "))
}

// agoText renders how long ago ts was, in whole minutes.
func agoText(ts string, now time.Time) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts
	}
	m := int(now.Sub(t) / time.Minute)
	if m < 1 {
		return "just now"
	}
	return fmt.Sprintf("%d min ago", m)
}
