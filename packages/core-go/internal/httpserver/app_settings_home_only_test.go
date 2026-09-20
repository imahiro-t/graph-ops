package httpserver

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
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

// captureLogs points the server's logger at a buffer and returns a function
// that reads what has been written so far. The same shape projects_test.go
// uses: the handler runs on this goroutine, but slog's own writes are not
// otherwise synchronized with the read.
func captureLogs(s *Server) func() string {
	var buf bytes.Buffer
	var mu sync.Mutex
	s.logger = slog.New(slog.NewTextHandler(&lockedWriter{w: &buf, mu: &mu}, nil))
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

// TestAppSettings_HomeSaveFailureIsLoggedAndNamed covers the half-done state
// the two-stage save can leave behind (non-functional review NF-1): every
// other setting is on disk under its new value while artifactsDir is not.
//
// writeError does not log 5xx responses and the Web UI shows a message
// chosen by the error CODE alone, so without both halves of this the user is
// told "something went wrong, try again later" about a save that did land,
// and nothing anywhere records what actually happened. The existing
// precedent is handleCreateProject's PROJECT_CREATED_LOCAL_PATH_NOT_SAVED.
func TestAppSettings_HomeSaveFailureIsLoggedAndNamed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based write failure cannot be simulated as root")
	}
	s, workDir, homeDir := newAppSettingsTestServer(t)
	workPath, homePath := bothConfigs(t, workDir, homeDir)
	logs := captureLogs(s)
	// Saves go through a temp file plus rename in the config file's own
	// directory, so a read-only directory fails the write while leaving the
	// file itself perfectly readable -- the second stage fails, the first
	// one has already succeeded.
	homeConfigDir := filepath.Dir(homePath)
	if err := os.Chmod(homeConfigDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(homeConfigDir, 0o700) })
	newDB := filepath.Join(workDir, "ui.db")

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app",
		appSettingsBody(filepath.Join(workDir, "ui-artifacts"), newDB))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("PUT expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if got, want := decodeError(t, rec).Code, domain.ErrCodeAppSettingsArtifactsDirNotSaved; got != want {
		t.Errorf("error code = %q, want %q -- INTERNAL_ERROR would tell the user nothing was saved", got, want)
	}
	// The first stage really did land, which is exactly why the generic
	// code would be wrong.
	if onWork := readConfigJSON(t, workPath); onWork.DBPath != newDB {
		t.Errorf("dbPath = %q, want %q -- the first stage must still have been saved", onWork.DBPath, newDB)
	}
	got := logs()
	for _, want := range []string{"app_settings_artifacts_dir_save_failed", workPath, homePath} {
		if !strings.Contains(got, want) {
			t.Errorf("log must mention %q, got %q", want, got)
		}
	}
}

// TestAppSettings_UnreadableHomeConfigIsVisibleInTheAPI covers NF-2: a home
// config that cannot be parsed is reported on stderr by the CLI, but someone
// who only ever opens the Web UI never sees that -- and the page would
// otherwise show an empty artifacts directory, with a save that fails
// identically every time and no way to find out why.
//
// The two sides are deliberately not symmetrical in kind: a GET can still
// answer (200 plus a warning), a PUT cannot, because overwriting a file it
// failed to parse would throw away whatever the user has in there. They are
// symmetrical in NAME, so the UI looks one condition up either way.
func TestAppSettings_UnreadableHomeConfigIsVisibleInTheAPI(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	workPath, homePath := bothConfigs(t, workDir, homeDir)
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

	newDB := filepath.Join(workDir, "ui.db")
	rec = doJSON(t, s, http.MethodPut, "/api/settings/app",
		appSettingsBody(filepath.Join(workDir, "ui-artifacts"), newDB))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("PUT expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if code, want := decodeError(t, rec).Code, domain.ErrCodeHomeConfigUnreadable; code != want {
		t.Errorf("error code = %q, want %q", code, want)
	}
	if onWork := readConfigJSON(t, workPath); onWork.DBPath != newDB {
		t.Errorf("dbPath = %q, want %q -- the rest of the save still lands", onWork.DBPath, newDB)
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
}

// TestAppSettings_NeverTouchesTheExecutionSettings is the regression test for
// security review S-5. This endpoint writes to the home config now -- the one
// file the home-only keys are trusted from -- so what it may put there is
// worth pinning: artifactsDir, and nothing else. terminalCommand and
// claudeBinary end up on a shell command line when "Launch Claude" is
// pressed, and host decides who can reach this unauthenticated API; none of
// the three has an editor in this UI, and an unauthenticated local API must
// not become the way they get set.
//
// The same test covers S-4 on the way out: the three are dropped from the
// response rather than being shown to a client that has no use for them.
func TestAppSettings_NeverTouchesTheExecutionSettings(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	workPath := filepath.Join(workDir, "graph-config.json")
	homePath := runtimeconfig.HomeConfigPath(homeDir)
	writeConfigJSON(t, workPath, runtimeconfig.FileConfig{DBPath: filepath.Join(workDir, "repo.db")})
	writeConfigJSON(t, homePath, runtimeconfig.FileConfig{
		TerminalCommand: "open -a Terminal {cwd}",
		ClaudeBinary:    "/usr/local/bin/claude",
		Host:            "127.0.0.1",
		ArtifactsDir:    filepath.Join(homeDir, "home-artifacts"),
	})

	// The body carries all three as well, so this pins that they are ignored
	// on the way in and not merely absent from the handler's struct.
	body := appSettingsBody(filepath.Join(workDir, "ui-artifacts"), filepath.Join(workDir, "ui.db"))
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
	if want := filepath.Join(workDir, "ui-artifacts"); onHome.ArtifactsDir != want {
		t.Errorf("home artifactsDir = %q, want %q", onHome.ArtifactsDir, want)
	}
	onWork := readConfigJSON(t, workPath)
	if onWork.TerminalCommand != "" || onWork.ClaudeBinary != "" || onWork.Host != "" {
		t.Errorf("nor may they be written to the working-directory config, got %+v", onWork)
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

// TestAppSettings_SavingDoesNotSilenceTheStartupWarning pins the consequence
// the README has to explain (QA review, minor 2): the working-directory
// config is not rewritten, so the keys it carries are still there, and the
// next startup still warns about them. "I fixed it in the UI and the warning
// is still there" is correct behaviour, not a bug -- the value in that file
// really is still being ignored.
func TestAppSettings_SavingDoesNotSilenceTheStartupWarning(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	workPath := filepath.Join(workDir, "graph-config.json")
	writeConfigJSON(t, workPath, runtimeconfig.FileConfig{
		ArtifactsDir: filepath.Join(workDir, "repo-artifacts"),
		Host:         "0.0.0.0",
		DBPath:       filepath.Join(workDir, "repo.db"),
	})

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app",
		appSettingsBody(filepath.Join(workDir, "ui-artifacts"), filepath.Join(workDir, "ui.db")))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	eff, err := runtimeconfig.LoadEffective(workDir, homeDir)
	if err != nil {
		t.Fatalf("LoadEffective: %v", err)
	}
	if want := []string{"host", "artifactsDir"}; !slices.Equal(eff.IgnoredKeys, want) {
		t.Errorf("IgnoredKeys after a save = %v, want %v -- the repository's file is left alone", eff.IgnoredKeys, want)
	}
	if want := filepath.Join(workDir, "ui-artifacts"); eff.Config.ArtifactsDir != want {
		t.Errorf("effective artifactsDir = %q, want the saved %q", eff.Config.ArtifactsDir, want)
	}
}
