package httpserver

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/graph-ops/core-go/internal/runtimeconfig"
)

// Completion criterion 3 of DFLT-00104: of the fields PUT /api/settings/app
// owns, artifactsDir is a home-only key (see runtimeconfig.HomeOnlyKeys), so
// it has to be saved to -- and shown from -- the home config file even while
// a working-directory graph-config.json is the one everything else uses.
// Saving it into that working-directory file would store a value the next
// startup is then guaranteed to ignore: "I changed it in the UI and nothing
// happened".

// writeConfigJSON writes a config file for these tests, creating its parent
// directory (the home candidate's $HOME/.graph-ops does not exist yet in a
// fresh temp home).
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

// bothConfigs sets up the situation this change is about: a
// working-directory graph-config.json (the one a cloned repository would
// carry) alongside the user's own home config, each naming a different
// artifactsDir.
func bothConfigs(t *testing.T, workDir, homeDir string) (workPath, homePath string) {
	t.Helper()
	workPath = filepath.Join(workDir, "graph-config.json")
	homePath = runtimeconfig.HomeConfigPath(homeDir)
	writeConfigJSON(t, workPath, runtimeconfig.FileConfig{
		ArtifactsDir: filepath.Join(workDir, "repo-artifacts"),
		DBPath:       filepath.Join(workDir, "repo.db"),
	})
	writeConfigJSON(t, homePath, runtimeconfig.FileConfig{
		ArtifactsDir: filepath.Join(homeDir, "home-artifacts"),
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

func TestAppSettings_GetShowsTheEffectiveArtifactsDir(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	workPath, _ := bothConfigs(t, workDir, homeDir)

	rec := doJSON(t, s, http.MethodGet, "/api/settings/app", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got appSettingsResponse
	mustDecode(t, rec, &got)

	if want := filepath.Join(homeDir, "home-artifacts"); got.File.ArtifactsDir != want {
		t.Errorf("file.artifactsDir = %q, want the home config's %q", got.File.ArtifactsDir, want)
	}
	if want := filepath.Join(workDir, "repo.db"); got.File.DBPath != want {
		t.Errorf("file.dbPath = %q, want the working-directory config's %q", got.File.DBPath, want)
	}
	if got.ConfigPath != workPath {
		t.Errorf("config_path = %q, want %q", got.ConfigPath, workPath)
	}
	if want := runtimeconfig.HomeConfigPath(homeDir); got.HomeConfigPath != want {
		t.Errorf("home_config_path = %q, want %q", got.HomeConfigPath, want)
	}
}

func TestAppSettings_PutSplitsSavesByKey(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	workPath, homePath := bothConfigs(t, workDir, homeDir)
	newArtifacts := filepath.Join(workDir, "ui-artifacts")
	newDB := filepath.Join(workDir, "ui.db")

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", appSettingsBody(newArtifacts, newDB))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got appSettingsResponse
	mustDecode(t, rec, &got)

	// artifactsDir -> the home config, and only there.
	if onDisk := readConfigJSON(t, homePath); onDisk.ArtifactsDir != newArtifacts {
		t.Errorf("home artifactsDir = %q, want %q", onDisk.ArtifactsDir, newArtifacts)
	}
	// The repository's own file is not rewritten -- its stale artifactsDir
	// stays, ignored, and its dbPath takes the new value.
	onWork := readConfigJSON(t, workPath)
	if want := filepath.Join(workDir, "repo-artifacts"); onWork.ArtifactsDir != want {
		t.Errorf("working-directory artifactsDir = %q, want it untouched at %q", onWork.ArtifactsDir, want)
	}
	if onWork.DBPath != newDB {
		t.Errorf("working-directory dbPath = %q, want %q", onWork.DBPath, newDB)
	}
	// dbPath must not have leaked into the home config.
	if onHome := readConfigJSON(t, homePath); onHome.DBPath != "" {
		t.Errorf("home dbPath = %q, want empty (only home-only keys go there)", onHome.DBPath)
	}

	// The response describes what actually happened, on both files.
	if got.ConfigPath != workPath || got.HomeConfigPath != homePath {
		t.Errorf("config_path/home_config_path = %q/%q, want %q/%q", got.ConfigPath, got.HomeConfigPath, workPath, homePath)
	}
	if got.File.ArtifactsDir != newArtifacts || got.File.DBPath != newDB {
		t.Errorf("response file must show the merged result, got %+v", got.File)
	}
	if len(got.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", got.Warnings)
	}
}

// TestAppSettings_PutThenGetKeepsTheNewArtifactsDir: the value has to come
// back out of the place it was put, through the same merge a restart does.
func TestAppSettings_PutThenGetKeepsTheNewArtifactsDir(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	bothConfigs(t, workDir, homeDir)
	newArtifacts := filepath.Join(workDir, "ui-artifacts")

	if rec := doJSON(t, s, http.MethodPut, "/api/settings/app",
		appSettingsBody(newArtifacts, filepath.Join(workDir, "repo.db"))); rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, s, http.MethodGet, "/api/settings/app", nil)
	var got appSettingsResponse
	mustDecode(t, rec, &got)
	if got.File.ArtifactsDir != newArtifacts {
		t.Errorf("file.artifactsDir = %q, want %q", got.File.ArtifactsDir, newArtifacts)
	}

	// And the merge a restart performs agrees.
	eff, err := runtimeconfig.LoadEffective(workDir, homeDir)
	if err != nil {
		t.Fatalf("LoadEffective: %v", err)
	}
	if eff.Config.ArtifactsDir != newArtifacts {
		t.Errorf("effective artifactsDir after restart = %q, want %q", eff.Config.ArtifactsDir, newArtifacts)
	}
}

// TestAppSettings_PutWithoutAWorkingDirConfigTargetsOneFile: in the ordinary
// setup both stages write the home config, and the response says so.
func TestAppSettings_PutWithoutAWorkingDirConfigTargetsOneFile(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	homePath := runtimeconfig.HomeConfigPath(homeDir)
	newArtifacts := filepath.Join(workDir, "ui-artifacts")
	newDB := filepath.Join(workDir, "ui.db")

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", appSettingsBody(newArtifacts, newDB))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got appSettingsResponse
	mustDecode(t, rec, &got)

	onDisk := readConfigJSON(t, homePath)
	if onDisk.ArtifactsDir != newArtifacts || onDisk.DBPath != newDB {
		t.Errorf("home config = %+v, want artifactsDir=%q dbPath=%q", onDisk, newArtifacts, newDB)
	}
	if got.ConfigPath != homePath || got.HomeConfigPath != homePath {
		t.Errorf("config_path/home_config_path = %q/%q, want both %q", got.ConfigPath, got.HomeConfigPath, homePath)
	}
	if _, err := os.Stat(filepath.Join(workDir, "graph-config.json")); !os.IsNotExist(err) {
		t.Errorf("no working-directory config may be created, stat err = %v", err)
	}
}

// TestAppSettings_PutWithoutAHomeDirSkipsOnlyArtifactsDir: an unresolvable
// home directory must not block editing everything else, and must not put
// artifactsDir somewhere it would be ignored -- so the request succeeds,
// minus that one field, and says which one.
func TestAppSettings_PutWithoutAHomeDirSkipsOnlyArtifactsDir(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	workPath, _ := bothConfigs(t, workDir, homeDir)
	s.cfg.HomeDir = ""
	newDB := filepath.Join(workDir, "ui.db")

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app",
		appSettingsBody(filepath.Join(workDir, "ui-artifacts"), newDB))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got appSettingsResponse
	mustDecode(t, rec, &got)

	onWork := readConfigJSON(t, workPath)
	if onWork.DBPath != newDB {
		t.Errorf("dbPath = %q, want %q -- every other field must still save", onWork.DBPath, newDB)
	}
	if want := filepath.Join(workDir, "repo-artifacts"); onWork.ArtifactsDir != want {
		t.Errorf("artifactsDir = %q, want it untouched at %q", onWork.ArtifactsDir, want)
	}
	if got.HomeConfigPath != "" {
		t.Errorf("home_config_path = %q, want empty", got.HomeConfigPath)
	}
	if !slices.Contains(got.Warnings, warnHomeConfigUnavailable) {
		t.Errorf("warnings = %v, want it to contain %q", got.Warnings, warnHomeConfigUnavailable)
	}
}

// TestAppSettings_PutKeepsTheResentMySQLPasswordAcrossBothSaves is a
// non-regression check on the split: the secret-resend rule runs in the
// first stage, against the file that actually holds the secret, and the
// second stage must neither disturb it nor copy it into the home config.
func TestAppSettings_PutKeepsTheResentMySQLPasswordAcrossBothSaves(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	workPath := filepath.Join(workDir, "graph-config.json")
	homePath := runtimeconfig.HomeConfigPath(homeDir)
	writeConfigJSON(t, workPath, runtimeconfig.FileConfig{
		DBBackend:     "mysql",
		MySQLHost:     "db.example.com",
		MySQLPort:     3306,
		MySQLDatabase: "graph",
		MySQLUser:     "app",
		MySQLPassword: "s3cret",
	})
	writeConfigJSON(t, homePath, runtimeconfig.FileConfig{ArtifactsDir: filepath.Join(homeDir, "home-artifacts")})

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{
		"dbBackend":          "mysql",
		"mysqlHost":          "db.example.com",
		"mysqlPort":          3306,
		"mysqlDatabase":      "graph",
		"mysqlUser":          "app",
		"mysqlPassword":      runtimeconfig.RedactedSecretPlaceholder,
		"artifactsDir":       filepath.Join(workDir, "ui-artifacts"),
		"paginationPageSize": 10,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got appSettingsResponse
	mustDecode(t, rec, &got)

	if onWork := readConfigJSON(t, workPath); onWork.MySQLPassword != "s3cret" {
		t.Errorf("stored mysqlPassword = %q, want it preserved", onWork.MySQLPassword)
	}
	if onHome := readConfigJSON(t, homePath); onHome.MySQLPassword != "" {
		t.Errorf("home mysqlPassword = %q, want empty -- secrets must not be copied across files", onHome.MySQLPassword)
	}
	if got.File.MySQLPassword != runtimeconfig.RedactedSecretPlaceholder {
		t.Errorf("response mysqlPassword = %q, want the placeholder", got.File.MySQLPassword)
	}
}
