package httpserver

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/store"
)

// DFLT-00331: a running Web UI server whose graph-engine the DB has moved
// past answers its /api/ requests with 503 CLIENT_TOO_OLD, re-checking the
// DB's schema record at most every clientSchemaCheckInterval.

// schemaCheckingRepo is a SQLite repository whose CheckClientSchema answer
// the test controls and counts.
type schemaCheckingRepo struct {
	*store.SQLiteRepository
	mu     sync.Mutex
	answer error
	calls  atomic.Int32
}

func (r *schemaCheckingRepo) CheckClientSchema() error {
	r.calls.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.answer
}

func (r *schemaCheckingRepo) set(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.answer = err
}

func clientTooOldErr() error {
	return domain.NewAPIError(domain.ErrCodeClientTooOld, "CLIENT_TOO_OLD: update graph-engine").WithDetails(map[string]any{
		"db_schema_version": 2, "min_client_schema_version": 2, "client_schema_version": 1,
	})
}

// newSchemaGuardServer is newTestServer with a repository whose schema check
// the test drives, a fake clock, and the server's log captured.
func newSchemaGuardServer(t *testing.T) (*Server, *schemaCheckingRepo, *time.Time, *bytes.Buffer) {
	t.Helper()
	sqlite, err := store.NewSQLiteRepository(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	if err := sqlite.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	repo := &schemaCheckingRepo{SQLiteRepository: sqlite}
	repo.set(nil)
	var logBuf bytes.Buffer
	s := New(repo, engine.New(repo), Config{
		ArtifactsDir: t.TempDir(), HomeDir: t.TempDir(),
		Logger: slog.New(slog.NewTextHandler(&logBuf, nil)),
	})
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	s.schemaGuard.now = func() time.Time { return now }
	return s, repo, &now, &logBuf
}

func TestClientSchemaGuard_TooOldAnswers503(t *testing.T) {
	s, repo, _, logBuf := newSchemaGuardServer(t)
	repo.set(clientTooOldErr())

	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/api/tickets?all=true"},
		{http.MethodGet, "/api/projects"},
		{http.MethodPost, "/api/projects"},
		{http.MethodGet, "/api/no-such-endpoint"},
	} {
		rec := doJSON(t, s, req.method, req.path, nil)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503 (body %s)", req.method, req.path, rec.Code, rec.Body.String())
			continue
		}
		body := decodeError(t, rec)
		if body.Code != domain.ErrCodeClientTooOld {
			t.Errorf("%s %s code = %q, want CLIENT_TOO_OLD", req.method, req.path, body.Code)
		}
		if body.Details["min_client_schema_version"] != float64(2) {
			t.Errorf("%s %s details = %v, want the schema versions", req.method, req.path, body.Details)
		}
	}

	// The health check, the settings API (home config and extension
	// files, not the DB) and the web UI's own files are still served.
	for _, path := range []string{"/api/health", "/api/settings/app", "/api/settings/catalog", "/"} {
		if rec := doJSON(t, s, http.MethodGet, path, nil); rec.Code == http.StatusServiceUnavailable {
			t.Errorf("GET %s = 503, want it served (body %s)", path, rec.Body.String())
		}
	}

	if got := strings.Count(logBuf.String(), "event=client_schema"); got != 1 {
		t.Errorf("CLIENT_TOO_OLD logged %d times, want once:\n%s", got, logBuf.String())
	}
}

func TestClientSchemaGuard_CachesForTheInterval(t *testing.T) {
	s, repo, now, _ := newSchemaGuardServer(t)

	if rec := doJSON(t, s, http.MethodGet, "/api/projects", nil); rec.Code != http.StatusOK {
		t.Fatalf("GET /api/projects = %d, want 200", rec.Code)
	}
	if got := repo.calls.Load(); got != 1 {
		t.Fatalf("first request checked %d times, want 1", got)
	}

	// Another member raises the minimum: within the interval, the cached
	// "passed" still stands and the DB is not read again.
	repo.set(clientTooOldErr())
	*now = now.Add(clientSchemaCheckInterval - time.Second)
	if rec := doJSON(t, s, http.MethodGet, "/api/projects", nil); rec.Code != http.StatusOK {
		t.Fatalf("within the interval, GET /api/projects = %d, want 200 (cached)", rec.Code)
	}
	if got := repo.calls.Load(); got != 1 {
		t.Fatalf("a request within the interval re-checked (%d checks)", got)
	}

	// Past the interval, the new answer is picked up.
	*now = now.Add(2 * time.Second)
	if rec := doJSON(t, s, http.MethodGet, "/api/projects", nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("past the interval, GET /api/projects = %d, want 503", rec.Code)
	}
	if got := repo.calls.Load(); got != 2 {
		t.Fatalf("checks after the interval = %d, want 2", got)
	}
	// And the 503 is cached the same way.
	if rec := doJSON(t, s, http.MethodGet, "/api/projects", nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /api/projects = %d, want 503 (cached)", rec.Code)
	}
	if got := repo.calls.Load(); got != 2 {
		t.Fatalf("checks = %d, want still 2", got)
	}
}

// A check that fails for another reason (the DB cannot be read, say) is
// not CLIENT_TOO_OLD and stops nothing: the handler runs as usual.
func TestClientSchemaGuard_OtherCheckFailureDoesNotBlock(t *testing.T) {
	s, repo, _, logBuf := newSchemaGuardServer(t)
	repo.set(errors.New("reading sqlite schema version: disk I/O error"))

	if rec := doJSON(t, s, http.MethodGet, "/api/projects", nil); rec.Code != http.StatusOK {
		t.Fatalf("GET /api/projects = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logBuf.String(), "disk I/O error") {
		t.Errorf("the failed check was not logged:\n%s", logBuf.String())
	}
}

// A repository without a schema record (the HTTP data source, a test fake)
// has no guard at all.
func TestClientSchemaGuard_RepositoryWithoutCheckerIsNotGuarded(t *testing.T) {
	g := newClientSchemaGuard(plainRepo{}, slog.Default())
	if g.checker != nil {
		t.Fatalf("a repository without CheckClientSchema got a checker")
	}
	if err := g.tooOldError(); err != nil {
		t.Fatalf("tooOldError = %v, want nil", err)
	}
}

// plainRepo is a GraphRepository that does not implement
// store.ClientSchemaChecker (embedding the interface leaves every method
// unimplemented, which is fine: the guard only type-asserts).
type plainRepo struct{ store.GraphRepository }

func TestStatusForError_ClientTooOldIs503(t *testing.T) {
	if got := statusForError(clientTooOldErr(), http.StatusInternalServerError); got != http.StatusServiceUnavailable {
		t.Errorf("statusForError(CLIENT_TOO_OLD) = %d, want 503", got)
	}
}
