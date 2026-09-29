package main

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/httpserver"
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

// `ui` reuses whatever server answers /api/health, which an out-of-date
// server still does. Once the database has moved past that server, its 503
// CLIENT_TOO_OLD must reach the person running `ui` -- with the code and the
// advice to stop that server -- rather than a bare "unexpected status 503"
// (DFLT-00331). The server here is a real one, started while the record
// still matched it, as a server left running across another member's
// update would be; its first DB request is what finds it out of date. The
// message names the refused call and prints the code once (DFLT-00353).
func TestUI_ReusedServerOlderThanTheDBReportsClientTooOld(t *testing.T) {
	repo, _, dbPath := newSubprocessSQLiteRepo(t)
	proj, err := repo.CreateProject("P", "TEST")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	stale := httptest.NewServer(httpserver.New(repo, engine.New(repo), httpserver.Config{ArtifactsDir: t.TempDir(), HomeDir: t.TempDir()}).Routes())
	defer stale.Close()
	raiseMinClientSchema(t, dbPath)

	if !uiHealthCheck(stale.URL) {
		t.Fatal("an out-of-date server's health check must still pass (that is why `ui` reuses it)")
	}

	for name, call := range map[string]func() error{
		"GET /api/projects":        func() error { _, err := fetchProjectsViaAPI(stale.URL); return err },
		"PUT /api/current-project": func() error { return switchCurrentProjectViaAPI(stale.URL, proj.ID) },
	} {
		err := call()
		var apiErr *domain.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeClientTooOld {
			t.Errorf("%s: err = %v, want CLIENT_TOO_OLD", name, err)
			continue
		}
		msg := err.Error()
		if prefix := "CLIENT_TOO_OLD: " + name + " was refused by the UI server already running at " + stale.URL; !strings.HasPrefix(msg, prefix) {
			t.Errorf("%s: message does not start with %q:\n%s", name, prefix, msg)
		}
		for _, want := range []string{"Stop that server", "Updating to a new release", "The server said: this database needs graph-engine schema version"} {
			if !strings.Contains(msg, want) {
				t.Errorf("%s: message does not contain %q:\n%s", name, want, msg)
			}
		}
		if n := strings.Count(msg, "CLIENT_TOO_OLD"); n != 1 {
			t.Errorf("%s: CLIENT_TOO_OLD appears %d times, want once:\n%s", name, n, msg)
		}
	}
}

// A CLIENT_TOO_OLD message without the server's usual "CLIENT_TOO_OLD: "
// prefix (another server's wording) is quoted as it is, and the code is still
// printed once.
func TestUIServerStatusError_ClientTooOldWithoutPrefix(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"CLIENT_TOO_OLD","message":"plain text"}}`))
	}))
	defer ts.Close()

	_, err := fetchProjectsViaAPI(ts.URL)
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.ErrCodeClientTooOld {
		t.Fatalf("err = %v, want CLIENT_TOO_OLD", err)
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "CLIENT_TOO_OLD: GET /api/projects was refused by the UI server already running at "+ts.URL) {
		t.Errorf("message does not name the call:\n%s", msg)
	}
	if !strings.HasSuffix(msg, "The server said: plain text") {
		t.Errorf("server message not quoted as it is:\n%s", msg)
	}
	if n := strings.Count(msg, "CLIENT_TOO_OLD"); n != 1 {
		t.Errorf("CLIENT_TOO_OLD appears %d times, want once:\n%s", n, msg)
	}
}

// Other failures keep the server's code and message; an answer without the
// error body falls back to the status alone.
func TestUIServerStatusError_OtherAnswers(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/projects" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"code":"INTERNAL_ERROR","message":"disk I/O error"}}`))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>bad gateway</html>"))
	}))
	defer ts.Close()

	_, err := fetchProjectsViaAPI(ts.URL)
	if err == nil || err.Error() != "GET /api/projects: unexpected status 500: INTERNAL_ERROR: disk I/O error" {
		t.Errorf("GET /api/projects: err = %v", err)
	}
	err = switchCurrentProjectViaAPI(ts.URL, "p")
	if err == nil || err.Error() != "PUT /api/current-project: unexpected status 502" {
		t.Errorf("PUT /api/current-project: err = %v", err)
	}
}
