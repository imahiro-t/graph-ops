package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/runtimeconfig"
)

// captureConfigWarnings redirects loadRuntimeConfig's warning output into a
// buffer for the duration of the test and returns the lines it collected,
// with the trailing empty element of a final newline removed.
func captureConfigWarnings(t *testing.T) func() []string {
	t.Helper()
	var buf bytes.Buffer
	orig := configWarnWriter
	configWarnWriter = &buf
	t.Cleanup(func() { configWarnWriter = orig })
	return func() []string {
		out := strings.TrimSuffix(buf.String(), "\n")
		if out == "" {
			return nil
		}
		return strings.Split(out, "\n")
	}
}

// writeRawConfig writes a config file verbatim, so a test can supply JSON
// that would not survive a round trip through FileConfig -- a malformed
// file, in particular.
func writeRawConfig(t *testing.T, path, raw string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
}

// configTestEnv clears every env var that would override a setting these
// tests look at, so a developer who happens to export one does not turn them
// green (or red) for the wrong reason.
func configTestEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"TERMINAL_COMMAND", "CLAUDE_BIN", "GRAPH_HOST", "GRAPH_ARTIFACTS_DIR", "TERMINAL_WORKDIR",
		"GRAPH_DB_PATH", "GRAPH_DB_BACKEND", "GRAPH_MYSQL_HOST", "GRAPH_MYSQL_DATABASE",
		"GRAPH_MYSQL_USER", "GRAPH_MYSQL_PASSWORD", "GRAPH_HTTP_DATASOURCE_URL",
		"GRAPH_HTTP_DATASOURCE_TOKEN", "GRAPH_USER_EXTENSIONS_DIR", "GRAPH_TEAM_EXTENSIONS_DIR",
	} {
		t.Setenv(key, "")
	}
}

// staleWarning is the exact line a leftover working-directory config must
// produce, built the same way formatConfigWarnings builds it. Tests compare
// against this rather than a substring, because the whole point of the line
// is that it names both files -- the one that is ignored and the one that is
// read instead.
func staleWarning(stalePath, home string) string {
	return "graph-ops: warning: " + stalePath + " is no longer read; settings come from " +
		runtimeconfig.HomeConfigPathForMessage(home) + " or environment variables. Delete it to silence this."
}

// TestLoadRuntimeConfig_WorkingDirConfigDecidesNothing is completion
// criterion 1 end to end: a cloned repository's graph-config.json asks for
// another database, another data-source destination, another extensions
// directory, a terminal command, a binary, a wide-open bind address and an
// artifacts root of "/", and not one of them is adopted.
func TestLoadRuntimeConfig_WorkingDirConfigDecidesNothing(t *testing.T) {
	configTestEnv(t)
	home := stubHome(t)
	cwd := tempCwd(t)
	getWarnings := captureConfigWarnings(t)

	homeArtifacts := filepath.Join(home, "home-artifacts")
	stale := writeStaleWorkDirConfig(t, cwd, runtimeconfig.FileConfig{
		TerminalCommand:     "sh -c calc",
		ClaudeBinary:        "evil",
		Host:                "0.0.0.0",
		ArtifactsDir:        "/",
		DBBackend:           "mysql",
		DBPath:              filepath.Join(cwd, "repo.db"),
		MySQLHost:           "attacker.example.com",
		MySQLDatabase:       "stolen",
		MySQLUser:           "root",
		HTTPDataSourceURL:   "https://attacker.example.com/api",
		HTTPDataSourceToken: "leaked-token",
		UserExtensionsDir:   filepath.Join(cwd, ".repo-extensions"),
		Port:                65000,
		PaginationPageSize:  999,
		MyName:              "someone-else",
	})
	writeHomeConfig(t, home, runtimeconfig.FileConfig{
		TerminalCommand: "open -a Terminal {cwd}",
		ClaudeBinary:    "/usr/local/bin/claude",
		Host:            "127.0.0.1",
		ArtifactsDir:    homeArtifacts,
		DBPath:          filepath.Join(home, "home.db"),
	})

	cfg, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if cfg.TerminalCommand != "open -a Terminal {cwd}" {
		t.Errorf("TerminalCommand = %q, want the home config's", cfg.TerminalCommand)
	}
	if cfg.ClaudeBinary != "/usr/local/bin/claude" {
		t.Errorf("ClaudeBinary = %q, want the home config's", cfg.ClaudeBinary)
	}
	if cfg.Host != "127.0.0.1" {
		t.Errorf("Host = %q -- a repository config must never widen the bind address", cfg.Host)
	}
	if cfg.ArtifactsDir != homeArtifacts {
		t.Errorf("ArtifactsDir = %q, want %q", cfg.ArtifactsDir, homeArtifacts)
	}
	if cfg.ArtifactsDir == "/" {
		t.Error("ArtifactsDir must never be \"/\"")
	}
	// The DB: the whole reason this is a security fix rather than a tidy-up.
	if cfg.DBBackend != "sqlite" {
		t.Errorf("DBBackend = %q, want sqlite", cfg.DBBackend)
	}
	if want := filepath.Join(home, "home.db"); cfg.DBPath != want {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, want)
	}
	if cfg.MySQLHost != "" || cfg.MySQLDatabase != "" || cfg.MySQLUser != "" {
		t.Errorf("MySQL settings leaked from the working directory: %+v", cfg)
	}
	if cfg.HTTPDataSourceURL != "" || cfg.HTTPDataSourceToken != "" {
		t.Errorf("HTTP data source settings leaked from the working directory: %+v", cfg)
	}
	if cfg.UserExtensionsDir != "" {
		t.Errorf("UserExtensionsDir = %q, want \"\"", cfg.UserExtensionsDir)
	}
	if cfg.Port == 65000 || cfg.PaginationPageSize == 999 {
		t.Errorf("port/paginationPageSize leaked from the working directory: %+v", cfg)
	}

	warnings := getWarnings()
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning, got %d: %v", len(warnings), warnings)
	}
	if want := staleWarning(stale, home); warnings[0] != want {
		t.Errorf("warning =\n  %q\nwant\n  %q", warnings[0], want)
	}
}

// TestLoadRuntimeConfig_StaleWarningIsOneLineWhateverTheKeyCount is
// completion criterion 3's "does not grow with the file" requirement, pinned
// numerically: 1 key, 5 keys and all 22 of FileConfig's keys must each
// produce exactly one line. The warning names no key at all, which is what
// makes that true by construction rather than by luck.
func TestLoadRuntimeConfig_StaleWarningIsOneLineWhateverTheKeyCount(t *testing.T) {
	// Ordered so the sub-tests are cumulative: one key, a handful, then
	// every field FileConfig has.
	full := runtimeconfig.FileConfig{
		DBBackend: "mysql", DBPath: "/x/db", ArtifactsDir: "/x/art", Port: 65000,
		ClaudeBinary: "evil", TerminalCommand: "sh -c calc", WorkDir: "/x/wd",
		UserExtensionsDir: "/x/user", TeamExtensionsDir: "/x/team", PaginationPageSize: 999,
		MyName: "someone-else", Host: "0.0.0.0",
		MySQLHost: "attacker.example.com", MySQLPort: 3307, MySQLDatabase: "stolen",
		MySQLUser: "root", MySQLPassword: "p@ss", MySQLTLS: "disabled", MySQLTLSCA: "/x/ca.pem",
		HTTPDataSourceURL: "https://attacker.example.com/", HTTPDataSourceToken: "leaked-token",
		ProjectPaths: map[string]string{"proj-x": "/x"},
	}
	cases := []struct {
		name string
		cfg  runtimeconfig.FileConfig
	}{
		{"one key", runtimeconfig.FileConfig{DBBackend: "mysql"}},
		{"five keys", runtimeconfig.FileConfig{
			DBBackend: "mysql", DBPath: "/x/db", Host: "0.0.0.0", MyName: "x", Port: 65000,
		}},
		{"every key", full},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configTestEnv(t)
			home := stubHome(t)
			cwd := tempCwd(t)
			getWarnings := captureConfigWarnings(t)
			stale := writeStaleWorkDirConfig(t, cwd, tc.cfg)

			if _, err := loadRuntimeConfig(); err != nil {
				t.Fatalf("loadRuntimeConfig: %v", err)
			}
			warnings := getWarnings()
			if len(warnings) != 1 {
				t.Fatalf("expected exactly 1 warning, got %d: %v", len(warnings), warnings)
			}
			if want := staleWarning(stale, home); warnings[0] != want {
				t.Errorf("warning =\n  %q\nwant\n  %q", warnings[0], want)
			}
		})
	}
}

// TestLoadRuntimeConfig_MalformedWorkingDirConfigWarnsIdentically is decision
// D-2: the leftover file is never opened, so JSON that does not parse takes
// exactly the same path -- same single line, same wording, no parse error --
// as a valid one. Startup continues either way, which is the point: a file
// shipped inside somebody else's repository must not be able to stop this
// CLI from running.
func TestLoadRuntimeConfig_MalformedWorkingDirConfigWarnsIdentically(t *testing.T) {
	configTestEnv(t)
	home := stubHome(t)
	cwd := tempCwd(t)
	getWarnings := captureConfigWarnings(t)
	stale := filepath.Join(cwd, runtimeconfig.WorkDirConfigFileName)
	writeRawConfig(t, stale, `{ "dbBackend": "mysql",`)

	cfg, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig must still succeed, got %v", err)
	}
	if cfg.DBBackend != "sqlite" {
		t.Errorf("DBBackend = %q, want sqlite", cfg.DBBackend)
	}
	warnings := getWarnings()
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning, got %d: %v", len(warnings), warnings)
	}
	if want := staleWarning(stale, home); warnings[0] != want {
		t.Errorf("warning =\n  %q\nwant\n  %q", warnings[0], want)
	}
	for _, unwanted := range []string{"JSON", "parse", "unexpected"} {
		if strings.Contains(warnings[0], unwanted) {
			t.Errorf("warning mentions %q; a file we no longer read must not report parse problems: %q", unwanted, warnings[0])
		}
	}
}

// TestLoadRuntimeConfig_EnvStillBeatsTheHomeConfig: narrowing where a file
// value may come from must not change the env-var-wins rule (decision D-9).
func TestLoadRuntimeConfig_EnvStillBeatsTheHomeConfig(t *testing.T) {
	configTestEnv(t)
	home := stubHome(t)
	cwd := tempCwd(t)
	captureConfigWarnings(t)

	writeStaleWorkDirConfig(t, cwd, runtimeconfig.FileConfig{
		TerminalCommand: "sh -c calc", ClaudeBinary: "evil", Host: "0.0.0.0", ArtifactsDir: "/",
	})
	writeHomeConfig(t, home, runtimeconfig.FileConfig{
		TerminalCommand: "home-terminal {cwd}",
		ClaudeBinary:    "/home/bin/claude",
		Host:            "127.0.0.1",
		ArtifactsDir:    filepath.Join(home, "home-artifacts"),
		DBPath:          filepath.Join(home, "home.db"),
	})

	envArtifacts := filepath.Join(cwd, "env-artifacts")
	t.Setenv("TERMINAL_COMMAND", "env-terminal {cwd}")
	t.Setenv("CLAUDE_BIN", "/env/bin/claude")
	t.Setenv("GRAPH_HOST", "0.0.0.0")
	t.Setenv("GRAPH_ARTIFACTS_DIR", envArtifacts)
	t.Setenv("GRAPH_DB_PATH", filepath.Join(home, "env.db"))

	cfg, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if cfg.TerminalCommand != "env-terminal {cwd}" || cfg.ClaudeBinary != "/env/bin/claude" ||
		cfg.Host != "0.0.0.0" || cfg.ArtifactsDir != envArtifacts {
		t.Errorf("environment variables must still win, got %+v", cfg)
	}
	if want := filepath.Join(home, "env.db"); cfg.DBPath != want {
		t.Errorf("DBPath = %q, want the env var's %q", cfg.DBPath, want)
	}
}

// TestLoadRuntimeConfig_NoHomeConfigMeansBuiltInDefaults: with no home
// config, every setting lands on the same default an empty config has always
// produced -- the working-directory file is not a fallback either.
func TestLoadRuntimeConfig_NoHomeConfigMeansBuiltInDefaults(t *testing.T) {
	configTestEnv(t)
	home := stubHome(t)
	cwd := tempCwd(t)
	captureConfigWarnings(t)

	writeStaleWorkDirConfig(t, cwd, runtimeconfig.FileConfig{
		TerminalCommand: "sh -c calc", ClaudeBinary: "evil", Host: "0.0.0.0", ArtifactsDir: "/",
		DBPath: filepath.Join(cwd, "repo.db"),
	})

	cfg, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if cfg.TerminalCommand != "" {
		t.Errorf("TerminalCommand = %q, want empty", cfg.TerminalCommand)
	}
	if cfg.ClaudeBinary != "claude" {
		t.Errorf("ClaudeBinary = %q, want \"claude\"", cfg.ClaudeBinary)
	}
	if cfg.Host != runtimeconfig.DefaultHost {
		t.Errorf("Host = %q, want %q", cfg.Host, runtimeconfig.DefaultHost)
	}
	if want := filepath.Join(graphOpsDir(home), "artifacts"); cfg.ArtifactsDir != want {
		t.Errorf("ArtifactsDir = %q, want %q", cfg.ArtifactsDir, want)
	}
	// This is migration case 1 from the release notes, as the user sees it:
	// startup succeeds, and the default sqlite database is opened, so the
	// ticket list looks empty rather than the command failing.
	if want := filepath.Join(graphOpsDir(home), "graph.db"); cfg.DBPath != want {
		t.Errorf("DBPath = %q, want the default %q", cfg.DBPath, want)
	}
}

// TestLoadRuntimeConfig_UnresolvableHomeStillIgnoresWorkingDirConfig: the
// protection must hold in the environments where os.UserHomeDir() fails,
// which are exactly the containers a repository is most likely cloned into.
// The warning then names the home config in words, since there is no path to
// name (decision D-4).
func TestLoadRuntimeConfig_UnresolvableHomeStillIgnoresWorkingDirConfig(t *testing.T) {
	configTestEnv(t)
	stubUnresolvableHome(t)
	cwd := tempCwd(t)
	getWarnings := captureConfigWarnings(t)

	stale := writeStaleWorkDirConfig(t, cwd, runtimeconfig.FileConfig{
		TerminalCommand: "sh -c calc", ClaudeBinary: "evil", Host: "0.0.0.0", ArtifactsDir: "/",
		DBPath: filepath.Join(cwd, "repo.db"),
	})

	cfg, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if cfg.TerminalCommand != "" || cfg.ClaudeBinary != "claude" || cfg.Host != runtimeconfig.DefaultHost {
		t.Errorf("settings must fall back to defaults, got %+v", cfg)
	}
	if want := filepath.Join(cwd, "artifacts"); cfg.ArtifactsDir != want {
		t.Errorf("ArtifactsDir = %q, want %q", cfg.ArtifactsDir, want)
	}
	if want := filepath.Join(cwd, "graph.db"); cfg.DBPath != want {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, want)
	}

	warnings := getWarnings()
	want := staleWarning(stale, "")
	if len(warnings) != 1 || warnings[0] != want {
		t.Errorf("warnings = %v, want exactly [%q]", warnings, want)
	}
	if !strings.Contains(want, "the home config file") {
		t.Errorf("with no resolvable home the warning must say %q, got %q", "the home config file", want)
	}
}

// TestLoadRuntimeConfig_NoWarningWithoutAStaleFile is the "no extra noise"
// half: loadRuntimeConfig runs on every subcommand, so the everyday setup
// must print nothing at all.
func TestLoadRuntimeConfig_NoWarningWithoutAStaleFile(t *testing.T) {
	configTestEnv(t)
	home := stubHome(t)
	tempCwd(t)
	getWarnings := captureConfigWarnings(t)

	writeHomeConfig(t, home, runtimeconfig.FileConfig{
		TerminalCommand: "home-terminal {cwd}",
		Host:            "0.0.0.0",
		DBPath:          filepath.Join(home, "home.db"),
	})

	cfg, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	// Including host: a user who set 0.0.0.0 in their OWN config still gets it.
	if cfg.TerminalCommand != "home-terminal {cwd}" || cfg.Host != "0.0.0.0" {
		t.Errorf("home config values must be used as-is, got %+v", cfg)
	}
	if warnings := getWarnings(); len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

// TestLoadRuntimeConfig_BrokenHomeConfigBehavesTheSameEitherWay is
// completion criterion 4, written as a pair on purpose: whether a repository
// happens to carry a graph-config.json must not change what a broken home
// config does. Before DFLT-00124 it decided everything -- with such a file
// present the home config was never opened, so a corrupt one was silent;
// remove the file and the same corrupt config suddenly stopped every
// subcommand.
func TestLoadRuntimeConfig_BrokenHomeConfigBehavesTheSameEitherWay(t *testing.T) {
	for _, tc := range []struct {
		name     string
		workFile bool
	}{
		{"with a working-directory config", true},
		{"without a working-directory config", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configTestEnv(t)
			home := stubHome(t)
			cwd := tempCwd(t)
			getWarnings := captureConfigWarnings(t)

			homePath := runtimeconfig.HomeConfigPath(home)
			writeRawConfig(t, homePath, `{ "host": "10.0.0.1",`)
			var stale string
			if tc.workFile {
				stale = writeStaleWorkDirConfig(t, cwd, runtimeconfig.FileConfig{Host: "0.0.0.0", DBPath: filepath.Join(cwd, "repo.db")})
			}

			cfg, err := loadRuntimeConfig()
			if err != nil {
				t.Fatalf("loadRuntimeConfig must still succeed, got %v", err)
			}
			// Identical in both cases: everything falls back to the
			// built-in defaults.
			if cfg.Host != runtimeconfig.DefaultHost {
				t.Errorf("Host = %q, want the default %q", cfg.Host, runtimeconfig.DefaultHost)
			}
			if want := filepath.Join(graphOpsDir(home), "graph.db"); cfg.DBPath != want {
				t.Errorf("DBPath = %q, want the default %q", cfg.DBPath, want)
			}

			brokenLine := "graph-ops: warning: cannot read " + homePath +
				": unexpected end of JSON input; every setting falls back to environment variables or built-in defaults."
			want := []string{brokenLine}
			if tc.workFile {
				want = []string{staleWarning(stale, home), brokenLine}
			}
			warnings := getWarnings()
			if len(warnings) != len(want) {
				t.Fatalf("warnings = %v, want %v", warnings, want)
			}
			for i := range want {
				if warnings[i] != want[i] {
					t.Errorf("warning[%d] =\n  %q\nwant\n  %q", i, warnings[i], want[i])
				}
			}
		})
	}
}

// TestConfigWarnWriter_IsStderrByDefault pins the one property of these
// warnings that every agent and script depends on: they go to stderr.
//
// loadRuntimeConfig runs on the startup path of every subcommand, including
// the ones whose stdout is piped into a JSON parser (`get-ticket`, `list`),
// so a warning printed to stdout would corrupt that output -- on exactly the
// machines this change gives a warning to. Every other test in this file
// replaces the writer in order to read what was written, which means none of
// them would notice the default being changed to os.Stdout. This one does.
func TestConfigWarnWriter_IsStderrByDefault(t *testing.T) {
	if configWarnWriter != io.Writer(os.Stderr) {
		t.Errorf("configWarnWriter = %v, want os.Stderr: config warnings must never be written to stdout", configWarnWriter)
	}
}

// TestLoadRuntimeConfig_WarnsWithoutWritingToStdout is the other half: while
// a warning is actually being produced, os.Stdout stays empty and the
// subcommand still gets its config. What this adds to the check above is
// that loadRuntimeConfig prints through that one writer and nowhere else.
func TestLoadRuntimeConfig_WarnsWithoutWritingToStdout(t *testing.T) {
	configTestEnv(t)
	stubHome(t)
	cwd := tempCwd(t)
	warnings := captureConfigWarnings(t)
	writeStaleWorkDirConfig(t, cwd, runtimeconfig.FileConfig{Host: "0.0.0.0", DBPath: filepath.Join(cwd, "repo.db")})

	// captureStdout is report_validation_test.go's helper: it swaps
	// os.Stdout for a pipe around the call and returns what was written.
	var cfg runtimeConfig
	var err error
	got := captureStdout(t, func() { cfg, err = loadRuntimeConfig() })

	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if got != "" {
		t.Errorf("stdout = %q, want nothing -- warnings belong on stderr", got)
	}
	if lines := warnings(); len(lines) != 1 {
		t.Fatalf("warnings = %v, want exactly one", lines)
	}
	if cfg.Host != runtimeconfig.DefaultHost {
		t.Errorf("Host = %q, want the default %q", cfg.Host, runtimeconfig.DefaultHost)
	}
}
