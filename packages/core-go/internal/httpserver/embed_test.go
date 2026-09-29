package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const (
	testIndexHTML = "<!doctype html><title>graph-ops test index</title>"
	testAppJS     = "console.log('app chunk');"
)

// TestStaticWebHandler_AssetsMissAre404SPAFallbackOtherwise pins the
// routing rules of newStaticWebHandler: a missing file under /assets/ is a
// 404 (not index.html with a 200, which a browser loading an old chunk
// would report as a MIME-type mismatch), while every other unknown path
// still falls back to index.html so client-side routes keep working.
func TestStaticWebHandler_AssetsMissAre404SPAFallbackOtherwise(t *testing.T) {
	h := newStaticWebHandler(fstest.MapFS{
		"index.html":           {Data: []byte(testIndexHTML)},
		"assets/app-abc123.js": {Data: []byte(testAppJS)},
	})

	cases := []struct {
		name     string
		path     string
		wantCode int
		wantBody string // exact body; empty means "check only that it isn't index.html"
	}{
		{"missing asset", "/assets/missing-deadbeef.js", http.StatusNotFound, ""},
		{"missing nested asset", "/assets/nested/missing.css", http.StatusNotFound, ""},
		{"assets directory itself", "/assets/", http.StatusNotFound, ""},
		{"existing asset", "/assets/app-abc123.js", http.StatusOK, testAppJS},
		{"root", "/", http.StatusOK, testIndexHTML},
		{"artifact preview deep link", "/artifacts/art-1/preview", http.StatusOK, testIndexHTML},
		{"unknown route", "/some/unknown/route", http.StatusOK, testIndexHTML},
		// The prefix check is per directory: a root-level name that merely
		// starts with "assets" is an ordinary unknown path.
		{"name starting with assets", "/assetsfoo.js", http.StatusOK, testIndexHTML},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

			if rec.Code != tc.wantCode {
				t.Fatalf("GET %s: status = %d, want %d", tc.path, rec.Code, tc.wantCode)
			}
			body := rec.Body.String()
			if tc.wantBody != "" {
				if body != tc.wantBody {
					t.Fatalf("GET %s: body = %q, want %q", tc.path, body, tc.wantBody)
				}
				return
			}
			if strings.Contains(body, testIndexHTML) {
				t.Fatalf("GET %s: body is index.html (%q), want a plain 404", tc.path, body)
			}
		})
	}
}

// TestRoutes_MissingAssetIs404 checks the same rule through the real wiring
// (Routes() and the embedded build, which in a test binary holds no file
// by that name).
func TestRoutes_MissingAssetIs404(t *testing.T) {
	s, _, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/assets/does-not-exist.js", nil)
	req.Host = testHost
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /assets/does-not-exist.js: status = %d, want %d (body %q)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}
