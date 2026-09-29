package domain

import "encoding/json"

// AutopilotRunRecord is an autopilot run as the data source shares it
// between members (DFLT-00326): the columns the duplicate-start check and
// listings need, plus Snapshot, the run's shared view (internal/autopilot's
// Run without what only makes sense on the machine that runs it -- its
// terminal tty, worktree paths, branch names, fingerprints). The data source
// stores Snapshot as opaque JSON and returns it unchanged.
//
// Timestamps are RFC3339Nano UTC strings, written from the writing client's
// clock like every other timestamp in the store.
type AutopilotRunRecord struct {
	ID           string `json:"id"`
	ProjectID    string `json:"project_id"`
	RootTicketID string `json:"root_ticket_id"`
	Mode         string `json:"mode"`
	State        string `json:"state"`
	Heartbeat    string `json:"heartbeat"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	// StartedByName is the display name of whoever started (or last took
	// over) the run. For display only, never compared.
	StartedByName string `json:"started_by_name"`
	// MachineID is the starting machine's ID (internal/identity): only that
	// machine may take the run over.
	MachineID string `json:"machine_id"`
	// Revision orders the writes of one run: a save carrying a revision not
	// greater than the stored one is ignored, so a copy that arrives late
	// never rolls the record back.
	Revision int64           `json:"revision"`
	Snapshot json.RawMessage `json:"snapshot"`
}
