package httpserver

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
)

// captureRejectLog replaces s's rejectLogger with one that writes to a fresh
// buffer instead of s's original logger, while keeping the same fingerprint
// key (see newRejectLogger) -- so a test that captures a server's log can
// still rely on host_fp being stable across requests made against that same
// server.
func captureRejectLog(t *testing.T, s *Server) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	s.rejectLog = &rejectLogger{
		logger: slog.New(slog.NewTextHandler(buf, nil)),
		fpKey:  s.rejectLog.fpKey,
	}
	return buf
}

// logLineCount reports how many log lines buf holds (slog's TextHandler
// writes exactly one trailing "\n" per record).
func logLineCount(buf *bytes.Buffer) int {
	s := strings.TrimRight(buf.String(), "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// attrValue extracts key's value from a logfmt-ish line as slog's
// TextHandler writes it (key=value, or key="quoted value").
func attrValue(t *testing.T, line, key string) string {
	t.Helper()
	re := regexp.MustCompile(key + `=("(?:[^"\\]|\\.)*"|\S+)`)
	m := re.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("key %q not found in log line: %s", key, line)
	}
	v := m[1]
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	return v
}

// assertNoneContain fails if line contains any of needles (case-sensitive)
// -- used to pin the "attacker-controlled input never reaches the log"
// masking policy (securitylog.go's doc comment).
func assertNoneContain(t *testing.T, line string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if strings.Contains(line, n) {
			t.Errorf("log line unexpectedly contains %q: %s", n, line)
		}
	}
}

// T1: HOST_NOT_ALLOWED produces exactly one masked log line.
func TestSecurityLog_HostNotAllowed(t *testing.T) {
	s, _, _ := newTestServer(t)
	buf := captureRejectLog(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if n := logLineCount(buf); n != 1 {
		t.Fatalf("expected exactly 1 log line, got %d: %s", n, buf.String())
	}
	line := buf.String()
	assertNoneContain(t, line, "evil.example.com", "evil")

	if got := attrValue(t, line, "level"); got != "WARN" {
		t.Errorf("level = %q, want WARN", got)
	}
	if got := attrValue(t, line, "msg"); got != "request rejected" {
		t.Errorf("msg = %q, want %q", got, "request rejected")
	}
	if got := attrValue(t, line, "event"); got != "security.reject" {
		t.Errorf("event = %q, want security.reject", got)
	}
	if got := attrValue(t, line, "code"); got != "HOST_NOT_ALLOWED" {
		t.Errorf("code = %q, want HOST_NOT_ALLOWED", got)
	}
	if got := attrValue(t, line, "status"); got != "403" {
		t.Errorf("status = %q, want 403", got)
	}
	if got := attrValue(t, line, "host_kind"); got != "name" {
		t.Errorf("host_kind = %q, want name", got)
	}
	if got := attrValue(t, line, "host_len"); got != "16" { // len("evil.example.com")
		t.Errorf("host_len = %q, want 16", got)
	}
	if fp := attrValue(t, line, "host_fp"); !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(fp) {
		t.Errorf("host_fp = %q, want 16 lowercase hex chars", fp)
	}
}

// T2: a Host header carrying a secret-looking token, or control characters
// aimed at forging a second log line, must never reach the log -- only its
// classification/length/fingerprint may. A real net/http server would 400
// a Host header containing control characters before this code ever runs;
// this test (driven straight at Routes(), bypassing net/http's own request
// line parsing) is a defense-in-depth check of this package's own masking,
// not a claim that such a Host reaches production from the network.
func TestSecurityLog_HostNotAllowedMasksInput(t *testing.T) {
	for _, tc := range []struct {
		name        string
		host        string
		wantKind    string
		mustNotHave []string
	}{
		{
			name:        "secret-looking token",
			host:        "tok-SECRET123.attacker.example",
			wantKind:    "name",
			mustNotHave: []string{"SECRET123", "attacker"},
		},
		{
			name:        "log injection attempt",
			host:        "a\nlevel=ERROR msg=forged",
			wantKind:    "malformed",
			mustNotHave: []string{"forged"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newTestServer(t)
			buf := captureRejectLog(t, s)

			req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			s.Routes().ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
			}
			if n := logLineCount(buf); n != 1 {
				t.Fatalf("expected exactly 1 log line (no forged second line), got %d: %s", n, buf.String())
			}
			line := buf.String()
			assertNoneContain(t, line, tc.mustNotHave...)
			if got := attrValue(t, line, "host_kind"); got != tc.wantKind {
				t.Errorf("host_kind = %q, want %q", got, tc.wantKind)
			}
		})
	}
}

// T3: host_fp correlates repeats of the same Host within one server/process,
// distinguishes different Hosts, and is not comparable across servers (a
// fresh fingerprint key per Server/process -- see newRejectLogger).
func TestSecurityLog_HostFingerprint(t *testing.T) {
	fpFor := func(s *Server, buf *bytes.Buffer, host string) string {
		t.Helper()
		buf.Reset()
		req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		s.Routes().ServeHTTP(rec, req)
		return attrValue(t, buf.String(), "host_fp")
	}

	s1, _, _ := newTestServer(t)
	buf1 := captureRejectLog(t, s1)

	fpA1 := fpFor(s1, buf1, "attacker.example.com")
	fpA2 := fpFor(s1, buf1, "attacker.example.com")
	if fpA1 != fpA2 {
		t.Errorf("same Host on the same server: host_fp differed (%q vs %q)", fpA1, fpA2)
	}

	fpB := fpFor(s1, buf1, "other.example.com")
	if fpB == fpA1 {
		t.Errorf("different Hosts on the same server produced the same host_fp %q", fpB)
	}

	s2, _, _ := newTestServer(t)
	buf2 := captureRejectLog(t, s2)
	fpA1OnS2 := fpFor(s2, buf2, "attacker.example.com")
	if fpA1OnS2 == fpA1 {
		t.Errorf("the same Host on a different server produced the same host_fp %q; keys must be per-process", fpA1OnS2)
	}
}

// T4: CSRF_HEADER_REQUIRED rejections are also masked and logged.
func TestSecurityLog_CSRFHeaderRequired(t *testing.T) {
	s, _, _ := newTestServer(t)
	buf := captureRejectLog(t, s)

	req := httptest.NewRequest(http.MethodPost, "/api/tickets", bytes.NewReader(nil))
	req.Host = testHost
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example.com")
	// Deliberately NOT setting csrfHeaderName.
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if n := logLineCount(buf); n != 1 {
		t.Fatalf("expected exactly 1 log line, got %d: %s", n, buf.String())
	}
	line := buf.String()
	assertNoneContain(t, line, "evil.example.com")

	if got := attrValue(t, line, "code"); got != "CSRF_HEADER_REQUIRED" {
		t.Errorf("code = %q, want CSRF_HEADER_REQUIRED", got)
	}
	if got := attrValue(t, line, "origin_kind"); got != "other" {
		t.Errorf("origin_kind = %q, want other", got)
	}
	if got := attrValue(t, line, "path_class"); got != "api" {
		t.Errorf("path_class = %q, want api", got)
	}
}

// T5: MYSQL_PASSWORD_RETYPE_REQUIRED rejections from PUT /api/settings/app
// are logged with only the changed field names -- never the submitted or
// stored connection details, host, port, database, user or password.
func TestSecurityLog_PasswordRetypeRequired_Put(t *testing.T) {
	const secret = "super-secret-password"

	for _, tc := range []struct {
		name        string
		body        map[string]any
		wantChanged string
	}{
		{"a different host", map[string]any{"mysqlHost": "attacker.example.net"}, "host"},
		{"a different port", map[string]any{"mysqlPort": 3307}, "port"},
		{"a different database", map[string]any{"mysqlDatabase": "other_db"}, "database"},
		{"a different user", map[string]any{"mysqlUser": "root"}, "user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, workDir, homeDir := newAppSettingsTestServer(t)
			seedStoredMySQLSecret(t, workDir, homeDir, secret)
			buf := captureRejectLog(t, s)

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
			if n := logLineCount(buf); n != 1 {
				t.Fatalf("expected exactly 1 log line, got %d: %s", n, buf.String())
			}
			line := buf.String()
			assertNoneContain(t, line, "attacker.example.net", "3307", "other_db", "root",
				secret, runtimeconfig.RedactedSecretPlaceholder, "db.example.com")

			if got := attrValue(t, line, "code"); got != "MYSQL_PASSWORD_RETYPE_REQUIRED" {
				t.Errorf("code = %q, want MYSQL_PASSWORD_RETYPE_REQUIRED", got)
			}
			if got := attrValue(t, line, "endpoint"); got != "PUT /api/settings/app" {
				t.Errorf("endpoint = %q, want %q", got, "PUT /api/settings/app")
			}
			if got := attrValue(t, line, "changed_fields"); got != tc.wantChanged {
				t.Errorf("changed_fields = %q, want %q", got, tc.wantChanged)
			}
		})
	}
}

// T6: the same rejection from the connection-test endpoint.
func TestSecurityLog_PasswordRetypeRequired_TestConnection(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	seedStoredMySQLSecret(t, workDir, homeDir, "super-secret-password")
	buf := captureRejectLog(t, s)

	rec := doJSON(t, s, http.MethodPost, "/api/settings/app/test-mysql-connection", map[string]any{
		"mysqlHost": "attacker.example.net", "mysqlDatabase": "graph_ops", "mysqlUser": "app",
		"mysqlPassword": runtimeconfig.RedactedSecretPlaceholder,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if n := logLineCount(buf); n != 1 {
		t.Fatalf("expected exactly 1 log line, got %d: %s", n, buf.String())
	}
	line := buf.String()
	assertNoneContain(t, line, "attacker.example.net", "super-secret-password",
		runtimeconfig.RedactedSecretPlaceholder, "db.example.com")

	if got := attrValue(t, line, "endpoint"); got != "POST /api/settings/app/test-mysql-connection" {
		t.Errorf("endpoint = %q, want %q", got, "POST /api/settings/app/test-mysql-connection")
	}
	if got := attrValue(t, line, "changed_fields"); got != "host" {
		t.Errorf("changed_fields = %q, want host", got)
	}
}

// T7: negative control -- allowed requests, and 400s outside the three
// tracked codes, must never write a reject-log line. Without this, a future
// change could start over-logging (e.g. every 400) unnoticed.
func TestSecurityLog_NoLogForAllowedOrUnrelatedRequests(t *testing.T) {
	t.Run("allowed GET", func(t *testing.T) {
		s, _, _ := newTestServer(t)
		buf := captureRejectLog(t, s)
		req := httptest.NewRequest(http.MethodGet, "/api/tickets", nil)
		req.Host = testHost
		rec := httptest.NewRecorder()
		s.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if n := logLineCount(buf); n != 0 {
			t.Fatalf("expected no log lines, got %d: %s", n, buf.String())
		}
	})

	t.Run("allowed POST with CSRF header", func(t *testing.T) {
		s, _, _ := newTestServer(t)
		buf := captureRejectLog(t, s)
		rec := doJSON(t, s, http.MethodPost, "/api/tickets", map[string]any{"title": "t"})
		if rec.Code == http.StatusForbidden {
			t.Fatalf("unexpected 403: %s", rec.Body.String())
		}
		if n := logLineCount(buf); n != 0 {
			t.Fatalf("expected no log lines, got %d: %s", n, buf.String())
		}
	})

	t.Run("placeholder for the unchanged connection", func(t *testing.T) {
		s, workDir, homeDir := newAppSettingsTestServer(t)
		seedStoredMySQLSecret(t, workDir, homeDir, "super-secret-password")
		buf := captureRejectLog(t, s)

		rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{
			"dbBackend": "mysql", "mysqlHost": "db.example.com", "mysqlDatabase": "graph_ops",
			"mysqlUser": "app", "mysqlPassword": runtimeconfig.RedactedSecretPlaceholder,
			"paginationPageSize": 10,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if n := logLineCount(buf); n != 0 {
			t.Fatalf("expected no log lines, got %d: %s", n, buf.String())
		}
	})

	t.Run("unrelated 400 (pagination page size)", func(t *testing.T) {
		s, _, _ := newAppSettingsTestServer(t)
		buf := captureRejectLog(t, s)

		rec := doJSON(t, s, http.MethodPut, "/api/settings/app", map[string]any{
			"paginationPageSize": 0,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
		if n := logLineCount(buf); n != 0 {
			t.Fatalf("expected no log lines for an unrelated 400, got %d: %s", n, buf.String())
		}
	})
}

// T8: table-driven coverage of the pure classification helpers.
func TestSecurityLog_SafeMethod(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{http.MethodGet, "GET"}, {http.MethodPost, "POST"}, {http.MethodPut, "PUT"},
		{http.MethodPatch, "PATCH"}, {http.MethodDelete, "DELETE"}, {http.MethodHead, "HEAD"},
		{http.MethodOptions, "OPTIONS"}, {"BREW", "OTHER"}, {"", "OTHER"},
	} {
		if got := safeMethod(tc.in); got != tc.want {
			t.Errorf("safeMethod(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSecurityLog_RemoteIP(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"192.0.2.1:1234", "192.0.2.1"},
		{"[::1]:5", "::1"},
		{"not-an-address", "unknown"},
		{"", "unknown"},
	} {
		if got := remoteIP(tc.in); got != tc.want {
			t.Errorf("remoteIP(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSecurityLog_PathClass(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/api/tickets", "api"},
		// DFLT-00103 removed the filesystem route this path used to reach;
		// it now falls through to the SPA handler like any other non-API path.
		{"/artifacts-static/foo.html", "web"},
		{"/", "web"},
		{"/some/other/path", "web"},
	} {
		if got := pathClass(tc.in); got != tc.want {
			t.Errorf("pathClass(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSecurityLog_HostKind(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "empty"},
		{"127.0.0.1:49173", "ip"},
		{"[::1]:49173", "ip"},
		{"evil.example.com", "name"},
		{"a\nlevel=ERROR", "malformed"},
		{"has space", "malformed"},
	} {
		if got := hostKind(tc.in); got != tc.want {
			t.Errorf("hostKind(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSecurityLog_OriginKind(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "none"},
		{"http://127.0.0.1:3000", "loopback"},
		{"https://evil.example.com", "other"},
		{"not a url", "other"},
	} {
		if got := originKind(tc.in); got != tc.want {
			t.Errorf("originKind(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSecurityLog_ChangedFields(t *testing.T) {
	stored := newMySQLTarget("db.example.com", 0, "graph_ops", "app", "", "")
	for _, tc := range []struct {
		name string
		t    mysqlTarget
		want []string
	}{
		{"unchanged", newMySQLTarget("db.example.com", 3306, "graph_ops", "app", "", ""), nil}, // port 0 == 3306
		{"host changed", newMySQLTarget("attacker.example.net", 0, "graph_ops", "app", "", ""), []string{"host"}},
		{"port changed", newMySQLTarget("db.example.com", 3307, "graph_ops", "app", "", ""), []string{"port"}},
		{"database changed", newMySQLTarget("db.example.com", 0, "other_db", "app", "", ""), []string{"database"}},
		{"user changed", newMySQLTarget("db.example.com", 0, "graph_ops", "root", "", ""), []string{"user"}},
		{"tls mode changed", newMySQLTarget("db.example.com", 0, "graph_ops", "app", "disabled", ""), []string{"tlsMode"}},
		{"tls CA changed", newMySQLTarget("db.example.com", 0, "graph_ops", "app", "", "/etc/mysql/ca.pem"), []string{"tlsCa"}},
		{"all changed, fixed order", newMySQLTarget("a", 1, "b", "c", "disabled", "/etc/mysql/ca.pem"), []string{"host", "port", "database", "user", "tlsMode", "tlsCa"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.t.changedFields(stored); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("changedFields = %v, want %v", got, tc.want)
			}
		})
	}
}

// T9: Config.Logger left unset must not silently discard output -- the
// entire point of DFLT-00035 is that a wiring gap must not reproduce the
// original "0 log lines" bug. Bypasses newTestServer deliberately: that
// helper's Config is exactly the "nobody passed a Logger" case this test
// means to check, so going through it would just retest the same default.
func TestSecurityLog_DefaultLoggerIsNotDiscarded(t *testing.T) {
	s := New(nil, nil, Config{})
	if s.rejectLog == nil || s.rejectLog.logger == nil {
		t.Fatal("expected a non-nil default logger")
	}
	if _, ok := s.rejectLog.logger.Handler().(*slog.TextHandler); !ok {
		t.Errorf("expected the default handler to be a *slog.TextHandler, got %T", s.rejectLog.logger.Handler())
	}
}

// T10: the 413 from withRequestBodyLimit is recorded. Repeated over-cap
// bodies are the only trace an operator would have of the memory-exhaustion
// attempt SEC-08 is about, and the rejection is detected by the status the
// middleware's wrapper saw, several frames above where the error was raised
// -- so it is worth a test of its own that the wiring holds.
func TestSecurityLog_RequestBodyTooLarge(t *testing.T) {
	s, _, _ := newTestServer(t)
	buf := captureRejectLog(t, s)

	const marker = "SENSITIVE-PAYLOAD-MARKER"
	body := `{"title":"` + marker + strings.Repeat("A", maxRequestBodyBytes+1024) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tickets", strings.NewReader(body))
	req.Host = testHost
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeaderName, "1")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", rec.Code, rec.Body.String())
	}
	if n := logLineCount(buf); n != 1 {
		t.Fatalf("expected exactly 1 log line, got %d: %s", n, buf.String())
	}
	line := buf.String()
	if got := attrValue(t, line, "code"); got != string(domain.ErrCodeRequestBodyTooLarge) {
		t.Errorf("code = %q, want %q", got, domain.ErrCodeRequestBodyTooLarge)
	}
	if got := attrValue(t, line, "status"); got != "413" {
		t.Errorf("status = %q, want 413", got)
	}
	if got := attrValue(t, line, "path_class"); got != "api" {
		t.Errorf("path_class = %q, want api", got)
	}
	// The masking policy holds for this rejection too: nothing the caller
	// sent appears in the line.
	assertNoneContain(t, line, marker, "/api/tickets")
}

// T11: an unrouted /api/ path is recorded as well. A run of these is how
// endpoint probing, or a client still calling a route this release removed,
// becomes visible at all.
func TestSecurityLog_APIRouteNotFound(t *testing.T) {
	s, _, _ := newTestServer(t)
	buf := captureRejectLog(t, s)

	req := httptest.NewRequest(http.MethodGet, "/api/tickets/PFX-00001/executable-nodes", nil)
	req.Host = testHost
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if n := logLineCount(buf); n != 1 {
		t.Fatalf("expected exactly 1 log line, got %d: %s", n, buf.String())
	}
	line := buf.String()
	if got := attrValue(t, line, "code"); got != string(domain.ErrCodeAPIRouteNotFound) {
		t.Errorf("code = %q, want %q", got, domain.ErrCodeAPIRouteNotFound)
	}
	if got := attrValue(t, line, "status"); got != "404" {
		t.Errorf("status = %q, want 404", got)
	}
	// The path is what the caller chose, so it is classified, never written.
	assertNoneContain(t, line, "PFX-00001", "executable-nodes")
}

// T12: a request that is answered normally writes nothing. The body-limit
// middleware now watches every non-GET response's status, and a middleware
// that logs on the way out must not turn ordinary traffic into warnings.
func TestSecurityLog_SuccessfulRequestLogsNothing(t *testing.T) {
	s, _, projectID := newTestServer(t)
	buf := captureRejectLog(t, s)

	rec := doJSON(t, s, http.MethodPost, "/api/tickets", map[string]any{
		"title": "ordinary", "project_id": projectID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if n := logLineCount(buf); n != 0 {
		t.Errorf("expected no log lines for a successful request, got %d: %s", n, buf.String())
	}
}
