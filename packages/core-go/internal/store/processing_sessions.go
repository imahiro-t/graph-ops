package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/graph-ops/core-go/internal/domain"
)

// This file is where processing sessions are kept (DFLT-00327): the
// processing_sessions table of both SQL backends and the processing-sessions
// endpoints of an HTTP data source speaking protocol 1.2.

// ErrProcessingSessionsUnsupported is what a ProcessingSessionStore answers
// when its data source cannot keep processing sessions: an HTTP data source
// older than protocol 1.2. The engine then still records who claimed a node,
// but cannot tell whether the claim is still being worked on (its lease is
// "unknown").
var ErrProcessingSessionsUnsupported = errors.New("the data source does not keep processing sessions (an HTTP data source needs protocol 1.2 or newer)")

// ProcessingSessionStore is an optional add-on to GraphRepository, like
// AutopilotRunStore: keeping processing sessions (domain.ProcessingSession)
// in the data source. It is kept off GraphRepository so the fakes and test
// repositories do not have to grow it, and so an HTTP data source older than
// 1.2 can say it has none (ErrProcessingSessionsUnsupported).
type ProcessingSessionStore interface {
	// SaveProcessingSession creates the session, or replaces every field of
	// the stored one with the same ID.
	SaveProcessingSession(s domain.ProcessingSession) error
	// TouchProcessingSession moves the session's heartbeat to heartbeat,
	// unless the stored one is already that late or later (then it changes
	// nothing and still succeeds). found is false when there is no such
	// session. heartbeat must be written with a fixed number of fractional
	// digits (see domain.ProcessingSession), since the SQL backends compare
	// it with the stored one as a string.
	TouchProcessingSession(id, heartbeat string) (found bool, err error)
	// GetProcessingSession returns nil (not an error) when there is no such
	// session.
	GetProcessingSession(id string) (*domain.ProcessingSession, error)
	// ListProcessingSessionsByTickets returns every session of the given
	// tickets, in no particular order. IDs may repeat.
	ListProcessingSessionsByTickets(ticketIDs []string) ([]domain.ProcessingSession, error)
	// DeleteProcessingSession removes a session; a missing one is not an
	// error.
	DeleteProcessingSession(id string) error
}

var (
	_ ProcessingSessionStore = (*SQLiteRepository)(nil)
	_ ProcessingSessionStore = (*MySQLRepository)(nil)
	_ ProcessingSessionStore = (*HTTPRepository)(nil)
)

const processingSessionCols = "id, project_id, ticket_id, actor_name, actor_name_is_fallback, machine_id, run_id, started_at, heartbeat"

func scanProcessingSession(row interface{ Scan(dest ...any) error }) (domain.ProcessingSession, error) {
	var s domain.ProcessingSession
	var fallback int
	var runID sql.NullString
	if err := row.Scan(&s.ID, &s.ProjectID, &s.TicketID, &s.ActorName, &fallback, &s.MachineID, &runID, &s.StartedAt, &s.Heartbeat); err != nil {
		return domain.ProcessingSession{}, err
	}
	s.ActorNameIsFallback = fallback != 0
	if runID.Valid {
		s.RunID = runID.String
	}
	return s, nil
}

func runIDOrNull(runID string) sql.NullString {
	return sql.NullString{String: runID, Valid: runID != ""}
}

// saveProcessingSession upserts s. There is one writer per session (the
// process that began it), so the check-then-write needs no lock.
func saveProcessingSession(db *sql.DB, s domain.ProcessingSession) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM processing_sessions WHERE id = ?`, s.ID).Scan(&n); err != nil {
		return fmt.Errorf("reading processing session %s: %w", s.ID, err)
	}
	if n == 0 {
		if _, err := db.Exec(`INSERT INTO processing_sessions (`+processingSessionCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			s.ID, s.ProjectID, s.TicketID, s.ActorName, boolToInt(s.ActorNameIsFallback), s.MachineID, runIDOrNull(s.RunID), s.StartedAt, s.Heartbeat); err != nil {
			return fmt.Errorf("inserting processing session %s: %w", s.ID, err)
		}
		return nil
	}
	if _, err := db.Exec(`UPDATE processing_sessions SET project_id = ?, ticket_id = ?, actor_name = ?, actor_name_is_fallback = ?,
		machine_id = ?, run_id = ?, started_at = ?, heartbeat = ? WHERE id = ?`,
		s.ProjectID, s.TicketID, s.ActorName, boolToInt(s.ActorNameIsFallback), s.MachineID, runIDOrNull(s.RunID), s.StartedAt, s.Heartbeat, s.ID); err != nil {
		return fmt.Errorf("updating processing session %s: %w", s.ID, err)
	}
	return nil
}

func touchProcessingSession(db *sql.DB, id, heartbeat string) (bool, error) {
	res, err := db.Exec(`UPDATE processing_sessions SET heartbeat = ? WHERE id = ? AND heartbeat < ?`, heartbeat, id, heartbeat)
	if err != nil {
		return false, fmt.Errorf("updating the heartbeat of processing session %s: %w", id, err)
	}
	if affected, err := res.RowsAffected(); err == nil && affected > 0 {
		return true, nil
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM processing_sessions WHERE id = ?`, id).Scan(&n); err != nil {
		return false, fmt.Errorf("reading processing session %s: %w", id, err)
	}
	return n > 0, nil
}

func getProcessingSession(db *sql.DB, id string) (*domain.ProcessingSession, error) {
	s, err := scanProcessingSession(db.QueryRow(`SELECT `+processingSessionCols+` FROM processing_sessions WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading processing session %s: %w", id, err)
	}
	return &s, nil
}

func listProcessingSessionsByTickets(db *sql.DB, ticketIDs []string) ([]domain.ProcessingSession, error) {
	out := []domain.ProcessingSession{}
	for _, chunk := range chunkIDs(dedupeIDs(ticketIDs), ticketGraphIDChunk) {
		ph, args := inPlaceholders(chunk)
		rows, err := db.Query(`SELECT `+processingSessionCols+` FROM processing_sessions WHERE ticket_id IN (`+ph+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("listing processing sessions of %d tickets: %w", len(chunk), err)
		}
		for rows.Next() {
			s, err := scanProcessingSession(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, s)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func deleteProcessingSession(db *sql.DB, id string) error {
	if _, err := db.Exec(`DELETE FROM processing_sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("deleting processing session %s: %w", id, err)
	}
	return nil
}

// --- SQLite ---

// SaveProcessingSession implements ProcessingSessionStore.
func (r *SQLiteRepository) SaveProcessingSession(s domain.ProcessingSession) error {
	return saveProcessingSession(r.db, s)
}

// TouchProcessingSession implements ProcessingSessionStore.
func (r *SQLiteRepository) TouchProcessingSession(id, heartbeat string) (bool, error) {
	return touchProcessingSession(r.db, id, heartbeat)
}

// GetProcessingSession implements ProcessingSessionStore.
func (r *SQLiteRepository) GetProcessingSession(id string) (*domain.ProcessingSession, error) {
	return getProcessingSession(r.db, id)
}

// ListProcessingSessionsByTickets implements ProcessingSessionStore.
func (r *SQLiteRepository) ListProcessingSessionsByTickets(ticketIDs []string) ([]domain.ProcessingSession, error) {
	return listProcessingSessionsByTickets(r.db, ticketIDs)
}

// DeleteProcessingSession implements ProcessingSessionStore.
func (r *SQLiteRepository) DeleteProcessingSession(id string) error {
	return deleteProcessingSession(r.db, id)
}

// --- MySQL ---

// SaveProcessingSession implements ProcessingSessionStore.
func (r *MySQLRepository) SaveProcessingSession(s domain.ProcessingSession) error {
	return saveProcessingSession(r.db, s)
}

// TouchProcessingSession implements ProcessingSessionStore.
func (r *MySQLRepository) TouchProcessingSession(id, heartbeat string) (bool, error) {
	return touchProcessingSession(r.db, id, heartbeat)
}

// GetProcessingSession implements ProcessingSessionStore.
func (r *MySQLRepository) GetProcessingSession(id string) (*domain.ProcessingSession, error) {
	return getProcessingSession(r.db, id)
}

// ListProcessingSessionsByTickets implements ProcessingSessionStore.
func (r *MySQLRepository) ListProcessingSessionsByTickets(ticketIDs []string) ([]domain.ProcessingSession, error) {
	return listProcessingSessionsByTickets(r.db, ticketIDs)
}

// DeleteProcessingSession implements ProcessingSessionStore.
func (r *MySQLRepository) DeleteProcessingSession(id string) error {
	return deleteProcessingSession(r.db, id)
}

// --- HTTP data source (protocol 1.2) ---

// httpDataSourceClaimMinor is the protocol minor version that added the
// nodes' claim fields and the processing-sessions endpoints (DFLT-00327).
// It is the same 1.2 the autopilot runs came with: this was added to 1.2
// before any release shipped it, so the version was not raised.
const httpDataSourceClaimMinor = 2

// httpProcessingSessionTicketChunk caps how many ticket_id parameters one
// GET /processing-sessions carries, so the query string stays well inside
// what servers and proxies accept; more tickets are read in several GETs.
const httpProcessingSessionTicketChunk = 100

func (r *HTTPRepository) supportsClaims() bool {
	return r.serverMinor >= httpDataSourceClaimMinor
}

// SaveProcessingSession implements ProcessingSessionStore
// (PUT /processing-sessions/{sessionId}).
func (r *HTTPRepository) SaveProcessingSession(s domain.ProcessingSession) error {
	if !r.supportsClaims() {
		return ErrProcessingSessionsUnsupported
	}
	return r.do(http.MethodPut, "/processing-sessions/"+esc(s.ID), s, nil)
}

// TouchProcessingSession implements ProcessingSessionStore with one
// POST /processing-sessions/{sessionId}/heartbeat: the data source keeps the
// later of the two heartbeats, and answers 404 for a session it does not
// have. Only the heartbeat is sent, never the whole session read back or
// kept from earlier, which could put an older copy of the other fields back.
func (r *HTTPRepository) TouchProcessingSession(id, heartbeat string) (bool, error) {
	if !r.supportsClaims() {
		return false, ErrProcessingSessionsUnsupported
	}
	err := r.do(http.MethodPost, "/processing-sessions/"+esc(id)+"/heartbeat", map[string]string{"heartbeat": heartbeat}, nil)
	if err != nil {
		if isHTTPNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// GetProcessingSession implements ProcessingSessionStore
// (GET /processing-sessions/{sessionId}).
func (r *HTTPRepository) GetProcessingSession(id string) (*domain.ProcessingSession, error) {
	if !r.supportsClaims() {
		return nil, ErrProcessingSessionsUnsupported
	}
	var out domain.ProcessingSession
	if err := r.do(http.MethodGet, "/processing-sessions/"+esc(id), nil, &out); err != nil {
		if isHTTPNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

// ListProcessingSessionsByTickets implements ProcessingSessionStore
// (GET /processing-sessions?ticket_id=...&ticket_id=...), one GET per
// httpProcessingSessionTicketChunk tickets.
func (r *HTTPRepository) ListProcessingSessionsByTickets(ticketIDs []string) ([]domain.ProcessingSession, error) {
	if !r.supportsClaims() {
		return nil, ErrProcessingSessionsUnsupported
	}
	out := []domain.ProcessingSession{}
	for _, chunk := range chunkIDs(dedupeIDs(ticketIDs), httpProcessingSessionTicketChunk) {
		q := url.Values{}
		for _, id := range chunk {
			q.Add("ticket_id", id)
		}
		var page []domain.ProcessingSession
		if err := r.do(http.MethodGet, "/processing-sessions?"+q.Encode(), nil, &page); err != nil {
			return nil, err
		}
		out = append(out, page...)
	}
	return out, nil
}

// DeleteProcessingSession implements ProcessingSessionStore
// (DELETE /processing-sessions/{sessionId}).
func (r *HTTPRepository) DeleteProcessingSession(id string) error {
	if !r.supportsClaims() {
		return ErrProcessingSessionsUnsupported
	}
	err := r.do(http.MethodDelete, "/processing-sessions/"+esc(id), nil, nil)
	if err != nil && isHTTPNotFound(err) {
		return nil
	}
	return err
}

// isHTTPNotFound reports whether err is the data source's "no such
// session" answer: a 404 whose body carries no error code graph-engine
// knows (openapi.yaml does not define one for a session), or a not-found
// code of a ticket or node.
func isHTTPNotFound(err error) bool {
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) && statusErr.status == http.StatusNotFound {
		return true
	}
	return isNotFoundCode(err, domain.ErrCodeTicketNotFound) || isNotFoundCode(err, domain.ErrCodeNodeNotFound)
}
