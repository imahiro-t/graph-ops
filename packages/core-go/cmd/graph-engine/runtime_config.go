package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
)

// runtimeConfig is the server/CLI's own operational settings (db path, port,
// claude binary, artifacts dir) -- distinct from internal/config's workflow
// catalog (node/review-gate definitions). Precedence: env vars > a
// graph-config.json found in cwd or $HOME/.graph-ops/ > defaults.
// The file itself (shape, search path) is internal/runtimeconfig's job, so
// the web settings UI can read/write the exact same file this merges on top
// of -- see that package's doc comment.
type runtimeConfig struct {
	// DBBackend is "sqlite", "mysql" or "http" -- always normalized to one of
	// these (never ""), see loadRuntimeConfig.
	DBBackend    string
	DBPath       string
	ArtifactsDir string
	Port         int
	// Host is the interface `serve` binds to -- always a concrete address,
	// never "" (see loadRuntimeConfig). It defaults to
	// runtimeconfig.DefaultHost (loopback); see that constant for why this
	// server is not network-exposed unless someone explicitly asks for it.
	Host            string
	ClaudeBinary    string
	WorkDir         string
	TerminalCommand string
	TerminalWorkDir string
	// MySQL connection settings, meaningful only when DBBackend == "mysql".
	// MySQLPassword has already been resolved (via
	// runtimeconfig.ResolveSecret) from a possible "${ENV_VAR_NAME}"
	// reference to its real value -- callers never need to deal with that
	// syntax themselves.
	MySQLHost     string
	MySQLPort     int
	MySQLDatabase string
	MySQLUser     string
	MySQLPassword string
	// MySQLTLSMode/MySQLTLSCAFile are already normalized/validated (see
	// loadRuntimeConfig): MySQLTLSMode is always one of
	// store.MySQLTLSVerifyFull/VerifyCA/Disabled, never "" or an
	// unrecognized value.
	MySQLTLSMode   string
	MySQLTLSCAFile string
	// HTTPDataSourceURL/HTTPDataSourceToken configure the HTTP custom data
	// source, meaningful only when DBBackend == "http" (DFLT-00088). The
	// token has already been resolved from a possible "${ENV_VAR_NAME}"
	// reference, and both have passed store.ValidateHTTPDataSourceSettings.
	HTTPDataSourceURL   string
	HTTPDataSourceToken string
	// UserExtensionsDir/TeamExtensionsDir override the default roots
	// internal/config.ResolveRoots would otherwise pick ($HOME/.graph-ops
	// and the nearest ancestor .graph-ops directory other than
	// $HOME/.graph-ops, respectively). Empty means "use the default for that
	// tier" -- see config.ResolveRoots, which also drops a team root that is
	// the same directory as the user root.
	UserExtensionsDir string
	TeamExtensionsDir string
	// ProjectPaths is graph-config.json's projectPaths as loaded at startup:
	// project ID -> this environment's local path for that project
	// (DFLT-00080). It is nil when nothing is set. Writes go through
	// runtimeconfig.SetProjectPath(WorkDir, HomeDir, ...), not this copy.
	ProjectPaths map[string]string
	// PaginationPageSize is how many tickets the web UI's ticket list shows
	// per page. Unlike every other field here, it has no env var override --
	// it's only ever set via graph-config.json (by hand, or through the web
	// settings UI's "全体設定" tab).
	PaginationPageSize int
	// HomeDir is the resolved os.UserHomeDir() value (or "" if it couldn't
	// be resolved), threaded through to httpserver.Config so the app-settings
	// API resolves graph-config.json's path the exact same way this method
	// just did, without independently re-calling os.UserHomeDir() itself.
	HomeDir string
}

// defaultDataDir is the directory the DB file and the artifacts directory
// default into when neither an env var nor graph-config.json names them:
// $HOME/.graph-ops, the same directory that already holds config.json and
// the user-tier extensions (see config.ResolveRoots). Keeping all four in
// one place means a user's GraphOps state no longer depends on which
// project directory the tool happened to be started from -- the previous
// default, <cwd>/graph.db, silently created a separate empty database per
// directory, even though the schema has always been able to hold many
// projects in one DB.
//
// There is deliberately NO backward-compatibility fallback: an existing
// <cwd>/graph.db is not preferred over the new default, because "use the
// old DB if one happens to be lying around" makes the effective path depend
// on invocation directory again, which is the exact problem being removed.
// Keeping such a DB is an explicit act: GRAPH_DB_PATH, or dbPath in
// graph-config.json.
//
// home is os.UserHomeDir()'s result, which is "" when it could not be
// resolved (some CI images and minimal containers). That case falls back to
// cwd -- the pre-change behaviour -- rather than failing. Note that this is a
// different axis from the paragraph above: it is an unconditional fallback
// that turns on when the home directory cannot be resolved at all, never the
// result of noticing an old DB lying in cwd. loadRuntimeConfig
// and runtimeconfig.CandidatePaths both already treat an unresolvable home
// as a normal condition to route around, and hard-failing here would turn
// environments that start fine today into ones that cannot start at all.
func defaultDataDir(cwd, home string) string {
	if home == "" {
		return cwd
	}
	return filepath.Join(home, ".graph-ops")
}

// configWarnWriter is where loadRuntimeConfig's config warnings go. It is a
// variable only so a test can capture them; nothing but a test ever assigns
// to it.
//
// stderr, never stdout: loadRuntimeConfig runs on the startup path of every
// subcommand, including the ones agents pipe and parse (`get-ticket`,
// `list`), and a warning that landed in stdout would corrupt their output.
var configWarnWriter io.Writer = os.Stderr

// formatConfigWarnings renders what LoadEffective dropped as the lines to
// print, keys in runtimeconfig.HomeOnlyKeys order. It returns nothing at all
// in the ordinary case -- a config with none of those keys in it must stay
// silent, or every single subcommand invocation would grow a line of noise
// (completion criterion 2 of DFLT-00104).
//
// The wording names the file the keys were ignored in AND the file they are
// read from instead, because that pair is the whole actionable content: it
// tells the user both where to delete the key to silence this and where to
// move it if they meant it.
func formatConfigWarnings(eff runtimeconfig.Effective) []string {
	var lines []string
	// "the home config file" rather than a path when home could not be
	// resolved at all: naming a path we do not have would be a lie, and
	// there is nothing there for the user to edit.
	source := "the home config file"
	if eff.HomeConfigPath != "" {
		source = eff.HomeConfigPath
	}
	if len(eff.IgnoredKeys) > 0 {
		lines = append(lines, fmt.Sprintf(
			"graph-ops: warning: ignoring %s in %s; these settings are read only from %s or the environment.",
			strings.Join(eff.IgnoredKeys, ", "), eff.Path, source))
	}
	if eff.HomeConfigErr != nil {
		// Not fatal -- see LoadEffective. The error names the file itself
		// (runtimeconfig.HomeConfigReadError); what this adds is which keys
		// the failure costs, since a file that cannot be read is otherwise
		// indistinguishable from one that simply sets none of them.
		lines = append(lines, fmt.Sprintf(
			"graph-ops: warning: %v; %s fall back to environment variables or built-in defaults.",
			eff.HomeConfigErr, runtimeconfig.HomeOnlyKeyList()))
	}
	return lines
}

func loadRuntimeConfig() (runtimeConfig, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return runtimeConfig{}, err
	}
	home, _ := os.UserHomeDir()

	// LoadEffective, not Load: terminalCommand, claudeBinary, host and
	// artifactsDir must not come from the graph-config.json of whatever
	// directory this process happens to have been started in (see
	// runtimeconfig's homeOnlyKeys). The firstNonEmpty chains below are
	// untouched, so an environment variable still wins over everything --
	// including the home config.
	eff, err := runtimeconfig.LoadEffective(cwd, home)
	if err != nil {
		return runtimeConfig{}, err
	}
	fileCfg := eff.Config
	for _, line := range formatConfigWarnings(eff) {
		fmt.Fprintln(configWarnWriter, line)
	}

	dbBackend := firstNonEmpty(os.Getenv("GRAPH_DB_BACKEND"), fileCfg.DBBackend, "sqlite")
	if err := store.ValidateBackend(dbBackend); err != nil {
		return runtimeConfig{}, fmt.Errorf("invalid dbBackend: %w", err)
	}

	dataDir := defaultDataDir(cwd, home)
	dbPath := firstNonEmpty(os.Getenv("GRAPH_DB_PATH"), fileCfg.DBPath, filepath.Join(dataDir, "graph.db"))
	artifactsDir := firstNonEmpty(os.Getenv("GRAPH_ARTIFACTS_DIR"), fileCfg.ArtifactsDir, filepath.Join(dataDir, "artifacts"))
	claudeBinary := firstNonEmpty(os.Getenv("CLAUDE_BIN"), fileCfg.ClaudeBinary, "claude")
	terminalCommand := firstNonEmpty(os.Getenv("TERMINAL_COMMAND"), fileCfg.TerminalCommand)
	terminalWorkDir := firstNonEmpty(os.Getenv("TERMINAL_WORKDIR"), fileCfg.WorkDir, cwd)
	userExtensionsDir := firstNonEmpty(os.Getenv("GRAPH_USER_EXTENSIONS_DIR"), fileCfg.UserExtensionsDir)
	teamExtensionsDir := firstNonEmpty(os.Getenv("GRAPH_TEAM_EXTENSIONS_DIR"), fileCfg.TeamExtensionsDir)

	mysqlHost := firstNonEmpty(os.Getenv("GRAPH_MYSQL_HOST"), fileCfg.MySQLHost)
	mysqlDatabase := firstNonEmpty(os.Getenv("GRAPH_MYSQL_DATABASE"), fileCfg.MySQLDatabase)
	mysqlUser := firstNonEmpty(os.Getenv("GRAPH_MYSQL_USER"), fileCfg.MySQLUser)
	mysqlPasswordRaw := firstNonEmpty(os.Getenv("GRAPH_MYSQL_PASSWORD"), fileCfg.MySQLPassword)
	mysqlPort := fileCfg.MySQLPort
	if envPort := os.Getenv("GRAPH_MYSQL_PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil {
			mysqlPort = p
		}
	}
	if mysqlPort == 0 {
		mysqlPort = 3306
	}
	// mysqlPasswordRaw may be a "${ENV_VAR_NAME}" reference (see
	// runtimeconfig.ResolveSecret's doc comment); resolve it here, once, so
	// every caller of loadRuntimeConfig gets the real value and never has to
	// deal with that syntax itself. Only resolved when the mysql backend is
	// actually selected -- an unrelated/unset env var referenced by a
	// leftover mysqlPassword value must not block sqlite-only startups.
	mysqlPassword := ""
	if dbBackend == "mysql" {
		resolved, _, _, err := runtimeconfig.ResolveSecret(mysqlPasswordRaw)
		if err != nil {
			return runtimeConfig{}, fmt.Errorf("resolving mysql password: %w", err)
		}
		mysqlPassword = resolved
	}

	// mysqlTLSMode/mysqlTLSCAFile: validated only when the mysql backend is
	// actually selected -- see store.ValidateMySQLTLSSettings's doc comment
	// and D-4 in this ticket's (DFLT-00037) execution plan for why a
	// leftover/garbage value under sqlite must not block startup the way it
	// would under mysql (mirroring how mysqlPassword's "${ENV_VAR}"
	// resolution above is likewise skipped for sqlite). Once mysql is
	// selected, an invalid mode or a verify-ca mode without a CA file fails
	// startup loudly rather than silently connecting in plaintext or
	// falling back to a different mode -- there is no such fallback (see
	// store.NormalizeMySQLTLSMode).
	mysqlTLSModeRaw := firstNonEmpty(os.Getenv("GRAPH_MYSQL_TLS"), fileCfg.MySQLTLS)
	mysqlTLSCAFile := firstNonEmpty(os.Getenv("GRAPH_MYSQL_TLS_CA"), fileCfg.MySQLTLSCA)
	mysqlTLSMode := mysqlTLSModeRaw
	if dbBackend == "mysql" {
		normalized, err := store.NormalizeMySQLTLSMode(mysqlTLSModeRaw)
		if err != nil {
			return runtimeConfig{}, fmt.Errorf("invalid mysql TLS settings (mysqlTls / GRAPH_MYSQL_TLS): %w", err)
		}
		if err := store.ValidateMySQLTLSSettings(normalized, mysqlTLSCAFile); err != nil {
			return runtimeConfig{}, fmt.Errorf("invalid mysql TLS settings (mysqlTls/mysqlTlsCa or GRAPH_MYSQL_TLS/GRAPH_MYSQL_TLS_CA): %w", err)
		}
		mysqlTLSMode = normalized
	}

	// HTTP custom data source (DFLT-00088): resolved and validated only when
	// the http backend is actually selected, exactly like the MySQL password
	// and TLS settings above -- a leftover httpDataSourceUrl or a token
	// referencing an unset env var must not block sqlite/mysql startups. Once
	// http is selected, a non-loopback plaintext URL or a remote URL without
	// a token stops startup here, before any request is sent.
	httpDataSourceURL := firstNonEmpty(os.Getenv("GRAPH_HTTP_DATASOURCE_URL"), fileCfg.HTTPDataSourceURL)
	httpDataSourceToken := ""
	if dbBackend == "http" {
		tokenRaw := firstNonEmpty(os.Getenv("GRAPH_HTTP_DATASOURCE_TOKEN"), fileCfg.HTTPDataSourceToken)
		resolved, _, _, err := runtimeconfig.ResolveSecret(tokenRaw)
		if err != nil {
			return runtimeConfig{}, fmt.Errorf("resolving HTTP data source token (httpDataSourceToken / GRAPH_HTTP_DATASOURCE_TOKEN): %w", err)
		}
		if err := store.ValidateHTTPDataSourceSettings(httpDataSourceURL, resolved); err != nil {
			return runtimeConfig{}, fmt.Errorf("invalid HTTP data source settings (httpDataSourceUrl/httpDataSourceToken or GRAPH_HTTP_DATASOURCE_URL/GRAPH_HTTP_DATASOURCE_TOKEN): %w", err)
		}
		httpDataSourceToken = resolved
	}

	port := fileCfg.Port
	if port == 0 {
		// 3001 collides too often with other local dev servers (Next.js,
		// json-server, etc. all default near it); the IANA-registered
		// dynamic/private range (49152-65535) is reserved so nothing should
		// be permanently squatting there, making a collision far less likely.
		port = 49173
	}
	if envPort := os.Getenv("PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil {
			port = p
		}
	}

	host := firstNonEmpty(os.Getenv("GRAPH_HOST"), fileCfg.Host, runtimeconfig.DefaultHost)

	paginationPageSize := fileCfg.PaginationPageSize
	if paginationPageSize <= 0 {
		paginationPageSize = 10
	}

	// Unconditional, i.e. also under the mysql backend: artifactsDir is where
	// html/image artifact files live regardless of which store holds the rows,
	// so it is not a sqlite-only concern. The path is named in the error
	// because this runs on the startup path of *every* subcommand -- a bare
	// "permission denied" with no path would be unactionable.
	//
	// 0o700, not 0o755, and applied unconditionally -- including when the
	// user pointed artifactsDir somewhere else entirely. This is the first
	// MkdirAll on every subcommand's startup path, so with the default
	// artifactsDir it is the call that creates $HOME/.graph-ops itself
	// (MkdirAll applies perm to every parent it has to make). 0o755 here
	// would therefore decide the mode of the directory that also holds
	// config.json -- which can carry a MySQL password, and is written 0o600
	// for exactly that reason -- ui-serve.log, and now a single graph.db
	// holding every project's tickets and artifacts. It would also win over
	// the deliberate 0o700 in ui.go's uiServerLogPath, because MkdirAll
	// never re-modes a directory that already exists and this runs first.
	// The same 0o700 is used by store.NewSQLiteRepository and
	// runtimeconfig.Save; all three have to agree, since in a fresh
	// environment whichever runs first is the one that fixes the mode.
	//
	// A user-named artifactsDir gets 0o700 as well: this is a single-user
	// local tool, nothing it writes is meant to be read by other accounts on
	// the host, and making the mode depend on where the path came from would
	// just bring back a permission that varies by configuration. Directories
	// that already exist keep whatever mode they have -- there is no
	// retroactive Chmod, because an existing looser mode may be the user's
	// own doing and silently tightening a directory they set up is worse
	// than leaving one already-created directory as it is.
	if err := os.MkdirAll(artifactsDir, 0o700); err != nil {
		return runtimeConfig{}, fmt.Errorf("creating artifacts directory %s: %w", artifactsDir, err)
	}

	return runtimeConfig{
		DBBackend:           dbBackend,
		DBPath:              dbPath,
		ArtifactsDir:        artifactsDir,
		Port:                port,
		Host:                host,
		ClaudeBinary:        claudeBinary,
		WorkDir:             cwd,
		TerminalCommand:     terminalCommand,
		TerminalWorkDir:     terminalWorkDir,
		MySQLHost:           mysqlHost,
		MySQLPort:           mysqlPort,
		MySQLDatabase:       mysqlDatabase,
		MySQLUser:           mysqlUser,
		MySQLPassword:       mysqlPassword,
		MySQLTLSMode:        mysqlTLSMode,
		MySQLTLSCAFile:      mysqlTLSCAFile,
		HTTPDataSourceURL:   httpDataSourceURL,
		HTTPDataSourceToken: httpDataSourceToken,
		UserExtensionsDir:   userExtensionsDir,
		TeamExtensionsDir:   teamExtensionsDir,
		PaginationPageSize:  paginationPageSize,
		HomeDir:             home,
		ProjectPaths:        fileCfg.ProjectPaths,
	}, nil
}

// clientBaseURL is the base URL a program running on this same machine must
// use to actually reach a listener bound to host:port -- the one `ui` health
// checks and calls the API on. A wildcard bind address becomes "localhost"
// (see runtimeconfig.DisplayHost); every concrete address is kept, which is
// the whole point: before DFLT-00023 item N-1 this was hardcoded to
// "localhost", so with host set to e.g. 192.168.1.5 the `ui` command health
// checked an address nothing was listening on, concluded the server was
// down, and tried to start a second one -- which then failed to bind.
func clientBaseURL(host string, port int) string {
	return "http://" + net.JoinHostPort(runtimeconfig.DisplayHost(host), strconv.Itoa(port))
}

// humanBaseURL is clientBaseURL's counterpart for a URL a person reads or a
// browser opens: identical, except that the two addresses "localhost"
// resolves to are shown as "localhost", which is what users recognize and
// what the README and docs use.
//
// The substitution is deliberately limited to those two addresses. Any other
// loopback address (127.0.0.2, say) is printed as itself, because a server
// bound only to 127.0.0.2 is NOT reachable at localhost -- printing
// "localhost" there would advertise a URL that does not work. Same for a
// concrete non-loopback bind: DFLT-00023 item N-3 is exactly that bug, where
// `serve` announced "http://localhost:<port>" while listening only on a LAN
// address.
//
// Both addresses are spelled out as literals rather than reusing
// runtimeconfig.DefaultHost, which happens to be "127.0.0.1" today. This list
// is "what the name localhost resolves to", a property of the host's name
// resolution; DefaultHost is "which address we bind when the user picked
// none", a default we are free to change. Writing the first in terms of the
// second would make a change to the default silently turn this into a wrong
// substitution.
func humanBaseURL(host string, port int) string {
	h := runtimeconfig.DisplayHost(host)
	if h == "127.0.0.1" || h == "::1" {
		h = "localhost"
	}
	return "http://" + net.JoinHostPort(h, strconv.Itoa(port))
}

// storeConfigFromRuntimeConfig translates the CLI's own runtimeConfig into
// store.Config, the shape store.Open expects. Kept as a small standalone
// step (rather than having loadRuntimeConfig build a store.Config
// directly) so cmd/graph-engine doesn't need to import internal/store just
// to declare its own settings struct.
func storeConfigFromRuntimeConfig(rc runtimeConfig) store.Config {
	return store.Config{
		Backend:        rc.DBBackend,
		SQLitePath:     rc.DBPath,
		MySQLHost:      rc.MySQLHost,
		MySQLPort:      rc.MySQLPort,
		MySQLDatabase:  rc.MySQLDatabase,
		MySQLUser:      rc.MySQLUser,
		MySQLPassword:  rc.MySQLPassword,
		MySQLTLSMode:   rc.MySQLTLSMode,
		MySQLTLSCAFile: rc.MySQLTLSCAFile,
		HTTPURL:        rc.HTTPDataSourceURL,
		HTTPToken:      rc.HTTPDataSourceToken,
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
