package httpserver

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
)

// DFLT-00124: GET/PUT /api/settings/app read and write exactly one file, the
// home config ($HOME/.graph-ops/config.json). There is no second file for
// some keys, no working-directory file for the rest, and therefore no
// half-saved outcome to report. A leftover graph-config.json in the working
// directory appears in several of these tests, purely to show that it is
// neither read nor written.

// writeConfigJSON writes a config file for these tests, creating its parent
// directory ($HOME/.graph-ops does not exist yet in a fresh temp home).
func writeConfigJSON(t *testing.T, path string, cfg runtimeconfig.FileConfig) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
}

// readRawConfigBytes returns a config file's bytes verbatim, for the
// "this file was not touched" assertions.
func readRawConfigBytes(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%s): %v", path, err)
	}
	return string(raw)
}

func readConfigJSON(t *testing.T, path string) runtimeconfig.FileConfig {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%s): %v", path, err)
	}
	var cfg runtimeconfig.FileConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("json.Unmarshal(%s): %v", path, err)
	}
	return cfg
}

// staleWorkDirConfigPath is the leftover file in the working directory.
func staleWorkDirConfigPath(workDir string) string {
	return filepath.Join(workDir, runtimeconfig.WorkDirConfigFileName)
}

// bothConfigs sets up a working-directory graph-config.json (the one a cloned
// repository would carry) alongside the user's own home config, each naming
// different values, and returns their paths.
func bothConfigs(t *testing.T, workDir, homeDir string) (workPath, homePath string) {
	t.Helper()
	workPath = staleWorkDirConfigPath(workDir)
	homePath = runtimeconfig.HomeConfigPath(homeDir)
	writeConfigJSON(t, workPath, runtimeconfig.FileConfig{
		ArtifactsDir: filepath.Join(workDir, "repo-artifacts"),
		DBPath:       filepath.Join(workDir, "repo.db"),
	})
	writeConfigJSON(t, homePath, runtimeconfig.FileConfig{
		ArtifactsDir: filepath.Join(homeDir, "home-artifacts"),
		DBPath:       filepath.Join(homeDir, "home.db"),
	})
	return workPath, homePath
}

// appSettingsBody is a complete PUT body -- this endpoint treats its owned
// fields as a full replacement, so a test that omitted one would be clearing
// it rather than leaving it alone.
func appSettingsBody(artifactsDir, dbPath string) map[string]any {
	return map[string]any{
		"dbBackend":          "sqlite",
		"dbPath":             dbPath,
		"artifactsDir":       artifactsDir,
		"userExtensionsDir":  "",
		"paginationPageSize": 10,
	}
}

// TestAppSettings_GetShowsOnlyTheHomeConfig is completion criterion 11 on the
// read side: every field comes from the home config, and config_path names
// that one file.
func TestAppSettings_GetShowsOnlyTheHomeConfig(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	_, homePath := bothConfigs(t, workDir, homeDir)

	rec := doJSON(t, s, http.MethodGet, "/api/settings/app", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got appSettingsResponse
	mustDecode(t, rec, &got)

	if want := filepath.Join(homeDir, "home-artifacts"); got.File.ArtifactsDir != want {
		t.Errorf("file.artifactsDir = %q, want the home config's %q", got.File.ArtifactsDir, want)
	}
	if want := filepath.Join(homeDir, "home.db"); got.File.DBPath != want {
		t.Errorf("file.dbPath = %q, want the home config's %q", got.File.DBPath, want)
	}
	if got.ConfigPath != homePath {
		t.Errorf("config_path = %q, want %q", got.ConfigPath, homePath)
	}
}

// TestAppSettings_ResponseHasNoHomeConfigPathField: the second path field
// existed only because there were two files. One file, one path.
func TestAppSettings_ResponseHasNoHomeConfigPathField(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	bothConfigs(t, workDir, homeDir)

	rec := doJSON(t, s, http.MethodGet, "/api/settings/app", nil)
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, present := raw["home_config_path"]; present {
		t.Error("home_config_path must be gone from the response")
	}
}

// TestAppSettings_ResponseNeverNamesTheStaleWorkDirConfig is plan review
// condition F-3: the leftover file is detected, but only the CLI warns about
// it. Nothing about it may appear in an HTTP response -- neither its path nor
// a warning code -- because that would add an API surface (and an i18n
// string) for what is a migration aid announced in the release notes.
func TestAppSettings_ResponseNeverNamesTheStaleWorkDirConfig(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	workPath, _ := bothConfigs(t, workDir, homeDir)

	rec := doJSON(t, s, http.MethodGet, "/api/settings/app", nil)
	if body := rec.Body.String(); contains(body, workPath) {
		t.Errorf("the response names the stale working-directory config: %s", body)
	}
	var got appSettingsResponse
	mustDecode(t, rec, &got)
	for _, w := range got.Warnings {
		if w != warnHomeConfigUnreadable {
			t.Errorf("unexpected warning %q; the stale file gets no warning code", w)
		}
	}
}

// TestAppSettings_NeverLooksAtTheWorkingDirectory pins non-functional review
// condition NF-2: the app-settings handlers read the home config with an
// empty cwd, so the server does not stat the directory it was started in at
// all -- not even to detect the leftover graph-config.json, which is the
// CLI's warning to give and nothing this API reports.
//
// It asserts on loadHomeEffective rather than on a response body on purpose:
// the leftover file is already kept out of the response by
// TestAppSettings_ResponseNeverNamesTheStaleWorkDirConfig, so a handler that
// went back to passing s.cfg.WorkDir would still answer byte for byte the
// same and no response-level assertion could tell. What changes is whether
// the work-dir path is looked up at all, and that is visible right here.
func TestAppSettings_NeverLooksAtTheWorkingDirectory(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	bothConfigs(t, workDir, homeDir)

	eff := s.loadHomeEffective()
	if eff.StaleWorkDirConfigPath != "" {
		t.Errorf("StaleWorkDirConfigPath = %q, want \"\": the server must not stat its working directory",
			eff.StaleWorkDirConfigPath)
	}
	// The home config is still read, i.e. the empty cwd did not cost the
	// handlers their actual settings source.
	if eff.HomeConfigPath != runtimeconfig.HomeConfigPath(homeDir) {
		t.Errorf("HomeConfigPath = %q, want %q", eff.HomeConfigPath, runtimeconfig.HomeConfigPath(homeDir))
	}
	if eff.Config.DBPath != filepath.Join(homeDir, "home.db") {
		t.Errorf("dbPath = %q, want the home config's value", eff.Config.DBPath)
	}
}

// TestAppSettings_PutWritesOneFile is completion criterion 10's second half:
// every field this page owns lands in the home config in a single write, and
// the repository's own file is neither read nor rewritten.
func TestAppSettings_PutWritesOneFile(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	workPath, homePath := bothConfigs(t, workDir, homeDir)
	before := readRawConfigBytes(t, workPath)
	newArtifacts := filepath.Join(homeDir, "ui-artifacts")
	newDB := filepath.Join(homeDir, "ui.db")

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", appSettingsBody(newArtifacts, newDB))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got appSettingsResponse
	mustDecode(t, rec, &got)

	onHome := readConfigJSON(t, homePath)
	if onHome.ArtifactsDir != newArtifacts || onHome.DBPath != newDB {
		t.Errorf("home config = %+v, want artifactsDir=%q dbPath=%q", onHome, newArtifacts, newDB)
	}
	if after := readRawConfigBytes(t, workPath); after != before {
		t.Errorf("the working-directory config was rewritten: %s, want %s", after, before)
	}
	if got.ConfigPath != homePath {
		t.Errorf("config_path = %q, want %q", got.ConfigPath, homePath)
	}
	if got.File.ArtifactsDir != newArtifacts || got.File.DBPath != newDB {
		t.Errorf("response file must show what was saved, got %+v", got.File)
	}
	if len(got.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", got.Warnings)
	}
}

// TestAppSettings_PutThenGetKeepsTheNewValues: the values have to come back
// out of the place they were put, through exactly the read a restart does.
func TestAppSettings_PutThenGetKeepsTheNewValues(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	bothConfigs(t, workDir, homeDir)
	newArtifacts := filepath.Join(homeDir, "ui-artifacts")
	newDB := filepath.Join(homeDir, "ui.db")

	if rec := doJSON(t, s, http.MethodPut, "/api/settings/app",
		appSettingsBody(newArtifacts, newDB)); rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, s, http.MethodGet, "/api/settings/app", nil)
	var got appSettingsResponse
	mustDecode(t, rec, &got)
	if got.File.ArtifactsDir != newArtifacts || got.File.DBPath != newDB {
		t.Errorf("file = %+v, want artifactsDir=%q dbPath=%q", got.File, newArtifacts, newDB)
	}

	eff := runtimeconfig.LoadEffective(workDir, homeDir)
	if eff.Config.ArtifactsDir != newArtifacts || eff.Config.DBPath != newDB {
		t.Errorf("effective config after restart = %+v, want the saved values", eff.Config)
	}
}

// TestAppSettings_PutCreatesNoWorkingDirConfig: in a clean environment the
// save must create the home config and nothing else.
func TestAppSettings_PutCreatesNoWorkingDirConfig(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	homePath := runtimeconfig.HomeConfigPath(homeDir)
	newArtifacts := filepath.Join(homeDir, "ui-artifacts")
	newDB := filepath.Join(homeDir, "ui.db")

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", appSettingsBody(newArtifacts, newDB))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	onDisk := readConfigJSON(t, homePath)
	if onDisk.ArtifactsDir != newArtifacts || onDisk.DBPath != newDB {
		t.Errorf("home config = %+v, want artifactsDir=%q dbPath=%q", onDisk, newArtifacts, newDB)
	}
	if _, err := os.Stat(staleWorkDirConfigPath(workDir)); !os.IsNotExist(err) {
		t.Errorf("no working-directory config may be created, stat err = %v", err)
	}
}

// TestAppSettings_PutWithoutAHomeDirFails is completion criterion 10: with no
// home directory there is no file to write, so the request must fail rather
// than answer 200 for a save that went nowhere. Before DFLT-00124 this was a
// warning on a successful response, which is exactly the shape the criterion
// exists to remove.
func TestAppSettings_PutWithoutAHomeDirFails(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	workPath, _ := bothConfigs(t, workDir, homeDir)
	before := readRawConfigBytes(t, workPath)
	s.cfg.HomeDir = ""

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app",
		appSettingsBody(filepath.Join(workDir, "ui-artifacts"), filepath.Join(workDir, "ui.db")))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("PUT expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if got, want := decodeError(t, rec).Code, domain.ErrCodeHomeConfigUnavailable; got != want {
		t.Errorf("error code = %q, want %q", got, want)
	}
	// And nothing was written anywhere, least of all the repository's file.
	if after := readRawConfigBytes(t, workPath); after != before {
		t.Errorf("the working-directory config was written: %s, want %s", after, before)
	}
}

// TestAppSettings_GetWithoutAHomeDirReturnsAnEmptyConfigPath is decision D-4:
// the API answers "" rather than a path that does not exist. Wording for that
// case is the client's business (the CLI has its own, see
// runtimeconfig.HomeConfigPathForMessage).
func TestAppSettings_GetWithoutAHomeDirReturnsAnEmptyConfigPath(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	bothConfigs(t, workDir, homeDir)
	s.cfg.HomeDir = ""

	rec := doJSON(t, s, http.MethodGet, "/api/settings/app", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got appSettingsResponse
	mustDecode(t, rec, &got)
	if got.ConfigPath != "" {
		t.Errorf("config_path = %q, want \"\" -- never a path that does not exist", got.ConfigPath)
	}
	if got.File.ArtifactsDir != "" || got.File.DBPath != "" {
		t.Errorf("file = %+v, want empty: there is no config file to read", got.File)
	}
}

// TestAppSettings_PutKeepsTheResentMySQLPassword pins that the secret-resend
// rule compares against the home config -- the file that actually holds the
// secret and the file the save rewrites.
func TestAppSettings_PutKeepsTheResentMySQLPassword(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	workPath := staleWorkDirConfigPath(workDir)
	homePath := runtimeconfig.HomeConfigPath(homeDir)
	writeConfigJSON(t, homePath, runtimeconfig.FileConfig{
		DBBackend:     "mysql",
		MySQLHost:     "db.example.com",
		MySQLPort:     3306,
		MySQLDatabase: "graph",
		MySQLUser:     "app",
		MySQLPassword: "s3cret",
	})
	// A different password in the repository's file, which must not be
	// consulted for the comparison.
	writeConfigJSON(t, workPath, runtimeconfig.FileConfig{MySQLPassword: "attacker-secret"})

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{
		"dbBackend":          "mysql",
		"mysqlHost":          "db.example.com",
		"mysqlPort":          3306,
		"mysqlDatabase":      "graph",
		"mysqlUser":          "app",
		"mysqlPassword":      runtimeconfig.RedactedSecretPlaceholder,
		"artifactsDir":       filepath.Join(homeDir, "ui-artifacts"),
		"paginationPageSize": 10,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got appSettingsResponse
	mustDecode(t, rec, &got)

	if onHome := readConfigJSON(t, homePath); onHome.MySQLPassword != "s3cret" {
		t.Errorf("stored mysqlPassword = %q, want it preserved", onHome.MySQLPassword)
	}
	if got.File.MySQLPassword != runtimeconfig.RedactedSecretPlaceholder {
		t.Errorf("response mysqlPassword = %q, want the placeholder", got.File.MySQLPassword)
	}
}

// TestAppSettings_UnreadableHomeConfigIsVisibleInTheAPI covers NF-2: a home
// config that cannot be parsed is reported on stderr by the CLI, but someone
// who only ever opens the Web UI never sees that -- and the page would
// otherwise show empty fields, with a save that fails identically every time
// and no way to find out why.
//
// The two sides are deliberately not symmetrical in kind: a GET can still
// answer (200 plus a warning), a PUT cannot, because overwriting a file it
// failed to parse would throw away whatever the user has in there. They are
// symmetrical in NAME, so the UI looks one condition up either way.
func TestAppSettings_UnreadableHomeConfigIsVisibleInTheAPI(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	workPath, homePath := bothConfigs(t, workDir, homeDir)
	beforeWork := readRawConfigBytes(t, workPath)
	broken := `{ "artifactsDir": "/home/artifacts",`
	if err := os.WriteFile(homePath, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, s, http.MethodGet, "/api/settings/app", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got appSettingsResponse
	mustDecode(t, rec, &got)
	if !slices.Contains(got.Warnings, warnHomeConfigUnreadable) {
		t.Errorf("GET warnings = %v, want it to contain %q", got.Warnings, warnHomeConfigUnreadable)
	}
	// Nothing of the broken file may be used, not even the part that parsed.
	if got.File.ArtifactsDir != "" {
		t.Errorf("file.artifactsDir = %q, want empty", got.File.ArtifactsDir)
	}

	rec = doJSON(t, s, http.MethodPut, "/api/settings/app",
		appSettingsBody(filepath.Join(homeDir, "ui-artifacts"), filepath.Join(homeDir, "ui.db")))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("PUT expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if code, want := decodeError(t, rec).Code, domain.ErrCodeHomeConfigUnreadable; code != want {
		t.Errorf("error code = %q, want %q", code, want)
	}
	// The file is left exactly as it was: a broken config the user can
	// repair, not one this endpoint has overwritten.
	raw, err := os.ReadFile(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != broken {
		t.Errorf("home config was rewritten: %s", raw)
	}
	if after := readRawConfigBytes(t, workPath); after != beforeWork {
		t.Errorf("the working-directory config was written: %s, want %s", after, beforeWork)
	}
}

// TestAppSettings_NeverTouchesTheExecutionSettings is the regression test for
// DFLT-00104's security review S-5. This endpoint writes the home config --
// the file every setting is trusted from -- so what it may put there is worth
// pinning: not terminalCommand or claudeBinary, which end up on a shell
// command line when "Launch Claude" is pressed, and not host, which decides
// who can reach this unauthenticated API. None of the three has an editor in
// this UI, and an unauthenticated local API must not become the way they get
// set.
//
// The same test covers S-4 on the way out: the three are dropped from the
// response rather than being shown to a client that has no use for them.
func TestAppSettings_NeverTouchesTheExecutionSettings(t *testing.T) {
	s, _, homeDir := newAppSettingsTestServer(t)
	homePath := runtimeconfig.HomeConfigPath(homeDir)
	writeConfigJSON(t, homePath, runtimeconfig.FileConfig{
		TerminalCommand: "open -a Terminal {cwd}",
		ClaudeBinary:    "/usr/local/bin/claude",
		Host:            "127.0.0.1",
		ArtifactsDir:    filepath.Join(homeDir, "home-artifacts"),
	})

	// The body carries all three as well, so this pins that they are ignored
	// on the way in and not merely absent from the handler's struct.
	body := appSettingsBody(filepath.Join(homeDir, "ui-artifacts"), filepath.Join(homeDir, "ui.db"))
	body["terminalCommand"] = "sh -c calc"
	body["claudeBinary"] = "evil"
	body["host"] = "0.0.0.0"
	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	onHome := readConfigJSON(t, homePath)
	if onHome.TerminalCommand != "open -a Terminal {cwd}" || onHome.ClaudeBinary != "/usr/local/bin/claude" || onHome.Host != "127.0.0.1" {
		t.Errorf("the execution settings must survive a save untouched, got %+v", onHome)
	}
	if want := filepath.Join(homeDir, "ui-artifacts"); onHome.ArtifactsDir != want {
		t.Errorf("home artifactsDir = %q, want %q", onHome.ArtifactsDir, want)
	}

	// S-4: and they are not in the response either. Checked against the
	// decoded JSON object, so this sees the wire format rather than a Go
	// struct that could hold a value under a field the client never reads.
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	file, ok := raw["file"].(map[string]any)
	if !ok {
		t.Fatalf("response has no file object: %s", rec.Body.String())
	}
	for _, key := range []string{"terminalCommand", "claudeBinary", "host"} {
		if value, present := file[key]; present {
			t.Errorf("file.%s = %v, want the key absent (this page does not edit it)", key, value)
		}
	}
	if _, present := file["artifactsDir"]; !present {
		t.Error("file.artifactsDir must still be sent -- this page does edit it")
	}
}
