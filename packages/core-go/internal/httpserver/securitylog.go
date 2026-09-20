package httpserver

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/graph-ops/core-go/internal/domain"
)

// This file records the "request rejected" security events for the three
// attack-detection rejections this package can make: HOST_NOT_ALLOWED
// (server.go's withAllowedHost), CSRF_HEADER_REQUIRED (server.go's
// withCORS) and MYSQL_PASSWORD_RETYPE_REQUIRED (app_settings.go's
// handlePutAppSettings/handleTestMySQLConnection). Before DFLT-00035 none of
// these were logged anywhere, so a real attack against this server was both
// undetectable and untraceable after the fact.
//
// Masking policy (do not weaken this without updating README.md EN/JA):
// every value in the request that the *caller* controls -- the Host header,
// the Origin header, the URL path/query, the request body, any other header
// -- is never written to the log, not even a substring of it. What is
// written instead is always one of: a classification into a small,
// server-defined set of values (e.g. host_kind, origin_kind, path_class), a
// length (host_len), or a keyed fingerprint (host_fp). This is what keeps
// the rejection log itself from becoming a new information-disclosure or
// log-injection surface (the exact concern the ticket that created this
// file was opened to address) -- see the execution plan (art-28b0e6d7) §2.3
// for the full reasoning. Anyone adding a new field to a rejection log line
// must fit it into one of these three shapes, not the raw value.
//
// remote_ip is the one exception: it comes from the TCP connection
// (r.RemoteAddr), not from anything the caller can put in a header, so it is
// logged as-is.

// rejectLogger is the shared destination for every "request rejected" log
// line this package writes, plus the per-process key used to fingerprint
// Host header values (see hostFingerprint). Server holds exactly one of
// these; there is deliberately no separate plain *slog.Logger field on
// Server, so a test that wants to capture output only has one place to
// replace it (see the plan review's M note on this, art-7c049e48 §2.3).
type rejectLogger struct {
	logger *slog.Logger
	fpKey  []byte
}

// newRejectLogger creates a rejectLogger with a fresh, random fingerprint
// key. Called once per Server (in New), never per-request: the key must
// stay fixed for the life of the process so that repeated rejections for
// the same Host value can be correlated (see hostFingerprint), and must
// never be derived from anything an attacker could guess or reproduce
// across processes.
func newRejectLogger(logger *slog.Logger) *rejectLogger {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		// crypto/rand.Read failing is not something this process can
		// meaningfully recover from; panicking here (at startup, inside
		// New) is preferable to silently running with a predictable or
		// all-zero key.
		panic("httpserver: failed to generate reject-log fingerprint key: " + err.Error())
	}
	return &rejectLogger{logger: logger, fpKey: key}
}

// log writes one "request rejected" line at WARN, with the common fields
// every rejection carries (event, code, status, method, remote_ip) followed
// by attrs, which supply whatever additional, already-masked fields are
// specific to this rejection code (see withAllowedHost, withCORS and
// logPasswordRetypeRejected for the three call sites).
func (rl *rejectLogger) log(r *http.Request, code domain.ErrorCode, status int, attrs ...slog.Attr) {
	base := []slog.Attr{
		slog.String("event", "security.reject"),
		slog.String("code", string(code)),
		slog.Int("status", status),
		slog.String("method", safeMethod(r.Method)),
		slog.String("remote_ip", remoteIP(r.RemoteAddr)),
	}
	rl.logger.LogAttrs(r.Context(), slog.LevelWarn, "request rejected", append(base, attrs...)...)
}

// knownMethods is the fixed set of HTTP methods this server's mux ever
// routes on (see Routes). safeMethod normalizes anything outside this set to
// "OTHER" so a malformed or attacker-chosen method string can never reach
// the log verbatim.
var knownMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodPost:    true,
	http.MethodPut:     true,
	http.MethodPatch:   true,
	http.MethodDelete:  true,
	http.MethodOptions: true,
}

// safeMethod returns method unchanged if it is one of the handful of HTTP
// methods this server understands, or "OTHER" otherwise.
func safeMethod(method string) string {
	if knownMethods[method] {
		return method
	}
	return "OTHER"
}

// remoteIP extracts the IP address portion of remoteAddr (an
// http.Request.RemoteAddr-shaped "host:port" string), or "unknown" if it
// cannot be parsed that way. This is the TCP peer address, not anything read
// from a header, so -- unlike Host/Origin -- it is safe to log as-is: the
// caller cannot put arbitrary text here.
func remoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil || host == "" {
		return "unknown"
	}
	return host
}

// pathClass buckets a request path into one of the two route groups Routes()
// actually serves, without ever logging the path itself (a path can carry
// attacker-chosen or otherwise sensitive substrings, e.g. a ticket ID or
// artifact ID).
//
// There used to be a third bucket, "artifacts-static", for the filesystem
// route that served the artifacts directory. DFLT-00103 removed that route,
// and the bucket with it: everything outside /api/ is now the SPA (including
// its catch-all fallback), so a separate class would only ever be an empty
// one.
func pathClass(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/"):
		return "api"
	default:
		return "web"
	}
}

// isDNSNameChars reports whether s consists only of characters that are
// valid in a DNS name (letters, digits, '-' and '.'). It does not otherwise
// validate DNS name structure (label lengths, leading/trailing '-', etc.) --
// it only needs to separate "looks like a plausible hostname" from
// "contains something else entirely" (control characters, whitespace,
// injection attempts) for hostKind's classification.
func isDNSNameChars(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '.':
		default:
			return false
		}
	}
	return true
}

// hostKind classifies a raw Host header value into one of "empty", "ip",
// "name" or "malformed", without ever returning (or logging) the value
// itself. "name" is the interesting case for the DNS-rebinding rejection
// this backs (withAllowedHost) -- a syntactically ordinary hostname is
// exactly what a rebinding attack's domain looks like.
func hostKind(hostHeader string) string {
	name := hostHeaderName(hostHeader)
	switch {
	case name == "":
		return "empty"
	case net.ParseIP(name) != nil:
		return "ip"
	case isDNSNameChars(name):
		return "name"
	default:
		return "malformed"
	}
}

// hostFingerprint returns a short (16 hex char), keyed fingerprint of a Host
// header's bare name (hostHeaderName(hostHeader)), so repeated log lines can
// be correlated as "the same Host" without the Host value itself ever being
// written down. The key (rl.fpKey) lives only in this process's memory and
// is generated fresh at startup (newRejectLogger), so a third party reading
// the log cannot recover the Host value by hashing candidate domains
// themselves, and the same Host produces a different fingerprint in a
// different process. The trade-off (accepted at plan review, art-7c049e48
// §2.2/§2.3) is that the fingerprint cannot be correlated across a restart
// either -- see README.md's security section.
func (rl *rejectLogger) hostFingerprint(hostHeader string) string {
	name := hostHeaderName(hostHeader)
	mac := hmac.New(sha256.New, rl.fpKey)
	mac.Write([]byte(name))
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

// originKind classifies a raw Origin header value into "none" (header
// absent), "loopback" (isLoopbackOrigin) or "other", without logging the
// header's value.
func originKind(origin string) string {
	switch {
	case origin == "":
		return "none"
	case isLoopbackOrigin(origin):
		return "loopback"
	default:
		return "other"
	}
}
