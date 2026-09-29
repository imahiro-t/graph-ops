package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/graph-ops/core-go/internal/domain"
)

// This file is where autopilot runs are shared between members (DFLT-00326):
// the autopilot_runs table of both SQL backends and the autopilot-runs
// endpoints of an HTTP data source speaking protocol 1.2.

// ErrAutopilotRunsUnsupported is what an AutopilotRunStore answers when its
// data source cannot keep autopilot runs: an HTTP data source older than
// protocol 1.2. The caller then decides from the local run registry alone
// (and says once that other members' runs cannot be seen).
var ErrAutopilotRunsUnsupported = errors.New("the data source does not share autopilot runs (an HTTP data source needs protocol 1.2 or newer)")

// AutopilotRunStore is an optional add-on to GraphRepository, like
// TicketGraphLister: sharing autopilot runs through the data source. It is
// kept off GraphRepository so the fakes and test repositories do not have to
// grow it, and so an HTTP data source older than 1.2 can say it has none
// (ErrAutopilotRunsUnsupported).
//
// Records are the runs' shared views (domain.AutopilotRunRecord); the local
// run registry stays each machine's source of truth for its own runs.
type AutopilotRunStore interface {
	// ListAutopilotRuns returns every run record of projectID, in no
	// particular order.
	ListAutopilotRuns(projectID string) ([]domain.AutopilotRunRecord, error)
	// BeginAutopilotRun serializes the starts of projectID's runs: it reads
	// the project's run records, calls decide with them, upserts the record
	// decide returns (nothing when it returns nil) and deletes the
	// project's records decide lists in drop (the retention of settled
	// records) -- all under one lock on the SQL backends, so two members
	// starting overlapping runs at the same moment cannot both pass, and a
	// record is only dropped in the state decide saw. If decide fails
	// nothing is written and its error is returned as is. A missing
	// project is PROJECT_NOT_FOUND.
	//
	// decide must not call the repository: on SQLite the one connection is
	// held by the transaction decide runs in, so such a call would wait for
	// itself forever.
	//
	// err is the start's own failure; nothing was written then (on HTTP:
	// it failed before the PUT). dropErr is non-nil only when the record
	// was saved but deleting some of drop failed -- the start itself
	// succeeded, and the records left are dropped by a later start. The two
	// are separate so a caller looking at err alone cannot take a saved
	// record for a failed start. The SQL backends delete in the same
	// transaction and fail as a whole (err), so their dropErr is always
	// nil.
	//
	// On an HTTP data source this is a GET, a PUT and a DELETE per dropped
	// record, which is not atomic: two starts at the same moment may both
	// pass there, and a record saved again between the GET and its DELETE
	// is deleted anyway (its run's next save brings it back). Only the
	// first httpAutopilotRunDropLimit records of drop (the saved record
	// itself not counted) are deleted per call, so a slow data source does
	// not hold the caller's lock for as many requests as records have piled
	// up; drop should therefore list the records to delete first (the
	// oldest) first. The rest are dropped by later starts. A DELETE that
	// fails does not stop the next one, and the failures come back in
	// dropErr as one line ("run-a: ...; run-b: ...").
	BeginAutopilotRun(projectID string, decide func(existing []domain.AutopilotRunRecord) (save *domain.AutopilotRunRecord, drop []string, err error)) (dropErr error, err error)
	// SaveAutopilotRun upserts rec, unless the stored record's revision is
	// already rec.Revision or more (then it does nothing and succeeds).
	//
	// There is no tombstone: a save that arrives after DeleteAutopilotRun
	// brings the record back. The runs deleted (a cancelled reservation, a
	// pruned old run) are not saved again in practice, and a record brought
	// back that way stops blocking anyone once its heartbeat is 10 minutes
	// old.
	SaveAutopilotRun(rec domain.AutopilotRunRecord) error
	// DeleteAutopilotRun removes a run record; a missing one is not an
	// error. Run IDs are unique across projects, so the ID alone names it.
	// On the SQL backends it deletes the one row by its primary key without
	// taking the project's lock (see lockProjectForRuns).
	DeleteAutopilotRun(id string) error
}

var (
	_ AutopilotRunStore = (*SQLiteRepository)(nil)
	_ AutopilotRunStore = (*MySQLRepository)(nil)
	_ AutopilotRunStore = (*HTTPRepository)(nil)
)

const autopilotRunCols = "id, project_id, root_ticket_id, mode, state, heartbeat, created_at, updated_at, started_by_name, machine_id, revision, snapshot"

type sqlQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}

func listAutopilotRuns(q sqlQuerier, projectID string) ([]domain.AutopilotRunRecord, error) {
	rows, err := q.Query(`SELECT `+autopilotRunCols+` FROM autopilot_runs WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, fmt.Errorf("listing autopilot runs of project %s: %w", projectID, err)
	}
	defer rows.Close()
	out := []domain.AutopilotRunRecord{}
	for rows.Next() {
		var rec domain.AutopilotRunRecord
		var snapshot string
		if err := rows.Scan(&rec.ID, &rec.ProjectID, &rec.RootTicketID, &rec.Mode, &rec.State, &rec.Heartbeat,
			&rec.CreatedAt, &rec.UpdatedAt, &rec.StartedByName, &rec.MachineID, &rec.Revision, &snapshot); err != nil {
			return nil, fmt.Errorf("reading an autopilot run of project %s: %w", projectID, err)
		}
		rec.Snapshot = []byte(snapshot)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// lockProjectForRuns takes the lock the autopilot_runs writes of a project
// go through -- Begin (its upsert and its retention deletes) and Save: the
// project row, FOR UPDATE on MySQL (the same point CreateTicket serializes
// on); SQLite's immediate transaction already holds the database's write
// lock. Taking the project row first keeps the lock order the same for
// every write (no gap-lock deadlock on MySQL between an insert and a
// Begin).
//
// DeleteAutopilotRun is the exception: a DELETE of one row by its primary
// key, which takes no gap lock and cannot deadlock with the writes above,
// so it does not take this lock. It is only used for the runs a machine
// itself removes (a cancelled reservation, its pruned runs, a compensation).
func lockProjectForRuns(tx *sql.Tx, d sqlDialect, projectID string) error {
	var id string
	err := tx.QueryRow(`SELECT id FROM projects WHERE id = ?`+d.forUpdate, projectID).Scan(&id)
	if err == sql.ErrNoRows {
		return domain.NewAPIError(domain.ErrCodeProjectNotFound, "project %s not found", projectID)
	}
	if err != nil {
		return fmt.Errorf("locking project %s: %w", projectID, err)
	}
	return nil
}

// upsertAutopilotRun inserts rec, or updates the stored record when rec's
// revision is greater. The caller holds the project's lock.
func upsertAutopilotRun(tx *sql.Tx, d sqlDialect, rec domain.AutopilotRunRecord) error {
	snapshot := string(rec.Snapshot)
	if snapshot == "" {
		snapshot = "{}"
	}
	var stored int64
	err := tx.QueryRow(`SELECT revision FROM autopilot_runs WHERE id = ?`+d.forUpdate, rec.ID).Scan(&stored)
	switch {
	case err == sql.ErrNoRows:
		_, err = tx.Exec(`INSERT INTO autopilot_runs (`+autopilotRunCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			rec.ID, rec.ProjectID, rec.RootTicketID, rec.Mode, rec.State, rec.Heartbeat, rec.CreatedAt, rec.UpdatedAt,
			rec.StartedByName, rec.MachineID, rec.Revision, snapshot)
		if err != nil {
			return fmt.Errorf("inserting autopilot run %s: %w", rec.ID, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("reading autopilot run %s: %w", rec.ID, err)
	case rec.Revision <= stored:
		return nil // an older (or the same) copy: keep what is stored
	}
	_, err = tx.Exec(`UPDATE autopilot_runs SET project_id = ?, root_ticket_id = ?, mode = ?, state = ?, heartbeat = ?,
		created_at = ?, updated_at = ?, started_by_name = ?, machine_id = ?, revision = ?, snapshot = ?
		WHERE id = ? AND revision < ?`,
		rec.ProjectID, rec.RootTicketID, rec.Mode, rec.State, rec.Heartbeat, rec.CreatedAt, rec.UpdatedAt,
		rec.StartedByName, rec.MachineID, rec.Revision, snapshot, rec.ID, rec.Revision)
	if err != nil {
		return fmt.Errorf("updating autopilot run %s: %w", rec.ID, err)
	}
	return nil
}

func beginAutopilotRun(db *sql.DB, d sqlDialect, projectID string, decide func([]domain.AutopilotRunRecord) (*domain.AutopilotRunRecord, []string, error)) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := lockProjectForRuns(tx, d, projectID); err != nil {
		return err
	}
	existing, err := listAutopilotRuns(tx, projectID)
	if err != nil {
		return err
	}
	rec, drop, err := decide(existing)
	if err != nil {
		return err
	}
	if rec != nil {
		if rec.ProjectID != projectID {
			return fmt.Errorf("autopilot run %s belongs to project %s, not %s", rec.ID, rec.ProjectID, projectID)
		}
		if err := upsertAutopilotRun(tx, d, *rec); err != nil {
			return err
		}
	}
	for _, id := range drop {
		if rec != nil && id == rec.ID {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM autopilot_runs WHERE id = ? AND project_id = ?`, id, projectID); err != nil {
			return fmt.Errorf("deleting settled autopilot run %s: %w", id, err)
		}
	}
	return tx.Commit()
}

func saveAutopilotRun(db *sql.DB, d sqlDialect, rec domain.AutopilotRunRecord) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := lockProjectForRuns(tx, d, rec.ProjectID); err != nil {
		return err
	}
	if err := upsertAutopilotRun(tx, d, rec); err != nil {
		return err
	}
	return tx.Commit()
}

func deleteAutopilotRun(db *sql.DB, id string) error {
	if _, err := db.Exec(`DELETE FROM autopilot_runs WHERE id = ?`, id); err != nil {
		return fmt.Errorf("deleting autopilot run %s: %w", id, err)
	}
	return nil
}

// --- SQLite ---

// ListAutopilotRuns implements AutopilotRunStore.
func (r *SQLiteRepository) ListAutopilotRuns(projectID string) ([]domain.AutopilotRunRecord, error) {
	return listAutopilotRuns(r.db, projectID)
}

// BeginAutopilotRun implements AutopilotRunStore. The immediate transaction
// (see NewSQLiteRepository's _txlock) holds the database's write lock from
// the read to the write, across processes too.
func (r *SQLiteRepository) BeginAutopilotRun(projectID string, decide func([]domain.AutopilotRunRecord) (*domain.AutopilotRunRecord, []string, error)) (dropErr, err error) {
	return nil, beginAutopilotRun(r.db, sqliteDialect, projectID, decide)
}

// SaveAutopilotRun implements AutopilotRunStore.
func (r *SQLiteRepository) SaveAutopilotRun(rec domain.AutopilotRunRecord) error {
	return saveAutopilotRun(r.db, sqliteDialect, rec)
}

// DeleteAutopilotRun implements AutopilotRunStore.
func (r *SQLiteRepository) DeleteAutopilotRun(id string) error { return deleteAutopilotRun(r.db, id) }

// --- MySQL ---

// ListAutopilotRuns implements AutopilotRunStore.
func (r *MySQLRepository) ListAutopilotRuns(projectID string) ([]domain.AutopilotRunRecord, error) {
	return listAutopilotRuns(r.db, projectID)
}

// BeginAutopilotRun implements AutopilotRunStore: the project row, locked
// FOR UPDATE, serializes the starts of the project's runs.
func (r *MySQLRepository) BeginAutopilotRun(projectID string, decide func([]domain.AutopilotRunRecord) (*domain.AutopilotRunRecord, []string, error)) (dropErr, err error) {
	return nil, beginAutopilotRun(r.db, mysqlDialect, projectID, decide)
}

// SaveAutopilotRun implements AutopilotRunStore.
func (r *MySQLRepository) SaveAutopilotRun(rec domain.AutopilotRunRecord) error {
	return saveAutopilotRun(r.db, mysqlDialect, rec)
}

// DeleteAutopilotRun implements AutopilotRunStore.
func (r *MySQLRepository) DeleteAutopilotRun(id string) error { return deleteAutopilotRun(r.db, id) }

// --- HTTP data source (protocol 1.2) ---

// httpDataSourceAutopilotRunsMinor is the protocol minor version that added
// the autopilot-runs endpoints (DFLT-00326).
const httpDataSourceAutopilotRunsMinor = 2

func (r *HTTPRepository) supportsAutopilotRuns() bool {
	return r.serverMinor >= httpDataSourceAutopilotRunsMinor
}

// ListAutopilotRuns implements AutopilotRunStore
// (GET /projects/{projectId}/autopilot-runs).
func (r *HTTPRepository) ListAutopilotRuns(projectID string) ([]domain.AutopilotRunRecord, error) {
	if !r.supportsAutopilotRuns() {
		return nil, ErrAutopilotRunsUnsupported
	}
	out := []domain.AutopilotRunRecord{}
	if err := r.do(http.MethodGet, "/projects/"+esc(projectID)+"/autopilot-runs", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []domain.AutopilotRunRecord{}
	}
	return out, nil
}

// httpAutopilotRunDropLimit is how many settled records one
// BeginAutopilotRun deletes at most on an HTTP data source (DFLT-00337).
//
// The DELETEs stay inside the call -- under the caller's lock -- rather
// than being sent after it: the lock orders them against this machine's
// other starts, and a start that took over or adopted one of the records
// in between would otherwise have it deleted while it is active again,
// hiding it from other members until its next save (see the implementation
// notes of DFLT-00326). The limit bounds how long they hold that lock
// instead: at most two requests' timeouts, not one per record piled up. A
// steady project adds at most one settled record per start, so deleting two
// also works off a backlog (after an upgrade, or of many members), over
// several starts. There is no time limit on top: a data source that is
// down fails the GET or the PUT first.
const httpAutopilotRunDropLimit = 2

// runDropErrors is the dropErr of BeginAutopilotRun: the DELETEs that
// failed, reported as one line so a log line stays one line.
type runDropErrors []runDropError

type runDropError struct {
	id  string
	err error
}

func (e runDropErrors) Error() string {
	var b strings.Builder
	b.WriteString("deleting settled autopilot run records: ")
	for i, d := range e {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(d.id)
		b.WriteString(": ")
		b.WriteString(oneLine(d.err.Error()))
	}
	return b.String()
}

// Unwrap lets errors.Is and errors.As see each DELETE's error.
func (e runDropErrors) Unwrap() []error {
	out := make([]error, len(e))
	for i, d := range e {
		out[i] = d.err
	}
	return out
}

// oneLine turns the line breaks of s (an HTTP error body may have some)
// into single spaces.
func oneLine(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	return strings.Join(strings.Fields(s), " ")
}

// BeginAutopilotRun implements AutopilotRunStore as a GET, a PUT, and a
// DELETE per dropped record, up to httpAutopilotRunDropLimit of them. The
// protocol has no cross-request transaction, so this is not atomic: two
// members starting overlapping runs at the same moment may both pass. A
// failed DELETE is reported in dropErr (see the interface).
func (r *HTTPRepository) BeginAutopilotRun(projectID string, decide func([]domain.AutopilotRunRecord) (*domain.AutopilotRunRecord, []string, error)) (dropErr, err error) {
	existing, err := r.ListAutopilotRuns(projectID)
	if err != nil {
		return nil, err
	}
	rec, drop, err := decide(existing)
	if err != nil {
		return nil, err
	}
	if rec != nil {
		if err := r.SaveAutopilotRun(*rec); err != nil {
			return nil, err
		}
	}
	var failed runDropErrors
	sent := 0
	for _, id := range drop {
		if sent == httpAutopilotRunDropLimit {
			break // the rest go at a later start
		}
		if rec != nil && id == rec.ID {
			continue
		}
		sent++
		if derr := r.DeleteAutopilotRun(id); derr != nil {
			failed = append(failed, runDropError{id: id, err: derr})
		}
	}
	if len(failed) > 0 {
		return failed, nil
	}
	return nil, nil
}

// SaveAutopilotRun implements AutopilotRunStore (PUT /autopilot-runs/{runId}).
func (r *HTTPRepository) SaveAutopilotRun(rec domain.AutopilotRunRecord) error {
	if !r.supportsAutopilotRuns() {
		return ErrAutopilotRunsUnsupported
	}
	if len(rec.Snapshot) == 0 {
		rec.Snapshot = []byte("{}")
	}
	return r.do(http.MethodPut, "/autopilot-runs/"+esc(rec.ID), rec, nil)
}

// DeleteAutopilotRun implements AutopilotRunStore
// (DELETE /autopilot-runs/{runId}).
func (r *HTTPRepository) DeleteAutopilotRun(id string) error {
	if !r.supportsAutopilotRuns() {
		return ErrAutopilotRunsUnsupported
	}
	return r.do(http.MethodDelete, "/autopilot-runs/"+esc(id), nil, nil)
}
