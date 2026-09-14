package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/runtimeconfig"
)

// runInDir chdirs into dir for the duration of the test, restoring the
// original working directory on cleanup.
func runInDir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("os.Chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(orig)
	})
}

// stubHome points os.UserHomeDir() (and therefore both the graph-config.json
// home candidate and the $HOME/.graph-ops data directory defaults) at a
// throwaway directory, returning its symlink-resolved path.
//
// Every test that calls loadRuntimeConfig must use this. Without it the test
// reads the developer's real ~/.graph-ops/config.json -- whose dbPath is an
// absolute path on that one machine -- so results differ per machine, and
// since the defaults moved under $HOME the tests would additionally litter
// the real home directory with a .graph-ops/artifacts they never clean up.
func stubHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("filepath.EvalSymlinks: %v", err)
	}
	setHomeEnv(t, home)
	return home
}

// stubUnresolvableHome emulates an environment where os.UserHomeDir() fails
// (minimal containers and some CI images), which is what drives the
// fall-back-to-cwd branch of defaultDataDir.
func stubUnresolvableHome(t *testing.T) {
	t.Helper()
	setHomeEnv(t, "")
}

// setHomeEnv writes every variable os.UserHomeDir() consults, so these
// helpers behave the same on every platform rather than silently leaving the
// real home in place on the ones they forgot: $HOME on unix/darwin,
// %USERPROFILE% on windows, $home on plan9.
func setHomeEnv(t *testing.T, value string) {
	t.Helper()
	for _, key := range []string{"HOME", "USERPROFILE", "home"} {
		t.Setenv(key, value)
	}
}

// tempCwd creates a throwaway directory and chdirs into it, returning its
// symlink-resolved path (macOS's /tmp -> /private/tmp indirection otherwise
// breaks comparisons against os.Getwd()'s result inside loadRuntimeConfig).
func tempCwd(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("filepath.EvalSymlinks: %v", err)
	}
	runInDir(t, dir)
	return dir
}

// clearPathEnv unsets the two env vars that would otherwise override the
// path defaults under test. They are cleared explicitly rather than assumed
// absent because a developer running `go test` may well have them exported.
func clearPathEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GRAPH_DB_PATH", "")
	t.Setenv("GRAPH_ARTIFACTS_DIR", "")
}

// graphOpsDir is $HOME/.graph-ops, the directory the DB and artifacts
// defaults now live in.
func graphOpsDir(home string) string {
	return filepath.Join(home, ".graph-ops")
}

func assertNotExists(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%s must not exist (%s), stat err = %v", path, why, err)
	}
}

func assertIsDir(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(%s): %v", path, err)
	}
	if !info.IsDir() {
		t.Fatalf("%s exists but is not a directory", path)
	}
}

// assertDirPerm pins the permission bits of a directory the code under test
// created. Callers skip on Windows before reaching it -- the unix permission
// bits have no equivalent there.
//
// The expectation is exact rather than the weaker "no group/other bits", so
// that a future change loosening the mode fails here instead of passing
// quietly. umask can only clear bits, and 0o700's owner bits survive every
// umask a developer or CI runner realistically has (022 and 002 both leave
// them untouched), so an exact match is stable in practice.
func assertDirPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(%s): %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s has mode %#o, want %#o (if the owner bits are the ones missing, check the umask)", path, got, want)
	}
}

// skipIfNoUnixPerms skips a test whose subject is the permission bits of a
// created directory, on platforms where those bits carry no meaning.
func skipIfNoUnixPerms(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits have no equivalent on windows")
	}
}

// writeGraphConfig writes a graph-config.json in dir with the given content,
// merged with an empty runtimeconfig.FileConfig where fields are omitted if empty.
func writeGraphConfig(t *testing.T, dir string, cfg runtimeconfig.FileConfig) {
	t.Helper()
	writeGraphConfigAt(t, filepath.Join(dir, "graph-config.json"), cfg)
}

// writeGraphConfigAt is writeGraphConfig for the home-side candidate, whose
// file is named config.json and lives in $HOME/.graph-ops rather than being
// a graph-config.json in a directory (see runtimeconfig.CandidatePaths), so
// the path has to be given in full.
func writeGraphConfigAt(t *testing.T, path string, cfg runtimeconfig.FileConfig) {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
}

// TestLoadRuntimeConfig_TerminalWorkDir covers the precedence rules for the
// terminal launch working directory: TERMINAL_WORKDIR env var > graph-config.json
// "workDir" key > fallback to the process's os.Getwd(), mirroring the existing
// terminalCommand precedence pattern.
func TestLoadRuntimeConfig_TerminalWorkDir(t *testing.T) {
	tests := []struct {
		name        string
		envValue    string // "" means unset
		jsonWorkDir string // "" means key omitted
		wantFn      func(cwd string) string
	}{
		{
			name:        "env var set, no JSON config",
			envValue:    "/tmp/env-target-project",
			jsonWorkDir: "",
			wantFn:      func(cwd string) string { return "/tmp/env-target-project" },
		},
		{
			name:        "only JSON config set",
			envValue:    "",
			jsonWorkDir: "/tmp/json-target-project",
			wantFn:      func(cwd string) string { return "/tmp/json-target-project" },
		},
		{
			name:        "neither set falls back to process cwd",
			envValue:    "",
			jsonWorkDir: "",
			wantFn:      func(cwd string) string { return cwd },
		},
		{
			name:        "both set, env wins",
			envValue:    "/tmp/env-wins-project",
			jsonWorkDir: "/tmp/json-loses-project",
			wantFn:      func(cwd string) string { return "/tmp/env-wins-project" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolvedDir := tempCwd(t)
			stubHome(t)

			if tt.envValue != "" {
				t.Setenv("TERMINAL_WORKDIR", tt.envValue)
			} else {
				t.Setenv("TERMINAL_WORKDIR", "")
			}

			if tt.jsonWorkDir != "" {
				writeGraphConfig(t, resolvedDir, runtimeconfig.FileConfig{WorkDir: tt.jsonWorkDir})
			}

			rc, err := loadRuntimeConfig()
			if err != nil {
				t.Fatalf("loadRuntimeConfig: %v", err)
			}

			want := tt.wantFn(resolvedDir)
			if rc.TerminalWorkDir != want {
				t.Errorf("TerminalWorkDir = %q, want %q", rc.TerminalWorkDir, want)
			}

			// The catalog search root (WorkDir) must never be affected by
			// the new terminal-workdir setting -- it always stays the
			// process's cwd.
			if rc.WorkDir != resolvedDir {
				t.Errorf("WorkDir = %q, want %q (must stay process cwd, unaffected by TerminalWorkDir)", rc.WorkDir, resolvedDir)
			}
		})
	}
}

// TestLoadRuntimeConfig_TerminalWorkDir_DoesNotAffectTerminalCommand ensures
// the new workDir/TERMINAL_WORKDIR resolution is independent of the existing
// terminalCommand precedence logic.
func TestLoadRuntimeConfig_TerminalWorkDir_DoesNotAffectTerminalCommand(t *testing.T) {
	resolvedDir := tempCwd(t)
	stubHome(t)

	t.Setenv("TERMINAL_COMMAND", "iterm")
	t.Setenv("TERMINAL_WORKDIR", "/tmp/target-project")
	writeGraphConfig(t, resolvedDir, runtimeconfig.FileConfig{
		TerminalCommand: "kitty",
		WorkDir:         "/tmp/json-project",
	})

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}

	if rc.TerminalCommand != "iterm" {
		t.Errorf("TerminalCommand = %q, want %q (env should still win)", rc.TerminalCommand, "iterm")
	}
	if rc.TerminalWorkDir != "/tmp/target-project" {
		t.Errorf("TerminalWorkDir = %q, want %q", rc.TerminalWorkDir, "/tmp/target-project")
	}
}

// TestLoadRuntimeConfig_DBBackendDefaultsToSQLite covers the backward-
// compatibility completion criterion: a graph-config.json with no
// dbBackend key (the shape every existing installation has) must resolve
// to "sqlite", not fail or leave the field empty.
func TestLoadRuntimeConfig_DBBackendDefaultsToSQLite(t *testing.T) {
	tempCwd(t)
	stubHome(t)

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if rc.DBBackend != "sqlite" {
		t.Errorf("DBBackend = %q, want %q", rc.DBBackend, "sqlite")
	}
}

// TestLoadRuntimeConfig_UnsupportedDBBackendFailsLoudly covers the
// completion criterion "サポート外のdbBackend値は起動時にエラーになる": an
// unrecognized value must fail loadRuntimeConfig outright, never silently
// fall back to sqlite.
func TestLoadRuntimeConfig_UnsupportedDBBackendFailsLoudly(t *testing.T) {
	resolvedDir := tempCwd(t)
	stubHome(t)
	writeGraphConfig(t, resolvedDir, runtimeconfig.FileConfig{DBBackend: "postgres"})

	if _, err := loadRuntimeConfig(); err == nil {
		t.Fatal("expected an error for an unsupported dbBackend value")
	}
}

// TestLoadRuntimeConfig_ResolvesMySQLPasswordFromEnv covers completion
// criterion 7 end-to-end through loadRuntimeConfig: a mysqlPassword stored
// as "${ENV_VAR}" resolves to that variable's real value once dbBackend is
// "mysql".
func TestLoadRuntimeConfig_ResolvesMySQLPasswordFromEnv(t *testing.T) {
	resolvedDir := tempCwd(t)
	stubHome(t)
	t.Setenv("GRAPH_OPS_TEST_MYSQL_PW", "actual-password")
	writeGraphConfig(t, resolvedDir, runtimeconfig.FileConfig{
		DBBackend: "mysql", MySQLHost: "db.example.com", MySQLDatabase: "graph_ops",
		MySQLUser: "app", MySQLPassword: "${GRAPH_OPS_TEST_MYSQL_PW}",
	})

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if rc.MySQLPassword != "actual-password" {
		t.Errorf("MySQLPassword = %q, want %q", rc.MySQLPassword, "actual-password")
	}
	if rc.MySQLHost != "db.example.com" || rc.MySQLDatabase != "graph_ops" || rc.MySQLUser != "app" {
		t.Errorf("unexpected mysql settings: %+v", rc)
	}
}

// TestLoadRuntimeConfig_MySQLPasswordMissingEnvVarFailsLoudly covers the
// Gherkin scenario where the env var a mysqlPassword references isn't set:
// loadRuntimeConfig must fail rather than silently connecting with an
// empty/literal password.
func TestLoadRuntimeConfig_MySQLPasswordMissingEnvVarFailsLoudly(t *testing.T) {
	resolvedDir := tempCwd(t)
	stubHome(t)
	writeGraphConfig(t, resolvedDir, runtimeconfig.FileConfig{
		DBBackend: "mysql", MySQLHost: "db.example.com", MySQLDatabase: "graph_ops",
		MySQLUser: "app", MySQLPassword: "${GRAPH_OPS_TEST_DEFINITELY_UNSET_VAR_DFLT_00020}",
	})

	if _, err := loadRuntimeConfig(); err == nil {
		t.Fatal("expected an error when the mysqlPassword's referenced env var is unset")
	}
}

// TestLoadRuntimeConfig_MySQLPasswordEnvVarIgnoredWhenBackendIsSQLite
// covers a subtle edge case: a leftover mysqlPassword referencing an unset
// env var (e.g. from a previous mysql experiment) must not block a plain
// sqlite startup -- only actually selecting the mysql backend should
// trigger resolution (and thus that failure mode).
func TestLoadRuntimeConfig_MySQLPasswordEnvVarIgnoredWhenBackendIsSQLite(t *testing.T) {
	resolvedDir := tempCwd(t)
	stubHome(t)
	writeGraphConfig(t, resolvedDir, runtimeconfig.FileConfig{
		MySQLPassword: "${GRAPH_OPS_TEST_DEFINITELY_UNSET_VAR_DFLT_00020}",
	})

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig should succeed for sqlite regardless of a stale mysqlPassword: %v", err)
	}
	if rc.DBBackend != "sqlite" {
		t.Errorf("DBBackend = %q, want %q", rc.DBBackend, "sqlite")
	}
}

// --- DFLT-00037: MySQL TLS settings (T-4) ----------------------------------

// TestLoadRuntimeConfig_MySQLTLSDefaultsToVerifyFull covers this ticket's
// (DFLT-00037) D-2: an unset mysqlTls under the mysql backend must resolve
// to "verify-full", not an empty string -- the whole point of this ticket
// is that a configuration that never mentions TLS gets a verified
// connection, not a plaintext one.
func TestLoadRuntimeConfig_MySQLTLSDefaultsToVerifyFull(t *testing.T) {
	resolvedDir := tempCwd(t)
	stubHome(t)
	writeGraphConfig(t, resolvedDir, runtimeconfig.FileConfig{
		DBBackend: "mysql", MySQLHost: "db.example.com", MySQLDatabase: "graph_ops", MySQLUser: "app",
	})

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if rc.MySQLTLSMode != "verify-full" {
		t.Errorf("MySQLTLSMode = %q, want %q", rc.MySQLTLSMode, "verify-full")
	}
}

// TestLoadRuntimeConfig_MySQLTLSEnvVarOverridesFile covers the same env >
// file precedence every other mysql* setting already has.
func TestLoadRuntimeConfig_MySQLTLSEnvVarOverridesFile(t *testing.T) {
	resolvedDir := tempCwd(t)
	stubHome(t)
	caPath := filepath.Join(resolvedDir, "ca.pem")
	writeGraphConfig(t, resolvedDir, runtimeconfig.FileConfig{
		DBBackend: "mysql", MySQLHost: "db.example.com", MySQLDatabase: "graph_ops", MySQLUser: "app",
		MySQLTLS: "disabled",
	})
	t.Setenv("GRAPH_MYSQL_TLS", "verify-ca")
	t.Setenv("GRAPH_MYSQL_TLS_CA", caPath)

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if rc.MySQLTLSMode != "verify-ca" {
		t.Errorf("MySQLTLSMode = %q, want %q (env var should win over the file)", rc.MySQLTLSMode, "verify-ca")
	}
	if rc.MySQLTLSCAFile != caPath {
		t.Errorf("MySQLTLSCAFile = %q, want %q", rc.MySQLTLSCAFile, caPath)
	}
}

// TestLoadRuntimeConfig_MySQLTLSInvalidModeFailsLoudly covers D-4: an
// unsupported mysqlTls value (in particular, the driver-level "preferred",
// which is exactly the plaintext-fallback downgrade this ticket exists to
// close -- see F-2/F-3 in the execution plan) must fail startup outright
// under the mysql backend.
func TestLoadRuntimeConfig_MySQLTLSInvalidModeFailsLoudly(t *testing.T) {
	resolvedDir := tempCwd(t)
	stubHome(t)
	writeGraphConfig(t, resolvedDir, runtimeconfig.FileConfig{
		DBBackend: "mysql", MySQLHost: "db.example.com", MySQLDatabase: "graph_ops", MySQLUser: "app",
		MySQLTLS: "preferred",
	})

	if _, err := loadRuntimeConfig(); err == nil {
		t.Fatal("expected an error for mysqlTls=preferred")
	}
}

// TestLoadRuntimeConfig_MySQLTLSVerifyCAWithoutCAFailsLoudly covers D-4's
// other validated case: verify-ca with no CA file configured has nothing to
// pin trust to and must not silently fall back to the OS trust store (see
// store.ValidateMySQLTLSSettings).
func TestLoadRuntimeConfig_MySQLTLSVerifyCAWithoutCAFailsLoudly(t *testing.T) {
	resolvedDir := tempCwd(t)
	stubHome(t)
	writeGraphConfig(t, resolvedDir, runtimeconfig.FileConfig{
		DBBackend: "mysql", MySQLHost: "db.example.com", MySQLDatabase: "graph_ops", MySQLUser: "app",
		MySQLTLS: "verify-ca",
	})

	if _, err := loadRuntimeConfig(); err == nil {
		t.Fatal("expected an error for mysqlTls=verify-ca with no mysqlTlsCa configured")
	}
}

// TestLoadRuntimeConfig_MySQLTLSInvalidModeIgnoredWhenBackendIsSQLite
// mirrors TestLoadRuntimeConfig_MySQLPasswordEnvVarIgnoredWhenBackendIsSQLite
// for TLS settings: a leftover invalid mysqlTls (e.g. from a past mysql
// experiment) must not block a plain sqlite startup.
func TestLoadRuntimeConfig_MySQLTLSInvalidModeIgnoredWhenBackendIsSQLite(t *testing.T) {
	resolvedDir := tempCwd(t)
	stubHome(t)
	writeGraphConfig(t, resolvedDir, runtimeconfig.FileConfig{MySQLTLS: "preferred"})

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig should succeed for sqlite regardless of a stale invalid mysqlTls: %v", err)
	}
	if rc.DBBackend != "sqlite" {
		t.Errorf("DBBackend = %q, want %q", rc.DBBackend, "sqlite")
	}
}

// TestLoadRuntimeConfig_StoreConfigFromRuntimeConfigCarriesMySQLTLS covers
// storeConfigFromRuntimeConfig's wiring: the normalized TLS settings must
// actually reach store.Config, not just runtimeConfig.
func TestLoadRuntimeConfig_StoreConfigFromRuntimeConfigCarriesMySQLTLS(t *testing.T) {
	rc := runtimeConfig{DBBackend: "mysql", MySQLTLSMode: "verify-ca", MySQLTLSCAFile: "/etc/mysql/ca.pem"}
	cfg := storeConfigFromRuntimeConfig(rc)
	if cfg.MySQLTLSMode != "verify-ca" || cfg.MySQLTLSCAFile != "/etc/mysql/ca.pem" {
		t.Errorf("store.Config TLS fields = %q/%q, want verify-ca//etc/mysql/ca.pem", cfg.MySQLTLSMode, cfg.MySQLTLSCAFile)
	}
}

// --- DFLT-00027: DB/artifacts defaults live under $HOME/.graph-ops ---------
//
// The tests below fix the behaviour specified in DFLT-00027's Gherkin
// feature. They fall into four groups: the defaults themselves, the
// directory creation that makes those defaults usable on a first run, the
// precedence chain (env > graph-config.json > default) which the change must
// leave structurally intact, and the deliberate *absence* of a
// backward-compatibility fallback to <cwd>/graph.db.

// TestLoadRuntimeConfig_DefaultsUnderHomeGraphOpsDir is the core assertion:
// with nothing configured, both paths resolve under $HOME/.graph-ops rather
// than under the directory the process happens to have been started from.
func TestLoadRuntimeConfig_DefaultsUnderHomeGraphOpsDir(t *testing.T) {
	cwd := tempCwd(t)
	home := stubHome(t)
	clearPathEnv(t)

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}

	wantDB := filepath.Join(graphOpsDir(home), "graph.db")
	if rc.DBPath != wantDB {
		t.Errorf("DBPath = %q, want %q", rc.DBPath, wantDB)
	}
	wantArtifacts := filepath.Join(graphOpsDir(home), "artifacts")
	if rc.ArtifactsDir != wantArtifacts {
		t.Errorf("ArtifactsDir = %q, want %q", rc.ArtifactsDir, wantArtifacts)
	}
	// Spelled out as its own check rather than left implicit in the two
	// above: "not the old cwd-relative path" is the actual point of the
	// change, and a future refactor that reintroduced a cwd default while
	// keeping some other home-ish path would otherwise slip through.
	if rc.DBPath == filepath.Join(cwd, "graph.db") {
		t.Errorf("DBPath fell back to the old <cwd>/graph.db default")
	}
	if rc.ArtifactsDir == filepath.Join(cwd, "artifacts") {
		t.Errorf("ArtifactsDir fell back to the old <cwd>/artifacts default")
	}
}

// TestLoadRuntimeConfig_CreatesArtifactsDirWhenGraphOpsDirMissing covers the
// first-run case: $HOME/.graph-ops does not exist at all, and the artifacts
// directory (and thus its parent) has to be created rather than reported as
// an error.
func TestLoadRuntimeConfig_CreatesArtifactsDirWhenGraphOpsDirMissing(t *testing.T) {
	tempCwd(t)
	home := stubHome(t)
	clearPathEnv(t)
	assertNotExists(t, graphOpsDir(home), "precondition: a fresh home has no .graph-ops yet")

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	assertIsDir(t, rc.ArtifactsDir)
}

// TestLoadRuntimeConfig_CreatesDataDirUserOnly pins the mode of the
// directories the default paths bring into existence.
//
// loadRuntimeConfig's MkdirAll is the first one to run on every subcommand's
// startup path, so with the default artifactsDir it is the call that creates
// $HOME/.graph-ops itself -- MkdirAll applies perm to every parent it has to
// make, and never re-modes one that already exists, so the mode chosen here
// is final for every later writer, ui.go's deliberate 0o700 included. That
// directory now holds config.json (which can carry a MySQL password),
// ui-serve.log, and a single graph.db with every project's tickets and
// artifact content in it, so group and other must not be able to read or
// traverse it.
func TestLoadRuntimeConfig_CreatesDataDirUserOnly(t *testing.T) {
	skipIfNoUnixPerms(t)
	tempCwd(t)
	home := stubHome(t)
	clearPathEnv(t)
	assertNotExists(t, graphOpsDir(home), "precondition: a fresh home has no .graph-ops yet")

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}

	// Both levels. The parent is the one that matters most -- it is what
	// holds config.json and graph.db -- and it only ever gets its mode from
	// being created as a parent of this artifacts directory.
	assertDirPerm(t, graphOpsDir(home), 0o700)
	assertDirPerm(t, rc.ArtifactsDir, 0o700)
}

// TestLoadRuntimeConfig_CreatesExplicitArtifactsDirUserOnly fixes the other
// half of the rule: 0o700 is unconditional, not something reserved for the
// default path. An artifactsDir the user named gets the same mode, so that
// the permission never varies with where the path came from.
func TestLoadRuntimeConfig_CreatesExplicitArtifactsDirUserOnly(t *testing.T) {
	skipIfNoUnixPerms(t)
	cwd := tempCwd(t)
	stubHome(t)
	clearPathEnv(t)
	// Two levels below cwd, so the parent it has to create along the way is
	// checked too.
	explicit := filepath.Join(cwd, "elsewhere", "artifacts")
	t.Setenv("GRAPH_ARTIFACTS_DIR", explicit)

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if rc.ArtifactsDir != explicit {
		t.Fatalf("ArtifactsDir = %q, want the explicit override %q", rc.ArtifactsDir, explicit)
	}
	assertDirPerm(t, explicit, 0o700)
	assertDirPerm(t, filepath.Dir(explicit), 0o700)
}

// TestLoadRuntimeConfig_LeavesAnExistingDataDirModeAlone documents the
// deliberate absence of a retroactive Chmod. A directory that already exists
// keeps the mode it has: it may have been loosened on purpose, or created
// 0o755 by an earlier build, and silently tightening a directory the user set
// up is worse than leaving one already-created directory as it is.
func TestLoadRuntimeConfig_LeavesAnExistingDataDirModeAlone(t *testing.T) {
	skipIfNoUnixPerms(t)
	tempCwd(t)
	home := stubHome(t)
	clearPathEnv(t)
	dir := graphOpsDir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	// Explicitly, because MkdirAll's perm is masked by the umask and the
	// point of the test is a directory that really is group/other-readable.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("os.Chmod: %v", err)
	}

	if _, err := loadRuntimeConfig(); err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	assertDirPerm(t, dir, 0o755)
}

// TestLoadRuntimeConfig_LeavesExistingGraphOpsContentsAlone guards the
// realistic case, since $HOME/.graph-ops already exists on most installs as
// the home of config.json and the user-tier extensions: creating the
// artifacts directory inside it must be idempotent and must not disturb
// what is already there.
func TestLoadRuntimeConfig_LeavesExistingGraphOpsContentsAlone(t *testing.T) {
	tempCwd(t)
	home := stubHome(t)
	clearPathEnv(t)

	dir := graphOpsDir(home)
	if err := os.MkdirAll(filepath.Join(dir, "extensions", "node-types"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	extension := filepath.Join(dir, "extensions", "node-types", "plan.md")
	if err := os.WriteFile(extension, []byte("existing extension"), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	// An empty object: present (so it is the config source that gets read)
	// but setting nothing, leaving the defaults under test in force.
	configJSON := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configJSON, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	assertIsDir(t, rc.ArtifactsDir)

	if got, err := os.ReadFile(configJSON); err != nil || string(got) != "{}\n" {
		t.Errorf("config.json = %q (err %v), want it untouched", got, err)
	}
	if got, err := os.ReadFile(extension); err != nil || string(got) != "existing extension" {
		t.Errorf("extensions file = %q (err %v), want it untouched", got, err)
	}
}

// TestLoadRuntimeConfig_ArtifactsDirCreationFailureNamesThePath covers the
// error path. loadRuntimeConfig sits on the startup path of every
// subcommand, so a failure here takes the whole CLI down; the message has to
// identify the offending directory. A plain file occupying the directory's
// own path is the cheapest portable way to make MkdirAll fail.
func TestLoadRuntimeConfig_ArtifactsDirCreationFailureNamesThePath(t *testing.T) {
	tempCwd(t)
	home := stubHome(t)
	clearPathEnv(t)

	// .graph-ops itself is left a real directory: blocking it instead would
	// make runtimeconfig.Load fail first, on config.json, and the test would
	// then pass while asserting nothing about MkdirAll.
	if err := os.MkdirAll(graphOpsDir(home), 0o755); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(graphOpsDir(home), "artifacts"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	_, err := loadRuntimeConfig()
	if err == nil {
		t.Fatal("expected an error when the artifacts directory cannot be created")
	}
	wantPath := filepath.Join(graphOpsDir(home), "artifacts")
	if !strings.Contains(err.Error(), wantPath) {
		t.Errorf("error %q does not name the path %q it failed on", err, wantPath)
	}
}

// TestLoadRuntimeConfig_PathPrecedenceStillHolds is the regression test for
// the requirement that only the final fallback changed: the env > file >
// default chain, and the relative order within it, must be exactly what it
// was before.
func TestLoadRuntimeConfig_PathPrecedenceStillHolds(t *testing.T) {
	tests := []struct {
		name            string
		envDB           string
		envArtifacts    string
		fileDB          string
		fileArtifacts   string
		wantDBFn        func(home, cwd string) string
		wantArtifactsFn func(home, cwd string) string
	}{
		{
			name:            "env var beats the new default",
			envDB:           "env-graph.db",
			envArtifacts:    "env-artifacts",
			wantDBFn:        func(home, cwd string) string { return filepath.Join(cwd, "env-graph.db") },
			wantArtifactsFn: func(home, cwd string) string { return filepath.Join(cwd, "env-artifacts") },
		},
		{
			name:            "graph-config.json beats the new default",
			fileDB:          "file-graph.db",
			fileArtifacts:   "file-artifacts",
			wantDBFn:        func(home, cwd string) string { return filepath.Join(cwd, "file-graph.db") },
			wantArtifactsFn: func(home, cwd string) string { return filepath.Join(cwd, "file-artifacts") },
		},
		{
			name:            "env var beats graph-config.json",
			envDB:           "env-graph.db",
			envArtifacts:    "env-artifacts",
			fileDB:          "file-graph.db",
			fileArtifacts:   "file-artifacts",
			wantDBFn:        func(home, cwd string) string { return filepath.Join(cwd, "env-graph.db") },
			wantArtifactsFn: func(home, cwd string) string { return filepath.Join(cwd, "env-artifacts") },
		},
		{
			name:            "neither set falls through to the new default",
			wantDBFn:        func(home, cwd string) string { return filepath.Join(graphOpsDir(home), "graph.db") },
			wantArtifactsFn: func(home, cwd string) string { return filepath.Join(graphOpsDir(home), "artifacts") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cwd := tempCwd(t)
			home := stubHome(t)
			clearPathEnv(t)

			// Paths are built under cwd (not hardcoded /tmp literals) so the
			// "was the default used?" assertions can distinguish an override
			// that took effect from one that silently did not.
			if tt.envDB != "" {
				t.Setenv("GRAPH_DB_PATH", filepath.Join(cwd, tt.envDB))
			}
			if tt.envArtifacts != "" {
				t.Setenv("GRAPH_ARTIFACTS_DIR", filepath.Join(cwd, tt.envArtifacts))
			}
			if tt.fileDB != "" || tt.fileArtifacts != "" {
				cfg := runtimeconfig.FileConfig{}
				if tt.fileDB != "" {
					cfg.DBPath = filepath.Join(cwd, tt.fileDB)
				}
				if tt.fileArtifacts != "" {
					cfg.ArtifactsDir = filepath.Join(cwd, tt.fileArtifacts)
				}
				writeGraphConfig(t, cwd, cfg)
			}

			rc, err := loadRuntimeConfig()
			if err != nil {
				t.Fatalf("loadRuntimeConfig: %v", err)
			}

			if want := tt.wantDBFn(home, cwd); rc.DBPath != want {
				t.Errorf("DBPath = %q, want %q", rc.DBPath, want)
			}
			if want := tt.wantArtifactsFn(home, cwd); rc.ArtifactsDir != want {
				t.Errorf("ArtifactsDir = %q, want %q", rc.ArtifactsDir, want)
			}
		})
	}
}

// TestLoadRuntimeConfig_ExplicitOverrideDoesNotCreateDefaultPaths is the
// other half of "the override won": nothing may be created under
// $HOME/.graph-ops when the user pointed both settings somewhere else. A
// stray default directory appearing anyway would mean the default was
// evaluated for its side effects even while losing the precedence contest.
func TestLoadRuntimeConfig_ExplicitOverrideDoesNotCreateDefaultPaths(t *testing.T) {
	cwd := tempCwd(t)
	home := stubHome(t)
	t.Setenv("GRAPH_DB_PATH", filepath.Join(cwd, "env-graph.db"))
	t.Setenv("GRAPH_ARTIFACTS_DIR", filepath.Join(cwd, "env-artifacts"))

	if _, err := loadRuntimeConfig(); err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}

	assertNotExists(t, filepath.Join(graphOpsDir(home), "graph.db"), "GRAPH_DB_PATH overrode the default")
	assertNotExists(t, filepath.Join(graphOpsDir(home), "artifacts"), "GRAPH_ARTIFACTS_DIR overrode the default")
}

// TestLoadRuntimeConfig_HomeConfigJSONBeatsTheDefault covers the path the
// change touches most directly, and the one no other test here exercises:
// the settings file found at $HOME/.graph-ops/config.json now sits in the
// very same directory as the default DB, so "the file next to the default
// still wins over the default" is worth pinning down explicitly.
func TestLoadRuntimeConfig_HomeConfigJSONBeatsTheDefault(t *testing.T) {
	cwd := tempCwd(t)
	home := stubHome(t)
	clearPathEnv(t)

	if err := os.MkdirAll(graphOpsDir(home), 0o755); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	writeGraphConfigAt(t, filepath.Join(graphOpsDir(home), "config.json"), runtimeconfig.FileConfig{
		DBPath: filepath.Join(cwd, "home-config-graph.db"),
	})

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if want := filepath.Join(cwd, "home-config-graph.db"); rc.DBPath != want {
		t.Errorf("DBPath = %q, want %q", rc.DBPath, want)
	}
	assertNotExists(t, filepath.Join(graphOpsDir(home), "graph.db"), "config.json's dbPath overrode the default")
}

// TestLoadRuntimeConfig_NoFallbackToExistingCwdDB pins down a deliberate
// non-feature. An existing <cwd>/graph.db -- every pre-change installation
// has one -- must NOT be picked up, because "use whichever old DB happens to
// be lying around" would make the effective path depend on the invocation
// directory all over again. Recovering such a DB is an explicit act, which
// the second half of this test checks still works.
func TestLoadRuntimeConfig_NoFallbackToExistingCwdDB(t *testing.T) {
	cwd := tempCwd(t)
	home := stubHome(t)
	clearPathEnv(t)

	legacyDB := filepath.Join(cwd, "graph.db")
	const legacyContent = "pretend this is a populated sqlite file"
	if err := os.WriteFile(legacyDB, []byte(legacyContent), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if want := filepath.Join(graphOpsDir(home), "graph.db"); rc.DBPath != want {
		t.Errorf("DBPath = %q, want %q -- an existing <cwd>/graph.db must not win", rc.DBPath, want)
	}
	// Not referenced also means not touched: the old DB is left intact for
	// the user to migrate or point at later.
	if got, err := os.ReadFile(legacyDB); err != nil || string(got) != legacyContent {
		t.Errorf("<cwd>/graph.db = %q (err %v), want it left untouched", got, err)
	}

	t.Run("explicit GRAPH_DB_PATH restores it", func(t *testing.T) {
		t.Setenv("GRAPH_DB_PATH", legacyDB)
		rc, err := loadRuntimeConfig()
		if err != nil {
			t.Fatalf("loadRuntimeConfig: %v", err)
		}
		if rc.DBPath != legacyDB {
			t.Errorf("DBPath = %q, want %q", rc.DBPath, legacyDB)
		}
	})
}

// TestLoadRuntimeConfig_UnresolvableHomeFallsBackToCwd fixes the decision
// taken in the plan for environments where os.UserHomeDir() fails: keep the
// pre-change cwd-relative behaviour instead of refusing to start, so such
// environments (minimal containers, some CI images) do not regress from
// working to unbootable.
func TestLoadRuntimeConfig_UnresolvableHomeFallsBackToCwd(t *testing.T) {
	cwd := tempCwd(t)
	stubUnresolvableHome(t)
	clearPathEnv(t)

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig must not fail when the home directory is unresolvable: %v", err)
	}
	if want := filepath.Join(cwd, "graph.db"); rc.DBPath != want {
		t.Errorf("DBPath = %q, want %q", rc.DBPath, want)
	}
	if want := filepath.Join(cwd, "artifacts"); rc.ArtifactsDir != want {
		t.Errorf("ArtifactsDir = %q, want %q", rc.ArtifactsDir, want)
	}

	t.Run("an explicit override still wins", func(t *testing.T) {
		t.Setenv("GRAPH_DB_PATH", filepath.Join(cwd, "env-graph.db"))
		rc, err := loadRuntimeConfig()
		if err != nil {
			t.Fatalf("loadRuntimeConfig: %v", err)
		}
		if want := filepath.Join(cwd, "env-graph.db"); rc.DBPath != want {
			t.Errorf("DBPath = %q, want %q", rc.DBPath, want)
		}
	})
}

// TestLoadRuntimeConfig_MySQLBackendStillUsesTheArtifactsDefault documents
// that the artifacts default is backend-independent: artifact *files* live
// on disk no matter which store holds the rows, so selecting mysql still
// resolves and creates $HOME/.graph-ops/artifacts -- while of course
// creating no SQLite database file.
func TestLoadRuntimeConfig_MySQLBackendStillUsesTheArtifactsDefault(t *testing.T) {
	cwd := tempCwd(t)
	home := stubHome(t)
	clearPathEnv(t)
	writeGraphConfig(t, cwd, runtimeconfig.FileConfig{
		DBBackend: "mysql", MySQLHost: "db.example.com",
		MySQLDatabase: "graph_ops", MySQLUser: "app",
	})

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if want := filepath.Join(graphOpsDir(home), "artifacts"); rc.ArtifactsDir != want {
		t.Errorf("ArtifactsDir = %q, want %q", rc.ArtifactsDir, want)
	}
	assertIsDir(t, rc.ArtifactsDir)
	assertNotExists(t, filepath.Join(graphOpsDir(home), "graph.db"), "the mysql backend opens no sqlite file")
}
