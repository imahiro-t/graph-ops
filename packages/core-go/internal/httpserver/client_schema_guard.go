package httpserver

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/store"
)

// clientSchemaCheckInterval is how long one schema-record check is trusted.
// A running Web UI server re-reads the record at most this often, so a
// member raising the minimum client version with a newer graph-engine is
// noticed within it (DFLT-00331). The CLI checks on every call instead
// (store.Open's Init), so only this long-lived server needs it. 30 seconds
// trades an extra read per request against how long an out-of-date server
// can keep writing.
const clientSchemaCheckInterval = 30 * time.Second

// clientSchemaGuard remembers the last answer of the repository's
// store.ClientSchemaChecker. Its state lives on the Server rather than in
// Routes(), which tests call once per request.
type clientSchemaGuard struct {
	checker store.ClientSchemaChecker // nil: the repository keeps no schema record
	logger  *slog.Logger
	now     func() time.Time // replaced by tests

	mu        sync.Mutex
	checked   bool
	checkedAt time.Time
	tooOld    error // the CLIENT_TOO_OLD of the last check, nil if it passed
}

func newClientSchemaGuard(repo store.GraphRepository, logger *slog.Logger) *clientSchemaGuard {
	checker, _ := repo.(store.ClientSchemaChecker)
	return &clientSchemaGuard{checker: checker, logger: logger, now: time.Now}
}

// tooOldError returns CLIENT_TOO_OLD when the latest check (at most
// clientSchemaCheckInterval old) found this server out of date, nil
// otherwise. A check that fails for any other reason -- the DB cannot be
// reached, say -- keeps the previous answer: before any CLIENT_TOO_OLD it
// stops nothing (the handler runs and meets the same failure with its own
// error); after one, the server stays stopped. Holding mu across the read makes concurrent requests
// share one check.
func (g *clientSchemaGuard) tooOldError() error {
	if g == nil || g.checker == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if g.checked && now.Sub(g.checkedAt) < clientSchemaCheckInterval {
		return g.tooOld
	}
	err := g.checker.CheckClientSchema()
	var apiErr *domain.APIError
	tooOld := g.tooOld
	switch {
	case err == nil:
		tooOld = nil
	case errors.As(err, &apiErr) && apiErr.Code == domain.ErrCodeClientTooOld:
		if g.tooOld == nil {
			// Once per change, not per request: the operator reading the
			// UI server's log learns why the pages stopped working.
			g.logger.Error(err.Error(), slog.String("event", "client_schema"),
				slog.String("code", string(domain.ErrCodeClientTooOld)))
		}
		tooOld = err
	default:
		// A failed check keeps the previous answer: the record never goes
		// down and this server's own version does not change until it is
		// restarted, so a transient DB error must not reopen the API of a
		// server already found out of date.
		g.logger.Warn("checking the database's schema record failed: "+err.Error(),
			slog.String("event", "client_schema"))
	}
	g.checked, g.checkedAt, g.tooOld = true, now, tooOld
	return tooOld
}

// clientSchemaExempt reports whether path is served even by an out-of-date
// server: the health check, and the settings API, which works on the home
// config and extension files rather than the DB -- and is where a person
// looks at the data source this server was started with.
func clientSchemaExempt(path string) bool {
	return path == "/api/health" || strings.HasPrefix(path, "/api/settings/")
}

// withClientSchemaGuard answers an /api/ request with 503 CLIENT_TOO_OLD,
// without calling its handler, once the database has been migrated past
// what this server's graph-engine knows (DFLT-00331). 503 rather than a 4xx:
// neither the request nor the state of a row is at fault -- this server is,
// and it cannot answer until it is updated and restarted. Non-/api/ paths
// (the web UI's own files) are served as usual, so the page that shows the
// error still loads.
func withClientSchemaGuard(g *clientSchemaGuard, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && !clientSchemaExempt(r.URL.Path) {
			if err := g.tooOldError(); err != nil {
				writeError(w, http.StatusServiceUnavailable, err)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}
