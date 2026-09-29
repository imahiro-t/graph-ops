package main

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00331: a graph-engine older than the database's recorded minimum
// client schema version stops before touching the database -- every
// subcommand that opens the store, serve included -- with CLIENT_TOO_OLD on
// stderr and a non-zero exit, and without the MySQL TLS advice openStore
// gives for other failures.

// raiseMinClientSchema makes the SQLite DB at dbPath require a newer
// graph-engine than this one, the way a newer release would record it.
func raiseMinClientSchema(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("opening %s: %v", dbPath, err)
	}
	defer db.Close()
	next := store.CurrentSchemaVersion + 1
	res, err := db.Exec(`UPDATE graphops_schema SET schema_version = ?, min_client_schema_version = ? WHERE id = 1`, next, next)
	if err != nil {
		t.Fatalf("raising the schema record: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("raising the schema record updated %d rows, want 1", n)
	}
}

func TestCLI_ClientTooOldStopsEveryStoreCommand(t *testing.T) {
	runMainIfSubprocess()

	repo, dir, dbPath := newSubprocessSQLiteRepo(t)
	proj, err := repo.CreateProject("P", "TEST")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	raiseMinClientSchema(t, dbPath)

	for _, args := range [][]string{
		{"list-projects"},
		{"create-ticket", "t", "--project", proj.ID},
		{"serve", "--port", "0"},
	} {
		t.Run(args[0], func(t *testing.T) {
			out, code := runCLISubprocess(t, "TestCLI_ClientTooOldStopsEveryStoreCommand", dir, dbPath, args...)
			if code == 0 {
				t.Fatalf("exit code 0, want non-zero; output:\n%s", out)
			}
			for _, want := range []string{"Error: CLIENT_TOO_OLD", "Update graph-engine (the GraphOps plugin)", "Nothing was written to the database"} {
				if !strings.Contains(out, want) {
					t.Errorf("output does not contain %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "TLS") {
				t.Errorf("CLIENT_TOO_OLD must not carry the MySQL TLS advice:\n%s", out)
			}
		})
	}

	tickets, err := repo.ListTicketsByProject(proj.ID)
	if err != nil {
		t.Fatalf("ListTicketsByProject: %v", err)
	}
	if len(tickets) != 0 {
		t.Errorf("create-ticket wrote %d ticket(s) through a too-old client", len(tickets))
	}
}

// On MySQL, openStore appends TLS recovery steps to every failure to open
// the store -- except CLIENT_TOO_OLD, where the connection itself worked.
func TestExplainOpenStoreError_ClientTooOldHasNoTLSAdvice(t *testing.T) {
	rc := runtimeConfig{DBBackend: "mysql", HomeDir: t.TempDir(), MySQLTLSMode: "verify-full"}

	tooOld := domain.NewAPIError(domain.ErrCodeClientTooOld, "CLIENT_TOO_OLD: update graph-engine")
	got := explainOpenStoreError(rc, tooOld)
	if got != error(tooOld) {
		t.Errorf("CLIENT_TOO_OLD was rewrapped: %v", got)
	}

	other := errors.New("dial tcp: connection refused")
	got = explainOpenStoreError(rc, other)
	if !errors.Is(got, other) || !strings.Contains(got.Error(), "TLS settings") {
		t.Errorf("other MySQL failures should keep the TLS advice, got: %v", got)
	}

	rc.DBBackend = "sqlite"
	if got := explainOpenStoreError(rc, other); got != other {
		t.Errorf("a SQLite failure was rewrapped: %v", got)
	}
}
