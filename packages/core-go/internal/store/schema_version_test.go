package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// --- Pure functions ---

func TestCheckClientSchema(t *testing.T) {
	cases := []struct {
		name   string
		rec    *schemaRecord
		client clientSchema
		tooOld bool
	}{
		{"no record", nil, clientSchema{Current: 0, MinClient: 0}, false},
		{"client meets the minimum exactly", &schemaRecord{SchemaVersion: 3, MinClientSchemaVersion: 2}, clientSchema{Current: 2, MinClient: 2}, false},
		{"client ahead of the record", &schemaRecord{SchemaVersion: 1, MinClientSchemaVersion: 1}, clientSchema{Current: 2, MinClient: 2}, false},
		{"client below the minimum", &schemaRecord{SchemaVersion: 2, MinClientSchemaVersion: 2}, clientSchema{Current: 1, MinClient: 1}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkClientSchema(c.rec, c.client)
			if !c.tooOld {
				if err != nil {
					t.Fatalf("checkClientSchema = %v, want nil", err)
				}
				return
			}
			assertClientTooOld(t, err, c.rec.SchemaVersion, c.rec.MinClientSchemaVersion, c.client.Current)
		})
	}
}

func TestNeedsSchemaRecordWrite(t *testing.T) {
	client := clientSchema{Current: 2, MinClient: 2}
	cases := []struct {
		name string
		rec  *schemaRecord
		want bool
	}{
		{"no record", nil, true},
		{"both lower", &schemaRecord{SchemaVersion: 1, MinClientSchemaVersion: 1}, true},
		{"equal", &schemaRecord{SchemaVersion: 2, MinClientSchemaVersion: 2}, false},
		{"both higher", &schemaRecord{SchemaVersion: 3, MinClientSchemaVersion: 3}, false},
		{"only schema version lower", &schemaRecord{SchemaVersion: 1, MinClientSchemaVersion: 2}, true},
		{"only minimum lower", &schemaRecord{SchemaVersion: 3, MinClientSchemaVersion: 1}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := needsSchemaRecordWrite(c.rec, client); got != c.want {
				t.Errorf("needsSchemaRecordWrite(%+v, %+v) = %v, want %v", c.rec, client, got, c.want)
			}
		})
	}
}

// assertClientTooOld checks that err is CLIENT_TOO_OLD with the given
// details and that its message tells the reader what to do.
func assertClientTooOld(t *testing.T, err error, dbSchema, minClient, clientVersion int) {
	t.Helper()
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeClientTooOld {
		t.Fatalf("err = %v, want a CLIENT_TOO_OLD *domain.APIError", err)
	}
	want := map[string]any{
		"db_schema_version":         dbSchema,
		"min_client_schema_version": minClient,
		"client_schema_version":     clientVersion,
	}
	if !reflect.DeepEqual(apiErr.Details, want) {
		t.Errorf("Details = %v, want %v", apiErr.Details, want)
	}
	for _, s := range []string{"CLIENT_TOO_OLD", "Update graph-engine (the GraphOps plugin)", "Nothing was written to the database"} {
		if !strings.Contains(apiErr.Message, s) {
			t.Errorf("message %q does not contain %q", apiErr.Message, s)
		}
	}
}

// --- Both SQL backends ---

// schemaTestRepo is what the schema record tests need of a SQL repository.
type schemaTestRepo interface {
	GraphRepository
	ClientSchemaChecker
	readSchemaRecord() (*schemaRecord, error)
}

func rawDB(repo schemaTestRepo) *sql.DB {
	switch r := repo.(type) {
	case *SQLiteRepository:
		return r.db
	case *MySQLRepository:
		return r.db
	}
	panic(fmt.Sprintf("rawDB: unexpected %T", repo))
}

// schemaBackend is one SQL backend the schema record tests run against.
// newDB gives a database without a schema record and returns a function
// opening a repository on it that has not been Init-ed yet; each call is a
// separate connection pool, standing in for a separate graph-engine process.
type schemaBackend struct {
	name  string
	newDB func(t *testing.T) (open func() schemaTestRepo)
}

func schemaBackends() []schemaBackend {
	return []schemaBackend{
		{name: "sqlite", newDB: func(t *testing.T) func() schemaTestRepo {
			path := filepath.Join(t.TempDir(), "schema.db")
			return func() schemaTestRepo {
				repo, err := NewSQLiteRepository(path)
				if err != nil {
					t.Fatalf("NewSQLiteRepository: %v", err)
				}
				t.Cleanup(func() { repo.db.Close() })
				return repo
			}
		}},
		{name: "mysql", newDB: func(t *testing.T) func() schemaTestRepo {
			cfg := mysqlTestConfig(t) // skips without a test MySQL
			open := func() schemaTestRepo {
				repo, err := NewMySQLRepository(cfg)
				if err != nil {
					t.Fatalf("NewMySQLRepository: %v", err)
				}
				t.Cleanup(func() { repo.db.Close() })
				return repo
			}
			// The test database is shared with every other MySQL test, so
			// "no record" is made by dropping the record's table, and the
			// test leaves the record at (1, 1) behind it, whatever it did.
			admin := open()
			if err := admin.Init(); err != nil {
				t.Fatalf("Init before the test: %v", err)
			}
			restoreDefaultSchemaRecord(t, admin)
			if _, err := rawDB(admin).Exec(`DROP TABLE graphops_schema`); err != nil {
				t.Fatalf("dropping graphops_schema: %v", err)
			}
			t.Cleanup(func() { restoreDefaultSchemaRecord(t, open()) })
			return open
		}},
	}
}

// restoreDefaultSchemaRecord leaves repo's database with the record a
// default client writes, (1, 1), and every table in place: a default Init
// (re)creates anything missing, and the record is reset in case a test
// raised it.
func restoreDefaultSchemaRecord(t *testing.T, repo schemaTestRepo) {
	t.Helper()
	db := rawDB(repo)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS graphops_schema (id TINYINT PRIMARY KEY, schema_version INT NOT NULL, min_client_schema_version INT NOT NULL, updated_at VARCHAR(64) NOT NULL)`); err != nil {
		t.Errorf("restoring graphops_schema: %v", err)
		return
	}
	if _, err := db.Exec(`UPDATE graphops_schema SET schema_version = ?, min_client_schema_version = ? WHERE id = 1`, CurrentSchemaVersion, MinClientSchemaVersion); err != nil {
		t.Errorf("resetting the schema record: %v", err)
		return
	}
	if err := repo.Init(); err != nil {
		t.Errorf("default Init while restoring: %v", err)
		return
	}
	rec, err := repo.readSchemaRecord()
	if err != nil || rec == nil || *rec != (schemaRecord{SchemaVersion: CurrentSchemaVersion, MinClientSchemaVersion: MinClientSchemaVersion}) {
		t.Errorf("after restoring, record = %+v (%v), want (%d, %d)", rec, err, CurrentSchemaVersion, MinClientSchemaVersion)
	}
}

func setRecord(t *testing.T, repo schemaTestRepo, schemaVersion, minClient int) {
	t.Helper()
	if _, err := rawDB(repo).Exec(`UPDATE graphops_schema SET schema_version = ?, min_client_schema_version = ? WHERE id = 1`, schemaVersion, minClient); err != nil {
		t.Fatalf("setting the schema record: %v", err)
	}
}

func wantRecord(t *testing.T, repo schemaTestRepo, schemaVersion, minClient int) {
	t.Helper()
	rec, err := repo.readSchemaRecord()
	if err != nil {
		t.Fatalf("readSchemaRecord: %v", err)
	}
	if rec == nil {
		t.Fatalf("no schema record, want (%d, %d)", schemaVersion, minClient)
	}
	if rec.SchemaVersion != schemaVersion || rec.MinClientSchemaVersion != minClient {
		t.Fatalf("schema record = (%d, %d), want (%d, %d)", rec.SchemaVersion, rec.MinClientSchemaVersion, schemaVersion, minClient)
	}
}

func recordUpdatedAt(t *testing.T, repo schemaTestRepo) string {
	t.Helper()
	var at string
	if err := rawDB(repo).QueryRow(`SELECT updated_at FROM graphops_schema WHERE id = 1`).Scan(&at); err != nil {
		t.Fatalf("reading the record's updated_at: %v", err)
	}
	return at
}

func tableExists(t *testing.T, repo schemaTestRepo, table string) bool {
	t.Helper()
	var n int
	var err error
	switch repo.(type) {
	case *SQLiteRepository:
		err = rawDB(repo).QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n)
	default:
		err = rawDB(repo).QueryRow(`SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?`, table).Scan(&n)
	}
	if err != nil {
		t.Fatalf("looking up table %s: %v", table, err)
	}
	return n > 0
}

// A database without a record gets (1, 1) from the first Init; a second
// Init leaves the record untouched (nothing is written on an ordinary call).
func TestSchemaRecord_FirstInitWritesAndSecondDoesNot(t *testing.T) {
	for _, b := range schemaBackends() {
		t.Run(b.name, func(t *testing.T) {
			open := b.newDB(t)
			repo := open()
			if rec, err := repo.readSchemaRecord(); err != nil || rec != nil {
				t.Fatalf("before Init, record = %+v (%v), want none", rec, err)
			}
			if err := repo.Init(); err != nil {
				t.Fatalf("Init: %v", err)
			}
			wantRecord(t, repo, 1, 1)
			before := recordUpdatedAt(t, repo)
			if err := open().Init(); err != nil {
				t.Fatalf("second Init: %v", err)
			}
			wantRecord(t, repo, 1, 1)
			if after := recordUpdatedAt(t, repo); after != before {
				t.Errorf("the second Init rewrote the record: updated_at %q -> %q", before, after)
			}
		})
	}
}

// An existing database from before the record (data in it, no
// graphops_schema) is migrated: the record is created and the data kept.
func TestSchemaRecord_ExistingDBWithoutRecordIsMigrated(t *testing.T) {
	for _, b := range schemaBackends() {
		t.Run(b.name, func(t *testing.T) {
			open := b.newDB(t)
			repo := open()
			if err := repo.Init(); err != nil {
				t.Fatalf("Init: %v", err)
			}
			proj, err := repo.CreateProject("Schema "+b.name, "")
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}
			if _, err := rawDB(repo).Exec(`DROP TABLE graphops_schema`); err != nil {
				t.Fatalf("dropping graphops_schema: %v", err)
			}
			if rec, err := repo.readSchemaRecord(); err != nil || rec != nil {
				t.Fatalf("after the drop, record = %+v (%v), want none", rec, err)
			}

			again := open()
			if err := again.Init(); err != nil {
				t.Fatalf("Init on a DB without a record: %v", err)
			}
			wantRecord(t, again, 1, 1)
			if got, err := again.GetProject(proj.ID); err != nil || got == nil {
				t.Errorf("the existing project is gone after Init: %+v (%v)", got, err)
			}
		})
	}
}

// A client below the recorded minimum stops with CLIENT_TOO_OLD and changes
// nothing: a table it would otherwise have created stays missing, and the
// record stays as it was.
func TestSchemaRecord_OldClientStopsWithoutWriting(t *testing.T) {
	for _, b := range schemaBackends() {
		t.Run(b.name, func(t *testing.T) {
			open := b.newDB(t)
			repo := open()
			if err := repo.Init(); err != nil {
				t.Fatalf("Init: %v", err)
			}
			if _, err := rawDB(repo).Exec(`DROP TABLE processing_sessions`); err != nil {
				t.Fatalf("dropping processing_sessions: %v", err)
			}
			// Put the table (indexes and foreign keys included) back with a
			// default Init, rather than leaving that to whichever test runs
			// next on a shared MySQL database.
			t.Cleanup(func() {
				fixer := open()
				if err := fixer.Init(); err != nil {
					t.Errorf("default Init in cleanup: %v", err)
					return
				}
				if !tableExists(t, fixer, "processing_sessions") {
					t.Errorf("processing_sessions was not recreated in cleanup")
				}
				wantRecordOrError(t, fixer, 1, 1)
			})

			old := open()
			SetClientSchemaForTest(old, 0, 0)
			assertClientTooOld(t, old.Init(), 1, 1, 0)
			if tableExists(t, repo, "processing_sessions") {
				t.Errorf("the old client's Init created processing_sessions; it must not touch the schema")
			}
			wantRecord(t, repo, 1, 1)

			assertClientTooOld(t, old.CheckClientSchema(), 1, 1, 0)
			if err := repo.CheckClientSchema(); err != nil {
				t.Errorf("CheckClientSchema of a default client = %v, want nil", err)
			}
		})
	}
}

// wantRecordOrError is wantRecord for cleanups, where a failure must not
// stop the remaining cleanups.
func wantRecordOrError(t *testing.T, repo schemaTestRepo, schemaVersion, minClient int) {
	t.Helper()
	rec, err := repo.readSchemaRecord()
	if err != nil || rec == nil || rec.SchemaVersion != schemaVersion || rec.MinClientSchemaVersion != minClient {
		t.Errorf("schema record = %+v (%v), want (%d, %d)", rec, err, schemaVersion, minClient)
	}
}

// A newer client raises the record, after which the current one is too old.
func TestSchemaRecord_NewerClientRaisesTheRecord(t *testing.T) {
	for _, b := range schemaBackends() {
		t.Run(b.name, func(t *testing.T) {
			open := b.newDB(t)
			repo := open()
			if err := repo.Init(); err != nil {
				t.Fatalf("Init: %v", err)
			}
			t.Cleanup(func() { setRecordOrError(t, repo, 1, 1) })

			newer := open()
			SetClientSchemaForTest(newer, 2, 2)
			if err := newer.Init(); err != nil {
				t.Fatalf("newer client's Init: %v", err)
			}
			wantRecord(t, repo, 2, 2)

			assertClientTooOld(t, open().Init(), 2, 2, CurrentSchemaVersion)
			wantRecord(t, repo, 2, 2)
		})
	}
}

func setRecordOrError(t *testing.T, repo schemaTestRepo, schemaVersion, minClient int) {
	t.Helper()
	if _, err := rawDB(repo).Exec(`UPDATE graphops_schema SET schema_version = ?, min_client_schema_version = ? WHERE id = 1`, schemaVersion, minClient); err != nil {
		t.Errorf("resetting the schema record: %v", err)
	}
}

// An older client that still meets the minimum works, and does not lower
// the record a newer client wrote.
func TestSchemaRecord_OlderClientDoesNotLowerTheRecord(t *testing.T) {
	for _, b := range schemaBackends() {
		t.Run(b.name, func(t *testing.T) {
			open := b.newDB(t)
			repo := open()
			if err := repo.Init(); err != nil {
				t.Fatalf("Init: %v", err)
			}
			t.Cleanup(func() { setRecordOrError(t, repo, 1, 1) })

			newer := open()
			SetClientSchemaForTest(newer, 2, 1)
			if err := newer.Init(); err != nil {
				t.Fatalf("newer client's Init: %v", err)
			}
			wantRecord(t, repo, 2, 1)

			if err := open().Init(); err != nil {
				t.Fatalf("an older client meeting the minimum was refused: %v", err)
			}
			wantRecord(t, repo, 2, 1)
			if err := repo.CheckClientSchema(); err != nil {
				t.Errorf("CheckClientSchema = %v, want nil", err)
			}
		})
	}
}

// Concurrent Inits on a database without a record all succeed and leave
// the highest record; mixing clients of different versions is no
// different.
func TestSchemaRecord_ConcurrentInit(t *testing.T) {
	for _, b := range schemaBackends() {
		t.Run(b.name, func(t *testing.T) {
			for _, mixed := range []bool{false, true} {
				t.Run(fmt.Sprintf("mixed=%v", mixed), func(t *testing.T) {
					open := b.newDB(t)
					const n = 6
					repos := make([]schemaTestRepo, n)
					for i := range repos {
						repos[i] = open()
						if mixed && i%2 == 1 {
							SetClientSchemaForTest(repos[i], 2, 1)
						}
					}
					if mixed {
						t.Cleanup(func() { setRecordOrError(t, repos[0], 1, 1) })
					}
					var wg sync.WaitGroup
					errs := make([]error, n)
					for i, r := range repos {
						wg.Add(1)
						go func(i int, r schemaTestRepo) {
							defer wg.Done()
							errs[i] = r.Init()
						}(i, r)
					}
					wg.Wait()
					for i, err := range errs {
						if err != nil {
							t.Errorf("Init #%d: %v", i, err)
						}
					}
					if mixed {
						wantRecord(t, repos[0], 2, 1)
					} else {
						wantRecord(t, repos[0], 1, 1)
					}
				})
			}
		})
	}
}

// TestSQLiteSchemaFingerprint pins a new SQLite DB's tables, columns and
// indexes. If you changed any of them, this fails on purpose: update the
// expectation below, and review CurrentSchemaVersion / MinClientSchemaVersion
// by the rules in schema_version.go (and add the change to the list there).
// MySQL's schema mirrors SQLite's (mysqlSchemaStatements), so pinning one is
// enough to catch a change made to both.
func TestSQLiteSchemaFingerprint(t *testing.T) {
	repo := newTestRepo(t)

	got := map[string][]string{}
	rows, err := repo.db.Query(`SELECT type, name, tbl_name FROM sqlite_master WHERE type IN ('table', 'index') AND name NOT LIKE 'sqlite_autoindex_%' ORDER BY name`)
	if err != nil {
		t.Fatalf("reading sqlite_master: %v", err)
	}
	var tables []string
	var indexes []string
	for rows.Next() {
		var typ, name, tbl string
		if err := rows.Scan(&typ, &name, &tbl); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if typ == "table" {
			tables = append(tables, name)
		} else {
			indexes = append(indexes, name+" ON "+tbl)
		}
	}
	rows.Close()
	sort.Strings(indexes)
	got["(indexes)"] = indexes
	for _, table := range tables {
		cols, err := repo.sqliteColumns(table)
		if err != nil {
			t.Fatalf("columns of %s: %v", table, err)
		}
		var names []string
		for c := range cols {
			names = append(names, c)
		}
		sort.Strings(names)
		got[table] = names
	}

	want := map[string][]string{
		"(indexes)": {
			"idx_artifacts_node ON artifacts",
			"idx_artifacts_ticket ON artifacts",
			"idx_autopilot_runs_project ON autopilot_runs",
			"idx_edges_ticket ON edges",
			"idx_labels_project_name_nocase ON labels",
			"idx_nodes_ticket ON nodes",
			"idx_processing_sessions_ticket ON processing_sessions",
			"idx_projects_prefix_nocase ON projects",
			"idx_ticket_labels_label ON ticket_labels",
			"idx_tickets_parent ON tickets",
			"idx_tickets_project ON tickets",
		},
	}
	wantTables := map[string]string{
		"app_state":           "current_project_id id",
		"artifacts":           "content created_at file_path id metadata name node_id ticket_id type",
		"autopilot_runs":      "created_at heartbeat id machine_id mode project_id revision root_ticket_id snapshot started_by_name state updated_at",
		"edges":               "condition created_at from_node_id id ticket_id to_node_id",
		"graphops_schema":     "id min_client_schema_version schema_version updated_at",
		"labels":              "color created_at id name project_id updated_at",
		"nodes":               "assignee claim_session_id claim_token claimed_at claimed_by_name claimed_by_name_is_fallback config_id created_at criteria decided_at decided_by_autopilot decided_by_name decided_by_name_is_fallback gate_id id is_manual iteration_count max_iterations name status ticket_id type updated_at",
		"processing_sessions": "actor_name actor_name_is_fallback heartbeat id machine_id project_id run_id started_at ticket_id",
		"projects":            "created_at id name prefix ticket_seq updated_at",
		"ticket_labels":       "created_at label_id ticket_id",
		"tickets":             "assignee_name auto_executable blocked closed_reason created_at description graph_expanded_at id node_seq parent_ticket_id priority project_id refined_at status title updated_at",
	}
	for table, cols := range wantTables {
		want[table] = strings.Fields(cols)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the SQLite schema changed. Update this expectation and review CurrentSchemaVersion by the rules in schema_version.go.\n got: %v\nwant: %v", got, want)
	}
}
