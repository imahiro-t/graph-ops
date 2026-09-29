package domain

// NodeClaim is what a claim records on a node (DFLT-00327): see GraphNode's
// Claimed*/Claim* fields. SessionID is "" when the claim was made outside a
// processing session.
type NodeClaim struct {
	Name           string
	NameIsFallback bool
	Token          string
	SessionID      string
	ClaimedAt      string
}

// The values of GraphNode.ClaimLease.
const (
	// ClaimLeaseLive: the claim's session (or autopilot run) has shown a
	// heartbeat recently enough that somebody is taken to be working on it.
	ClaimLeaseLive = "live"
	// ClaimLeaseExpired: the claim's session has been silent for longer
	// than its lease. The claim is not released automatically.
	ClaimLeaseExpired = "expired"
	// ClaimLeaseUnknown: the claim records no session, its session cannot
	// be found, or the data source cannot keep sessions -- whether anybody
	// is still working on it cannot be told.
	ClaimLeaseUnknown = "unknown"
	// ClaimLeaseLegacy: the node was claimed by a client that records no
	// claim at all (before DFLT-00327, or an HTTP data source older than
	// protocol 1.2).
	ClaimLeaseLegacy = "legacy"
)

// ProcessingSession is one run of process-ticket over a ticket
// (DFLT-00327), created by `graph-engine begin-session`. Its heartbeat --
// moved by every graph-engine call made with --session -- is what tells
// whether the claims made in it are still being worked on.
//
// RunID is the autopilot run the session belongs to ("" for a session a
// person started by hand): the run's own heartbeat then decides the lease,
// and two sessions of the same run count as the same claimer.
//
// Timestamps are RFC3339 UTC strings from the writing client's clock.
// Heartbeat is written with a fixed number of fractional digits
// (engine's sessionTimestamp) so that the SQL backends can compare two of
// them as strings.
type ProcessingSession struct {
	ID                  string `json:"id"`
	ProjectID           string `json:"project_id"`
	TicketID            string `json:"ticket_id"`
	ActorName           string `json:"actor_name"`
	ActorNameIsFallback bool   `json:"actor_name_is_fallback"`
	MachineID           string `json:"machine_id"`
	RunID               string `json:"run_id,omitempty"`
	StartedAt           string `json:"started_at"`
	Heartbeat           string `json:"heartbeat"`
}
