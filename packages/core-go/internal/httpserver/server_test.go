package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// TestCSRFHeader_MutatingRequestRejectedWithoutIt verifies the mitigation
// added for Security review node-5f79e568: this server has no
// authentication, a wildcard CORS origin, and listens on all interfaces, so
// any state-changing request (POST/PATCH/PUT/DELETE) that doesn't carry
// csrfHeaderName must be rejected before it ever reaches a handler -- that's
// what stops an arbitrary web page (or LAN host) from e.g. POSTing
// /api/projects with a chosen local_path and then POSTing /api/claude/launch
// to open a terminal there.
func TestCSRFHeader_MutatingRequestRejectedWithoutIt(t *testing.T) {
	s, _, _ := newTestServer(t)

	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/tickets", nil)
			req.Host = testHost
			req.Header.Set("Content-Type", "application/json")
			// Deliberately NOT setting csrfHeaderName.
			rec := httptest.NewRecorder()
			s.Routes().ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s without %s header: got status %d, want %d", method, csrfHeaderName, rec.Code, http.StatusForbidden)
			}
			got := decodeError(t, rec)
			if got.Code != domain.ErrCodeCSRFHeaderRequired {
				t.Fatalf("error code = %q, want %q", got.Code, domain.ErrCodeCSRFHeaderRequired)
			}
		})
	}
}

// TestCSRFHeader_GetRequestNeedsNoHeader verifies read-only GET requests are
// exempt (see requiresCSRFHeader) -- they don't change server state, so
// there's nothing to protect there, and the frontend's initial page load
// must not be blocked before it even has a chance to set the header.
func TestCSRFHeader_GetRequestNeedsNoHeader(t *testing.T) {
	s, _, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/tickets", nil)
	req.Host = testHost
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET without %s header: got status %d, want %d", csrfHeaderName, rec.Code, http.StatusOK)
	}
}

// TestCSRFHeader_MutatingRequestSucceedsWithHeader is the positive control:
// the same header the frontend (packages/web/src/lib/apiFetch.ts) always
// sends must let a legitimate mutating request through as before.
func TestCSRFHeader_MutatingRequestSucceedsWithHeader(t *testing.T) {
	s, _, projectID := newTestServer(t)

	rec := doJSON(t, s, http.MethodPost, "/api/tickets", map[string]any{
		"title":      "hello",
		"project_id": projectID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST with %s header: got status %d, want %d (body: %s)", csrfHeaderName, rec.Code, http.StatusCreated, rec.Body.String())
	}
}

// TestCSRFHeader_NotInAllowedCORSHeaders verifies csrfHeaderName is
// deliberately absent from Access-Control-Allow-Headers: that's what makes a
// cross-origin fetch/XHR that tries to attach it fail the browser's CORS
// preflight before the real request is ever sent. If this header were ever
// added to the allow-list, the whole mitigation would be defeated for any
// cross-origin page willing to also set it.
func TestCSRFHeader_NotInAllowedCORSHeaders(t *testing.T) {
	s, _, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodOptions, "/api/tickets", nil)
	req.Host = testHost
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	allowed := rec.Header().Get("Access-Control-Allow-Headers")
	if allowed == "" {
		t.Fatalf("Access-Control-Allow-Headers is empty")
	}
	for _, h := range strings.Split(allowed, ",") {
		if strings.EqualFold(strings.TrimSpace(h), csrfHeaderName) {
			t.Fatalf("Access-Control-Allow-Headers %q must not include %s -- doing so would let cross-origin requests pass CORS preflight with it set", allowed, csrfHeaderName)
		}
	}
}

// TestCORS_AllowsOnlyLoopbackOrigins is the regression test for security
// review art-86c49ea7's cross-origin half: this API is unauthenticated and
// GET requests carry no CSRF header, so an unconditional
// "Access-Control-Allow-Origin: *" let any page the user's browser loaded
// read every GET response. Only a loopback origin (the dev server pointed
// straight at the API) may be echoed back.
func TestCORS_AllowsOnlyLoopbackOrigins(t *testing.T) {
	s, _, _ := newTestServer(t)

	for _, tc := range []struct {
		origin string
		echoed bool
	}{
		{"http://localhost:3000", true},
		{"http://127.0.0.1:49173", true},
		{"http://[::1]:3000", true},
		{"https://evil.example.com", false},
		{"http://localhost.evil.example.com", false},
		{"null", false},
		{"", false},
	} {
		t.Run(tc.origin, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
			req.Host = testHost
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			s.Routes().ServeHTTP(rec, req)

			got := rec.Header().Get("Access-Control-Allow-Origin")
			if tc.echoed && got != tc.origin {
				t.Fatalf("origin %q: expected it echoed back, got %q", tc.origin, got)
			}
			if !tc.echoed && got != "" {
				t.Fatalf("origin %q: expected no Access-Control-Allow-Origin, got %q", tc.origin, got)
			}
			if vary := rec.Header().Get("Vary"); !strings.Contains(vary, "Origin") {
				t.Errorf("expected Vary to include Origin so caches can't reuse one origin's response, got %q", vary)
			}
		})
	}
}

// TestHostHeader_OnlyAddressesThisServerAnswersTo is the DNS rebinding
// regression test (DFLT-00023 item S-2). Nothing in this server used to look
// at Host at all, which let an attacker-controlled domain re-resolved to
// 127.0.0.1 become same-origin with this API -- and same-origin defeats the
// CORS check, the CSRF header and the loopback bind all at once. Only a Host
// that actually names this server may get through.
func TestHostHeader_OnlyAddressesThisServerAnswersTo(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cfgHost  string
		reqHost  string
		wantPass bool
	}{
		// How real clients address a default (loopback) server.
		{"loopback default", "127.0.0.1", "127.0.0.1:49173", true},
		{"localhost name", "127.0.0.1", "localhost:49173", true},
		{"vite dev proxy", "127.0.0.1", "localhost:3000", true},
		{"ipv6 loopback", "127.0.0.1", "[::1]:49173", true},
		{"no port", "127.0.0.1", "localhost", true},
		{"other loopback address", "127.0.0.2", "127.0.0.2:49173", true},
		// The rebinding attack itself: the browser sends the attacker's
		// domain even though it resolved to this machine.
		{"rebound attacker domain", "127.0.0.1", "evil.example.com:49173", false},
		{"lookalike subdomain", "127.0.0.1", "localhost.evil.example.com", false},
		{"empty host", "127.0.0.1", "", false},
		// A LAN address is not this server unless it was bound to it.
		{"lan address, loopback bind", "127.0.0.1", "192.168.1.5:49173", false},
		{"lan address, matching bind", "192.168.1.5", "192.168.1.5:49173", true},
		{"name, matching bind unrelated", "192.168.1.5", "evil.example.com", false},
		// A wildcard bind is reachable at addresses this process cannot
		// enumerate, so any IP literal is accepted -- but still never a name,
		// which is all a rebinding attack can send.
		{"wildcard bind, lan address", "0.0.0.0", "192.168.1.5:49173", true},
		{"wildcard bind, ipv6 address", "::", "[2001:db8::1]:49173", true},
		{"wildcard bind, attacker domain", "0.0.0.0", "evil.example.com", false},
		// An unset Host in Config means the default loopback bind.
		{"unset cfg host, loopback request", "", "127.0.0.1:49173", true},
		{"unset cfg host, attacker domain", "", "evil.example.com", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newTestServer(t)
			s.cfg.Host = tc.cfgHost

			req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
			req.Host = tc.reqHost
			rec := httptest.NewRecorder()
			s.Routes().ServeHTTP(rec, req)

			if tc.wantPass {
				if rec.Code == http.StatusForbidden {
					t.Fatalf("Host %q with bind host %q: rejected (%s), want it allowed through", tc.reqHost, tc.cfgHost, rec.Body.String())
				}
				return
			}
			if rec.Code != http.StatusForbidden {
				t.Fatalf("Host %q with bind host %q: got status %d, want %d", tc.reqHost, tc.cfgHost, rec.Code, http.StatusForbidden)
			}
			if got := decodeError(t, rec); got.Code != domain.ErrCodeHostNotAllowed {
				t.Fatalf("error code = %q, want %q", got.Code, domain.ErrCodeHostNotAllowed)
			}
		})
	}
}

// TestHostHeader_GuardsStaticAndArtifactRoutesToo verifies the check wraps
// everything Routes() serves, not just /api: a rebound origin reading stored
// artifact files is as much of a leak as one driving the API.
func TestHostHeader_GuardsStaticAndArtifactRoutesToo(t *testing.T) {
	s, _, _ := newTestServer(t)

	for _, path := range []string{"/", "/artifacts-static/anything.html"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Host = "evil.example.com"
			rec := httptest.NewRecorder()
			s.Routes().ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("GET %s with a foreign Host: got status %d, want %d", path, rec.Code, http.StatusForbidden)
			}
		})
	}
}

// TestHostHeader_RejectedBeforeCORSPreflightIsAnswered pins the layering:
// withAllowedHost wraps withCORS, so a foreign Host never even gets a
// preflight answered (which would otherwise hand it an
// Access-Control-Allow-* response before anything looked at Host).
func TestHostHeader_RejectedBeforeCORSPreflightIsAnswered(t *testing.T) {
	s, _, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodOptions, "/api/tickets", nil)
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("OPTIONS preflight with a foreign Host: got status %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// TestHostHeaderName covers the parsing allowedHost depends on: a Host header
// may or may not carry a port, and an IPv6 literal is bracketed.
func TestHostHeaderName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"localhost:49173", "localhost"},
		{"localhost", "localhost"},
		{"127.0.0.1:49173", "127.0.0.1"},
		{"[::1]:49173", "::1"},
		{"[::1]", "::1"}, // no port, but still bracketed
		{"example.com", "example.com"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := hostHeaderName(tt.in); got != tt.want {
			t.Errorf("hostHeaderName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
