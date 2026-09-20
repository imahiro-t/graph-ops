package runtimeconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// writeWorkConfig writes a graph-config.json into cwd, the "found first, so
// it wins" candidate -- the file a cloned repository would be carrying.
func writeWorkConfig(t *testing.T, cwd string, raw string) string {
	t.Helper()
	path := filepath.Join(cwd, "graph-config.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	return path
}

// writeHomeConfig writes $HOME/.graph-ops/config.json, the only file the
// home-only keys may come from.
func writeHomeConfig(t *testing.T, home string, raw string) string {
	t.Helper()
	path := HomeConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	return path
}

// TestLoadEffective_HomeOnlyKeysComeFromHomeNotCwd is the core of completion
// criterion 1: a working-directory config supplies everything except the
// four home-only keys, which come from the home config even though that file
// is not the one ResolvePath picked.
func TestLoadEffective_HomeOnlyKeysComeFromHomeNotCwd(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	workPath := writeWorkConfig(t, cwd, `{
		"terminalCommand": "sh -c calc",
		"claudeBinary": "evil",
		"host": "0.0.0.0",
		"artifactsDir": "/",
		"dbPath": "/work/repo.db",
		"port": 4321
	}`)
	writeHomeConfig(t, home, `{
		"terminalCommand": "open -a Terminal {cwd}",
		"claudeBinary": "/usr/local/bin/claude",
		"host": "127.0.0.1",
		"artifactsDir": "/home/artifacts",
		"dbPath": "/home/home.db"
	}`)

	eff, err := LoadEffective(cwd, home)
	if err != nil {
		t.Fatalf("LoadEffective: %v", err)
	}
	if eff.Config.TerminalCommand != "open -a Terminal {cwd}" || eff.Config.ClaudeBinary != "/usr/local/bin/claude" ||
		eff.Config.Host != "127.0.0.1" || eff.Config.ArtifactsDir != "/home/artifacts" {
		t.Errorf("home-only keys must come from the home config, got %+v", eff.Config)
	}
	// Everything else keeps the old precedence: the cwd file still wins.
	if eff.Config.DBPath != "/work/repo.db" || eff.Config.Port != 4321 {
		t.Errorf("non-home-only keys must keep cwd precedence, got dbPath=%q port=%d", eff.Config.DBPath, eff.Config.Port)
	}
	if eff.Path != workPath {
		t.Errorf("Path = %q, want the resolved (cwd) path %q", eff.Path, workPath)
	}
	if eff.HomeConfigPath != HomeConfigPath(home) {
		t.Errorf("HomeConfigPath = %q, want %q", eff.HomeConfigPath, HomeConfigPath(home))
	}
	want := []IgnoredSetting{{Path: workPath, Keys: []string{"terminalCommand", "claudeBinary", "host", "artifactsDir"}}}
	if !reflect.DeepEqual(eff.Ignored, want) {
		t.Errorf("Ignored = %+v, want %+v", eff.Ignored, want)
	}
}

// TestLoadEffective_ReportsOnlyTheKeysActuallyPresent pins the warning input
// for completion criterion 2: the keys that were really there, in
// homeOnlyKeys order rather than the file's, and nothing else.
func TestLoadEffective_ReportsOnlyTheKeysActuallyPresent(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	// Written host-first to prove the order comes from homeOnlyKeys.
	workPath := writeWorkConfig(t, cwd, `{"host": "0.0.0.0", "terminalCommand": "sh -c calc", "dbPath": "/work/repo.db"}`)

	eff, err := LoadEffective(cwd, home)
	if err != nil {
		t.Fatalf("LoadEffective: %v", err)
	}
	want := []IgnoredSetting{{Path: workPath, Keys: []string{"terminalCommand", "host"}}}
	if !reflect.DeepEqual(eff.Ignored, want) {
		t.Errorf("Ignored = %+v, want %+v", eff.Ignored, want)
	}
}

// TestLoadEffective_NoHomeOnlyKeysMeansNothingIgnored is the other half of
// criterion 2: an ordinary per-repository config must produce no warning at
// all. An empty string counts as "not set" -- warning about a key that says
// nothing would be pure noise.
func TestLoadEffective_NoHomeOnlyKeysMeansNothingIgnored(t *testing.T) {
	for name, raw := range map[string]string{
		"absent": `{"dbPath": "/work/repo.db", "port": 4321, "paginationPageSize": 50}`,
		"empty":  `{"terminalCommand": "", "host": "", "dbPath": "/work/repo.db"}`,
	} {
		t.Run(name, func(t *testing.T) {
			cwd, home := t.TempDir(), t.TempDir()
			writeWorkConfig(t, cwd, raw)
			eff, err := LoadEffective(cwd, home)
			if err != nil {
				t.Fatalf("LoadEffective: %v", err)
			}
			if len(eff.Ignored) != 0 {
				t.Errorf("Ignored = %+v, want none", eff.Ignored)
			}
		})
	}
}

// TestLoadEffective_NoHomeConfigLeavesHomeOnlyKeysEmpty: with nothing
// trustworthy to read, the four keys fall through to the caller's env
// var/default chain rather than to the cwd file's values.
func TestLoadEffective_NoHomeConfigLeavesHomeOnlyKeysEmpty(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	writeWorkConfig(t, cwd, `{"terminalCommand": "sh -c calc", "claudeBinary": "evil", "host": "0.0.0.0", "artifactsDir": "/"}`)

	eff, err := LoadEffective(cwd, home)
	if err != nil {
		t.Fatalf("LoadEffective: %v", err)
	}
	if eff.Config.TerminalCommand != "" || eff.Config.ClaudeBinary != "" || eff.Config.Host != "" || eff.Config.ArtifactsDir != "" {
		t.Errorf("home-only keys must be empty when there is no home config, got %+v", eff.Config)
	}
}

// TestLoadEffective_UnresolvableHomeStillRefusesCwdValues covers the
// minimal-container case: os.UserHomeDir() failed, so there is no trusted
// file at all. The protection must not quietly switch off there.
func TestLoadEffective_UnresolvableHomeStillRefusesCwdValues(t *testing.T) {
	cwd := t.TempDir()
	workPath := writeWorkConfig(t, cwd, `{"host": "0.0.0.0", "dbPath": "/work/repo.db"}`)

	eff, err := LoadEffective(cwd, "")
	if err != nil {
		t.Fatalf("LoadEffective: %v", err)
	}
	if eff.Config.Host != "" {
		t.Errorf("Host = %q, want empty", eff.Config.Host)
	}
	if eff.Config.DBPath != "/work/repo.db" {
		t.Errorf("DBPath = %q, want the cwd value", eff.Config.DBPath)
	}
	if eff.HomeConfigPath != "" {
		t.Errorf("HomeConfigPath = %q, want empty", eff.HomeConfigPath)
	}
	want := []IgnoredSetting{{Path: workPath, Keys: []string{"host"}}}
	if !reflect.DeepEqual(eff.Ignored, want) {
		t.Errorf("Ignored = %+v, want %+v", eff.Ignored, want)
	}
}

// TestLoadEffective_HomeConfigAloneIsUnchanged: with no working-directory
// config, the home config is the resolved path and nothing about this
// change applies -- not the ignoring, not the second read.
func TestLoadEffective_HomeConfigAloneIsUnchanged(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	homePath := writeHomeConfig(t, home, `{"terminalCommand": "home-terminal {cwd}", "host": "0.0.0.0", "dbPath": "/home/home.db"}`)

	eff, err := LoadEffective(cwd, home)
	if err != nil {
		t.Fatalf("LoadEffective: %v", err)
	}
	if eff.Config.TerminalCommand != "home-terminal {cwd}" || eff.Config.Host != "0.0.0.0" || eff.Config.DBPath != "/home/home.db" {
		t.Errorf("home config values must be used as-is, got %+v", eff.Config)
	}
	if eff.Path != homePath {
		t.Errorf("Path = %q, want %q", eff.Path, homePath)
	}
	if len(eff.Ignored) != 0 {
		t.Errorf("Ignored = %+v, want none", eff.Ignored)
	}
}

// TestLoadEffective_MalformedHomeConfigIsNotFatal: the home config is a file
// this path did not use to open at all, so a corrupt one must not become a
// new reason for every subcommand to refuse to start. It is reported, and
// the four keys fall back.
func TestLoadEffective_MalformedHomeConfigIsNotFatal(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	// 10.0.0.1, not 127.0.0.1: a value distinct from DefaultHost, so a
	// partial read of the broken file could not pass for the fallback.
	writeWorkConfig(t, cwd, `{"host": "0.0.0.0", "dbPath": "/work/repo.db"}`)
	homePath := writeHomeConfig(t, home, `{ "host": "10.0.0.1",`)

	eff, err := LoadEffective(cwd, home)
	if err != nil {
		t.Fatalf("LoadEffective must not fail on a malformed home config, got %v", err)
	}
	if eff.HomeConfigErr == nil {
		t.Fatalf("HomeConfigErr = nil, want the parse error for %s", homePath)
	}
	if eff.Config.Host != "" {
		t.Errorf("Host = %q, want empty (nothing of the broken file may be used)", eff.Config.Host)
	}
	if eff.Config.DBPath != "/work/repo.db" {
		t.Errorf("DBPath = %q, want the cwd value", eff.Config.DBPath)
	}
}

// TestLoadEffective_MalformedResolvedConfigStillFails pins the two paths
// that were already errors before this change, so the leniency above stays
// confined to the newly-read file.
func TestLoadEffective_MalformedResolvedConfigStillFails(t *testing.T) {
	t.Run("cwd config", func(t *testing.T) {
		cwd, home := t.TempDir(), t.TempDir()
		writeWorkConfig(t, cwd, `{ "dbPath": "/work/repo.db",`)
		writeHomeConfig(t, home, `{}`)
		if _, err := LoadEffective(cwd, home); err == nil {
			t.Fatal("LoadEffective = nil error, want a parse error for the cwd config")
		}
	})
	t.Run("home config as the resolved path", func(t *testing.T) {
		cwd, home := t.TempDir(), t.TempDir()
		writeHomeConfig(t, home, `{ "host": "10.0.0.1",`)
		if _, err := LoadEffective(cwd, home); err == nil {
			t.Fatal("LoadEffective = nil error, want a parse error for the home config")
		}
	})
}

// TestUpdateHome_WritesHomeConfigEvenWithACwdConfig is completion criterion
// 3 at the storage layer: the write goes to the file the value will actually
// be read from, not to the one ResolvePath would pick.
func TestUpdateHome_WritesHomeConfigEvenWithACwdConfig(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	workPath := writeWorkConfig(t, cwd, `{"artifactsDir": "/work/repo-artifacts", "dbPath": "/work/repo.db"}`)
	writeHomeConfig(t, home, `{"artifactsDir": "/home/artifacts", "myName": "someone"}`)

	_, path, err := UpdateHome(home, func(cfg *FileConfig) error {
		cfg.ArtifactsDir = "/tmp/ui-artifacts"
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateHome: %v", err)
	}
	if path != HomeConfigPath(home) {
		t.Errorf("path = %q, want %q", path, HomeConfigPath(home))
	}

	onDisk := readConfigFile(t, path)
	if onDisk.ArtifactsDir != "/tmp/ui-artifacts" {
		t.Errorf("home artifactsDir = %q, want /tmp/ui-artifacts", onDisk.ArtifactsDir)
	}
	if onDisk.MyName != "someone" {
		t.Errorf("unrelated home fields must survive, got myName=%q", onDisk.MyName)
	}
	if work := readConfigFile(t, workPath); work.ArtifactsDir != "/work/repo-artifacts" {
		t.Errorf("the working-directory config must not be rewritten, got artifactsDir=%q", work.ArtifactsDir)
	}
}

// TestUpdateHome_CreatesTheHomeConfigDirectory: a first save on a fresh
// machine has no $HOME/.graph-ops yet.
func TestUpdateHome_CreatesTheHomeConfigDirectory(t *testing.T) {
	home := t.TempDir()
	if _, _, err := UpdateHome(home, func(cfg *FileConfig) error {
		cfg.ArtifactsDir = "/tmp/ui-artifacts"
		return nil
	}); err != nil {
		t.Fatalf("UpdateHome: %v", err)
	}
	if got := readConfigFile(t, HomeConfigPath(home)).ArtifactsDir; got != "/tmp/ui-artifacts" {
		t.Errorf("artifactsDir = %q, want /tmp/ui-artifacts", got)
	}
}

// TestUpdateHome_UnresolvableHomeIsAnError: silently writing nowhere is the
// exact failure this function exists to remove, so the caller is told.
func TestUpdateHome_UnresolvableHomeIsAnError(t *testing.T) {
	if _, _, err := UpdateHome("", func(*FileConfig) error { return nil }); err == nil {
		t.Fatal("UpdateHome(\"\") = nil error, want one")
	}
}

// TestHomeOnlyKeys_MatchFileConfigJSONTags stops the key list and the struct
// from drifting apart. It walks FileConfig's tags by reflection rather than
// comparing one constant against another, so renaming a field's json tag
// (which would quietly un-protect that setting) fails here.
func TestHomeOnlyKeys_MatchFileConfigJSONTags(t *testing.T) {
	keys := HomeOnlyKeys()
	if want := []string{"terminalCommand", "claudeBinary", "host", "artifactsDir"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("HomeOnlyKeys() = %v, want %v", keys, want)
	}
	tags := fileConfigJSONTags()
	for _, key := range keys {
		if !slices.Contains(tags, key) {
			t.Errorf("home-only key %q is not a json tag of FileConfig (tags: %v)", key, tags)
		}
	}
	// Mutating the returned slice must not reach the definition.
	keys[0] = "mutated"
	if HomeOnlyKeys()[0] != "terminalCommand" {
		t.Error("HomeOnlyKeys() must return a copy")
	}
	if got := HomeOnlyKeyList(); got != "terminalCommand, claudeBinary, host, artifactsDir" {
		t.Errorf("HomeOnlyKeyList() = %q", got)
	}
}

// TestClearHomeOnly_CoversEveryHomeOnlyKey ties the key names to the fields
// they actually clear: a key listed in homeOnlyKeys whose field clearHomeOnly
// forgot would be reported as ignored while still being used.
func TestClearHomeOnly_CoversEveryHomeOnlyKey(t *testing.T) {
	cfg := FileConfig{
		TerminalCommand: "t", ClaudeBinary: "c", Host: "h", ArtifactsDir: "a",
		DBPath: "keep", Port: 1,
	}
	cleared := clearHomeOnly(&cfg)
	if !reflect.DeepEqual(cleared, HomeOnlyKeys()) {
		t.Errorf("cleared = %v, want %v", cleared, HomeOnlyKeys())
	}
	if cfg.TerminalCommand != "" || cfg.ClaudeBinary != "" || cfg.Host != "" || cfg.ArtifactsDir != "" {
		t.Errorf("every home-only field must be zeroed, got %+v", cfg)
	}
	if cfg.DBPath != "keep" || cfg.Port != 1 {
		t.Errorf("no other field may be touched, got %+v", cfg)
	}
}

func readConfigFile(t *testing.T, path string) FileConfig {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%s): %v", path, err)
	}
	var cfg FileConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("json.Unmarshal(%s): %v", path, err)
	}
	return cfg
}
