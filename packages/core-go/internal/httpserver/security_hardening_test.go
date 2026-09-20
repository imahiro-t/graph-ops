package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// DFLT-00103: the two routes that let a browser be used as a stepping stone
// into this unauthenticated server, the clickjacking headers that close the
// framing route, and the request body cap.

// GET /api/tickets/{id}/executable-nodes changed state (it seeded a ticket's
// graph and claimed nodes) yet was a GET, which is exempt from the CSRF
// header check -- so a single <img src="..."> on any page the user opened
// drove it. It is gone, and an /api/ path with no route must say so rather
// than fall through to the SPA.
func TestRemovedRoute_ExecutableNodesIs404(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	rec := doJSON(t, s, http.MethodGet, "/api/tickets/"+ticket.ID+"/executable-nodes", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET executable-nodes = %d, want %d: %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if got := decodeError(t, rec).Code; got != domain.ErrCodeAPIRouteNotFound {
		t.Errorf("error code = %q, want %q", got, domain.ErrCodeAPIRouteNotFound)
	}

	// And it really is the route that is gone, not just the seeding: the
	// ticket must not have acquired a graph on the way to that 404.
	detail, err := repo.GetTicketDetail(ticket.ID)
	if err != nil || detail == nil {
		t.Fatalf("GetTicketDetail: %v", err)
	}
	if len(detail.Nodes) != 0 {
		t.Errorf("ticket has %d nodes after the removed endpoint was called; it must change nothing", len(detail.Nodes))
	}
}

// Any unrouted /api/ path answers as the API (JSON 404), not as the web UI.
func TestUnknownAPIRoute_Is404JSON(t *testing.T) {
	s, _, _ := newTestServer(t)

	for _, path := range []string{"/api/nope", "/api/tickets/T-1/executable-nodes", "/api/artifacts"} {
		t.Run(path, func(t *testing.T) {
			rec := doJSON(t, s, http.MethodGet, path, nil)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("GET %s = %d, want 404: %s", path, rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want JSON (an /api/ path must never answer with the SPA)", ct)
			}
		})
	}
}

// /artifacts-static/ served the artifacts directory off the filesystem, on
// the app's own origin and with no sandbox: agent-authored HTML opened from
// there ran as same-origin script and could call the API with the CSRF header
// attached. The route is gone. Two things are pinned here: that nothing from
// the artifacts directory can still be reached through it (the security
// property), and that it answers an explicit 404 rather than falling through
// to the SPA catch-all with a 200 (the tombstone in Routes()).
//
// "/artifacts-static" with no trailing slash is a 301 to the subtree pattern,
// which is ServeMux's own behaviour and still ends at the 404; it is listed
// so that a redirect to something else would be noticed.
func TestRemovedRoute_ArtifactsStaticServesNothing(t *testing.T) {
	s, _, _ := newTestServer(t)

	const secret = "SECRET-ARTIFACT-FILE-CONTENT"
	const name = "leaked-report.html"
	if err := os.WriteFile(filepath.Join(s.cfg.ArtifactsDir, name), []byte(secret), 0o600); err != nil {
		t.Fatalf("seed artifacts dir: %v", err)
	}

	for _, tc := range []struct {
		path       string
		wantStatus int
	}{
		{"/artifacts-static/" + name, http.StatusNotFound},
		{"/artifacts-static/", http.StatusNotFound},
		{"/artifacts-static", http.StatusMovedPermanently},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rec := doJSON(t, s, http.MethodGet, tc.path, nil)
			if body := rec.Body.String(); strings.Contains(body, secret) {
				t.Errorf("GET %s returned the artifact file's content", tc.path)
			} else if strings.Contains(body, name) {
				t.Errorf("GET %s returned a directory listing naming %q", tc.path, name)
			}
			if rec.Code != tc.wantStatus {
				t.Fatalf("GET %s = %d, want %d: %s", tc.path, rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusNotFound {
				return
			}
			if got := decodeError(t, rec).Code; got != domain.ErrCodeRouteNotFound {
				t.Errorf("error code = %q, want %q", got, domain.ErrCodeRouteNotFound)
			}
		})
	}
}

// Every response carries the security headers -- successful API calls, the
// SPA itself, and the 403s the Host/CSRF layers produce before any handler
// runs. The last of those is why withSecurityHeaders is the outermost
// middleware.
func TestSecurityHeaders_OnEveryResponse(t *testing.T) {
	s, _, _ := newTestServer(t)

	cases := []struct {
		name       string
		req        func() *http.Request
		wantStatus int
	}{
		{
			name: "api 200",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/api/health", nil)
				r.Host = testHost
				return r
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "spa 200",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				r.Host = testHost
				return r
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "api 404",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/api/nope", nil)
				r.Host = testHost
				return r
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "host rejected 403",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/api/tickets", nil)
				r.Host = "evil.example.com"
				return r
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "csrf rejected 403",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodPost, "/api/tickets", bytes.NewReader([]byte(`{"title":"x"}`)))
				r.Host = testHost
				// deliberately no csrfHeaderName
				return r
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "cors preflight 204",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodOptions, "/api/tickets", nil)
				r.Host = testHost
				return r
			},
			wantStatus: http.StatusNoContent,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.Routes().ServeHTTP(rec, tc.req())
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
				t.Errorf("X-Frame-Options = %q, want DENY", got)
			}
			if got := rec.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'none'" {
				t.Errorf("Content-Security-Policy = %q, want frame-ancestors 'none'", got)
			}
			// nosniff used to be set in one handler only. It belongs with
			// the other two now that there is a place that cannot be
			// forgotten -- especially since the /api/ 404 reflects the
			// request path into a JSON body.
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			// SEC-09's other half: an artifact preview's URL names the
			// artifact being read, and no external host gets to see it.
			if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
				t.Errorf("Referrer-Policy = %q, want no-referrer", got)
			}
		})
	}
}

// The one documented exception: the artifact content endpoint has to stay
// embeddable by the app's own origin, because that is how the UI previews an
// artifact (a sandboxed <iframe> inline on the ticket screen and on
// /artifacts/{id}/preview). Both directives must ride in a single CSP header
// -- a second header would be intersected with this one by the browser and
// 'none' would win, breaking the preview.
func TestSecurityHeaders_ArtifactContentIsTheException(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticketID, nodeID := newReportNode(t, repo, projectID)
	rec := postArtifact(t, s, ticketID, map[string]any{
		"node_id": nodeID, "name": "Preview me", "type": "text", "content": "hello",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("postArtifact: %d %s", rec.Code, rec.Body.String())
	}
	var created domain.Artifact
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created artifact: %v", err)
	}

	contentRec := getArtifactContent(t, s, created.ID)
	if contentRec.Code != http.StatusOK {
		t.Fatalf("GET content: %d %s", contentRec.Code, contentRec.Body.String())
	}

	csp := contentRec.Header()["Content-Security-Policy"]
	if len(csp) != 1 {
		t.Fatalf("got %d Content-Security-Policy headers (%q), want exactly 1", len(csp), csp)
	}
	if !strings.Contains(csp[0], "sandbox allow-scripts") {
		t.Errorf("CSP %q lost the sandbox directive", csp[0])
	}
	if !strings.Contains(csp[0], "frame-ancestors 'self'") {
		t.Errorf("CSP %q must allow the app's own origin to frame it", csp[0])
	}
	if strings.Contains(csp[0], "frame-ancestors 'none'") {
		t.Errorf("CSP %q still carries the server-wide 'none', which would break the UI's preview", csp[0])
	}
	if got := contentRec.Header().Get("X-Frame-Options"); got == "DENY" {
		t.Errorf("X-Frame-Options = DENY on the artifact content endpoint; the UI's <iframe> preview needs SAMEORIGIN")
	}
	// The pre-existing hardening is untouched.
	if got := contentRec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

// A body past maxRequestBodyBytes is the caller's problem (413), not a server
// failure (5xx) -- and that has to hold for handlers that answer a decode
// error with 400 as well as for those that answer 500, which is why
// writeError classifies it rather than each handler.
func TestRequestBodyLimit_IsA413NotA5xx(t *testing.T) {
	s, _, projectID := newTestServer(t)

	oversized := `{"title":"` + strings.Repeat("A", maxRequestBodyBytes+1024) + `"}`

	for _, tc := range []struct{ name, method, path string }{
		{"POST", http.MethodPost, "/api/tickets"},
		{"PATCH", http.MethodPatch, "/api/tickets/does-not-matter"},
		{"PUT", http.MethodPut, "/api/settings/app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(oversized))
			req.Host = testHost
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(csrfHeaderName, "1")
			rec := httptest.NewRecorder()
			s.Routes().ServeHTTP(rec, req)

			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
			}
			if got := decodeError(t, rec).Code; got != domain.ErrCodeRequestBodyTooLarge {
				t.Errorf("error code = %q, want %q", got, domain.ErrCodeRequestBodyTooLarge)
			}
		})
	}

	// A body under the cap is unaffected -- the limit must not change how a
	// normal request is handled.
	rec := doJSON(t, s, http.MethodPost, "/api/tickets", map[string]any{
		"title": "ordinary", "project_id": projectID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("a body within the cap = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}
