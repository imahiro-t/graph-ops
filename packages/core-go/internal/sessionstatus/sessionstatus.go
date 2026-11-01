// Package sessionstatus keeps the small per-session status file the
// graph-ops plugin's mod draws above Claude Code's prompt (DFLT-00383): which
// ticket this Claude Code session is working on and what it is doing right
// now, and, for an autopilot controller, how far the run has got.
//
// graph-engine is the only writer. It keys the file by the Claude Code
// session ID that Claude Code puts into every Bash tool call's environment
// (CLAUDE_CODE_SESSION_ID), so two terminals never see each other's status:
//
//	<HOME>/.graph-ops/session-status/<sessionID>.json
//
// The mod is the only reader: it asks Claude Code for its own session's ID
// and reads that one file. Everything here is best effort. A failure to
// write is reported to the caller, which only warns on stderr: a status
// band must never change what a graph-engine command prints or how it exits.
//
// Every string a status holds is made safe to draw on a terminal before it
// is written (Normalize): titles and node names come from the data source,
// which other members can write, and the detail from a skill's arguments.
package sessionstatus

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/graph-ops/core-go/internal/displayname"
)

// EnvSessionID is the environment variable Claude Code sets, in every Bash
// tool call it runs, to the ID of the session (the main conversation's ID,
// also inside the subagents it starts). Without it graph-engine writes no
// status at all.
const EnvSessionID = "CLAUDE_CODE_SESSION_ID"

// DirName is the directory, under the graph-ops user directory
// ($HOME/.graph-ops), that holds the status files.
const DirName = "session-status"

// Version is the status file's format version.
const Version = 1

// PruneAfter is how long a status file nobody has written may stay: a
// session that ended without clearing its status (a crash, a closed
// terminal) leaves one behind, and the next write of any session removes it
// once it is this old. The mod never hides a status for being old -- a
// session can wait days for a person's approval -- so this is the only
// clean-up of a forgotten file.
const PruneAfter = 7 * 24 * time.Hour

// Limits of what a status holds, so a band stays a few lines.
const (
	// MaxSessionIDLen bounds a session ID used as a file name.
	MaxSessionIDLen = 128
	// MaxDetailRunes bounds the free-form detail (and the controller's
	// awaiting-human note).
	MaxDetailRunes = 200
	// MaxNodes bounds the running nodes a status lists.
	MaxNodes = 8
	// maxTypeRunes bounds a node type, a mode and a language code.
	maxTypeRunes = 64
)

// Phases: what the session is doing. A closed set, so the mod can label
// each in the session's language.
const (
	PhaseRefining         = "refining"
	PhasePlanning         = "planning"
	PhaseRunning          = "running"
	PhaseAwaitingApproval = "awaiting_approval"
	PhaseAwaitingHuman    = "awaiting_human"
	PhaseReleasing        = "releasing"
	PhaseFinishing        = "finishing"
	PhaseIdle             = "idle"
)

// Phases lists every phase, in the order a ticket normally goes through them.
var Phases = []string{PhaseRefining, PhasePlanning, PhaseRunning, PhaseAwaitingApproval, PhaseAwaitingHuman, PhaseReleasing, PhaseFinishing, PhaseIdle}

// Roles: a session that works one ticket (process-ticket, refine-ticket,
// autopilot-worker) or one that drives an autopilot run (autopilot-ticket,
// autopilot-tree).
const (
	RoleWorker     = "worker"
	RoleController = "controller"
)

// Outcomes of a ticket in an autopilot run, as the controller shows its
// latest result.
var outcomes = map[string]bool{"done": true, "failed": true, "blocked": true, "skipped": true}

// Status is one session's status file.
type Status struct {
	Version   int    `json:"version"`
	SessionID string `json:"session_id"`
	UpdatedAt string `json:"updated_at"`
	// Language is the language the band's labels are drawn in (the
	// configured one; "" for English).
	Language string  `json:"language,omitempty"`
	Role     string  `json:"role,omitempty"`
	Ticket   *Ticket `json:"ticket,omitempty"`
	Phase    string  `json:"phase,omitempty"`
	Detail   string  `json:"detail,omitempty"`
	// Nodes are the ticket's nodes being worked right now.
	Nodes []Node `json:"nodes,omitempty"`
	// Autopilot is the run a controller drives; nil for a worker.
	Autopilot *Autopilot `json:"autopilot,omitempty"`
}

// Ticket names the ticket a session works on.
type Ticket struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

// Node is one node being worked.
type Node struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
}

// Autopilot is what a controller shows about its run.
type Autopilot struct {
	RunID        string `json:"run_id"`
	Mode         string `json:"mode,omitempty"`
	RootTicketID string `json:"root_ticket_id,omitempty"`
	State        string `json:"state,omitempty"`
	Counts       Counts `json:"counts"`
	// Current is the ticket whose worker session is running, nil when none.
	Current *RunTicket `json:"current,omitempty"`
	// AwaitingHuman is what the current worker waits for a person on.
	AwaitingHuman string `json:"awaiting_human,omitempty"`
	// Last is the latest ticket that reached a result.
	Last *RunTicket `json:"last,omitempty"`
}

// Counts are a run's tickets by state.
type Counts struct {
	Done       int `json:"done"`
	InProgress int `json:"in_progress"`
	Remaining  int `json:"remaining"`
	Blocked    int `json:"blocked"`
	Failed     int `json:"failed"`
	Skipped    int `json:"skipped"`
}

// RunTicket is a ticket of a run, with its outcome when it has one.
type RunTicket struct {
	TicketID string `json:"ticket_id"`
	Title    string `json:"title,omitempty"`
	Outcome  string `json:"outcome,omitempty"`
}

// ValidSessionID reports whether id can name a status file: 1 to
// MaxSessionIDLen ASCII letters, digits, '-' and '_'. Claude Code's session
// IDs are UUIDs; anything else (a path separator, "..") is never written.
func ValidSessionID(id string) bool {
	if id == "" || len(id) > MaxSessionIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// ValidPhase reports whether p is one of Phases.
func ValidPhase(p string) bool {
	for _, known := range Phases {
		if p == known {
			return true
		}
	}
	return false
}

// ValidRole reports whether r is RoleWorker or RoleController.
func ValidRole(r string) bool { return r == RoleWorker || r == RoleController }

// Dir is the status directory under graphOpsDir ($HOME/.graph-ops).
func Dir(graphOpsDir string) string { return filepath.Join(graphOpsDir, DirName) }

// Path is sessionID's status file under graphOpsDir. It refuses an ID
// ValidSessionID does not accept, so no ID can name a file elsewhere.
func Path(graphOpsDir, sessionID string) (string, error) {
	if !ValidSessionID(sessionID) {
		return "", fmt.Errorf("session ID %q cannot name a status file", truncate(displayname.Sanitize(sessionID), 40))
	}
	return filepath.Join(Dir(graphOpsDir), sessionID+".json"), nil
}

// Read loads sessionID's status, nil when there is none. A file that does
// not parse reads as none too: the next write replaces it.
func Read(graphOpsDir, sessionID string) (*Status, error) {
	path, err := Path(graphOpsDir, sessionID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st Status
	if json.Unmarshal(data, &st) != nil {
		return nil, nil
	}
	return &st, nil
}

// Update reads sessionID's status (an empty one when there is none), lets
// fn change it, and writes it back normalized, stamped with now. It writes
// atomically (a temporary file renamed over the old one), so the mod never
// reads half a file. When the file is new, it also prunes the forgotten
// files of other sessions (Prune).
//
// Two graph-engine commands of the same session running at once can lose
// one of their updates; the next command writes the status again, so a band
// is at worst one step behind.
func Update(graphOpsDir, sessionID string, now time.Time, fn func(*Status)) (*Status, error) {
	path, err := Path(graphOpsDir, sessionID)
	if err != nil {
		return nil, err
	}
	old, err := Read(graphOpsDir, sessionID)
	if err != nil {
		return nil, err
	}
	st := Status{}
	if old != nil {
		st = *old
	}
	fn(&st)
	st.Version = Version
	st.SessionID = sessionID
	st.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	st = Normalize(st)
	if err := write(path, st); err != nil {
		return nil, err
	}
	if old == nil {
		Prune(graphOpsDir, now)
	}
	return &st, nil
}

// Clear removes sessionID's status (nothing when there is none) and prunes
// the forgotten files of other sessions.
func Clear(graphOpsDir, sessionID string, now time.Time) (bool, error) {
	path, err := Path(graphOpsDir, sessionID)
	if err != nil {
		return false, err
	}
	err = os.Remove(path)
	Prune(graphOpsDir, now)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Prune removes the status files (and leftover temporary files) in
// graphOpsDir's status directory that nobody has written for PruneAfter. It
// goes by the file's modification time, which every write sets. Errors are
// ignored: pruning is housekeeping.
func Prune(graphOpsDir string, now time.Time) {
	dir := Dir(graphOpsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !(strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".tmp")) {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < PruneAfter {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// write saves st at path through a temporary file in the same directory,
// the directory private to the user (0700) and the file too (0600).
func write(path string, st Status) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".status-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// Normalize returns st with every string made safe to draw on a terminal and
// cut to the band's limits:
//
//   - titles, node names and types, the mode and the run state go through
//     displayname.Sanitize (no control characters, escape sequences, line
//     breaks, bidirectional or zero-width characters; at most 128 runes);
//   - the detail and the awaiting-human note go through displayname.Text and
//     are then cut to MaxDetailRunes;
//   - IDs (tickets, nodes, the run) that are not ID-shaped become "", and a
//     ticket or run ticket without an ID is dropped;
//   - a phase, role, outcome or language outside its set becomes "";
//   - at most MaxNodes nodes are kept, and negative counts become 0.
//
// Normalize is idempotent.
func Normalize(st Status) Status {
	out := Status{
		Version:   st.Version,
		SessionID: st.SessionID,
		UpdatedAt: st.UpdatedAt,
		Language:  language(st.Language),
		Detail:    truncate(displayname.Text(st.Detail), MaxDetailRunes),
	}
	if !ValidSessionID(out.SessionID) {
		out.SessionID = ""
	}
	if displayname.Timestamp(out.UpdatedAt) != out.UpdatedAt {
		out.UpdatedAt = ""
	}
	if ValidRole(st.Role) {
		out.Role = st.Role
	}
	if ValidPhase(st.Phase) {
		out.Phase = st.Phase
	}
	if st.Ticket != nil {
		if id := cleanID(st.Ticket.ID); id != "" {
			out.Ticket = &Ticket{ID: id, Title: displayname.Sanitize(st.Ticket.Title)}
		}
	}
	for _, n := range st.Nodes {
		if len(out.Nodes) >= MaxNodes {
			break
		}
		id := cleanID(n.ID)
		if id == "" {
			continue
		}
		out.Nodes = append(out.Nodes, Node{ID: id, Name: displayname.Sanitize(n.Name), Type: truncate(displayname.Sanitize(n.Type), maxTypeRunes)})
	}
	if a := st.Autopilot; a != nil {
		if runID := cleanID(a.RunID); runID != "" {
			out.Autopilot = &Autopilot{
				RunID:         runID,
				Mode:          truncate(displayname.Sanitize(a.Mode), maxTypeRunes),
				RootTicketID:  cleanID(a.RootTicketID),
				State:         truncate(displayname.Sanitize(a.State), maxTypeRunes),
				Counts:        nonNegative(a.Counts),
				Current:       runTicket(a.Current),
				AwaitingHuman: truncate(displayname.Text(a.AwaitingHuman), MaxDetailRunes),
				Last:          runTicket(a.Last),
			}
		}
	}
	return out
}

func runTicket(t *RunTicket) *RunTicket {
	if t == nil {
		return nil
	}
	id := cleanID(t.TicketID)
	if id == "" {
		return nil
	}
	out := &RunTicket{TicketID: id, Title: displayname.Sanitize(t.Title)}
	if outcomes[t.Outcome] {
		out.Outcome = t.Outcome
	}
	return out
}

func nonNegative(c Counts) Counts {
	for _, p := range []*int{&c.Done, &c.InProgress, &c.Remaining, &c.Blocked, &c.Failed, &c.Skipped} {
		if *p < 0 {
			*p = 0
		}
	}
	return c
}

// cleanID is id when displayname.ID accepts it, "" otherwise.
func cleanID(id string) string {
	if id == "" || displayname.ID(id) != id {
		return ""
	}
	return id
}

// language keeps a language code made of letters, digits, '-' and '_'
// (ja, en, pt-BR), "" otherwise.
func language(code string) string {
	if code == "" || len(code) > maxTypeRunes {
		return ""
	}
	for i := 0; i < len(code); i++ {
		c := code[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return ""
		}
	}
	return code
}

// truncate cuts s to at most max runes (s is valid UTF-8 here, or nearly:
// invalid bytes count as one rune each).
func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return strings.TrimRight(s[:i], " ")
		}
		n++
	}
	return s
}
