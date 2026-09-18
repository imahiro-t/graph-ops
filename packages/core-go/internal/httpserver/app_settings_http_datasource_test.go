package httpserver

import (
	"net/http"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/runtimeconfig"
)

// DFLT-00088: the HTTP custom data source fields of GET/PUT
// /api/settings/app.

func seedStoredHTTPDataSource(t *testing.T, workDir, homeDir, url, token string) {
	t.Helper()
	if _, err := runtimeconfig.Save(workDir, homeDir, runtimeconfig.FileConfig{
		DBBackend: "http", HTTPDataSourceURL: url, HTTPDataSourceToken: token,
	}); err != nil {
		t.Fatalf("seeding graph-config.json: %v", err)
	}
}

func httpDataSourcePutBody(url, token string) map[string]any {
	return map[string]any{
		"dbBackend": "http", "httpDataSourceUrl": url, "httpDataSourceToken": token,
		"paginationPageSize": 10,
	}
}

func TestAppSettings_GetRedactsPlaintextHTTPDataSourceToken(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	seedStoredHTTPDataSource(t, workDir, homeDir, "https://a.example.com", "plain-token")

	rec := doJSON(t, s, http.MethodGet, "/api/settings/app", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "plain-token") {
		t.Fatalf("response contains the stored token: %s", rec.Body.String())
	}
	var got appSettingsResponse
	mustDecode(t, rec, &got)
	if got.File.HTTPDataSourceToken != runtimeconfig.RedactedSecretPlaceholder {
		t.Fatalf("httpDataSourceToken = %q, want the placeholder", got.File.HTTPDataSourceToken)
	}
	if got.File.HTTPDataSourceURL != "https://a.example.com" {
		t.Fatalf("httpDataSourceUrl = %q", got.File.HTTPDataSourceURL)
	}
}

func TestAppSettings_GetReturnsHTTPDataSourceEnvVarRefVerbatim(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	t.Setenv("GRAPHOPS_DATASOURCE_TOKEN", "resolved-secret-value")
	seedStoredHTTPDataSource(t, workDir, homeDir, "https://a.example.com", "${GRAPHOPS_DATASOURCE_TOKEN}")

	rec := doJSON(t, s, http.MethodGet, "/api/settings/app", nil)
	var got appSettingsResponse
	mustDecode(t, rec, &got)
	if got.File.HTTPDataSourceToken != "${GRAPHOPS_DATASOURCE_TOKEN}" {
		t.Fatalf("httpDataSourceToken = %q, want the reference", got.File.HTTPDataSourceToken)
	}
	if strings.Contains(rec.Body.String(), "resolved-secret-value") {
		t.Fatal("response contains the resolved env var value")
	}
}

func TestAppSettings_EffectiveIncludesHTTPDataSourceURLButNoToken(t *testing.T) {
	s, _, _ := newAppSettingsTestServer(t)
	s.cfg.DBBackend = "http"
	s.cfg.HTTPDataSourceURL = "https://a.example.com"

	rec := doJSON(t, s, http.MethodGet, "/api/settings/app", nil)
	var got appSettingsResponse
	mustDecode(t, rec, &got)
	if got.Effective.DBBackend != "http" || got.Effective.HTTPDataSourceURL != "https://a.example.com" {
		t.Fatalf("effective = %+v", got.Effective)
	}
	var raw struct {
		Effective map[string]any `json:"effective"`
	}
	mustDecode(t, rec, &raw)
	for key := range raw.Effective {
		if strings.Contains(strings.ToLower(key), "token") {
			t.Fatalf("effective settings carry a token field %q", key)
		}
	}
}

func TestAppSettings_PutKeepsStoredHTTPTokenForTheSameURL(t *testing.T) {
	for _, url := range []string{"https://a.example.com", "https://A.EXAMPLE.com", "https://a.example.com/", "https://a.example.com:443"} {
		t.Run(url, func(t *testing.T) {
			s, workDir, homeDir := newAppSettingsTestServer(t)
			seedStoredHTTPDataSource(t, workDir, homeDir, "https://a.example.com", "plain-token")

			rec := doJSON(t, s, http.MethodPut, "/api/settings/app", httpDataSourcePutBody(url, runtimeconfig.RedactedSecretPlaceholder))
			if rec.Code != http.StatusOK {
				t.Fatalf("PUT = %d: %s", rec.Code, rec.Body.String())
			}
			onDisk, _, _ := runtimeconfig.Load(workDir, homeDir)
			if onDisk.HTTPDataSourceToken != "plain-token" {
				t.Fatalf("stored token = %q, want plain-token", onDisk.HTTPDataSourceToken)
			}
		})
	}
}

func TestAppSettings_PutRequiresHTTPTokenRetypeForAChangedURL(t *testing.T) {
	cases := []struct {
		name, stored, newURL, submitted string
	}{
		{"plaintext, other host", "plain-token", "https://b.example.com", runtimeconfig.RedactedSecretPlaceholder},
		{"env ref, other host", "${GRAPHOPS_DATASOURCE_TOKEN}", "https://b.example.com", "${GRAPHOPS_DATASOURCE_TOKEN}"},
		{"plaintext, http scheme", "plain-token", "http://a.example.com", runtimeconfig.RedactedSecretPlaceholder},
		{"plaintext, other port", "plain-token", "https://a.example.com:8443", runtimeconfig.RedactedSecretPlaceholder},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, workDir, homeDir := newAppSettingsTestServer(t)
			logBuf := captureRejectLog(t, s)
			seedStoredHTTPDataSource(t, workDir, homeDir, "https://a.example.com", tc.stored)

			rec := doJSON(t, s, http.MethodPut, "/api/settings/app", httpDataSourcePutBody(tc.newURL, tc.submitted))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PUT = %d: %s", rec.Code, rec.Body.String())
			}
			if got := decodeError(t, rec).Code; got != "HTTP_DATASOURCE_TOKEN_RETYPE_REQUIRED" {
				t.Fatalf("code = %q, want HTTP_DATASOURCE_TOKEN_RETYPE_REQUIRED", got)
			}
			onDisk, _, _ := runtimeconfig.Load(workDir, homeDir)
			if onDisk.HTTPDataSourceURL != "https://a.example.com" || onDisk.HTTPDataSourceToken != tc.stored {
				t.Fatalf("settings changed despite the rejection: %+v", onDisk)
			}
			logged := logBuf.String()
			if !strings.Contains(logged, "HTTP_DATASOURCE_TOKEN_RETYPE_REQUIRED") {
				t.Fatalf("rejection was not logged: %q", logged)
			}
			if strings.Contains(logged, "plain-token") || strings.Contains(logged, "b.example.com") {
				t.Fatalf("the rejection log carries the token or the submitted URL: %q", logged)
			}
		})
	}
}

func TestAppSettings_PutAcceptsANewHTTPTokenForANewURL(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	seedStoredHTTPDataSource(t, workDir, homeDir, "https://a.example.com", "plain-token")

	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", httpDataSourcePutBody("https://b.example.com", "new-token"))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", rec.Code, rec.Body.String())
	}
	onDisk, _, _ := runtimeconfig.Load(workDir, homeDir)
	if onDisk.HTTPDataSourceURL != "https://b.example.com" || onDisk.HTTPDataSourceToken != "new-token" {
		t.Fatalf("stored = %+v", onDisk)
	}
}

func TestAppSettings_PutRejectsInvalidHTTPDataSourceSettings(t *testing.T) {
	cases := []struct{ url, token string }{
		{"", "t"},
		{"http://example.com", "t"},
		{"https://example.com", ""},
		{"ftp://example.com", "t"},
	}
	for _, tc := range cases {
		t.Run(tc.url+"|"+tc.token, func(t *testing.T) {
			s, workDir, homeDir := newAppSettingsTestServer(t)
			rec := doJSON(t, s, http.MethodPut, "/api/settings/app", httpDataSourcePutBody(tc.url, tc.token))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PUT = %d: %s", rec.Code, rec.Body.String())
			}
			onDisk, _, _ := runtimeconfig.Load(workDir, homeDir)
			if onDisk.DBBackend != "" || onDisk.HTTPDataSourceURL != "" {
				t.Fatalf("settings were saved despite the 400: %+v", onDisk)
			}
		})
	}
}

func TestAppSettings_PutKeepsUnresolvedEnvVarRefToken(t *testing.T) {
	s, workDir, homeDir := newAppSettingsTestServer(t)
	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", httpDataSourcePutBody("https://example.com", "${UNSET_AT_SAVE_DFLT_00088}"))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", rec.Code, rec.Body.String())
	}
	onDisk, _, _ := runtimeconfig.Load(workDir, homeDir)
	if onDisk.HTTPDataSourceToken != "${UNSET_AT_SAVE_DFLT_00088}" {
		t.Fatalf("stored token = %q, want the unresolved reference", onDisk.HTTPDataSourceToken)
	}
}

func TestAppSettings_PutIgnoresLeftoverHTTPSettingsUnderSQLite(t *testing.T) {
	s, _, _ := newAppSettingsTestServer(t)
	body := httpDataSourcePutBody("http://example.com", "")
	body["dbBackend"] = "sqlite"
	rec := doJSON(t, s, http.MethodPut, "/api/settings/app", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", rec.Code, rec.Body.String())
	}
}
