// Package runtimeconfig handles graph-config.json -- the server/CLI's own
// operational settings file (db backend selection and its connection
// settings, artifacts dir, port, claude binary, terminal command,
// extension-directory overrides, ticket-list pagination page size) --
// distinct from internal/config's workflow catalog (node/review-gate
// definitions).
//
// cmd/graph-engine's own runtimeConfig/loadRuntimeConfig reads this (merged
// with env vars and hardcoded defaults) once at process startup. This
// package is also the read/write path the web settings UI's "全体設定"
// app-settings tab uses to edit graph-config.json live (see
// internal/httpserver/app_settings.go), sharing the exact same FileConfig
// shape and file-resolution precedence so an edit through the UI always
// targets whichever file actually governs the server the next time it
// starts -- there is deliberately only one place that knows this file's
// shape and search path.
package runtimeconfig

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// FileConfig is graph-config.json's shape on disk. Every field is optional
// (omitempty on write): an absent field just means "fall back to the next
// thing in the precedence chain" -- an env var, then a hardcoded default --
// see cmd/graph-engine/runtime_config.go's loadRuntimeConfig, which is the
// only place that actually applies that chain.
type FileConfig struct {
	// DBBackend selects which store.GraphRepository implementation is used:
	// "" or "sqlite" (the default, for backward compatibility), "mysql", or
	// "http" (an HTTP custom data source plugin, DFLT-00088 -- see
	// HTTPDataSourceURL below).
	// Switching this does not migrate data -- each backend keeps its own
	// data, and switching back and forth just changes which one the app
	// points at (see DFLT-00020).
	DBBackend       string `json:"dbBackend,omitempty"`
	DBPath          string `json:"dbPath,omitempty"`
	ArtifactsDir    string `json:"artifactsDir,omitempty"`
	Port            int    `json:"port,omitempty"`
	ClaudeBinary    string `json:"claudeBinary,omitempty"`
	TerminalCommand string `json:"terminalCommand,omitempty"`
	// WorkDir is the terminal-launch fallback working directory
	// (TERMINAL_WORKDIR). It has nothing to do with a project's local path --
	// see ProjectPaths for that.
	WorkDir            string `json:"workDir,omitempty"`
	UserExtensionsDir  string `json:"userExtensionsDir,omitempty"`
	TeamExtensionsDir  string `json:"teamExtensionsDir,omitempty"`
	PaginationPageSize int    `json:"paginationPageSize,omitempty"`
	// MyName is the Web UI viewer's own display name, set from the
	// "全体設定" app-settings tab and used by the per-ticket "assign to
	// me"/"unassign" buttons (see domain.Ticket.Assignee). Unlike every
	// other field in this struct, it takes effect immediately in the
	// browser -- it's read fresh on every GET /api/settings/app rather than
	// cached in Server.cfg at startup. An empty value just means "no
	// identity configured yet", which hides the assign/unassign buttons
	// entirely.
	MyName string `json:"myName,omitempty"`

	// Host is the network interface `serve` binds its HTTP listener to.
	// Empty means the DefaultHost loopback address -- see that constant's
	// doc comment for why this server is not exposed to the network by
	// default, and what a user who deliberately wants it exposed has to
	// accept. Unlike the other fields here it has no settings-UI editor on
	// purpose: widening the listener is an operator decision, not a
	// one-click one made from a page the widened listener would itself
	// serve.
	Host string `json:"host,omitempty"`

	// MySQL connection settings, used only when DBBackend == "mysql".
	// MySQLPassword is stored verbatim, exactly as entered in the settings
	// UI: either a plaintext password, or a "${ENV_VAR_NAME}" reference to
	// be resolved at connection time by ResolveSecret. It is never resolved
	// before being written back to this file.
	//
	// A plaintext value here is deliberately NEVER handed back out over
	// GET/PUT /api/settings/app -- see RedactSecret.
	MySQLHost     string `json:"mysqlHost,omitempty"`
	MySQLPort     int    `json:"mysqlPort,omitempty"`
	MySQLDatabase string `json:"mysqlDatabase,omitempty"`
	MySQLUser     string `json:"mysqlUser,omitempty"`
	MySQLPassword string `json:"mysqlPassword,omitempty"`

	// MySQLTLS and MySQLTLSCA select and configure how the MySQL
	// connection above is secured -- store.MySQLTLSVerifyFull (the
	// default when this is empty), store.MySQLTLSVerifyCA or
	// store.MySQLTLSDisabled, and an absolute path to a PEM CA file,
	// respectively (see store.ValidateMySQLTLSSettings for the full
	// contract). Neither is a secret -- unlike MySQLPassword, both are
	// returned as-is over GET /api/settings/app, never redacted.
	MySQLTLS   string `json:"mysqlTls,omitempty"`
	MySQLTLSCA string `json:"mysqlTlsCa,omitempty"`

	// HTTP custom data source settings, used only when DBBackend == "http"
	// (DFLT-00088): the plugin's base URL (see docs/http-datasource/) and the
	// bearer token sent to it. Like MySQLPassword, the token is stored
	// verbatim -- plaintext or a "${ENV_VAR_NAME}" reference resolved at
	// startup by ResolveSecret -- and is never handed back out over the
	// settings API except through RedactSecret. The URL is not a secret.
	HTTPDataSourceURL   string `json:"httpDataSourceUrl,omitempty"`
	HTTPDataSourceToken string `json:"httpDataSourceToken,omitempty"`

	// ProjectPaths maps a project ID (the only key) to this environment's
	// local path for that project -- an absolute directory (DFLT-00080). It
	// replaced the per-project directory column the DB used to hold: the DB
	// (possibly a MySQL shared by a whole team) holds the project itself,
	// while where each member has it checked out is a per-environment
	// setting kept here. Being
	// a map, a project can have at most one path per environment; a project
	// with no entry simply has no local path ("未設定"). Read it through
	// ProjectPath and write it through SetProjectPath / Update.
	ProjectPaths map[string]string `json:"projectPaths,omitempty"`
}

// DefaultHost is the interface `serve` binds to when nothing overrides it:
// the IPv4 loopback address, i.e. this machine only.
//
// This API has no authentication of any kind (by design -- it is a local,
// single-user developer tool), and some of its endpoints are powerful:
// POST /api/claude/launch opens an interactive Claude Code session in an
// arbitrary directory, and GET /api/artifacts/{id}/content serves whatever
// was stored. Binding to every interface (the pre-DFLT-00021 behavior)
// therefore handed every other host on the same LAN -- a coffee-shop or
// office Wi-Fi included -- that entire surface. Loopback-by-default makes
// exposure an explicit, opted-into decision (host / GRAPH_HOST /
// `serve --host`, e.g. "0.0.0.0") rather than the silent default, which is
// the only part of the problem that can be fixed without inventing an
// authentication scheme this tool does not otherwise need.
const DefaultHost = "127.0.0.1"

// wildcardHosts are the bind addresses that mean "every interface on this
// machine" rather than one concrete address: the IPv4 and IPv6 unspecified
// addresses. They are the values a user sets when they deliberately want
// this server reachable from the network (see DefaultHost), and they need
// their own treatment nearly everywhere a bind address is examined -- they
// are not loopback (IsLoopbackHost), they are not usable as the host part
// of a URL (DisplayHost), and they name no single address a Host header
// could be matched against (IsWildcardHost's callers in
// internal/httpserver).
var wildcardHosts = []string{"0.0.0.0", "::"}

// normalizeHost puts a host from a config value or a Host header into the
// one shape the predicates below compare: surrounding whitespace gone, an
// IPv6 literal's "[...]" brackets gone (a Host header carries them, a
// config value does not), and a fully-qualified name's single trailing dot
// gone ("localhost." and "localhost" are the same name to DNS, and a Host
// header may legitimately carry either).
func normalizeHost(host string) string {
	h := strings.TrimSpace(host)
	if len(h) >= 2 && h[0] == '[' && h[len(h)-1] == ']' {
		h = h[1 : len(h)-1]
	}
	return strings.TrimSuffix(h, ".")
}

// IsWildcardHost reports whether host is one of wildcardHosts -- "bind to
// every interface", i.e. the deliberate network exposure DefaultHost's doc
// comment describes. An empty host is NOT a wildcard: everywhere in this
// project an unset host falls back to DefaultHost (see FileConfig.Host and
// loadRuntimeConfig), which is loopback.
func IsWildcardHost(host string) bool {
	h := normalizeHost(host)
	for _, w := range wildcardHosts {
		if h == w {
			return true
		}
	}
	return false
}

// IsLoopbackHost reports whether host names this machine and only this
// machine -- "localhost" or any address in a loopback range (127.0.0.0/8,
// ::1). It is the single definition of "is this address loopback?" for the
// whole codebase: cmd/graph-engine's `serve` exposure warning and
// internal/httpserver's Origin and Host header checks all route through it,
// so they cannot drift apart or disagree about an edge case.
//
// Before this existed the same question was asked two different ways: the
// HTTP layer used net.IP.IsLoopback, while the `serve` warning compared
// against the three string literals "127.0.0.1"/"localhost"/"::1" -- which
// wrongly warned about e.g. 127.0.0.2 (a loopback address that is not one
// of those three literals). See ticket DFLT-00023 items N-2 and N-4.
//
// A wildcard bind address is deliberately not loopback: 0.0.0.0 does include
// 127.0.0.1, but it also includes every other interface, which is exactly
// the case the warning exists for.
func IsLoopbackHost(host string) bool {
	h := normalizeHost(host)
	if h == "" {
		return false
	}
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// DisplayHost maps a bind address to a host that can actually be used in a
// URL aimed back at that listener from this same machine: a wildcard (or
// empty) bind address becomes "localhost", because "http://0.0.0.0:49173"
// is not a URL a client can reliably connect to, while any concrete address
// is returned as it is so the URL really does point at the interface the
// server is listening on.
//
// The result is a bare host, without brackets around an IPv6 literal --
// build the URL with net.JoinHostPort, which adds them where they are
// needed. See ticket DFLT-00023 item N-1: `graph-engine ui` used to
// hardcode "localhost" here, so with a concrete non-loopback host its own
// health check never reached the server it had just started.
func DisplayHost(host string) string {
	h := normalizeHost(host)
	if h == "" || IsWildcardHost(h) {
		return "localhost"
	}
	return h
}

// RedactedSecretPlaceholder is what RedactSecret substitutes for a stored
// plaintext secret, and the value a client sends back to mean "leave the
// stored secret as it is". It is deliberately a fixed, recognizable token
// rather than a length-preserving mask of asterisks, so neither the UI nor
// a human reading the JSON can mistake it for the real value.
//
// Detecting "the client sent back exactly what a GET of the stored secret
// would have shown it" is no longer this constant's job alone -- a stored
// "${ENV_VAR}" reference round-trips as itself, not as this placeholder, so
// a caller that needs to recognize either kind of resend compares the
// submitted value against RedactSecret(stored) as a whole (see
// internal/httpserver's resolveSubmittedMySQLPassword) rather than against
// this constant directly.
const RedactedSecretPlaceholder = "__GRAPH_OPS_SECRET_UNCHANGED__"

// RedactSecret maps a stored secret to the value that may safely leave the
// server over the settings API:
//
//   - "" (nothing configured) stays "", so the UI can still tell
//     "unset" from "set".
//   - A "${ENV_VAR_NAME}" reference is returned verbatim. It is a variable
//     *name*, not the secret itself; the UI needs it to render the "read
//     from env var X" hint, and a client must be able to PUT it back
//     unchanged.
//   - Anything else -- an actual plaintext password -- becomes
//     RedactedSecretPlaceholder.
//
// Security review art-86c49ea7: GET /api/settings/app used to return
// FileConfig verbatim, so the MySQL password was readable by anyone who
// could reach the port (and, because that GET needed no CSRF header and the
// server answered CORS with a wildcard origin, by any web page the user's
// browser happened to load). Redacting at the one place the value would
// otherwise leave the process is what makes that unreachable, independently
// of who can reach the port.
func RedactSecret(raw string) string {
	if raw == "" {
		return ""
	}
	if _, ok := IsEnvVarRef(raw); ok {
		return raw
	}
	return RedactedSecretPlaceholder
}

// envVarRefPattern matches a value that is, in its entirety, "${NAME}" --
// deliberately anchored (^...$) so a password that merely contains a
// substring shaped like that isn't misread as an env var reference.
var envVarRefPattern = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

// IsEnvVarRef reports whether raw is a "${ENV_VAR_NAME}" reference and, if
// so, extracts the variable's name. Used by both the connection-resolving
// side (ResolveSecret) and the settings UI (to render "from env var X"
// instead of a plaintext value) so the two agree on exactly one syntax.
func IsEnvVarRef(raw string) (envVarName string, ok bool) {
	m := envVarRefPattern.FindStringSubmatch(raw)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// ResolveSecret resolves raw into the value that should actually be used
// for a connection: if raw is a "${ENV_VAR_NAME}" reference, it looks up
// that environment variable (and fails loudly, rather than silently
// treating the literal "${...}" string as the secret, if it's unset); any
// other value -- including "" -- is returned unchanged as a plaintext
// secret.
func ResolveSecret(raw string) (value string, fromEnv bool, envVarName string, err error) {
	name, ok := IsEnvVarRef(raw)
	if !ok {
		return raw, false, "", nil
	}
	v, set := os.LookupEnv(name)
	if !set {
		return "", true, name, fmt.Errorf("environment variable %q referenced by %q is not set", name, raw)
	}
	return v, true, name, nil
}

// CandidatePaths returns graph-config.json's search path, in the same
// cwd-then-home precedence loadRuntimeConfig applies when the server starts
// (the first candidate that exists wins). home is the resolved
// $HOME/os.UserHomeDir() value -- deliberately a parameter rather than this
// package calling os.UserHomeDir() itself, so a caller (a test, in
// particular) can supply a sandboxed stand-in instead of ever touching the
// real home directory; pass "" to omit that candidate entirely (mirroring
// what callers already do when os.UserHomeDir() itself fails).
func CandidatePaths(cwd, home string) []string {
	paths := []string{filepath.Join(cwd, "graph-config.json")}
	if home != "" {
		paths = append(paths, filepath.Join(home, ".graph-ops", "config.json"))
	}
	return paths
}

// ResolvePath returns the first candidate that already exists, or the last
// candidate (home-based, "the global settings directory") if none exist yet
// -- or the only candidate, if home is "" -- so a fresh write always lands
// somewhere that will actually be read on the next server start.
func ResolvePath(cwd, home string) string {
	candidates := CandidatePaths(cwd, home)
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return candidates[len(candidates)-1]
}

// Load reads graph-config.json from its resolved path. A missing file is
// not an error -- it just yields the zero value (every setting "unset",
// falling back further down the precedence chain). The resolved path is
// always returned too, so a caller that's about to Save knows (and can
// display) exactly which file it's targeting.
//
// On error the returned FileConfig is always the zero value, never a
// partially-populated one (DFLT-00023 C-5). A malformed JSON file used to
// come back as whatever encoding/json had managed to decode before it hit
// the syntax error, which the signature made look usable; every caller
// already checks err first, so nothing observable changes, but the contract
// is now "cfg is meaningful only when err == nil" rather than something a
// future caller has to infer from the call sites.
func Load(cwd, home string) (FileConfig, string, error) {
	path := ResolvePath(cwd, home)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return FileConfig{}, path, nil
		}
		return FileConfig{}, path, err
	}
	var cfg FileConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return FileConfig{}, path, err
	}
	return cfg, path, nil
}

// Save writes cfg to graph-config.json at its resolved path, creating the
// parent directory first if needed (relevant the first time anything writes
// to the canonical home-based default). Returns the path written to.
func Save(cwd, home string, cfg FileConfig) (string, error) {
	path := ResolvePath(cwd, home)
	// 0o700: the only directory this call ever actually creates is
	// $HOME/.graph-ops (the cwd candidate's parent is cwd itself, which
	// necessarily exists), the same directory cmd/graph-engine's
	// loadRuntimeConfig and store.NewSQLiteRepository create user-only -- and
	// the file written below is already 0o600 because it can hold a MySQL
	// password. Whichever of the three runs first in a fresh environment sets
	// the mode, so they are kept in step.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return path, err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return path, err
	}
	return path, writeFileAtomic(path, raw)
}

// writeFileAtomic replaces path's contents with raw via a temp file in the
// same directory plus a rename, so a concurrent Load (which takes no lock --
// see Update) never observes a truncated, half-written file (DFLT-00080:
// the server now reads graph-config.json on every project API call while
// other requests may be saving it). If path is a symlink, the file it points
// at is replaced rather than the link itself. The result is 0o600, same as
// before.
func writeFileAtomic(path string, raw []byte) error {
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".graph-config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		cleanup()
		return err
	}
	return nil
}
