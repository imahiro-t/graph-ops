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

// writeHomeGraphConfig is writeGraphConfigAt aimed at the home candidate,
// creating $HOME/.graph-ops first -- a fresh stubHome has no such directory.
func writeHomeGraphConfig(t *testing.T, home string, cfg runtimeconfig.FileConfig) string {
	t.Helper()
	path := runtimeconfig.HomeConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	writeGraphConfigAt(t, path, cfg)
	return path
}

// homeOnlyTestEnv clears every env var that overrides a home-only key, so a
// developer who happens to export one does not turn these tests green (or
// red) for the wrong reason.
func homeOnlyTestEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"TERMINAL_COMMAND", "CLAUDE_BIN", "GRAPH_HOST", "GRAPH_ARTIFACTS_DIR", "TERMINAL_WORKDIR"} {
		t.Setenv(key, "")
	}
	t.Setenv("GRAPH_DB_PATH", "")
}

// TestLoadRuntimeConfig_HomeOnlyKeysIgnoredFromWorkingDirConfig is finding
// SEC-05 end to end: a cloned repository's graph-config.json asks for a
// terminal command, a binary, a wide-open bind address and an artifacts root
// of "/", and none of the four is adopted.
func TestLoadRuntimeConfig_HomeOnlyKeysIgnoredFromWorkingDirConfig(t *testing.T) {
	homeOnlyTestEnv(t)
	home := stubHome(t)
	cwd := tempCwd(t)
	getWarnings := captureConfigWarnings(t)

	homeArtifacts := filepath.Join(home, "home-artifacts")
	writeGraphConfig(t, cwd, runtimeconfig.FileConfig{
		TerminalCommand: "sh -c calc",
		ClaudeBinary:    "evil",
		Host:            "0.0.0.0",
		ArtifactsDir:    "/",
		DBPath:          filepath.Join(cwd, "repo.db"),
	})
	writeHomeGraphConfig(t, home, runtimeconfig.FileConfig{
		TerminalCommand: "open -a Terminal {cwd}",
		ClaudeBinary:    "/usr/local/bin/claude",
		Host:            "127.0.0.1",
		ArtifactsDir:    homeArtifacts,
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
	// "/" would have been handed to os.MkdirAll and then used as the root
	// every artifact path is resolved under, so pin it separately.
	if cfg.ArtifactsDir == "/" {
		t.Error("ArtifactsDir must never be \"/\"")
	}
	// Everything else keeps working exactly as before.
	if want := filepath.Join(cwd, "repo.db"); cfg.DBPath != want {
		t.Errorf("DBPath = %q, want %q (dbPath is not a home-only key)", cfg.DBPath, want)
	}

	warnings := getWarnings()
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning, got %d: %v", len(warnings), warnings)
	}
	want := "graph-ops: warning: ignoring terminalCommand, claudeBinary, host, artifactsDir in " +
		filepath.Join(cwd, "graph-config.json") + "; these settings are read only from " +
		runtimeconfig.HomeConfigPath(home) + " or the environment."
	if warnings[0] != want {
		t.Errorf("warning =\n  %q\nwant\n  %q", warnings[0], want)
	}
}

// TestLoadRuntimeConfig_EnvStillBeatsTheHomeConfig: narrowing where the file
// value may come from must not change the env-var-wins rule.
func TestLoadRuntimeConfig_EnvStillBeatsTheHomeConfig(t *testing.T) {
	homeOnlyTestEnv(t)
	home := stubHome(t)
	cwd := tempCwd(t)
	captureConfigWarnings(t)

	writeGraphConfig(t, cwd, runtimeconfig.FileConfig{
		TerminalCommand: "sh -c calc", ClaudeBinary: "evil", Host: "0.0.0.0", ArtifactsDir: "/",
	})
	writeHomeGraphConfig(t, home, runtimeconfig.FileConfig{
		TerminalCommand: "home-terminal {cwd}",
		ClaudeBinary:    "/home/bin/claude",
		Host:            "127.0.0.1",
		ArtifactsDir:    filepath.Join(home, "home-artifacts"),
	})

	envArtifacts := filepath.Join(cwd, "env-artifacts")
	t.Setenv("TERMINAL_COMMAND", "env-terminal {cwd}")
	t.Setenv("CLAUDE_BIN", "/env/bin/claude")
	t.Setenv("GRAPH_HOST", "0.0.0.0")
	t.Setenv("GRAPH_ARTIFACTS_DIR", envArtifacts)

	cfg, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if cfg.TerminalCommand != "env-terminal {cwd}" || cfg.ClaudeBinary != "/env/bin/claude" ||
		cfg.Host != "0.0.0.0" || cfg.ArtifactsDir != envArtifacts {
		t.Errorf("environment variables must still win, got %+v", cfg)
	}
}

// TestLoadRuntimeConfig_HomeOnlyKeysFallBackToDefaults: with no home config,
// the four keys land on the same defaults an empty config has always
// produced -- the working-directory values are not a fallback.
func TestLoadRuntimeConfig_HomeOnlyKeysFallBackToDefaults(t *testing.T) {
	homeOnlyTestEnv(t)
	home := stubHome(t)
	cwd := tempCwd(t)
	captureConfigWarnings(t)

	writeGraphConfig(t, cwd, runtimeconfig.FileConfig{
		TerminalCommand: "sh -c calc", ClaudeBinary: "evil", Host: "0.0.0.0", ArtifactsDir: "/",
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
}

// TestLoadRuntimeConfig_UnresolvableHomeStillIgnoresWorkingDirConfig: the
// protection must hold in the environments where os.UserHomeDir() fails,
// which are exactly the containers a repository is most likely cloned into.
func TestLoadRuntimeConfig_UnresolvableHomeStillIgnoresWorkingDirConfig(t *testing.T) {
	homeOnlyTestEnv(t)
	stubUnresolvableHome(t)
	cwd := tempCwd(t)
	getWarnings := captureConfigWarnings(t)

	writeGraphConfig(t, cwd, runtimeconfig.FileConfig{
		TerminalCommand: "sh -c calc", ClaudeBinary: "evil", Host: "0.0.0.0", ArtifactsDir: "/",
		DBPath: filepath.Join(cwd, "repo.db"),
	})

	cfg, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if cfg.TerminalCommand != "" || cfg.ClaudeBinary != "claude" || cfg.Host != runtimeconfig.DefaultHost {
		t.Errorf("home-only keys must fall back to defaults, got %+v", cfg)
	}
	if want := filepath.Join(cwd, "artifacts"); cfg.ArtifactsDir != want {
		t.Errorf("ArtifactsDir = %q, want %q", cfg.ArtifactsDir, want)
	}
	if want := filepath.Join(cwd, "repo.db"); cfg.DBPath != want {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, want)
	}

	warnings := getWarnings()
	want := "graph-ops: warning: ignoring terminalCommand, claudeBinary, host, artifactsDir in " +
		filepath.Join(cwd, "graph-config.json") +
		"; these settings are read only from the home config file or the environment."
	if len(warnings) != 1 || warnings[0] != want {
		t.Errorf("warnings = %v, want exactly [%q]", warnings, want)
	}
}

// TestLoadRuntimeConfig_NoWarningWithoutHomeOnlyKeys is completion criterion
// 2's "no extra noise" half: loadRuntimeConfig runs on every subcommand, so
// an ordinary per-repository config must print nothing at all.
func TestLoadRuntimeConfig_NoWarningWithoutHomeOnlyKeys(t *testing.T) {
	homeOnlyTestEnv(t)
	stubHome(t)
	cwd := tempCwd(t)
	getWarnings := captureConfigWarnings(t)

	writeGraphConfig(t, cwd, runtimeconfig.FileConfig{
		DBPath: filepath.Join(cwd, "repo.db"), Port: 4321, PaginationPageSize: 50,
	})

	if _, err := loadRuntimeConfig(); err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if warnings := getWarnings(); len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

// TestLoadRuntimeConfig_NoWarningWhenOnlyTheHomeConfigExists: the everyday
// setup (no working-directory config at all) is completely untouched.
func TestLoadRuntimeConfig_NoWarningWhenOnlyTheHomeConfigExists(t *testing.T) {
	homeOnlyTestEnv(t)
	home := stubHome(t)
	tempCwd(t)
	getWarnings := captureConfigWarnings(t)

	writeHomeGraphConfig(t, home, runtimeconfig.FileConfig{
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

// TestLoadRuntimeConfig_MalformedHomeConfigWarnsButStarts: this file was not
// being opened at all on this path before, so a corrupt one must not become
// a new way for every subcommand to fail.
func TestLoadRuntimeConfig_MalformedHomeConfigWarnsButStarts(t *testing.T) {
	homeOnlyTestEnv(t)
	home := stubHome(t)
	cwd := tempCwd(t)
	getWarnings := captureConfigWarnings(t)

	writeGraphConfig(t, cwd, runtimeconfig.FileConfig{Host: "0.0.0.0", DBPath: filepath.Join(cwd, "repo.db")})
	homePath := runtimeconfig.HomeConfigPath(home)
	writeRawConfig(t, homePath, `{ "host": "10.0.0.1",`)

	cfg, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig must still succeed, got %v", err)
	}
	if cfg.Host != runtimeconfig.DefaultHost {
		t.Errorf("Host = %q, want the default %q", cfg.Host, runtimeconfig.DefaultHost)
	}

	warnings := getWarnings()
	want := []string{
		"graph-ops: warning: ignoring host in " + filepath.Join(cwd, "graph-config.json") +
			"; these settings are read only from " + homePath + " or the environment.",
		"graph-ops: warning: cannot read " + homePath + ": unexpected end of JSON input; " +
			"terminalCommand, claudeBinary, host, artifactsDir fall back to environment variables or built-in defaults.",
	}
	if len(warnings) != len(want) {
		t.Fatalf("warnings = %v, want %v", warnings, want)
	}
	for i := range want {
		if warnings[i] != want[i] {
			t.Errorf("warning[%d] =\n  %q\nwant\n  %q", i, warnings[i], want[i])
		}
	}
}

// TestLoadRuntimeConfig_MalformedHomeConfigAsResolvedPathStillFails keeps the
// leniency above confined to the newly-read file: when the home config IS
// the resolved path it was always an error, and still is.
func TestLoadRuntimeConfig_MalformedHomeConfigAsResolvedPathStillFails(t *testing.T) {
	homeOnlyTestEnv(t)
	home := stubHome(t)
	tempCwd(t)
	captureConfigWarnings(t)

	homePath := runtimeconfig.HomeConfigPath(home)
	writeRawConfig(t, homePath, `{ "host": "10.0.0.1",`)

	_, err := loadRuntimeConfig()
	if err == nil {
		t.Fatal("loadRuntimeConfig = nil error, want a parse error")
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
	homeOnlyTestEnv(t)
	stubHome(t)
	cwd := tempCwd(t)
	warnings := captureConfigWarnings(t)
	writeGraphConfig(t, cwd, runtimeconfig.FileConfig{Host: "0.0.0.0", DBPath: filepath.Join(cwd, "repo.db")})

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
