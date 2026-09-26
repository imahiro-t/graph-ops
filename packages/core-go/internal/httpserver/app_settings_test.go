package httpserver

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
)

// newAppSettingsTestServer is newTestServer, but with cfg.WorkDir and
// cfg.HomeDir both pinned to fresh temp dirs. The home config file's path is
// derived from cfg.HomeDir alone (see internal/runtimeconfig.HomeConfigPath);
// leaving it at its zero value ("") would fall through to the *real*
// $HOME/.graph-ops/config.json, letting these tests read or (worse)
// overwrite the actual developer's own settings file. cfg.WorkDir is pinned
// too, even though nothing in the app-settings API reads it any more
// (DFLT-00124), so that a test can never be affected by the directory `go
// test` happened to run in.
// seedHomeConfig writes cfg to $HOME/.graph-ops/config.json wholesale -- the
// "this is what is already saved" setup most of these tests start from. It
// goes through UpdateHome because that is the only writer there is; the
// returned path is the file written.
func seedHomeConfig(homeDir string, cfg runtimeconfig.FileConfig) (string, error) {
	_, path, err := runtimeconfig.UpdateHome(homeDir, func(c *runtimeconfig.FileConfig) error {
		*c = cfg
		return nil
	})
	return path, err
}

func newAppSettingsTestServer(t *testing.T) (s *Server, workDir string, homeDir string) {
	t.Helper()
	repo, err := store.NewSQLiteRepository(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	if err := repo.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	eng := engine.New(repo)
	workDir = t.TempDir()
	homeDir = t.TempDir()
	cfg := Config{
		WorkDir:            workDir,
		HomeDir:            homeDir,
		DBPath:             filepath.Join(workDir, "graph.db"),
		ArtifactsDir:       filepath.Join(workDir, "artifacts"),
		UserExtensionsDir:  filepath.Join(workDir, "user-ext"),
		PaginationPageSize: 10,
	}
	return New(repo, eng, cfg), workDir, homeDir
}

func TestAppSettings_GetReflectsFileAndEffectiveValues(t *testing.T) {
	s, _, _ := newAppSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodGet, "/api/settings/app", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got appSettingsResponse
	mustDecode(t, rec, &got)

	// No the home config file exists yet -- every File field should be unset.
	if !reflect.DeepEqual(got.File, redactedFileConfig{}) {
		t.Errorf("expected empty file config before any save, got %+v", got.File)
	}
	// Effective must reflect this already-running server's own cfg, since
	// that's what's actually in effect regardless of what the file says.
	if got.Effective.DBPath != s.cfg.DBPath || got.Effective.ArtifactsDir != s.cfg.ArtifactsDir ||
		got.Effective.PaginationPageSize != s.cfg.PaginationPageSize {
		t.Errorf("expected effective to mirror server cfg, got %+v", got.Effective)
	}
}

func TestAppSettings_PutSavesThenGetReflectsIt(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	newDBPath := filepath.Join(workDir, "other.db")

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{
		"dbPath":             newDBPath,
		"artifactsDir":       filepath.Join(workDir, "other-artifacts"),
		"teamExtensionsDir":  filepath.Join(workDir, "team-ext"),
		"paginationPageSize": 25,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/settings/app", nil)
	var got appSettingsResponse
	mustDecode(t, rec, &got)
	if got.File.DBPath != newDBPath || got.File.PaginationPageSize != 25 ||
		got.File.TeamExtensionsDir != filepath.Join(workDir, "team-ext") {
		t.Errorf("expected saved file config to persist, got %+v", got.File)
	}

	// Persisted for real, not just echoed back -- reload straight from disk.
	onDisk, err := runtimeconfig.LoadHomeConfig(homeDir)
	if err != nil {
		t.Fatalf("runtimeconfig.LoadHomeConfig: %v", err)
	}
	if onDisk.DBPath != newDBPath || onDisk.PaginationPageSize != 25 {
		t.Errorf("expected the home config file on disk to contain the saved values, got %+v", onDisk)
	}

	// Nothing takes effect for the already-running process -- these fields
	// are only ever read once, at startup.
	if got.Effective.DBPath == newDBPath {
		t.Errorf("effective dbPath must not change without a restart, got %q", got.Effective.DBPath)
	}
}

func TestAppSettings_PutRejectsNonPositivePageSize(t *testing.T) {
	s, _, _ := newAppSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{
		"dbPath": "/tmp/x.db", "paginationPageSize": 0,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec).Code; got != "INVALID_PAGINATION_PAGE_SIZE" {
		t.Errorf("expected INVALID_PAGINATION_PAGE_SIZE, got %q", got)
	}
}

// TestAppSettings_GetDefaultsEffectiveDBBackendToSQLite covers the
// backward-compatibility completion criterion from the UI's perspective:
// a server started without cfg.DBBackend set (as every pre-DFLT-00020
// caller of httpserver.New still does) must report "sqlite", not "".
func TestAppSettings_GetDefaultsEffectiveDBBackendToSQLite(t *testing.T) {
	s, _, _ := newAppSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodGet, "/api/settings/app", nil)
	var got appSettingsResponse
	mustDecode(t, rec, &got)
	if got.Effective.DBBackend != "sqlite" {
		t.Errorf("expected effective.dbBackend %q, got %q", "sqlite", got.Effective.DBBackend)
	}
}

// TestAppSettings_PutSavesMySQLFields covers completion criterion: the app
// settings tab persists the MySQL connection fields (host/port/database/
// user/password) alongside dbBackend, and -- this ticket's (DFLT-00037) own
// addition -- the TLS mode/CA file fields, which are not secrets and so are
// never redacted the way mysqlPassword is (see newRedactedFileConfig).
func TestAppSettings_PutSavesMySQLFields(t *testing.T) {
	s, _, homeDir := newAppSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{
		"dbBackend": "mysql", "mysqlHost": "db.example.com", "mysqlPort": 3307,
		"mysqlDatabase": "graph_ops", "mysqlUser": "app", "mysqlPassword": "${SOME_ENV_VAR}",
		"mysqlTls": "verify-ca", "mysqlTlsCa": "/etc/mysql/ca.pem",
		"paginationPageSize": 10,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got appSettingsResponse
	mustDecode(t, rec, &got)
	if got.File.DBBackend != "mysql" || got.File.MySQLHost != "db.example.com" || got.File.MySQLPort != 3307 ||
		got.File.MySQLDatabase != "graph_ops" || got.File.MySQLUser != "app" || got.File.MySQLPassword != "${SOME_ENV_VAR}" ||
		got.File.MySQLTLS != "verify-ca" || got.File.MySQLTLSCA != "/etc/mysql/ca.pem" {
		t.Errorf("expected mysql fields to round-trip in the response, got %+v", got.File)
	}

	onDisk, err := runtimeconfig.LoadHomeConfig(homeDir)
	if err != nil {
		t.Fatalf("runtimeconfig.LoadHomeConfig: %v", err)
	}
	if onDisk.DBBackend != "mysql" || onDisk.MySQLHost != "db.example.com" || onDisk.MySQLPassword != "${SOME_ENV_VAR}" ||
		onDisk.MySQLTLS != "verify-ca" || onDisk.MySQLTLSCA != "/etc/mysql/ca.pem" {
		t.Errorf("expected mysql fields persisted to the home config file, got %+v", onDisk)
	}
}

// TestAppSettings_PutRejectsInvalidMySQLTLSMode covers this ticket's
// (DFLT-00037) D-4: an unsupported mysqlTls value (in particular
// go-sql-driver/mysql's own "preferred", which allows exactly the
// plaintext-fallback downgrade this ticket exists to close) must be
// rejected outright, not silently normalized to something safe.
func TestAppSettings_PutRejectsInvalidMySQLTLSMode(t *testing.T) {
	s, _, _ := newAppSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{
		"dbBackend": "mysql", "mysqlHost": "db.example.com", "mysqlDatabase": "graph_ops", "mysqlUser": "app",
		"mysqlTls": "preferred", "paginationPageSize": 10,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for mysqlTls=preferred, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAppSettings_PutRejectsVerifyCAWithoutCAFile covers the other half of
// D-4's validation: verify-ca with no CA file has nothing to pin trust to.
func TestAppSettings_PutRejectsVerifyCAWithoutCAFile(t *testing.T) {
	s, _, _ := newAppSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{
		"dbBackend": "mysql", "mysqlHost": "db.example.com", "mysqlDatabase": "graph_ops", "mysqlUser": "app",
		"mysqlTls": "verify-ca", "paginationPageSize": 10,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for mysqlTls=verify-ca with no mysqlTlsCa, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAppSettings_PutIgnoresInvalidMySQLTLSModeWhenBackendIsSQLite mirrors
// D-4's sqlite carve-out: a leftover invalid mysqlTls (e.g. left over from
// a past mysql experiment) must not block saving while dbBackend is
// "sqlite" -- the same rule loadRuntimeConfig applies at startup.
func TestAppSettings_PutIgnoresInvalidMySQLTLSModeWhenBackendIsSQLite(t *testing.T) {
	s, _, _ := newAppSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{
		"dbBackend": "sqlite", "mysqlTls": "preferred", "paginationPageSize": 10,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for an unvalidated leftover mysqlTls under sqlite, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAppSettings_PutRejectsMySQLBackendMissingRequiredFields covers the
// Gherkin scenario "MySQL選択時にホスト/DB名/ユーザー名が未入力だと接続テスト
// も保存もできない": saving with dbBackend=mysql but a required field blank
// must be rejected, not silently saved with empty connection details.
func TestAppSettings_PutRejectsMySQLBackendMissingRequiredFields(t *testing.T) {
	s, _, _ := newAppSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{
		"dbBackend": "mysql", "mysqlHost": "", "mysqlDatabase": "graph_ops", "mysqlUser": "app",
		"paginationPageSize": 10,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a missing mysqlHost, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAppSettings_PutRejectsUnsupportedDBBackend covers the completion
// criterion that only "sqlite"/"mysql" are valid dbBackend values.
func TestAppSettings_PutRejectsUnsupportedDBBackend(t *testing.T) {
	s, _, _ := newAppSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{
		"dbBackend": "postgres", "paginationPageSize": 10,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unsupported dbBackend, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestTestMySQLConnection_RejectsMissingRequiredFields covers the
// connection-test endpoint's own validation, independent of
// handlePutAppSettings's (the test button must work, and refuse to run,
// against whatever is currently typed into the form -- see the Gherkin
// scenario referenced above).
func TestTestMySQLConnection_RejectsMissingRequiredFields(t *testing.T) {
	s, _, _ := newAppSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPost, "/api/settings/app/test-mysql-connection", map[string]any{
		"mysqlHost": "", "mysqlDatabase": "graph_ops", "mysqlUser": "app",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a missing mysqlHost, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestTestMySQLConnection_RejectsInvalidMySQLTLSSettings covers this
// ticket's (DFLT-00037) D-4 for the connection-test endpoint: both an
// unsupported mysqlTls value and a verify-ca with no CA file must be
// rejected with 400 before anything is dialled.
func TestTestMySQLConnection_RejectsInvalidMySQLTLSSettings(t *testing.T) {
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"unsupported mode", map[string]any{"mysqlTls": "preferred"}},
		{"verify-ca without a CA file", map[string]any{"mysqlTls": "verify-ca"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newAppSettingsTestServer(t)
			body := map[string]any{"mysqlHost": "127.0.0.1", "mysqlDatabase": "graph_ops", "mysqlUser": "app"}
			for k, v := range tc.body {
				body[k] = v
			}
			rec := doJSON(t, s, http.MethodPost, "/api/settings/app/test-mysql-connection", body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestTestMySQLConnection_UnreachableHostReportsFailureNotHTTPError covers
// the Gherkin scenario "接続テストが失敗するとエラー内容が画面に表示される":
// a connection failure is a normal (200 OK, ok:false) response carrying the
// error text, not an HTTP-level error -- see handleTestMySQLConnection's
// doc comment for why. Port 1 on localhost is expected to refuse the
// connection immediately (nothing listens there) rather than hang until a
// timeout, keeping this test fast without a real MySQL server.
func TestTestMySQLConnection_UnreachableHostReportsFailureNotHTTPError(t *testing.T) {
	s, _, _ := newAppSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPost, "/api/settings/app/test-mysql-connection", map[string]any{
		"mysqlHost": "127.0.0.1", "mysqlPort": 1, "mysqlDatabase": "graph_ops", "mysqlUser": "app", "mysqlPassword": "x",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 even on connection failure, got %d: %s", rec.Code, rec.Body.String())
	}
	var got testMySQLConnectionResponse
	mustDecode(t, rec, &got)
	if got.Ok {
		t.Errorf("expected ok=false for an unreachable host, got %+v", got)
	}
	if got.Error == "" {
		t.Errorf("expected a non-empty error message")
	}
}

// TestTestMySQLConnection_MissingEnvVarPasswordReportsFailure covers the
// Gherkin scenario for a password env var reference that resolves to
// nothing: it must surface as a normal ok:false failure (not a 500 or a
// silent empty password), consistent with the "接続テストAPIはエラー
// メッセージにパスワードを含めない" scenario's spirit -- the raw
// "${...}" reference itself isn't a secret, so it's fine for the error to
// name the missing variable.
func TestTestMySQLConnection_MissingEnvVarPasswordReportsFailure(t *testing.T) {
	s, _, _ := newAppSettingsTestServer(t)

	rec := doJSON(t, s, http.MethodPost, "/api/settings/app/test-mysql-connection", map[string]any{
		"mysqlHost": "127.0.0.1", "mysqlDatabase": "graph_ops", "mysqlUser": "app",
		"mysqlPassword": "${GRAPH_OPS_TEST_DEFINITELY_UNSET_VAR_DFLT_00020}",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got testMySQLConnectionResponse
	mustDecode(t, rec, &got)
	if got.Ok {
		t.Errorf("expected ok=false when the referenced env var is unset, got %+v", got)
	}
}

func TestAppSettings_PutPreservesFieldsThisEndpointDoesNotOwn(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)

	if _, err := seedHomeConfig(homeDir, runtimeconfig.FileConfig{
		Port: 4001, ClaudeBinary: "my-claude", TerminalCommand: "custom {cwd} {command}",
	}); err != nil {
		t.Fatalf("seeding the home config: %v", err)
	}

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{
		"dbPath": filepath.Join(workDir, "x.db"), "paginationPageSize": 5,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	onDisk, err := runtimeconfig.LoadHomeConfig(homeDir)
	if err != nil {
		t.Fatalf("runtimeconfig.LoadHomeConfig: %v", err)
	}
	if onDisk.Port != 4001 || onDisk.ClaudeBinary != "my-claude" || onDisk.TerminalCommand != "custom {cwd} {command}" {
		t.Errorf("expected fields this endpoint doesn't own to survive untouched, got %+v", onDisk)
	}
	if onDisk.PaginationPageSize != 5 {
		t.Errorf("expected paginationPageSize to be updated, got %d", onDisk.PaginationPageSize)
	}
}

// TestAppSettings_GetNeverReturnsStoredPlaintextPassword is the regression
// test for security review art-86c49ea7: this endpoint needs no
// authentication and no CSRF header, so anything it returns must be assumed
// world-readable to whoever can reach the port. A stored plaintext MySQL
// password must therefore come back redacted, never verbatim.
func TestAppSettings_GetNeverReturnsStoredPlaintextPassword(t *testing.T) {
	s, _, homeDir := newAppSettingsTestServer(t)
	const secret = "super-secret-password"

	if _, err := seedHomeConfig(homeDir, runtimeconfig.FileConfig{
		DBBackend: "mysql", MySQLHost: "db.example.com", MySQLDatabase: "graph_ops",
		MySQLUser: "app", MySQLPassword: secret,
	}); err != nil {
		t.Fatalf("seeding the home config: %v", err)
	}

	rec := doJSON(t, s, http.MethodGet, "/api/settings/app", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// Check the raw body, not just the decoded struct: the point is that the
	// secret never appears on the wire at all, in any field.
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("GET /api/settings/app leaked the stored plaintext password in its response body")
	}
	var got appSettingsResponse
	mustDecode(t, rec, &got)
	if got.File.MySQLPassword != runtimeconfig.RedactedSecretPlaceholder {
		t.Errorf("expected the redaction placeholder, got %q", got.File.MySQLPassword)
	}
	// The rest of the connection settings are not secrets and must still be
	// editable, so they keep coming through untouched.
	if got.File.MySQLHost != "db.example.com" || got.File.MySQLUser != "app" {
		t.Errorf("expected non-secret mysql fields to survive redaction, got %+v", got.File)
	}
}

// TestRedactedFileConfig_MarshalsExactlyLikeFileConfig pins down the one
// thing that could have gone wrong when appSettingsResponse.File stopped
// being a runtimeconfig.FileConfig (S-4): the compiler now separates the
// redacted and raw forms, but clients must not be able to tell -- a defined
// type carries the underlying struct's json tags, and this fails loudly if
// the two ever drift apart (say, a field added to FileConfig that a
// hand-written redactedFileConfig would have missed).
func TestRedactedFileConfig_MarshalsExactlyLikeFileConfig(t *testing.T) {
	cfg := runtimeconfig.FileConfig{
		DBBackend: "mysql", DBPath: "/tmp/x.db", ArtifactsDir: "/tmp/artifacts",
		Port: 4001, ClaudeBinary: "my-claude", TerminalCommand: "custom {cwd} {command}",
		WorkDir: "/tmp/work", UserExtensionsDir: "/tmp/user", TeamExtensionsDir: "/tmp/team",
		PaginationPageSize: 25, MyName: "Ada", Host: "127.0.0.1",
		MySQLHost: "db.example.com", MySQLPort: 3307, MySQLDatabase: "graph_ops",
		MySQLUser: "app", MySQLPassword: runtimeconfig.RedactedSecretPlaceholder,
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal FileConfig: %v", err)
	}
	redacted, err := json.Marshal(redactedFileConfig(cfg))
	if err != nil {
		t.Fatalf("marshal redactedFileConfig: %v", err)
	}
	if string(raw) != string(redacted) {
		t.Errorf("the response shape changed:\n FileConfig: %s\n redactedFileConfig: %s", raw, redacted)
	}
}

// TestAppSettings_PutWithRedactedPasswordKeepsStoredOne covers the other
// half of the redaction contract: since the UI is never given the real
// password, saving an unrelated field submits the placeholder back, and that
// must preserve the stored password rather than overwrite it with the
// placeholder text.
//
// "Unrelated" now excludes the connection fields themselves -- editing
// those alongside the placeholder is the S-1 case, covered by
// TestAppSettings_PutRejectsRedactedPasswordForAChangedConnection below.
func TestAppSettings_PutWithRedactedPasswordKeepsStoredOne(t *testing.T) {
	s, _, homeDir := newAppSettingsTestServer(t)
	const secret = "super-secret-password"

	if _, err := seedHomeConfig(homeDir, runtimeconfig.FileConfig{
		DBBackend: "mysql", MySQLHost: "db.example.com", MySQLDatabase: "graph_ops",
		MySQLUser: "app", MySQLPassword: secret,
	}); err != nil {
		t.Fatalf("seeding the home config: %v", err)
	}

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{
		"dbBackend": "mysql", "mysqlHost": "db.example.com", "mysqlDatabase": "graph_ops",
		"mysqlUser": "app", "mysqlPassword": runtimeconfig.RedactedSecretPlaceholder,
		"paginationPageSize": 25,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	onDisk, err := runtimeconfig.LoadHomeConfig(homeDir)
	if err != nil {
		t.Fatalf("runtimeconfig.LoadHomeConfig: %v", err)
	}
	if onDisk.MySQLPassword != secret {
		t.Errorf("expected the stored password to survive a redacted PUT, got %q", onDisk.MySQLPassword)
	}
	if onDisk.PaginationPageSize != 25 {
		t.Errorf("expected the edited field to be saved, got %d", onDisk.PaginationPageSize)
	}
}

// mysqlSecretSeed is the stored state the two S-1 tests below share: a
// plaintext MySQL password saved against one specific connection. An unset
// MySQLPort is part of the point -- it has to compare equal to an explicit
// 3306 from a client, or the placeholder would stop working for the very
// configuration the settings UI produces by default.
func seedStoredMySQLSecret(t *testing.T, workDir, homeDir, secret string) {
	t.Helper()
	if _, err := seedHomeConfig(homeDir, runtimeconfig.FileConfig{
		DBBackend: "mysql", MySQLHost: "db.example.com", MySQLDatabase: "graph_ops",
		MySQLUser: "app", MySQLPassword: secret,
	}); err != nil {
		t.Fatalf("seeding the home config: %v", err)
	}
}

// TestAppSettings_PutRejectsRedactedPasswordForAChangedConnection is the
// regression test for security finding S-1 (art-991dfc4a), PUT half: the
// placeholder means "keep the password I was never shown", and honouring it
// while the request also moves the connection somewhere else would let
// anyone who can reach this port repoint the stored credential at a MySQL
// server they control -- persistently, without ever knowing the password.
// Each field of the destination tuple has to be checked, since any one of
// them is enough to hand the secret to a different party. The two mysqlTls*
// cases are this ticket's (DFLT-00037) own regression for finding F-6: with
// host/port/database/user left exactly as stored, weakening only the TLS
// mode (or swapping only the CA file) is just as much a hand-off of the
// secret to "however this new setting routes it" as changing the host would
// be, and must be refused the same way.
func TestAppSettings_PutRejectsRedactedPasswordForAChangedConnection(t *testing.T) {
	const secret = "super-secret-password"

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"a different host", map[string]any{"mysqlHost": "attacker.example.net"}},
		{"a different port", map[string]any{"mysqlPort": 3307}},
		{"a different database", map[string]any{"mysqlDatabase": "other_db"}},
		{"a different user", map[string]any{"mysqlUser": "root"}},
		{"a different TLS mode, F-6", map[string]any{"mysqlTls": "disabled"}},
		{"a different TLS CA file, F-6", map[string]any{"mysqlTls": "verify-ca", "mysqlTlsCa": "/tmp/attacker-ca.pem"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, workDir, homeDir := newAppSettingsTestServer(t)
			seedStoredMySQLSecret(t, workDir, homeDir, secret)

			body := map[string]any{
				"dbBackend": "mysql", "mysqlHost": "db.example.com", "mysqlDatabase": "graph_ops",
				"mysqlUser": "app", "mysqlPassword": runtimeconfig.RedactedSecretPlaceholder,
				"paginationPageSize": 10,
			}
			for k, v := range tc.body {
				body[k] = v
			}

			rec := doJSON(t, s, http.MethodPut, "/api/settings/app", body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
			if got := decodeError(t, rec).Code; got != "MYSQL_PASSWORD_RETYPE_REQUIRED" {
				t.Errorf("expected MYSQL_PASSWORD_RETYPE_REQUIRED, got %q", got)
			}
			// A rejected request must change nothing at all: the point is
			// that the secret does not end up attached to the new
			// destination, not merely that the response was a 400.
			onDisk, err := runtimeconfig.LoadHomeConfig(homeDir)
			if err != nil {
				t.Fatalf("runtimeconfig.LoadHomeConfig: %v", err)
			}
			if onDisk.MySQLPassword != secret || onDisk.MySQLHost != "db.example.com" ||
				onDisk.MySQLPort != 0 || onDisk.MySQLDatabase != "graph_ops" || onDisk.MySQLUser != "app" {
				t.Errorf("expected the stored connection to be left untouched, got %+v", onDisk)
			}
		})
	}
}

// TestTestMySQLConnection_RejectsRedactedPasswordForAChangedConnection is
// the same S-1 regression for the connection-test endpoint, which is the
// more direct exfiltration path of the two: it needs nothing saved, it just
// dials whatever host the body names. Before this ticket (DFLT-00037) added
// TLS support, store.MySQLDSN set up no TLS at all, so a hostile server on
// the other end could always recover the password it was handed; TLS
// support does not remove the need for this check (a hostile server the
// client's TLS mode does not verify is exactly as dangerous), which is why
// mysqlTarget compares TLS settings too (see that type's doc comment).
//
// Note that this is one of the few connection-test failures that is a real
// HTTP 400 rather than the endpoint's usual 200/ok:false -- the request was
// malformed (a password it could not resolve), so nothing was ever dialled.
func TestTestMySQLConnection_RejectsRedactedPasswordForAChangedConnection(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	seedStoredMySQLSecret(t, workDir, homeDir, "super-secret-password")

	rec := doJSON(t, s, http.MethodPost, "/api/settings/app/test-mysql-connection", map[string]any{
		"mysqlHost": "attacker.example.net", "mysqlDatabase": "graph_ops", "mysqlUser": "app",
		"mysqlPassword": runtimeconfig.RedactedSecretPlaceholder,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec).Code; got != "MYSQL_PASSWORD_RETYPE_REQUIRED" {
		t.Errorf("expected MYSQL_PASSWORD_RETYPE_REQUIRED, got %q", got)
	}
}

// TestTestMySQLConnection_ResolvesRedactedPasswordForTheSavedConnection is
// the allow-side of the same rule, and guards the port normalization: the
// settings UI always sends an explicit mysqlPort (its form defaults to
// 3306) even when the home config file has no port saved at all, so treating
// those two as different destinations would break the "接続テスト" button
// for the default MySQL configuration.
//
// The stored secret here is a plaintext password (submitted back as
// RedactedSecretPlaceholder, exactly what a redacted GET would have shown
// for it) -- TestTestMySQLConnection_ResolvesEnvVarReferenceForTheSavedConnection
// below is this same scenario's "${ENV_VAR}" counterpart, submitted as the
// reference text a redacted GET shows for that kind of stored secret.
func TestTestMySQLConnection_ResolvesRedactedPasswordForTheSavedConnection(t *testing.T) {
	s, _, homeDir := newAppSettingsTestServer(t)
	if _, err := seedHomeConfig(homeDir, runtimeconfig.FileConfig{
		DBBackend: "mysql", MySQLHost: "127.0.0.1", MySQLDatabase: "graph_ops",
		MySQLUser: "app", MySQLPassword: "super-secret-password",
	}); err != nil {
		t.Fatalf("seeding the home config: %v", err)
	}

	rec := doJSON(t, s, http.MethodPost, "/api/settings/app/test-mysql-connection", map[string]any{
		"mysqlHost": "127.0.0.1", "mysqlPort": 3306, "mysqlDatabase": "graph_ops", "mysqlUser": "app",
		"mysqlPassword": runtimeconfig.RedactedSecretPlaceholder,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("an unset port and an explicit 3306 must be the same destination, got %d: %s",
			rec.Code, rec.Body.String())
	}
	var got testMySQLConnectionResponse
	mustDecode(t, rec, &got)
	// No MySQL server is listening in a unit test, so this connects with the
	// right (resolved) password and still fails to dial -- what matters is
	// that it is *not* the MYSQL_PASSWORD_RETYPE_REQUIRED 400 the changed-
	// connection tests above assert, i.e. the unset-port/3306 pair was
	// correctly treated as the same destination.
	if got.Ok {
		t.Fatalf("expected the connection test to fail (nothing is listening), got ok=true")
	}
}

// TestTestMySQLConnection_ResolvesEnvVarReferenceForTheSavedConnection is
// TestTestMySQLConnection_ResolvesRedactedPasswordForTheSavedConnection's
// "${ENV_VAR}" counterpart, and the main regression test for DFLT-00036:
// resending the exact reference text a redacted GET would have shown for a
// stored "${ENV_VAR}" secret must resolve to that env var, but only while
// the destination still names the connection it was saved for. Before
// DFLT-00036 this endpoint resolved a resent "${ENV_VAR}" reference for
// *any* destination, since the old check only ever recognized
// RedactedSecretPlaceholder as a resend.
func TestTestMySQLConnection_ResolvesEnvVarReferenceForTheSavedConnection(t *testing.T) {
	s, _, homeDir := newAppSettingsTestServer(t)

	if _, err := seedHomeConfig(homeDir, runtimeconfig.FileConfig{
		DBBackend: "mysql", MySQLHost: "127.0.0.1", MySQLDatabase: "graph_ops",
		MySQLUser: "app", MySQLPassword: "${GRAPH_TEST_MYSQL_PASSWORD_UNSET}",
	}); err != nil {
		t.Fatalf("seeding the home config: %v", err)
	}

	rec := doJSON(t, s, http.MethodPost, "/api/settings/app/test-mysql-connection", map[string]any{
		"mysqlHost": "127.0.0.1", "mysqlPort": 3306, "mysqlDatabase": "graph_ops", "mysqlUser": "app",
		"mysqlPassword": "${GRAPH_TEST_MYSQL_PASSWORD_UNSET}",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got testMySQLConnectionResponse
	mustDecode(t, rec, &got)
	if !strings.Contains(got.Error, "GRAPH_TEST_MYSQL_PASSWORD_UNSET") {
		t.Errorf("expected the stored ${ENV_VAR} reference to have been resolved, got %q", got.Error)
	}
}

// TestTestMySQLConnection_RejectsEnvVarReferenceForAChangedConnection is the
// DFLT-00036 regression for the vulnerable path itself: before this fix, a
// stored "${ENV_VAR}" reference resent unchanged was resolved and dialled
// against *any* destination the request named, letting a caller repoint the
// destination at a server they control and have this process deliver the
// referenced environment variable's value to it.
func TestTestMySQLConnection_RejectsEnvVarReferenceForAChangedConnection(t *testing.T) {
	s, _, homeDir := newAppSettingsTestServer(t)

	if _, err := seedHomeConfig(homeDir, runtimeconfig.FileConfig{
		DBBackend: "mysql", MySQLHost: "db.example.com", MySQLDatabase: "graph_ops",
		MySQLUser: "app", MySQLPassword: "${GRAPH_MYSQL_PASSWORD_PROD}",
	}); err != nil {
		t.Fatalf("seeding the home config: %v", err)
	}

	rec := doJSON(t, s, http.MethodPost, "/api/settings/app/test-mysql-connection", map[string]any{
		"mysqlHost": "attacker.example.net", "mysqlDatabase": "graph_ops", "mysqlUser": "app",
		"mysqlPassword": "${GRAPH_MYSQL_PASSWORD_PROD}",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec).Code; got != "MYSQL_PASSWORD_RETYPE_REQUIRED" {
		t.Errorf("expected MYSQL_PASSWORD_RETYPE_REQUIRED, got %q", got)
	}
}

// TestAppSettings_PutRejectsEnvVarReferenceForAChangedConnection is the PUT
// half of the same DFLT-00036 regression, paired with
// TestAppSettings_PutRejectsRedactedPasswordForAChangedConnection above: a
// stored "${ENV_VAR}" reference resent alongside a changed host/port/
// database/user must be rejected (and change nothing on disk), exactly like
// a resent plaintext-password placeholder already was.
func TestAppSettings_PutRejectsEnvVarReferenceForAChangedConnection(t *testing.T) {
	const envRef = "${GRAPH_MYSQL_PASSWORD_PROD}"
	s, _, homeDir := newAppSettingsTestServer(t)
	if _, err := seedHomeConfig(homeDir, runtimeconfig.FileConfig{
		DBBackend: "mysql", MySQLHost: "db.example.com", MySQLDatabase: "graph_ops",
		MySQLUser: "app", MySQLPassword: envRef,
	}); err != nil {
		t.Fatalf("seeding the home config: %v", err)
	}

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{
		"dbBackend": "mysql", "mysqlHost": "attacker.example.net", "mysqlDatabase": "graph_ops",
		"mysqlUser": "app", "mysqlPassword": envRef, "paginationPageSize": 10,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec).Code; got != "MYSQL_PASSWORD_RETYPE_REQUIRED" {
		t.Errorf("expected MYSQL_PASSWORD_RETYPE_REQUIRED, got %q", got)
	}
	onDisk, err := runtimeconfig.LoadHomeConfig(homeDir)
	if err != nil {
		t.Fatalf("runtimeconfig.LoadHomeConfig: %v", err)
	}
	if onDisk.MySQLPassword != envRef || onDisk.MySQLHost != "db.example.com" {
		t.Errorf("expected the stored connection to be left untouched, got %+v", onDisk)
	}
}

// TestAppSettings_PutWithNewEnvVarReferenceForAChangedConnectionSucceeds is
// the regression the plan requires alongside the rejection above: entering a
// brand-new "${NEW_VAR}" reference the user has never seen back from a GET
// must keep working even when the connection changes in the same request --
// it does not match RedactSecret(stored), so it is never treated as a resend
// of the old secret. This is exactly the legitimate "register a new server"
// operation the plan calls out as something a naive fix must not break.
func TestAppSettings_PutWithNewEnvVarReferenceForAChangedConnectionSucceeds(t *testing.T) {
	s, _, homeDir := newAppSettingsTestServer(t)
	if _, err := seedHomeConfig(homeDir, runtimeconfig.FileConfig{
		DBBackend: "mysql", MySQLHost: "db.example.com", MySQLDatabase: "graph_ops",
		MySQLUser: "app", MySQLPassword: "${GRAPH_MYSQL_PASSWORD_PROD}",
	}); err != nil {
		t.Fatalf("seeding the home config: %v", err)
	}

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{
		"dbBackend": "mysql", "mysqlHost": "new-server.example.com", "mysqlDatabase": "graph_ops",
		"mysqlUser": "app", "mysqlPassword": "${GRAPH_MYSQL_PASSWORD_NEW}", "paginationPageSize": 10,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("a brand-new ${VAR} reference for a new connection must not be blocked, got %d: %s",
			rec.Code, rec.Body.String())
	}
	onDisk, err := runtimeconfig.LoadHomeConfig(homeDir)
	if err != nil {
		t.Fatalf("runtimeconfig.LoadHomeConfig: %v", err)
	}
	if onDisk.MySQLPassword != "${GRAPH_MYSQL_PASSWORD_NEW}" || onDisk.MySQLHost != "new-server.example.com" {
		t.Errorf("expected the new connection and reference to be saved, got %+v", onDisk)
	}
}

// TestAppSettings_EnvVarReferenceReuseAfterConnectionChangeTwoStepWorkaround
// checks the two-step workaround the plan and README document for reusing
// the *same* "${ENV_VAR}" reference against a new destination (there is no
// "retype" a client can perform for a reference -- the text is the same
// text either way): first move the connection while clearing the password,
// then, once that new connection is the stored one, resend the same
// reference and have it accepted.
func TestAppSettings_EnvVarReferenceReuseAfterConnectionChangeTwoStepWorkaround(t *testing.T) {
	const envRef = "${GRAPH_MYSQL_PASSWORD_SHARED}"
	s, _, homeDir := newAppSettingsTestServer(t)
	if _, err := seedHomeConfig(homeDir, runtimeconfig.FileConfig{
		DBBackend: "mysql", MySQLHost: "old-server.example.com", MySQLDatabase: "graph_ops",
		MySQLUser: "app", MySQLPassword: envRef,
	}); err != nil {
		t.Fatalf("seeding the home config: %v", err)
	}

	// Step 1: move the connection while clearing the password (an empty
	// submission is never mistaken for a resend, per resolveSubmittedMySQLPassword).
	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{
		"dbBackend": "mysql", "mysqlHost": "new-server.example.com", "mysqlDatabase": "graph_ops",
		"mysqlUser": "app", "mysqlPassword": "", "paginationPageSize": 10,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("step 1 (clear password, move connection) expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Step 2: now that new-server.example.com is the stored connection,
	// resending the same reference for that same connection must succeed.
	rec = doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{
		"dbBackend": "mysql", "mysqlHost": "new-server.example.com", "mysqlDatabase": "graph_ops",
		"mysqlUser": "app", "mysqlPassword": envRef, "paginationPageSize": 10,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("step 2 (re-enter the reference for the now-current connection) expected 200, got %d: %s",
			rec.Code, rec.Body.String())
	}
	onDisk, err := runtimeconfig.LoadHomeConfig(homeDir)
	if err != nil {
		t.Fatalf("runtimeconfig.LoadHomeConfig: %v", err)
	}
	if onDisk.MySQLPassword != envRef || onDisk.MySQLHost != "new-server.example.com" {
		t.Errorf("expected the reference to be saved against the new connection, got %+v", onDisk)
	}
}

// TestAppSettings_PutWithNewPasswordReplacesStoredOne guards the opposite
// case: a value that is not the placeholder is a real edit and must be
// written through, including "" (clear the password).
func TestAppSettings_PutWithNewPasswordReplacesStoredOne(t *testing.T) {
	s, _, homeDir := newAppSettingsTestServer(t)

	if _, err := seedHomeConfig(homeDir, runtimeconfig.FileConfig{
		DBBackend: "mysql", MySQLHost: "db.example.com", MySQLDatabase: "graph_ops",
		MySQLUser: "app", MySQLPassword: "old-password",
	}); err != nil {
		t.Fatalf("seeding the home config: %v", err)
	}

	for _, tc := range []struct{ name, submitted string }{
		{"a new plaintext password", "new-password"},
		{"an env var reference", "${GRAPH_MYSQL_PASSWORD_PROD}"},
		{"an explicit clear", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{
				"dbBackend": "mysql", "mysqlHost": "db.example.com", "mysqlDatabase": "graph_ops",
				"mysqlUser": "app", "mysqlPassword": tc.submitted, "paginationPageSize": 10,
			})
			if rec.Code != http.StatusOK {
				t.Fatalf("PUT expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			onDisk, err := runtimeconfig.LoadHomeConfig(homeDir)
			if err != nil {
				t.Fatalf("runtimeconfig.LoadHomeConfig: %v", err)
			}
			if onDisk.MySQLPassword != tc.submitted {
				t.Errorf("expected %q to be written through, got %q", tc.submitted, onDisk.MySQLPassword)
			}
		})
	}
}
