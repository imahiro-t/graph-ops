package store

import (
	"fmt"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

// Schema versions of the SQL backends (SQLite and MySQL; DFLT-00331).
//
// A SQL database records, in its one-row graphops_schema table, the schema
// version it has been migrated to and the lowest client schema version that
// may read and write it. Every graph-engine build knows one schema version,
// CurrentSchemaVersion, and Init compares it with the record before it
// touches the schema: a build older than the record's minimum stops with
// CLIENT_TOO_OLD and writes nothing, so a member who has not updated cannot
// silently bypass the shared-environment safeguards the newer members rely
// on. graph-engine carries no release (semver) version of its own, which is
// why the client side is expressed in the same integers as the schema.
//
// Schema version <-> release:
//
//	1 = graph-engine v0.11.0 and later
//
// Releases before v0.11.0 do not read the record at all, so they cannot be
// stopped; everyone sharing a database has to update at the same time once.
//
// Schema version 1 is the schema as of a2faf17 (projects, app_state,
// tickets, nodes, edges, artifacts, labels, ticket_labels, with their
// indexes and the migrations Init already ran) plus everything DFLT-00325's
// children added for several members sharing one database:
//
//   - DFLT-00326: table autopilot_runs (id, project_id, root_ticket_id, mode,
//     state, heartbeat, created_at, updated_at, started_by_name, machine_id,
//     revision, snapshot), index idx_autopilot_runs_project, and on MySQL
//     foreign key fk_autopilot_runs_project.
//   - DFLT-00327: nodes.claimed_by_name, claimed_by_name_is_fallback,
//     claim_token, claim_session_id, claimed_at (all NULL-able,
//     nodeClaimColumns); table processing_sessions (id, project_id,
//     ticket_id, actor_name, actor_name_is_fallback, machine_id, run_id,
//     started_at, heartbeat), index idx_processing_sessions_ticket, and on
//     MySQL foreign keys fk_processing_sessions_project and
//     fk_processing_sessions_ticket.
//   - DFLT-00328: no column or table; a write contract -- a graph's seed and
//     expansion are one transaction conditioned on tickets.graph_expanded_at
//     and the node count (READ COMMITTED on MySQL).
//   - DFLT-00329: nodes.decided_by_name, decided_by_name_is_fallback,
//     decided_at, decided_by_autopilot (all NULL-able, nodeDecisionColumns);
//     and a write contract -- a node's updated_at differs on every write
//     (nanosecond precision, past the previous value), and complete,
//     loop-back, reopen, grant and unstick are conditional and atomic.
//   - DFLT-00330: no column or table; a write contract -- a ticket's
//     updated_at is never reused (nextUpdatedAt), and if_updated_at compares
//     it as an exact string.
//   - DFLT-00331: table graphops_schema itself.
//
// Rules for changing the schema (for developers):
//
//   - Any change to a table, a column, an index or a write contract bumps
//     CurrentSchemaVersion, and the mapping above gains the release it ships
//     in. TestSQLiteSchemaFingerprint fails on a table/column/index change
//     until its expectation is updated, as a reminder of this rule; a change
//     to a write contract has no such test, so it relies on this comment.
//   - When an older client writing the database as it knows it would bypass
//     a new safeguard or break the meaning of what it does not know about,
//     MinClientSchemaVersion is raised to the new CurrentSchemaVersion as
//     well. A purely additive change that older clients can live with leaves
//     it where it is.
//
// Concurrent Init is tolerated the way the other schema additions are, with
// no lock: the check reads, the record is written last with an upsert that
// keeps the larger of each value (it never goes down), so any number of
// overlapping Inits end with the highest record. The one window left open:
// a client that passed the check just before a newer client raised the
// minimum carries on with that one call (its migrations are the idempotent
// additions it always makes, and it cannot lower the record) and is stopped
// from its next call on.
const (
	// CurrentSchemaVersion is the schema version this build knows and
	// migrates a database to.
	CurrentSchemaVersion = 1
	// MinClientSchemaVersion is the lowest CurrentSchemaVersion of a client
	// that may read and write a database this build has migrated.
	MinClientSchemaVersion = 1
)

// clientSchema is what a client knows: its own schema version and the
// minimum it records. The repositories hold a *clientSchema that is nil in
// production (meaning the constants above); only tests replace it, through
// export_test.go, to act as an older or newer client.
type clientSchema struct {
	Current   int
	MinClient int
}

func defaultClientSchema() clientSchema {
	return clientSchema{Current: CurrentSchemaVersion, MinClient: MinClientSchemaVersion}
}

// clientSchemaOrDefault resolves a repository's override.
func clientSchemaOrDefault(c *clientSchema) clientSchema {
	if c == nil {
		return defaultClientSchema()
	}
	return *c
}

// schemaRecord is a database's graphops_schema row. nil means the database
// has none yet (created before DFLT-00331, or brand new).
type schemaRecord struct {
	SchemaVersion          int
	MinClientSchemaVersion int
}

// ClientSchemaChecker is implemented by a repository that keeps a schema
// record (SQLite and MySQL; not the HTTP data source, whose schema belongs
// to the plugin and whose compatibility is the protocol version's job).
// CheckClientSchema only reads: it answers CLIENT_TOO_OLD when this client
// no longer meets the database's minimum -- which a long-running Web UI
// server needs, since another member may raise the minimum after it
// started.
type ClientSchemaChecker interface {
	CheckClientSchema() error
}

// checkClientSchema returns CLIENT_TOO_OLD when rec requires a newer client
// than client. A missing record requires nothing.
func checkClientSchema(rec *schemaRecord, client clientSchema) error {
	if rec == nil || client.Current >= rec.MinClientSchemaVersion {
		return nil
	}
	return domain.NewAPIError(domain.ErrCodeClientTooOld,
		"CLIENT_TOO_OLD: this database needs graph-engine schema version %d or later, but this graph-engine knows schema version %d "+
			"(the database has been migrated to schema version %d by a newer graph-engine). "+
			"Update graph-engine (the GraphOps plugin) and run it again -- see \"Updating to a new release\" in the README; "+
			"a running Web UI server has to be restarted after the update. Nothing was written to the database.",
		rec.MinClientSchemaVersion, client.Current, rec.SchemaVersion).WithDetails(map[string]any{
		"db_schema_version":         rec.SchemaVersion,
		"min_client_schema_version": rec.MinClientSchemaVersion,
		"client_schema_version":     client.Current,
	})
}

// needsSchemaRecordWrite reports whether Init has to write the record: only
// when there is none, or when either value is below what this client
// records. An equal or higher record is left alone, so an older client
// never lowers it and an ordinary call writes nothing.
func needsSchemaRecordWrite(rec *schemaRecord, client clientSchema) bool {
	if rec == nil {
		return true
	}
	return rec.SchemaVersion < client.Current || rec.MinClientSchemaVersion < client.MinClient
}

// initWithSchemaRecord is Init's shared frame around a backend's schema
// work: read the record and stop with CLIENT_TOO_OLD before anything is
// written, run migrate, then raise the record if this client is ahead of
// it. write must keep the larger of each value (see the doc comment at the
// top of this file).
func initWithSchemaRecord(backend string, client clientSchema, read func() (*schemaRecord, error), migrate func() error, write func(schemaVersion, minClient int, updatedAt string) error) error {
	rec, err := read()
	if err != nil {
		return fmt.Errorf("reading %s schema version: %w", backend, err)
	}
	if err := checkClientSchema(rec, client); err != nil {
		return err
	}
	if err := migrate(); err != nil {
		return err
	}
	if !needsSchemaRecordWrite(rec, client) {
		return nil
	}
	if err := write(client.Current, client.MinClient, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("recording %s schema version: %w", backend, err)
	}
	return nil
}

// checkClientSchemaWith is CheckClientSchema's shared body: read, then
// check. Nothing is written.
func checkClientSchemaWith(backend string, client clientSchema, read func() (*schemaRecord, error)) error {
	rec, err := read()
	if err != nil {
		return fmt.Errorf("reading %s schema version: %w", backend, err)
	}
	return checkClientSchema(rec, client)
}
