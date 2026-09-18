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
	DBBackend          string
	DBPath             string
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
	// to locate graph-config.json the same way cmd/graph-engine's
	// loadRuntimeConfig does. Deliberately supplied here rather than calling
	// os.UserHomeDir() directly in that handler, so a test can sandbox it.
	HomeDir string
	// Logger receives this package's "request rejected" security events (see
	// securitylog.go) -- currently HOST_NOT_ALLOWED, CSRF_HEADER_REQUIRED and
	// MYSQL_PASSWORD_RETYPE_REQUIRED. When nil, New falls back to a
	// slog.NewTextHandler on os.Stderr rather than discarding output: these
	// are "we detected and blocked something" events, and a nil Logger
	// silently turning into a no-op logger would reproduce this ticket's own
	// root cause (DFLT-00023 added the rejections; nothing ever logged
	// them). cmd/graph-engine's cmdServe passes one explicitly, but any
	// future caller that forgets to still gets stderr output instead of
	// silence.
	Logger *slog.Logger
}

type Server struct {
	repo      store.GraphRepository
	engine    *engine.GraphEngine
	cfg       Config
	rejectLog *rejectLogger
	// logger is the same logger rejectLog writes to, for this package's
	// other operational warnings (e.g. a best-effort graph-config.json
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
	mux.HandleFunc("GET /api/current-project", s.handleGetCurrentProject)
	mux.HandleFunc("PUT /api/current-project", s.handleSetCurrentProject)

	mux.HandleFunc("GET /api/projects/{id}/labels", s.handleListLabels)
	mux.HandleFunc("POST /api/projects/{id}/labels", s.handleCreateLabel)
	mux.HandleFunc("PATCH /api/labels/{id}", s.handleUpdateLabel)
	mux.HandleFunc("DELETE /api/labels/{id}", s.handleDeleteLabel)

	mux.HandleFunc("POST /api/tickets/{id}/refine", s.handleRefine)
	mux.HandleFunc("POST /api/tickets/{id}/close", s.handleCloseTicket)
	mux.HandleFunc("POST /api/tickets/{id}/reopen", s.handleReopenTicket)
	mux.HandleFunc("GET /api/tickets/{id}/executable-nodes", s.handleExecutableNodes)
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

	mux.Handle("/artifacts-static/", http.StripPrefix("/artifacts-static/", http.FileServer(http.Dir(s.cfg.ArtifactsDir))))

	// Serve the embedded, built web UI for everything else. In `npm run dev`
	// this handler is simply never hit (Vite's dev server + proxy is used
	// instead); it only matters for the single-binary `serve` path.
	mux.Handle("/", staticWebHandler())

	// withAllowedHost is outermost: a request whose Host header does not
	// name this server is answered before any other layer looks at it.
	return withAllowedHost(s.cfg.Host, s.rejectLog, withCORS(s.rejectLog, mux))
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// apiErrorPayload is the wire shape of an error response:
// {"error": {"code": "...", "message": "..."}}. Code is machine-readable and
// is what the frontend uses to resolve a localized (ja/en) message; Message
// is a developer-facing English string for logs/debugging only.
type apiErrorPayload struct {
	Code    domain.ErrorCode `json:"code"`
	Message string           `json:"message"`
}

// writeError writes a structured error response. If err is (or wraps) a
// *domain.APIError, its Code is used as-is; otherwise a code is inferred
// from the HTTP status (400 -> VALIDATION_ERROR, 404 -> TICKET_NOT_FOUND,
// anything else -> INTERNAL_ERROR).
func writeError(w http.ResponseWriter, status int, err error) {
	code := domain.ErrCodeInternal
	switch status {
	case http.StatusBadRequest:
		code = domain.ErrCodeValidation
	case http.StatusNotFound:
		code = domain.ErrCodeTicketNotFound
	}

	var apiErr *domain.APIError
	if errors.As(err, &apiErr) {
		code = apiErr.Code
	}

	writeJSON(w, status, map[string]apiErrorPayload{
		"error": {Code: code, Message: err.Error()},
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":       "ok",
		"db":           s.cfg.DBPath,
		"artifactsDir": s.cfg.ArtifactsDir,
	})
}

// loadCatalog re-reads the three-tier workflow config on every refine call so
// edits to project/user config files take effect without restarting the
// server.
func (s *Server) loadCatalog() (config.Catalog, error) {
	// languageOverride is always "" here: an HTTP request carries no
	// per-call session context the way a CLI invocation's --language flag
	// does, so language resolution rests solely on the persistent
	// user/team Document.Language tiers LoadWithRoots reads itself -- see
	// the execution plan's section 1.4 ("HTTP...における明示引数の扱い") and
	// section 1.6a for why this file needs no separate
	// ResolveLanguage/LocalizedDefault call the way settings.go does.
	return config.LoadWithRoots(s.cfg.WorkDir, s.cfg.UserExtensionsDir, s.cfg.TeamExtensionsDir, "")
}

// loadCatalogForTicket is loadCatalog, but -- when ticketID resolves to a
// ticket whose Project has a local path in this environment (graph-config.json's
// projectPaths, DFLT-00080) -- resolves the team tier from that local path
// instead of the server process's own cwd (s.cfg.WorkDir). A project with no
// local path falls back to loadCatalog, never an error.
//
// Without this, editing a project's settings through the settings UI (which
// always writes under that project's local path, see settingsScope in
// settings.go) would never actually affect that project's tickets unless the
// server process happened to be running with the project's directory as its
// cwd -- silently breaking the "変更が実際のチケット実行に反映される"
// completion criterion for any multi-project deployment. An explicit
// GRAPH_TEAM_EXTENSIONS_DIR (s.cfg.TeamExtensionsDir) still always wins, same
// precedence as every other team-tier resolution in this codebase, so an
// operator who has deliberately pinned a single shared team config is never
// silently overridden by whichever project a ticket happens to belong to.
func (s *Server) loadCatalogForTicket(ticketID string) (config.Catalog, error) {
	if s.cfg.TeamExtensionsDir != "" {
		return s.loadCatalog()
	}
	ticket, err := s.repo.GetTicket(ticketID)
	if err != nil || ticket == nil {
		return s.loadCatalog()
	}
	localPath := s.projectLocalPath(ticket.ProjectID)
	if localPath == "" {
		return s.loadCatalog()
	}
	// languageOverride "" -- see loadCatalog's doc comment above.
	return config.LoadWithRoots(localPath, s.cfg.UserExtensionsDir, "", "")
}

// resolveRoots resolves the same user-/team-tier roots the CLI resolves from
// the same GRAPH_USER_EXTENSIONS_DIR/GRAPH_TEAM_EXTENSIONS_DIR (or
// graph-config.json) settings, so the server and CLI never disagree about
// where user/team extension files live.
func (s *Server) resolveRoots() (config.Roots, error) {
	return config.ResolveRoots(s.cfg.WorkDir, s.cfg.UserExtensionsDir, s.cfg.TeamExtensionsDir)
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
			domain.ErrCodeInvalidScope, domain.ErrCodeCatalogCycleDetected, domain.ErrCodeCatalogUnknownReference,
			domain.ErrCodeCatalogDuplicateNode, domain.ErrCodeCatalogInvalidDocument, domain.ErrCodeInvalidMaxIterations,
			domain.ErrCodeInvalidReportTemplate, domain.ErrCodeProjectLocalPathNotSet, domain.ErrCodeProjectTeamRootIsUserRoot,
			domain.ErrCodeInvalidLabelName, domain.ErrCodeInvalidLabelColor, domain.ErrCodeLabelNameTaken:
			return http.StatusBadRequest
		case domain.ErrCodeProjectNotFound, domain.ErrCodeTicketNotFound, domain.ErrCodeNodeNotFound, domain.ErrCodeArtifactNotFound,
			domain.ErrCodeLabelNotFound:
			return http.StatusNotFound
		}
	}
	return fallback
}
