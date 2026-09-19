package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/httpserver"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store/httpdatasourcetest"
)

// --- DFLT-00088: HTTP custom data source settings ---------------------------

func clearHTTPDataSourceEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GRAPH_DB_BACKEND", "")
	t.Setenv("GRAPH_HTTP_DATASOURCE_URL", "")
	t.Setenv("GRAPH_HTTP_DATASOURCE_TOKEN", "")
}

func TestLoadRuntimeConfig_HTTPDataSourceFromFile(t *testing.T) {
	dir := tempCwd(t)
	stubHome(t)
	clearHTTPDataSourceEnv(t)
	writeGraphConfig(t, dir, runtimeconfig.FileConfig{
		DBBackend: "http", HTTPDataSourceURL: "http://127.0.0.1:8787", HTTPDataSourceToken: "plain-token",
	})

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	sc := storeConfigFromRuntimeConfig(rc)
	if sc.Backend != "http" || sc.HTTPURL != "http://127.0.0.1:8787" || sc.HTTPToken != "plain-token" {
		t.Fatalf("store.Config = %+v", sc)
	}
}

func TestLoadRuntimeConfig_HTTPDataSourceTokenFromEnvVarRef(t *testing.T) {
	dir := tempCwd(t)
	stubHome(t)
	clearHTTPDataSourceEnv(t)
	t.Setenv("GRAPHOPS_DATASOURCE_TOKEN", "resolved-token")
	writeGraphConfig(t, dir, runtimeconfig.FileConfig{
		DBBackend: "http", HTTPDataSourceURL: "https://example.com", HTTPDataSourceToken: "${GRAPHOPS_DATASOURCE_TOKEN}",
	})

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if rc.HTTPDataSourceToken != "resolved-token" {
		t.Fatalf("HTTPDataSourceToken = %q, want resolved-token", rc.HTTPDataSourceToken)
	}
}

func TestLoadRuntimeConfig_HTTPDataSourceTokenMissingEnvVarFails(t *testing.T) {
	dir := tempCwd(t)
	stubHome(t)
	clearHTTPDataSourceEnv(t)
	writeGraphConfig(t, dir, runtimeconfig.FileConfig{
		DBBackend: "http", HTTPDataSourceURL: "https://example.com", HTTPDataSourceToken: "${GRAPH_OPS_TEST_UNSET_TOKEN_DFLT_00088}",
	})

	_, err := loadRuntimeConfig()
	if err == nil || !strings.Contains(err.Error(), "GRAPH_OPS_TEST_UNSET_TOKEN_DFLT_00088") || !strings.Contains(err.Error(), "not set") {
		t.Fatalf("err = %v, want an error naming the unset env var", err)
	}
}

func TestLoadRuntimeConfig_HTTPDataSourceEnvOverridesFile(t *testing.T) {
	dir := tempCwd(t)
	stubHome(t)
	clearHTTPDataSourceEnv(t)
	writeGraphConfig(t, dir, runtimeconfig.FileConfig{
		DBBackend: "http", HTTPDataSourceURL: "http://127.0.0.1:1111", HTTPDataSourceToken: "file-token",
	})
	t.Setenv("GRAPH_HTTP_DATASOURCE_URL", "http://127.0.0.1:2222")
	t.Setenv("GRAPH_HTTP_DATASOURCE_TOKEN", "env-token")

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if rc.HTTPDataSourceURL != "http://127.0.0.1:2222" || rc.HTTPDataSourceToken != "env-token" {
		t.Fatalf("got URL %q token %q, want the env values", rc.HTTPDataSourceURL, rc.HTTPDataSourceToken)
	}
}

func TestLoadRuntimeConfig_HTTPDataSourceInsecureSettingsFailAtStartup(t *testing.T) {
	cases := []struct {
		name, url, token, want string
	}{
		{"plaintext to a remote host", "http://example.com", "t", "plaintext http:// is only allowed for a loopback"},
		{"remote without a token", "https://example.com", "", "requires a bearer token"},
		{"no URL", "", "t", "httpDataSourceUrl is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := tempCwd(t)
			stubHome(t)
			clearHTTPDataSourceEnv(t)
			writeGraphConfig(t, dir, runtimeconfig.FileConfig{DBBackend: "http", HTTPDataSourceURL: tc.url, HTTPDataSourceToken: tc.token})
			_, err := loadRuntimeConfig()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestRun_HTTPDataSourceInsecureURLStopsAnySubcommand checks the scenario
// "graph-engine stops at startup, and no request is sent": run() (every
// subcommand's entry point) fails in loadRuntimeConfig before opening the
// store.
func TestRun_HTTPDataSourceInsecureURLStopsAnySubcommand(t *testing.T) {
	dir := tempCwd(t)
	stubHome(t)
	clearHTTPDataSourceEnv(t)
	writeGraphConfig(t, dir, runtimeconfig.FileConfig{DBBackend: "http", HTTPDataSourceURL: "http://example.com", HTTPDataSourceToken: "t"})
	for _, cmd := range []string{"list-projects", "list-tickets", "serve"} {
		err := run(cmd, nil)
		if err == nil || !strings.Contains(err.Error(), "plaintext http:// is only allowed for a loopback") {
			t.Fatalf("run(%s) = %v", cmd, err)
		}
	}
}

func TestLoadRuntimeConfig_LeftoverHTTPSettingsIgnoredUnderSQLite(t *testing.T) {
	dir := tempCwd(t)
	stubHome(t)
	clearHTTPDataSourceEnv(t)
	writeGraphConfig(t, dir, runtimeconfig.FileConfig{
		DBBackend: "sqlite", HTTPDataSourceURL: "http://example.com", HTTPDataSourceToken: "${GRAPH_OPS_TEST_UNSET_TOKEN_DFLT_00088}",
	})
	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig under sqlite must ignore http settings: %v", err)
	}
	if rc.DBBackend != "sqlite" || rc.HTTPDataSourceToken != "" {
		t.Fatalf("rc = %+v", rc)
	}
}

// TestCLIAndServe_BothUseTheHTTPBackend covers "CLI と Web UI サーバーの両方が
// http バックエンドを使う": a CLI subcommand (run -> loadRuntimeConfig ->
// openStore) creates a project and a ticket, and the Web UI server built on
// the same openStore path (exactly as cmdServe builds it) lists that ticket
// -- every one of those operations reaching the reference plugin.
func TestCLIAndServe_BothUseTheHTTPBackend(t *testing.T) {
	dir := tempCwd(t)
	stubHome(t)
	clearPathEnv(t)
	clearHTTPDataSourceEnv(t)

	plugin := httpdatasourcetest.New("cli-token")
	srv := httptest.NewServer(plugin)
	defer srv.Close()
	t.Setenv("GRAPH_TEST_HTTP_DS_TOKEN", "cli-token")
	writeGraphConfig(t, dir, runtimeconfig.FileConfig{
		DBBackend: "http", HTTPDataSourceURL: srv.URL, HTTPDataSourceToken: "${GRAPH_TEST_HTTP_DS_TOKEN}",
	})

	captureStdout(t, func() {
		if err := run("create-project", []string{"Remote", "--prefix", "REM"}); err != nil {
			t.Fatalf("create-project: %v", err)
		}
	})
	captureStdout(t, func() {
		if err := run("create-ticket", []string{"From the CLI", "body"}); err != nil {
			t.Fatalf("create-ticket: %v", err)
		}
	})
	var sawCreateTicket bool
	for _, req := range plugin.Requests() {
		if req.Method == "POST" && regexp.MustCompile(`^/projects/[^/]+/tickets$`).MatchString(req.Path) {
			sawCreateTicket = true
		}
	}
	if !sawCreateTicket {
		t.Fatalf("the CLI's create-ticket never reached the plugin: %+v", plugin.Requests())
	}

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	repo, err := openStore(rc)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	server := httpserver.New(repo, engine.New(repo), httpserver.Config{
		DBBackend: rc.DBBackend, HTTPDataSourceURL: rc.HTTPDataSourceURL, WorkDir: rc.WorkDir, HomeDir: rc.HomeDir,
	})
	plugin.ResetRequests()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/tickets?all=true", nil)
	rec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/tickets = %d: %s", rec.Code, rec.Body.String())
	}
	var tickets []domain.Ticket
	if err := json.Unmarshal(rec.Body.Bytes(), &tickets); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	if len(tickets) != 1 || tickets[0].Title != "From the CLI" || tickets[0].ID != "REM-00001" {
		t.Fatalf("Web UI server listed %+v", tickets)
	}
	var sawList bool
	for _, r := range plugin.Requests() {
		if r.Method == "GET" && r.Path == "/tickets" {
			sawList = true
		}
	}
	if !sawList {
		t.Fatalf("the server's ticket listing never reached the plugin: %+v", plugin.Requests())
	}
}

// TestNoRepositoryIsBuiltOutsideStoreOpen guards "store.Open を通らずに SQLite
// リポジトリを直接作る経路が存在しない": outside tests, no production source
// under cmd/ or internal/ (other than internal/store itself) constructs a
// backend directly.
func TestNoRepositoryIsBuiltOutsideStoreOpen(t *testing.T) {
	root := filepath.Join("..", "..")
	pattern := regexp.MustCompile(`New(SQLite|MySQL|HTTP)Repository\(`)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.Contains(filepath.ToSlash(path), "internal/store/") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if pattern.Match(raw) {
			t.Errorf("%s constructs a repository directly instead of going through store.Open", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
