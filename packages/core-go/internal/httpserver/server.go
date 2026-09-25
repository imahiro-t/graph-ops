// Package httpserver exposes the graph engine over the same REST + SSE
// contract the original TS server.ts served, so packages/web needs no
// changes to talk to this implementation.
package httpserver

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/graph-ops/core-go/internal/autopilot"
	"github.com/graph-ops/core-go/internal/autopilot/runner"
	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
)

type Config struct {
	// Host is the address `serve` bound this server's listener to (always a
	// concrete value by the time it gets here -- see cmd/graph-engine's
	// loadRuntimeConfig). It is used only by the Host header check
	// (allowedHost), which needs to know which name, besides loopback, is a
	// legitimate way to address this server. An empty value is treated as
	// runtimeconfig.DefaultHost, matching the fallback everywhere else.
	Host string
	// DBBackend is "sqlite" or "mysql" (see runtimeconfig.FileConfig's doc
	// comment); only used to render the "現在有効な設定" (currently in
	// effect) block on the app-settings tab -- it plays no role in how this
	// already-running server talks to its own repo (that was decided once,
	// at process startup, by cmd/graph-engine's store.Open call).
	DBBackend string
	DBPath    string
	// HTTPDataSourceURL is the HTTP custom data source URL this server was
	// started with (DBBackend "http", DFLT-00088) -- like DBBackend, only
	// shown in the app-settings tab's "currently in effect" block. The token
	// is deliberately not part of Config at all.
	HTTPDataSourceURL  string
	ArtifactsDir       string
	ClaudeBinary       string
	WorkDir            string
	TerminalCommand    string
	TerminalWorkDir    string
	UserExtensionsDir  string
	TeamExtensionsDir  string
	PaginationPageSize int
	// HomeDir is the resolved os.UserHomeDir() value (or "" if unresolvable)
	// -- used only by the app-settings API (internal/httpserver/app_settings.go)
	// to locate the home config file the same way cmd/graph-engine's
	// loadRuntimeConfig does. Deliberately supplied here rather than calling
	// os.UserHomeDir() directly in that handler, so a test can sandbox it.
	HomeDir string
	// Logger receives this package's "request rejected" security events (see
	// securitylog.go) -- currently HOST_NOT_ALLOWED, CSRF_HEADER_REQUIRED,
	// MYSQL_PASSWORD_RETYPE_REQUIRED, REQUEST_BODY_TOO_LARGE and
	// API_ROUTE_NOT_FOUND. When nil, New falls back to a
	// slog.NewTextHandler on os.Stderr rather than discarding output: these
	// are "we detected and blocked something" events, and a nil Logger
	// silently turning into a no-op logger would reproduce this ticket's own
	// root cause (DFLT-00023 added the rejections; nothing ever logged
	// them). cmd/graph-engine's cmdServe passes one explicitly, but any
	// future caller that forgets to still gets stderr output instead of
	// silence.
	Logger *slog.Logger
	// AutopilotLauncher opens the orchestrator's terminal for POST
	// /api/tickets/{id}/autopilot (DFLT-00142). nil -- what `serve` passes --
	// means the real one (internal/terminal with TerminalCommand and
	// ClaudeBinary); tests pass a fake so that no terminal is opened.
	AutopilotLauncher runner.Launcher
}

type Server struct {
	repo      store.GraphRepository
	engine    *engine.GraphEngine
	cfg       Config
	rejectLog *rejectLogger
	// logger is the same logger rejectLog writes to, for this package's
	// other operational warnings (e.g. a best-effort home config
	// cleanup that failed -- see handleDeleteProject).
	logger *slog.Logger
}

func New(repo store.GraphRepository, eng *engine.GraphEngine, cfg Config) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return &Server{repo: repo, engine: eng, cfg: cfg, rejectLog: newRejectLogger(logger), logger: logger}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)

	mux.HandleFunc("GET /api/tickets", s.handleListTickets)
	mux.HandleFunc("POST /api/tickets", s.handleCreateTicket)
	mux.HandleFunc("GET /api/tickets/{id}", s.handleGetTicket)
	mux.HandleFunc("PATCH /api/tickets/{id}", s.handleUpdateTicket)
	mux.HandleFunc("DELETE /api/tickets/{id}", s.handleDeleteTicket)

	mux.HandleFunc("POST /api/projects", s.handleCreateProject)
	mux.HandleFunc("GET /api/projects", s.handleListProjects)
	mux.HandleFunc("GET /api/projects/{id}", s.handleGetProject)
	mux.HandleFunc("PATCH /api/projects/{id}", s.handleUpdateProject)
	mux.HandleFunc("DELETE /api/projects/{id}", s.handleDeleteProject)
	mux.HandleFunc("GET /api/projects/{id}/autopilot-settings", s.handleGetAutopilotSettings)
	mux.HandleFunc("PUT /api/projects/{id}/autopilot-settings", s.handlePutAutopilotSettings)
	mux.HandleFunc("GET /api/current-project", s.handleGetCurrentProject)
	mux.HandleFunc("PUT /api/current-project", s.handleSetCurrentProject)

	mux.HandleFunc("GET /api/projects/{id}/labels", s.handleListLabels)
	mux.HandleFunc("POST /api/projects/{id}/labels", s.handleCreateLabel)
	mux.HandleFunc("PATCH /api/labels/{id}", s.handleUpdateLabel)
	mux.HandleFunc("DELETE /api/labels/{id}", s.handleDeleteLabel)

	mux.HandleFunc("POST /api/tickets/{id}/refine", s.handleRefine)
	mux.HandleFunc("POST /api/tickets/{id}/autopilot", s.handleStartAutopilot)
	mux.HandleFunc("GET /api/autopilot/runs", s.handleListAutopilotRuns)
	mux.HandleFunc("POST /api/tickets/{id}/close", s.handleCloseTicket)
	mux.HandleFunc("POST /api/tickets/{id}/reopen", s.handleReopenTicket)
	mux.HandleFunc("POST /api/tickets/{id}/artifacts", s.handleCreateArtifact)
	mux.HandleFunc("GET /api/tickets/{id}/artifacts/download", s.handleDownloadTicketArtifacts)
	mux.HandleFunc("GET /api/artifacts/{id}/content", s.handleGetArtifactContent)

	mux.HandleFunc("POST /api/nodes/{id}/complete", s.handleCompleteNode)
	mux.HandleFunc("PATCH /api/nodes/{id}", s.handleUpdateNode)

	mux.HandleFunc("POST /api/claude/launch", s.handleClaudeLaunch)

	mux.HandleFunc("GET /api/settings/catalog", s.handleGetSettingsCatalog)
	mux.HandleFunc("PUT /api/settings/catalog", s.handlePutSettingsCatalog)
	mux.HandleFunc("GET /api/settings/app", s.handleGetAppSettings)
	mux.HandleFunc("PUT /api/settings/app", s.handlePutAppSettings)
	mux.HandleFunc("POST /api/settings/app/test-mysql-connection", s.handleTestMySQLConnection)
	mux.HandleFunc("GET /api/settings/node-types", s.handleListSettingsNodeTypes)
	mux.HandleFunc("GET /api/settings/node-types/{type}", s.handleGetSettingsNodeType)
	mux.HandleFunc("PUT /api/settings/node-types/{type}", s.handlePutSettingsNodeType)
	mux.HandleFunc("GET /api/settings/skills", s.handleListSettingsSkills)
	mux.HandleFunc("GET /api/settings/skills/{name}", s.handleGetSettingsSkill)
	mux.HandleFunc("PUT /api/settings/skills/{name}", s.handlePutSettingsSkill)
	mux.HandleFunc("GET /api/settings/report-template", s.handleGetSettingsReportTemplate)
	mux.HandleFunc("PUT /api/settings/report-template", s.handlePutSettingsReportTemplate)
	mux.HandleFunc("GET /api/settings/plan-template", s.handleGetSettingsPlanTemplate)
	mux.HandleFunc("PUT /api/settings/plan-template", s.handlePutSettingsPlanTemplate)
	mux.HandleFunc("GET /api/settings/review-template", s.handleGetSettingsReviewTemplate)
	mux.HandleFunc("PUT /api/settings/review-template", s.handlePutSettingsReviewTemplate)

	// An /api/ path with no route of its own is a 404 from the API, not the
	// SPA. Without this it would fall through to the catch-all below and be
	// answered with index.html and a 200 -- so a client calling a misspelled,
	// or since-removed, endpoint would get HTML where it expected JSON and no
	// indication anything was wrong. DFLT-00103 removed GET
	// /api/tickets/{id}/executable-nodes; "removed" has to mean gone, not
	// "quietly answers with the web UI".
	//
	// One side effect worth knowing about: this pattern also swallows the
	// 405 ServeMux would otherwise produce for a known path called with the
	// wrong method (POST /api/health was 405 with an Allow header, and is
	// now this 404). A caller can no longer tell "no such endpoint" from
	// "wrong method" -- accepted, and called out in the release notes,
	// because nothing in this project depends on the distinction and the
	// alternative is leaving unrouted /api/ paths answering 200 with HTML.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		s.rejectLog.log(r, domain.ErrCodeAPIRouteNotFound, http.StatusNotFound,
			slog.String("path_class", pathClass(r.URL.Path)))
		writeError(w, http.StatusNotFound, domain.NewAPIError(domain.ErrCodeAPIRouteNotFound,
			"no such API endpoint: %s %s", r.Method, r.URL.Path))
	})

	// A tombstone for the route DFLT-00103 removed (SEC-03). Without it,
	// /artifacts-static/... falls through to the SPA catch-all below and is
	// answered with index.html and a 200 -- nothing from the artifacts
	// directory leaks either way, but "the route is gone" is worth saying
	// plainly to anyone who still points something at it, and 404 is what
	// the ticket asks for and what the test below pins.
	mux.HandleFunc("/artifacts-static/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, domain.NewAPIError(domain.ErrCodeRouteNotFound,
			"the /artifacts-static/ route was removed; read artifacts through GET /api/artifacts/{id}/content"))
	})

	// Serve the embedded, built web UI for everything else. In `npm run dev`
	// this handler is simply never hit (Vite's dev server + proxy is used
	// instead); it only matters for the single-binary `serve` path.
	mux.Handle("/", staticWebHandler())

	// Layering, outermost first:
	//
	//  1. withSecurityHeaders -- outermost of all, so that EVERY response
	//     carries the clickjacking headers, including the 403s the two
	//     layers below produce before any handler runs. Being outermost is
	//     also what makes it impossible to forget the headers when a route
	//     is added: nothing registered on mux can bypass it.
	//  2. withAllowedHost -- a request whose Host header does not name this
	//     server is answered before any other layer looks at it.
	//  3. withCORS -- answers preflights and enforces the CSRF header.
	//  4. withRequestBodyLimit -- innermost, wrapping only the mux: a
	//     request rejected by 2 or 3 never has its body read at all, so
	//     capping it any further out would buy nothing.
	return withSecurityHeaders(withAllowedHost(s.cfg.Host, s.rejectLog, withCORS(s.rejectLog, withRequestBodyLimit(s.rejectLog, mux))))
}

// csrfHeaderName is the header every state-changing request to this API must
// carry. This server has no authentication, and (before DFLT-00021) answered
// CORS preflights with a wildcard origin and listened on every interface --
// so, without this, any web page the user's browser happens to
// load (or any other host on the LAN) could silently drive POST/PATCH/PUT/
// DELETE against it, e.g. creating a project pointed at an arbitrary
// directory via POST /api/projects and then opening an interactive Claude
// Code terminal there with an attacker-chosen prompt via POST
// /api/claude/launch (Security review node-5f79e568). Deliberately left out
// of Access-Control-Allow-Headers below (only "Content-Type" is allowed
// there), so a cross-origin fetch/XHR that tries to add it fails the
// browser's CORS preflight before the real request is ever sent; a plain
// HTML <form> POST can't add custom headers either. The app's own frontend
// always sends it (packages/web/src/lib/apiFetch.ts) -- same-origin requests
// (including through the Vite dev server's proxy) are never subject to CORS
// preflight, so this is transparent for legitimate use.
const csrfHeaderName = "X-Graph-Engine-Client"

// requiresCSRFHeader reports whether method is state-changing and therefore
// must carry csrfHeaderName. GET/HEAD/OPTIONS are read-only (or the CORS
// preflight itself) and are exempt.
func requiresCSRFHeader(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// isLoopbackOrigin reports whether origin is an http(s) origin whose host is
// this same machine -- the only kind of cross-origin caller this API has a
// legitimate reason to answer (concretely: the Vite dev server on
// localhost:3000 when it is pointed straight at the API instead of proxying
// it, see packages/web/vite.config.ts). A malformed origin, a non-http
// scheme, or any other host is not loopback.
//
// The "is this loopback?" half is runtimeconfig.IsLoopbackHost, shared with
// the Host header check (allowedHost) and with `serve`'s exposure warning,
// so the three cannot answer the same question differently -- this used to
// be one of two divergent hand-rolled versions (DFLT-00023 item N-2).
func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return runtimeconfig.IsLoopbackHost(u.Hostname())
}

// hostHeaderName extracts the bare host part of a Host header value: its
// port removed if it has one, and an IPv6 literal's "[...]" brackets
// stripped, so the result can be handed straight to net.ParseIP or
// runtimeconfig's host predicates.
//
// net.SplitHostPort is tried first and only trusted when it succeeds, since
// it fails both on a value with no port at all ("localhost") and on an
// unbracketed IPv6 literal -- neither of which should be mistaken for
// "host ..., port ..." by naive splitting on the last colon. Whatever is
// left is then unbracketed, which covers the no-port IPv6 form "[::1]".
func hostHeaderName(hostHeader string) string {
	h := hostHeader
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	if len(h) >= 2 && h[0] == '[' && h[len(h)-1] == ']' {
		h = h[1 : len(h)-1]
	}
	return h
}

// allowedHost reports whether a request carrying this Host header value is
// addressed to this server, given the address it was bound to (cfgHost, ""
// meaning runtimeconfig.DefaultHost).
//
// This is the DNS rebinding defense (ticket DFLT-00023 item S-2). This API
// is unauthenticated and its port is a published constant, so an attacker
// can point a short-TTL domain of their own at 127.0.0.1 and have the
// victim's browser treat the attacker's page as same-origin with this
// server. Same-origin is precisely what defeats the other three layers at
// once: the loopback-only CORS check never fires, the X-Graph-Engine-Client
// header can be set freely because no preflight is involved, and binding to
// loopback does not help when the browser making the request is on this
// machine. What the attacker cannot control is the Host header the browser
// sends -- it carries their domain, never a name this server answers to.
//
// Accepted:
//   - any loopback name or address (runtimeconfig.IsLoopbackHost), which
//     covers localhost, 127.0.0.0/8 and ::1 -- how the browser, the Vite dev
//     proxy and `graph-engine ui`'s health check all address this server;
//   - the bound address itself, for a deliberately exposed server reached at
//     e.g. http://192.168.1.5:49173/;
//   - any IP literal, but ONLY when the bind address is a wildcard
//     (0.0.0.0 / ::). Such a server is reachable at every address this
//     machine happens to have, which cannot be enumerated here -- yet
//     rejecting them all would break the one configuration whose entire
//     purpose is being reached from elsewhere. Allowing literals only is
//     what keeps the rebinding defense intact: an attack of this shape needs
//     the victim's browser to resolve a *name*, so the Host header it sends
//     is a name, never a literal.
func allowedHost(cfgHost, hostHeader string) bool {
	name := hostHeaderName(hostHeader)
	if name == "" {
		return false
	}
	if runtimeconfig.IsLoopbackHost(name) {
		return true
	}
	if cfgHost == "" {
		cfgHost = runtimeconfig.DefaultHost
	}
	if strings.EqualFold(strings.TrimSuffix(name, "."), strings.TrimSpace(cfgHost)) {
		return true
	}
	if runtimeconfig.IsWildcardHost(cfgHost) && net.ParseIP(name) != nil {
		return true
	}
	return false
}

// withAllowedHost rejects any request whose Host header does not name this
// server -- see allowedHost for why (DNS rebinding). It wraps everything,
// including the static file and artifact handlers, because a rebound origin
// reading stored artifacts is as much of a leak as one driving the API.
//
// Every rejection is recorded through rl (see securitylog.go) -- this is one
// of the two attack-detection rejections this ticket (DFLT-00035) exists to
// make visible. The raw Host header is deliberately never logged; only its
// classification, length and a process-local fingerprint are (§2.3 of the
// execution plan, art-28b0e6d7).
func withAllowedHost(cfgHost string, rl *rejectLogger, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedHost(cfgHost, r.Host) {
			rl.log(r, domain.ErrCodeHostNotAllowed, http.StatusForbidden,
				slog.String("path_class", pathClass(r.URL.Path)),
				slog.String("host_kind", hostKind(r.Host)),
				slog.Int("host_len", len(r.Host)),
				slog.String("host_fp", rl.hostFingerprint(r.Host)),
			)
			writeError(w, http.StatusForbidden, domain.NewAPIError(
				domain.ErrCodeHostNotAllowed,
				"request Host %q is not an address this server answers to", r.Host,
			))
			return
		}
		h.ServeHTTP(w, r)
	})
}

// withCORS answers cross-origin requests, and enforces the CSRF header on
// state-changing ones.
//
// Access-Control-Allow-Origin used to be an unconditional "*", which let any
// page the user's browser happened to load read this unauthenticated API's
// GET responses -- ticket and artifact content, and (security review
// art-86c49ea7) the MySQL password that GET /api/settings/app returned at the
// time. Echoing only loopback origins keeps the one legitimate cross-origin
// caller working while making every remote page's read fail in the browser.
// Requests with no Origin at all (same-origin navigations, the app's own
// fetches, curl) never needed the header and still don't get one.
//
// This is defense in depth layered on top of the two fixes that do not
// depend on the browser cooperating: not serving the secret in the first
// place (see runtimeconfig.RedactSecret) and not listening beyond this
// machine by default (see runtimeconfig.DefaultHost).
//
// The CSRF rejection is recorded through rl (see securitylog.go); the
// Origin header itself is never logged, only whether it was absent, a
// recognized loopback origin, or something else. Note this event also fires
// for a legitimate client that simply forgot the header (not only for an
// attack), which is worth keeping in mind when reading it back.
func withCORS(rl *rejectLogger, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Vary: Origin regardless of the outcome -- the response differs by
		// Origin, so a cache must not reuse one origin's response for another.
		w.Header().Add("Vary", "Origin")
		if origin := r.Header.Get("Origin"); origin != "" && isLoopbackOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if requiresCSRFHeader(r.Method) && r.Header.Get(csrfHeaderName) == "" {
			rl.log(r, domain.ErrCodeCSRFHeaderRequired, http.StatusForbidden,
				slog.String("path_class", pathClass(r.URL.Path)),
				slog.String("origin_kind", originKind(r.Header.Get("Origin"))),
			)
			writeError(w, http.StatusForbidden, domain.NewAPIError(
				domain.ErrCodeCSRFHeaderRequired,
				"missing required %s header", csrfHeaderName,
			))
			return
		}
		h.ServeHTTP(w, r)
	})
}

// withSecurityHeaders stamps the clickjacking defense on every response this
// server produces (DFLT-00103 / SEC-04). The two headers say the same thing
// to two generations of browser: CSP frame-ancestors is what current ones
// honour, X-Frame-Options is what older ones understand.
//
// Why this matters for an API nobody authenticates: the three layers that
// already exist -- the Host check (allowedHost), the loopback-only Origin
// echo and the required CSRF header -- all defend against a *foreign* page
// scripting this server. None of them defends against a foreign page
// *framing* it. A page that embeds http://127.0.0.1:49173/ in an <iframe>
// gets the real SPA, running on its real origin: the Host header is
// legitimate, apiFetch attaches the CSRF header itself, and no cross-origin
// read is ever attempted. All the attacker has to add is a transparent
// overlay and one click from the user -- and this UI's one-click actions
// include passing a review approval gate and launching Claude Code in an
// external terminal (packages/web/src/components/TicketItem.tsx). Refusing
// to be framed at all is what closes that.
//
// Two more headers ride along here rather than being repeated per handler,
// for the same "impossible to forget" reason:
//
//   - X-Content-Type-Options: nosniff. It used to be set in exactly one
//     place (writeArtifactContent). Every response this server writes
//     declares its own Content-Type, so there is never a reason to let a
//     browser guess one -- and the /api/ 404 added by this ticket reflects
//     the request path back in a JSON body, which is safe as written
//     (encoding/json escapes < > &, and the type is application/json) but
//     is exactly the shape that stops being safe if a browser decides the
//     body is HTML after all.
//   - Referrer-Policy: no-referrer. An artifact preview's URL names the
//     artifact being read; SEC-09 is about not handing that to a third
//     party, and this is the half of it that applies to every link and
//     subresource rather than just to MarkdownViewer's remote images.
//     packages/web/index.html carries the same policy in a <meta>, which is
//     what covers `npm run dev`, where Vite serves the page instead.
//
// Set (not Add) on purpose: a handler that needs a different policy for its
// own response -- writeArtifactContent, the single documented exception --
// overwrites these values rather than appending a second, conflicting
// header. Two Content-Security-Policy headers would be intersected by the
// browser, which is exactly the wrong semantics there: the artifact preview
// needs frame-ancestors 'self', and intersecting that with 'none' would
// leave it unframeable and break the UI's inline preview.
func withSecurityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		h.ServeHTTP(w, r)
	})
}

// maxRequestBodyBytes caps how much of a request body this server will read
// (DFLT-00103 / SEC-08). 64 MiB matches the ceiling the HTTP data source
// client already uses for a response body (internal/store/http.go), and is
// the smallest value that cannot break an existing legitimate call: an
// artifact is posted as JSON with its bytes base64-encoded inline (an image
// or a report's HTML), so the wire size is ~4/3 of the file's.
//
// The point is not to police size precisely -- it is that a body of
// unbounded length must not be buffered into memory by an unauthenticated
// server, which is what `--host 0.0.0.0` makes reachable from the network.
const maxRequestBodyBytes = 64 << 20

// withRequestBodyLimit caps every request body at maxRequestBodyBytes.
//
// One middleware rather than a MaxBytesReader in each handler: this package
// decodes a JSON body in ~20 places, and the failure mode of forgetting one
// of them is invisible until someone exploits it. Wrapping r.Body here means
// a handler added later is covered without its author knowing this exists.
//
// GET/HEAD/OPTIONS are skipped because they carry no body worth reading;
// wrapping them would only add a pointless allocation per request.
//
// A body that exceeds the cap surfaces as *http.MaxBytesError out of
// whatever decoder read it, and writeError turns that into 413 regardless of
// the status the handler asked for -- see writeError.
//
// The rejection is also recorded through rl. It is watched for on the way
// out (the status this middleware's own wrapper saw) rather than at the
// point of classification, because the error is raised inside whichever
// decoder happened to read the body, several frames below, and writeError --
// where it is turned into a 413 -- has neither the request nor the logger.
// Watching the status keeps one source of truth for "this was a 413" without
// threading either of them through ~20 handlers. It is worth recording at
// all because repeated over-cap bodies are the only trace an operator would
// have of the memory-exhaustion attempt SEC-08 is about.
func withRequestBodyLimit(rl *rejectLogger, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			h.ServeHTTP(w, r)
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		}
		rec := &statusRecorder{ResponseWriter: w}
		h.ServeHTTP(rec, r)
		if rec.status == http.StatusRequestEntityTooLarge {
			rl.log(r, domain.ErrCodeRequestBodyTooLarge, rec.status,
				slog.String("path_class", pathClass(r.URL.Path)))
		}
	})
}

// statusRecorder remembers the status a handler wrote, so a middleware can
// act on it afterwards (withRequestBodyLimit). It deliberately implements
// nothing beyond http.ResponseWriter: this package's handlers never assert
// http.Flusher or http.Hijacker, and it only ever wraps requests that carry
// a body (never the SSE or zip-streaming GETs), so there is no optional
// interface here to lose.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// apiErrorPayload is the wire shape of an error response:
// {"error": {"code": "...", "message": "..."}}. Code is machine-readable and
// is what the frontend uses to resolve a localized (ja/en) message; Message
// is a developer-facing English string for logs/debugging only.
//
// Details is present only when the error carries structured context (see
// domain.APIError.Details), e.g. {"keys": ["maxTickets"]}.
type apiErrorPayload struct {
	Code    domain.ErrorCode `json:"code"`
	Message string           `json:"message"`
	Details map[string]any   `json:"details,omitempty"`
}

// writeError writes a structured error response. If err is (or wraps) a
// *domain.APIError, its Code is used as-is; otherwise a code is inferred
// from the HTTP status (400 -> VALIDATION_ERROR, 404 -> TICKET_NOT_FOUND,
// anything else -> INTERNAL_ERROR).
//
// One case overrides the caller entirely: a body that hit
// maxRequestBodyBytes (withRequestBodyLimit). Handlers differ in what status
// they pass for a failed decode -- some 400, some 500 -- and "the server
// broke" is the wrong answer to "you sent too much". Classifying it here,
// where every error response in this package already passes, is what makes
// the 413 uniform without auditing ~20 call sites.
func writeError(w http.ResponseWriter, status int, err error) {
	code := domain.ErrCodeInternal
	switch status {
	case http.StatusBadRequest:
		code = domain.ErrCodeValidation
	case http.StatusNotFound:
		code = domain.ErrCodeTicketNotFound
	}

	var details map[string]any
	var apiErr *domain.APIError
	if errors.As(err, &apiErr) {
		code = apiErr.Code
		details = apiErr.Details
	}

	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		status = http.StatusRequestEntityTooLarge
		code = domain.ErrCodeRequestBodyTooLarge
	}

	writeJSON(w, status, map[string]apiErrorPayload{
		"error": {Code: code, Message: err.Error(), Details: details},
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":       "ok",
		"db":           s.cfg.DBPath,
		"artifactsDir": s.cfg.ArtifactsDir,
	})
}

// resolveRoots resolves the same user-/team-tier roots the CLI resolves from
// the same GRAPH_USER_EXTENSIONS_DIR/GRAPH_TEAM_EXTENSIONS_DIR (or home
// config) settings, so the server and CLI never disagree about where
// user/team extension files live. Neither consults the process's working
// directory any more -- a team tier exists only where it was explicitly
// pointed at (see config.ResolveRoots).
func (s *Server) resolveRoots() config.Roots {
	return config.ResolveRoots(s.cfg.UserExtensionsDir, s.cfg.TeamExtensionsDir)
}

// statusForError maps an error to an HTTP status: a *domain.APIError whose
// Code is known to correspond to a client-facing 400/404 is mapped
// accordingly regardless of what status the caller would otherwise use;
// anything else falls back to the caller-supplied default (typically 500,
// since most other failure modes here are server-side).
func statusForError(err error, fallback int) int {
	var apiErr *domain.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case domain.ErrCodeInvalidPrefix, domain.ErrCodePrefixTaken,
			domain.ErrCodeNoCurrentProject, domain.ErrCodeValidation, domain.ErrCodeTitleRequired,
			domain.ErrCodeCatalogCycleDetected, domain.ErrCodeCatalogUnknownReference,
			domain.ErrCodeCatalogDuplicateNode, domain.ErrCodeCatalogInvalidDocument, domain.ErrCodeInvalidMaxIterations,
			domain.ErrCodeInvalidReportTemplate,
			domain.ErrCodeInvalidLabelName, domain.ErrCodeInvalidLabelColor, domain.ErrCodeLabelNameTaken,
			domain.ErrCodeParentTicketUnsupported, domain.ErrCodeAutopilotSettingLocked:
			return http.StatusBadRequest
		case domain.ErrCodeProjectNotFound, domain.ErrCodeTicketNotFound, domain.ErrCodeNodeNotFound, domain.ErrCodeArtifactNotFound,
			domain.ErrCodeLabelNotFound:
			return http.StatusNotFound
		// Its own case, not folded into the 400 group: the request was
		// valid and the row exists -- what stands in the way is the
		// state the row is in right now, which is what 409 means
		// (DFLT-00102).
		case domain.ErrCodeInvalidNodeState:
			return http.StatusConflict
		// The autopilot (DFLT-00142): a start that collides with another
		// run or with the root's state is a conflict with the current
		// state, like INVALID_NODE_STATE; a project with no local path is
		// the request's precondition not being met, a 400 like the
		// validation errors above.
		case autopilot.ErrCodeAlreadyRunning, autopilot.ErrCodeRootFinished,
			autopilot.ErrCodeInvalidRunState, autopilot.ErrCodeRegistryLockTimed:
			return http.StatusConflict
		case autopilot.ErrCodeLocalPathNotSet:
			return http.StatusBadRequest
		case autopilot.ErrCodeRunNotFound:
			return http.StatusNotFound
		}
	}
	return fallback
}
