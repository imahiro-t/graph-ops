package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
)

// appSettingsResponse is GET/PUT /api/settings/app's shared response shape:
// File is the home config file's own saved values (what the settings UI's
// app-settings form shows/edits -- a blank field means "not set, falls back
// further down the precedence chain"); Effective is what this
// already-running server actually resolved at startup for the same fields
// (env var > file > hardcoded default), so the UI can show "here's what's
// really in effect right now" even though none of these fields take effect
// until the next restart; ConfigPath is the file File was read from and a PUT
// writes back to.
//
// File's type is redactedFileConfig, not runtimeconfig.FileConfig, so a
// stored plaintext secret cannot leave the process this way. Build one of
// these with newAppSettingsResponse rather than by hand.
type appSettingsResponse struct {
	File      redactedFileConfig   `json:"file"`
	Effective effectiveAppSettings `json:"effective"`
	// ConfigPath is the ONE file this endpoint reads and writes: the home
	// config file, $HOME/.graph-ops/config.json (DFLT-00124, completion
	// criterion 11). It used to mean "whichever candidate file resolution
	// happened to pick", with a second HomeConfigPath field beside it for the
	// four keys that were exceptions to it; there are no exceptions and no
	// candidates any more, so there is one path and it needs no qualifier.
	//
	// It is "" -- never a fabricated path -- when the home directory could
	// not be resolved, because there genuinely is no file then; a PUT in that
	// state fails with HOME_CONFIG_UNAVAILABLE rather than pretending to
	// save. The client shows its own wording for the empty case (see
	// runtimeconfig.HomeConfigPathForMessage, which is the CLI's equivalent
	// of that choice).
	ConfigPath string `json:"config_path"`
	// Warnings are fixed, machine-readable codes for things the server did
	// not do, on a request it nonetheless completed (HTTP 200). Clients look
	// each code up in their own message catalogue, the way they already do
	// for error codes; a code is never a sentence. See
	// warnHomeConfigUnreadable -- a GET can carry it, so the page shows the
	// home config being broken before anything has been saved.
	Warnings []string `json:"warnings,omitempty"`
}

// warnHomeConfigUnreadable is the one code appSettingsResponse.Warnings
// carries: the home config file exists but could not be read or parsed, so
// every setting on this page is coming from nowhere -- neither this
// response's values nor the next startup's. The CLI prints the same fact on
// stderr, but a user who only ever opens the Web UI never sees that, and
// without this the page would show empty fields with nothing to explain them
// (DFLT-00104, non-functional review NF-2). A PUT cannot merely warn about it
// (overwriting a file it failed to parse would discard whatever the user has
// in there), so on that side the same condition is
// domain.ErrCodeHomeConfigUnreadable -- and this is spelled as that constant
// rather than as a second literal, so the warning and the error can never
// drift into two different names for one state.
const warnHomeConfigUnreadable = string(domain.ErrCodeHomeConfigUnreadable)

// appSettingsWarnings returns the codes describing what an otherwise
// successful request could not do, given the load it is answering from. An
// unresolvable home directory is deliberately not among them: a PUT with
// nowhere to write now fails outright (HOME_CONFIG_UNAVAILABLE), so there is
// no such thing as a successful-but-unsaved response to warn about.
func appSettingsWarnings(eff runtimeconfig.Effective) []string {
	var warnings []string
	if eff.HomeConfigErr != nil {
		warnings = append(warnings, warnHomeConfigUnreadable)
	}
	return warnings
}

type effectiveAppSettings struct {
	DBBackend string `json:"dbBackend"`
	DBPath    string `json:"dbPath"`
	// HTTPDataSourceURL is the data source URL in effect when DBBackend is
	// "http" (empty otherwise). The token is never part of this block.
	HTTPDataSourceURL  string `json:"httpDataSourceUrl,omitempty"`
	ArtifactsDir       string `json:"artifactsDir"`
	UserExtensionsDir  string `json:"userExtensionsDir"`
	PaginationPageSize int    `json:"paginationPageSize"`
}

// redactedFileConfig is the home config file's contents after every
// secret-bearing field has been through runtimeconfig.RedactSecret -- the
// only shape in which they may be serialized to a client. It is a defined
// type over FileConfig rather than a plain FileConfig value so that the
// compiler, not just convention and a regression test, keeps the two apart:
// assigning a freshly loaded FileConfig into appSettingsResponse.File is now
// a compile error, and the redaction can no longer be forgotten by accident.
// (An explicit redactedFileConfig(cfg) conversion still compiles, since the
// underlying types are identical -- what the type buys is that skipping the
// redaction has to be written out deliberately, and greps as a single
// obvious line, instead of being the default that a missing function call
// silently produces. newRedactedFileConfig is the only conversion in this
// package; there should never be a second.)
//
// Being a defined type over the same struct, it marshals to exactly the same
// JSON as FileConfig -- the field tags come along -- so this is invisible to
// clients.
type redactedFileConfig runtimeconfig.FileConfig

// newRedactedFileConfig returns cfg with every secret-bearing field replaced
// by what may safely be sent to a client (see runtimeconfig.RedactSecret).
// Only scalar fields are replaced, so the copy taken by the parameter is
// defensive enough -- the caller's own cfg, which is what gets written back
// to disk, is untouched (ProjectPaths, the one map, is shared but never
// modified here).
func newRedactedFileConfig(cfg runtimeconfig.FileConfig) redactedFileConfig {
	cfg.MySQLPassword = runtimeconfig.RedactSecret(cfg.MySQLPassword)
	cfg.HTTPDataSourceToken = runtimeconfig.RedactSecret(cfg.HTTPDataSourceToken)
	// Three settings this page does not edit are dropped rather than sent
	// (DFLT-00104, security review S-4): terminalCommand and claudeBinary
	// decide what this machine executes, and host decides who can reach it.
	// This response is world-readable to anything that can reach the port
	// (there is no authentication), so settings the UI never displays have no
	// business on the wire. All three are omitempty, so they simply do not
	// appear; artifactsDir IS edited here and stays.
	cfg.TerminalCommand = ""
	cfg.ClaudeBinary = ""
	cfg.Host = ""
	return redactedFileConfig(cfg)
}

// newAppSettingsResponse is the single construction point for this
// endpoint's response, so neither handler can forget the redaction.
//
// eff comes from runtimeconfig.LoadEffective, so File shows the values that
// will actually apply after a restart -- the home config's, which is the only
// file read. Showing anything else is what would let a user "fix" a setting
// in the form, save it, and find nothing changed.
func newAppSettingsResponse(eff runtimeconfig.Effective, effective effectiveAppSettings, warnings []string) appSettingsResponse {
	return appSettingsResponse{
		File:       newRedactedFileConfig(eff.Config),
		Effective:  effective,
		ConfigPath: eff.HomeConfigPath,
		Warnings:   warnings,
	}
}

// defaultMySQLPort is the port assumed when a client leaves mysqlPort unset
// (0), matching what cmd/graph-engine's loadRuntimeConfig fills in for an
// absent mysqlPort when it actually opens the store (store.mysqlDriverConfig
// itself does no defaulting -- it would dial port 0). Applying it on both
// sides of mysqlTarget's comparison is what keeps an explicit 3306 from a
// client and an unset port on disk from looking like two different
// destinations, which matters because the settings form always sends an
// explicit port while the home config commonly has none (the field is
// omitempty).
const defaultMySQLPort = 3306

// mysqlTarget is the destination a MySQL password is handed to: the tuple
// that decides *which server, as whom, for which database, over what kind of
// connection* a credential is sent to. It is a comparable value type on
// purpose -- the whole point is the == in resolveSubmittedMySQLPassword.
//
// tlsMode/tlsCA are part of this tuple, not just host/port/database/user,
// because of this ticket's (DFLT-00037) finding F-6: TLS settings decide
// *how* a secret travels to host/port, which is exactly what S-1
// (art-991dfc4a) already cares about for the destination itself. Without
// this, a request that left host/port/database/user exactly as saved but
// changed mysqlTls to "disabled" could resolve the stored password (or
// "${ENV_VAR}" reference) and have it sent to the same server in plaintext
// -- an API-level downgrade of exactly the kind D-3 closes at the driver
// level. Note this cuts both ways deliberately: moving *from* "disabled" to
// a verified mode also counts as a change requiring retype, because judging
// "is this direction safe" (e.g. is swapping the CA file a strengthening or
// a weakening?) would itself be a new decision surface to get wrong; see
// this ticket's execution plan, D-6.
type mysqlTarget struct {
	host     string
	port     int
	database string
	user     string
	tlsMode  string
	tlsCA    string
}

// newMySQLTarget builds a mysqlTarget, defaulting port the same way
// loadRuntimeConfig does and normalizing tlsMode the same way
// store.NormalizeMySQLTLSMode does ("" means the verify-full default) --
// except that an unrecognized tlsMode is kept exactly as submitted, not
// normalized to anything, so it can never equal a validly-normalized stored
// mode (a request this malformed is going to be rejected by the caller's own
// validation anyway; this function's only job is to make sure it is never
// mistaken for an unchanged destination in the meantime).
func newMySQLTarget(host string, port int, database, user, tlsMode, tlsCA string) mysqlTarget {
	if port == 0 {
		port = defaultMySQLPort
	}
	normalizedTLSMode, err := store.NormalizeMySQLTLSMode(tlsMode)
	if err != nil {
		normalizedTLSMode = tlsMode
	}
	return mysqlTarget{host: host, port: port, database: database, user: user, tlsMode: normalizedTLSMode, tlsCA: tlsCA}
}

func storedMySQLTarget(cfg runtimeconfig.FileConfig) mysqlTarget {
	return newMySQLTarget(cfg.MySQLHost, cfg.MySQLPort, cfg.MySQLDatabase, cfg.MySQLUser, cfg.MySQLTLS, cfg.MySQLTLSCA)
}

// changedFields reports which of t's fields differ from stored, as a subset
// of the fixed vocabulary {"host", "port", "database", "user", "tlsMode",
// "tlsCa"} in that order -- never the field's actual (old or new) value.
// This is what logPasswordRetypeRejected writes to a
// MYSQL_PASSWORD_RETYPE_REQUIRED rejection log line's changed_fields field:
// one side of the comparison is exactly the connection details (and, via the
// caller, the password) an attacker attempting the S-1 exploit would have
// supplied in the request body, so only the field names -- never their
// values -- may be logged.
func (t mysqlTarget) changedFields(stored mysqlTarget) []string {
	var changed []string
	if t.host != stored.host {
		changed = append(changed, "host")
	}
	if t.port != stored.port {
		changed = append(changed, "port")
	}
	if t.database != stored.database {
		changed = append(changed, "database")
	}
	if t.user != stored.user {
		changed = append(changed, "user")
	}
	if t.tlsMode != stored.tlsMode {
		changed = append(changed, "tlsMode")
	}
	if t.tlsCA != stored.tlsCA {
		changed = append(changed, "tlsCa")
	}
	return changed
}

// resolveSubmittedMySQLPassword decides what a submitted mysqlPassword body
// field actually means: a fresh value to use as-is, or a resend of the
// secret already stored in the home config file that should resolve back to
// that stored value -- but, for a resend, only for the exact connection
// (host/port/database/user) it was saved for. cfg is the home config file's
// raw contents: the comparison is against what is actually stored.
//
// "Is this submission a resend of the stored secret?" is answered the same
// way for both shapes a stored secret can have (see runtimeconfig.
// RedactSecret): submitted == RedactSecret(stored). For a plaintext stored
// password, RedactSecret(stored) is the fixed RedactedSecretPlaceholder
// token, so this reduces to the original rule -- a client's form, populated
// from a redacted GET, sends the placeholder back unchanged whenever the
// user did not retype the password. For a stored "${ENV_VAR}" reference,
// RedactSecret(stored) is that reference itself (a variable *name* is not a
// secret, so GET hands it back verbatim) -- so a client resends the exact
// same "${ENV_VAR}" text whenever it did not change that field either. Either
// way, a match means "this value came back unedited from a GET", and a
// non-match (a newly typed plaintext password, or a brand-new "${NEW_VAR}"
// the user just typed for a server they have not saved yet) is a real edit,
// used exactly as submitted regardless of destination.
//
// The destination restriction on an actual resend is the fix for security
// review finding S-1 (art-991dfc4a), generalized to cover the "${ENV_VAR}"
// case DFLT-00036 found it did not (a stored reference used to resolve for
// any destination, since it never matched the placeholder-only check this
// function replaces). Both endpoints that accept a resend take their
// destination entirely from the request body, while the secret comes from
// disk, so without this restriction anyone able to make one request could
// point mysqlHost at a server they control, resend the stored secret's
// GET-shown value, and have this process deliver the real secret to them --
// resolving a plaintext password immediately, or (for an "${ENV_VAR}"
// resend) hosting a server that need only wait for a legitimate connection
// test/save against the real destination to leak the environment variable's
// value into that later request. Before this ticket (DFLT-00037) added TLS
// support, a plaintext password recovered this way was always recoverable in
// the clear (caching_sha2 falls back to RSA-encrypting it under a key the
// *server* offers, which a hostile server chooses) -- and mysqlTarget's
// tlsMode/tlsCA fields (see that type's doc comment, and F-6 in this
// ticket's execution plan) close the analogous gap TLS support itself would
// otherwise open: resending the stored secret alongside an unchanged
// host/port/database/user but a *weakened* mysqlTls is exactly the same
// attack, aimed at the transport instead of the destination. The persistent
// variant went through PUT: save a new mysqlHost while keeping the password
// by resending its GET-shown value. Requiring the destination -- transport
// included -- to be unchanged means a resend can only ever send the secret
// back where, and how, it already goes.
//
// The accepted cost is a UX one, decided by the user at this ticket's plan
// approval gate (DFLT-00023-03) and reaffirmed for "${ENV_VAR}" at
// DFLT-00036's: testing or saving a *changed* host, port, database or user
// requires retyping the password (or re-entering the "${ENV_VAR}"
// reference), because from here that is indistinguishable from the attack.
// For a stored "${ENV_VAR}" reference this is a real, if narrow, UX cost:
// there is no way to type the same reference name again to mean "yes, I
// really do mean this, for this new destination" -- resending it is
// indistinguishable from a browser doing it automatically. Reusing the same
// variable name against a new destination therefore takes two saves: first
// clear the password (or leave the old destination as it is) while moving
// the connection, then re-enter "${VAR}" once that new destination is the
// stored one.
//
// Not covered, deliberately, and out of this ticket's scope (recorded in
// this node's implementation notes as a residual): this function only
// guards a *resend* of the stored secret. It does nothing about the
// underlying SSRF this endpoint pair still allows -- an arbitrary
// host/port can always be named for a connection test, and a brand-new
// "${ARBITRARY_ENV_VAR}" a caller has never seen resolves and gets sent to
// whatever destination the same request names, whether or not that
// destination is the stored one.
func resolveSubmittedMySQLPassword(cfg runtimeconfig.FileConfig, target mysqlTarget, submitted string) (string, error) {
	stored := cfg.MySQLPassword
	if stored == "" || submitted != runtimeconfig.RedactSecret(stored) {
		return submitted, nil
	}
	if target != storedMySQLTarget(cfg) {
		return "", domain.NewAPIError(domain.ErrCodeMySQLPasswordRetypeRequired,
			"the saved MySQL password (or \"${ENV_VAR}\" reference) can only be reused for the connection "+
				"it was saved for; retype it to use a different host, port, database, user or TLS setting")
	}
	return stored, nil
}

// logPasswordRetypeRejected records a MYSQL_PASSWORD_RETYPE_REQUIRED
// rejection (see resolveSubmittedMySQLPassword) for both of its call sites
// (handlePutAppSettings and handleTestMySQLConnection), so the logging
// itself -- and its masking -- only has to be gotten right once.
//
// Neither the submitted nor the stored connection details are logged: only
// which fields differ (target.changedFields), via the same fixed vocabulary
// the execution plan's masking policy requires elsewhere in this package
// (securitylog.go). r.Pattern (the ServeMux pattern that matched, e.g.
// "PUT /api/settings/app") identifies which endpoint rejected the request
// without needing the raw URL path.
func (s *Server) logPasswordRetypeRejected(r *http.Request, fileCfg runtimeconfig.FileConfig, target mysqlTarget) {
	endpoint := r.Pattern
	if endpoint == "" {
		endpoint = r.Method + " " + r.URL.Path
	}
	changed := target.changedFields(storedMySQLTarget(fileCfg))
	s.rejectLog.log(r, domain.ErrCodeMySQLPasswordRetypeRequired, http.StatusBadRequest,
		slog.String("endpoint", endpoint),
		slog.String("changed_fields", strings.Join(changed, ",")),
	)
}

// isPasswordRetypeRequired reports whether err is (or wraps) the
// *domain.APIError resolveSubmittedMySQLPassword returns when a resend of
// the stored secret was submitted for a different connection than the one
// it was saved for. resolveSubmittedMySQLPassword currently returns nothing
// else, but checking the code rather than "err != nil" is what keeps the
// rejection log honest if that ever changes.
func isPasswordRetypeRequired(err error) bool {
	var apiErr *domain.APIError
	return errors.As(err, &apiErr) && apiErr.Code == domain.ErrCodeMySQLPasswordRetypeRequired
}

// resolveSubmittedHTTPDataSourceToken is resolveSubmittedMySQLPassword's
// counterpart for the HTTP custom data source token (DFLT-00088), with the
// same rule for the same reason: a submission equal to
// RedactSecret(stored) -- the placeholder for a stored plaintext token, or
// the stored "${ENV_VAR}" reference's own text -- is a resend of the stored
// secret, and is only honoured while the (normalized) httpDataSourceUrl is
// the one it was saved for. Otherwise anyone able to make one request could
// point the URL at a server they control and have graph-engine hand it the
// stored token at the next startup. Any other submission is a fresh value,
// used as submitted.
//
// URLs are compared with store.NormalizeHTTPDataSourceURL (case of scheme
// and host, an explicit default port and a trailing slash do not count as a
// change); a change of scheme, host, port or path does.
func resolveSubmittedHTTPDataSourceToken(cfg runtimeconfig.FileConfig, targetURL, submitted string) (string, error) {
	stored := cfg.HTTPDataSourceToken
	if stored == "" || submitted != runtimeconfig.RedactSecret(stored) {
		return submitted, nil
	}
	if store.NormalizeHTTPDataSourceURL(targetURL) != store.NormalizeHTTPDataSourceURL(cfg.HTTPDataSourceURL) {
		return "", domain.NewAPIError(domain.ErrCodeHTTPDataSourceTokenRetypeRequired,
			"the saved HTTP data source token (or \"${ENV_VAR}\" reference) can only be reused for the URL "+
				"it was saved for; retype it to use a different httpDataSourceUrl")
	}
	return stored, nil
}

// logHTTPDataSourceTokenRetypeRejected records an
// HTTP_DATASOURCE_TOKEN_RETYPE_REQUIRED rejection. Neither URL nor the token
// is logged -- only which endpoint rejected the request.
func (s *Server) logHTTPDataSourceTokenRetypeRejected(r *http.Request) {
	endpoint := r.Pattern
	if endpoint == "" {
		endpoint = r.Method + " " + r.URL.Path
	}
	s.rejectLog.log(r, domain.ErrCodeHTTPDataSourceTokenRetypeRequired, http.StatusBadRequest,
		slog.String("endpoint", endpoint),
		slog.String("changed_fields", "httpDataSourceUrl"),
	)
}

func (s *Server) effectiveAppSettings() effectiveAppSettings {
	roots := s.resolveRoots()
	dbBackend := s.cfg.DBBackend
	if dbBackend == "" {
		dbBackend = "sqlite"
	}
	httpURL := ""
	if dbBackend == "http" {
		httpURL = s.cfg.HTTPDataSourceURL
	}
	return effectiveAppSettings{
		DBBackend:          dbBackend,
		DBPath:             s.cfg.DBPath,
		HTTPDataSourceURL:  httpURL,
		ArtifactsDir:       s.cfg.ArtifactsDir,
		UserExtensionsDir:  roots.UserDir,
		PaginationPageSize: s.cfg.PaginationPageSize,
	}
}

// loadHomeEffective is how this file reads the settings that apply: the home
// config file, and nothing else.
//
// The cwd argument is deliberately "" rather than s.cfg.WorkDir. Its only
// use is filling Effective.StaleWorkDirConfigPath, a leftover
// graph-config.json this API never reports (see that field) -- so passing
// the server's working directory would mean an os.Stat per request whose
// result is thrown away, and would suggest in the code that the server still
// consults the directory it was started in. It does not (DFLT-00124,
// non-functional review condition NF-2). Both handlers go through here so
// there is one place to state that, and one place a test can pin it.
func (s *Server) loadHomeEffective() runtimeconfig.Effective {
	return runtimeconfig.LoadEffective("", s.cfg.HomeDir)
}

// handleGetAppSettings backs the settings UI's app-settings tab (DB path,
// artifacts dir, the node/workflow config directory override, ticket-list
// pagination page size).
//
// This endpoint needs no CSRF header (it is a GET) and this server has no
// authentication, so its response must be treated as world-readable by
// anyone who can reach the port: it deliberately answers with a redacted
// FileConfig, never the raw one.
func (s *Server) handleGetAppSettings(w http.ResponseWriter, r *http.Request) {
	eff := s.loadHomeEffective()
	writeJSON(w, http.StatusOK, newAppSettingsResponse(eff, s.effectiveAppSettings(), appSettingsWarnings(eff)))
}

// handlePutAppSettings saves the fields this endpoint owns
// (dbBackend/dbPath/mysqlHost/mysqlPort/mysqlDatabase/mysqlUser/
// mysqlPassword/mysqlTls/mysqlTlsCa/httpDataSourceUrl/httpDataSourceToken/
// artifactsDir/userExtensionsDir/paginationPageSize/myName) into the home
// config file, preserving every other field already in it
// (port/host/claudeBinary/terminalCommand/workDir/teamExtensionsDir/
// projectPaths) untouched.
//
// It is one write, through runtimeconfig.UpdateHome, which serializes it with
// every other in-process writer of that file -- in particular the project
// API's projectPaths edits (DFLT-00080) -- so a concurrent PATCH
// /api/projects/{id} and this PUT can never lose each other's change. One
// write also means there is no "saved some of it" outcome to report:
// artifactsDir used to go to the home config in a second write while
// everything else went to whichever file resolution had picked, which needed
// a code of its own for a half-done save (DFLT-00124 removed both the second
// file and that code).
//
// An unresolvable home directory is an error, not a warning: there is no file
// to write, so the request fails with HOME_CONFIG_UNAVAILABLE (500) rather
// than returning 200 for a save that went nowhere (completion criterion 10).
//
// Those owned fields are a full replacement, not a patch: each one is
// written from the request body as submitted, so a field the body leaves out
// is saved as its zero value -- omitting it clears whatever was stored.
// mysqlPassword is where that costs the most: leave it out and the saved
// password is erased, since only a submission that resolveSubmittedMySQLPassword
// recognizes as a resend of the stored secret (see below) means "keep the
// stored one". Any client other than this repo's own settings form must
// therefore send the complete set of owned fields on every PUT; the web UI
// does (packages/web/src/lib/settingsApi.ts's saveAppSettings submits the
// whole AppSettingsFile). The only fields an omission does not silently
// clear are the validated ones, and they are rejected rather than preserved:
// a paginationPageSize below 1 is a 400, as is a missing
// mysqlHost/mysqlDatabase/mysqlUser, an unsupported mysqlTls value, or a
// mysqlTls of "verify-ca" with no mysqlTlsCa, while dbBackend is "mysql".
//
// This is the endpoint's intended contract, not a defect -- reading an
// absent field as "unchanged" would leave no way to clear one, and every
// owned field behaves the same way. It is spelled out here, and in README.md
// (EN and JA), because a PUT that quietly drops a stored secret is not
// something the author of a second client would guess (ticket DFLT-00023
// item S-5).
//
// None of this takes effect for the currently-running process -- these are
// all read once at startup (see cmd/graph-engine's loadRuntimeConfig) -- so
// the response's `effective` block still reflects the pre-edit values until
// the server is restarted; the frontend is expected to surface that.
//
// mysqlPassword is the one field not taken from the body as-is: because
// handleGetAppSettings redacts it, a form populated from that GET submits
// back exactly what that GET showed it (RedactedSecretPlaceholder for a
// stored plaintext password, or the "${ENV_VAR}" text itself for a stored
// reference) whenever the user did not edit the field, and either of those
// means "keep the stored value" -- editing any other field must not wipe out
// a secret the client was never allowed to see in full. That "keep it"
// shorthand is only honoured while the connection the password belongs to is
// itself unchanged; see resolveSubmittedMySQLPassword for why, and for the
// retype this costs a user who is moving the database somewhere else.
func (s *Server) handlePutAppSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DBBackend         string `json:"dbBackend"`
		DBPath            string `json:"dbPath"`
		MySQLHost         string `json:"mysqlHost"`
		MySQLPort         int    `json:"mysqlPort"`
		MySQLDatabase     string `json:"mysqlDatabase"`
		MySQLUser         string `json:"mysqlUser"`
		MySQLPassword     string `json:"mysqlPassword"`
		MySQLTLS          string `json:"mysqlTls"`
		MySQLTLSCA        string `json:"mysqlTlsCa"`
		HTTPDataSourceURL string `json:"httpDataSourceUrl"`
		// HTTPDataSourceToken follows mysqlPassword's rules: see
		// resolveSubmittedHTTPDataSourceToken.
		HTTPDataSourceToken string `json:"httpDataSourceToken"`
		ArtifactsDir        string `json:"artifactsDir"`
		UserExtensionsDir   string `json:"userExtensionsDir"`
		PaginationPageSize  int    `json:"paginationPageSize"`
		MyName              string `json:"myName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.PaginationPageSize < 1 {
		writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeInvalidPaginationPageSize,
			"pagination page size must be 1 or greater, got %d", body.PaginationPageSize))
		return
	}
	dbBackend := body.DBBackend
	if dbBackend == "" {
		dbBackend = "sqlite"
	}
	if err := store.ValidateBackend(body.DBBackend); err != nil {
		writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation, "%s", err.Error()))
		return
	}
	// MySQL selection requires enough to actually connect; sqlite's dbPath
	// has no equivalent required-field check (an empty dbPath already falls
	// back to a default -- see loadRuntimeConfig).
	if dbBackend == "mysql" && (body.MySQLHost == "" || body.MySQLDatabase == "" || body.MySQLUser == "") {
		writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation,
			"mysqlHost, mysqlDatabase and mysqlUser are required when dbBackend is \"mysql\""))
		return
	}
	// TLS settings are validated the same way, and only under the same
	// condition (D-4 in this ticket's execution plan): a leftover invalid
	// mysqlTls left over from a past mysql experiment must not block saving
	// under sqlite, matching loadRuntimeConfig's own dbBackend-gated
	// validation.
	if dbBackend == "mysql" {
		if err := store.ValidateMySQLTLSSettings(body.MySQLTLS, body.MySQLTLSCA); err != nil {
			writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation, "%s", err.Error()))
			return
		}
	}

	target := newMySQLTarget(body.MySQLHost, body.MySQLPort, body.MySQLDatabase, body.MySQLUser, body.MySQLTLS, body.MySQLTLSCA)
	var passwordErr error
	_, _, err := runtimeconfig.UpdateHome(s.cfg.HomeDir, func(fileCfg *runtimeconfig.FileConfig) error {
		// Resolve the password before any of the body's values are copied
		// over fileCfg: the comparison resolveSubmittedMySQLPassword makes is
		// against what is *currently* on disk, which is exactly what the next
		// few lines are about to overwrite.
		password, err := resolveSubmittedMySQLPassword(*fileCfg, target, body.MySQLPassword)
		if err != nil {
			if isPasswordRetypeRequired(err) {
				s.logPasswordRetypeRejected(r, *fileCfg, target)
			}
			passwordErr = err
			return err
		}
		// The HTTP data source token: first decide whether the submission is
		// a resend of the stored token (and refuse it for a changed URL),
		// then validate the URL/token pair -- only when http is selected, so
		// a leftover URL never blocks saving under sqlite/mysql. The token is
		// judged as it will be stored: a "${ENV_VAR}" reference is not
		// resolved at save time and counts as non-empty. The retype check
		// comes first on purpose, so moving a stored token to e.g. a
		// plaintext URL is reported as "retype the token", not as a
		// validation error that would suggest the token itself was fine.
		httpToken, err := resolveSubmittedHTTPDataSourceToken(*fileCfg, body.HTTPDataSourceURL, body.HTTPDataSourceToken)
		if err != nil {
			s.logHTTPDataSourceTokenRetypeRejected(r)
			passwordErr = err
			return err
		}
		if dbBackend == "http" {
			if err := store.ValidateHTTPDataSourceSettings(body.HTTPDataSourceURL, httpToken); err != nil {
				passwordErr = domain.NewAPIError(domain.ErrCodeValidation, "%s", err.Error())
				return passwordErr
			}
		}

		fileCfg.DBBackend = body.DBBackend
		fileCfg.DBPath = body.DBPath
		fileCfg.MySQLHost = body.MySQLHost
		fileCfg.MySQLPort = body.MySQLPort
		fileCfg.MySQLDatabase = body.MySQLDatabase
		fileCfg.MySQLUser = body.MySQLUser
		fileCfg.MySQLPassword = password
		fileCfg.MySQLTLS = body.MySQLTLS
		fileCfg.MySQLTLSCA = body.MySQLTLSCA
		fileCfg.HTTPDataSourceURL = body.HTTPDataSourceURL
		fileCfg.HTTPDataSourceToken = httpToken
		fileCfg.ArtifactsDir = body.ArtifactsDir
		fileCfg.UserExtensionsDir = body.UserExtensionsDir
		fileCfg.PaginationPageSize = body.PaginationPageSize
		fileCfg.MyName = body.MyName
		return nil
	})
	if passwordErr != nil {
		writeError(w, http.StatusBadRequest, passwordErr)
		return
	}
	if err != nil {
		// Nothing was saved, so the client must not be told otherwise. The
		// code is chosen by cause, because the two need opposite advice: an
		// unresolvable home directory is an environment problem
		// (HOME_CONFIG_UNAVAILABLE), while an unparseable home config is a
		// specific file to repair or delete (HOME_CONFIG_UNREADABLE) that no
		// amount of retrying will get past. The message below is
		// developer-facing; the client picks its wording from the code alone.
		code := domain.ErrCodeHomeConfigUnavailable
		var readErr *runtimeconfig.HomeConfigReadError
		if errors.As(err, &readErr) {
			code = domain.ErrCodeHomeConfigUnreadable
		}
		writeError(w, http.StatusInternalServerError, domain.NewAPIError(code,
			"could not save the app settings to the home config file: %s", err.Error()))
		return
	}

	// Re-read rather than echoing what was submitted, so `file` shows what
	// the next startup will actually see.
	eff := s.loadHomeEffective()
	writeJSON(w, http.StatusOK, newAppSettingsResponse(eff, s.effectiveAppSettings(), appSettingsWarnings(eff)))
}

// testMySQLConnectionResponse is POST /api/settings/app/test-mysql-connection's
// response shape. A failed connection attempt is not an HTTP error (still
// 200 OK, Ok:false) -- it's an expected, common outcome of a "接続テスト"
// (connection test) button, and the UI just needs the error text to show
// inline next to the button. Error never includes the raw DSN (which would
// embed the password); it's built from the driver's own error message,
// which go-sql-driver/mysql does not include the password in.
type testMySQLConnectionResponse struct {
	Ok    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// handleTestMySQLConnection backs the settings UI's "接続テスト" button:
// it must work against whatever the user currently has typed into the
// form, before anything is saved, so the request body carries the
// candidate connection settings directly rather than reading the home config
// file's saved values.
//
// The one exception is mysqlPassword: a form that was populated by the
// redacted GET holds back exactly what that GET showed it -- either
// runtimeconfig.RedactedSecretPlaceholder or, for a stored "${ENV_VAR}"
// reference, that reference's own text -- rather than the real secret, so
// testing the *saved* connection would otherwise always fail (or, for the
// "${ENV_VAR}" case, resolve the wrong thing -- see the DFLT-00036 history
// below). Either shape is resolved back against the home config file here --
// the same rule handlePutAppSettings applies -- which keeps the button
// working without the secret ever having been sent to the browser. It is
// resolved only while host/port/database/user still name the saved
// connection, so this endpoint cannot be turned into a way of delivering the
// stored secret to a MySQL server of the caller's choosing; see
// resolveSubmittedMySQLPassword.
//
// The home config file is now read unconditionally on every call, unlike
// before DFLT-00036: telling "is this submission a resend of the stored
// secret?" apart from "is this a freshly typed value?" requires the stored
// value's RedactSecret shape to compare against (see
// resolveSubmittedMySQLPassword), which cannot be known without loading it
// first. The file is small and local, so this is not a meaningful cost; it
// replaces an earlier optimization (O-8/M-4) that skipped the read whenever
// the submitted value was not literally RedactedSecretPlaceholder -- a
// shortcut that is exactly what let a stored "${ENV_VAR}" reference resolve
// for any destination unchecked (DFLT-00036's S-1 gap).
func (s *Server) handleTestMySQLConnection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MySQLHost     string `json:"mysqlHost"`
		MySQLPort     int    `json:"mysqlPort"`
		MySQLDatabase string `json:"mysqlDatabase"`
		MySQLUser     string `json:"mysqlUser"`
		MySQLPassword string `json:"mysqlPassword"`
		MySQLTLS      string `json:"mysqlTls"`
		MySQLTLSCA    string `json:"mysqlTlsCa"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.MySQLHost == "" || body.MySQLDatabase == "" || body.MySQLUser == "" {
		writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation,
			"mysqlHost, mysqlDatabase and mysqlUser are required to test a connection"))
		return
	}
	if err := store.ValidateMySQLTLSSettings(body.MySQLTLS, body.MySQLTLSCA); err != nil {
		writeError(w, http.StatusBadRequest, domain.NewAPIError(domain.ErrCodeValidation, "%s", err.Error()))
		return
	}
	target := newMySQLTarget(body.MySQLHost, body.MySQLPort, body.MySQLDatabase, body.MySQLUser, body.MySQLTLS, body.MySQLTLSCA)

	// The raw read, not LoadEffective's redacted view: the comparison below
	// needs the secret exactly as it sits on disk. It is the home config and
	// only the home config -- the same file a save writes to -- so a resend
	// is judged against the value that will actually be used.
	fileCfg, err := runtimeconfig.LoadHomeConfig(s.cfg.HomeDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	rawPassword, err := resolveSubmittedMySQLPassword(fileCfg, target, body.MySQLPassword)
	if err != nil {
		if isPasswordRetypeRequired(err) {
			s.logPasswordRetypeRejected(r, fileCfg, target)
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}

	password, _, _, err := runtimeconfig.ResolveSecret(rawPassword)
	if err != nil {
		writeJSON(w, http.StatusOK, testMySQLConnectionResponse{Ok: false, Error: err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := store.PingMySQL(ctx, store.Config{
		MySQLHost: target.host, MySQLPort: target.port, MySQLDatabase: target.database,
		MySQLUser: target.user, MySQLPassword: password,
		MySQLTLSMode: target.tlsMode, MySQLTLSCAFile: target.tlsCA,
	}); err != nil {
		writeJSON(w, http.StatusOK, testMySQLConnectionResponse{Ok: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, testMySQLConnectionResponse{Ok: true})
}
