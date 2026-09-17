package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/graph-ops/core-go/internal/domain"
)

func uiP(id, localPath string) uiProject {
	return uiProject{Project: domain.Project{ID: id}, LocalPath: localPath}
}

func TestFindProjectByLocalPath(t *testing.T) {
	skipOnWindows(t)
	projects := []uiProject{
		uiP("p1", "/Users/dev/foo"),
		uiP("p2", "/Users/dev/bar/"), // trailing slash, should still match once Clean()'d
	}

	if got := findProjectByLocalPath(projects, "/Users/dev/foo"); got == nil || got.ID != "p1" {
		t.Fatalf("expected p1, got %+v", got)
	}
	if got := findProjectByLocalPath(projects, "/Users/dev/bar"); got == nil || got.ID != "p2" {
		t.Fatalf("expected p2 (trailing-slash normalized), got %+v", got)
	}
	if got := findProjectByLocalPath(projects, "/Users/dev/baz"); got != nil {
		t.Fatalf("expected no match, got %+v", got)
	}
}

// TestFindProjectByLocalPath_SubdirectoryMatchesDeepest pins the DFLT-00080
// change: `ui` now uses the same deepest-containing-path rule as
// create-ticket, so a subdirectory (e.g. a git worktree) selects the
// enclosing project instead of opening the setup dialog. Before, `ui` was
// exact-match only.
func TestFindProjectByLocalPath_SubdirectoryMatchesDeepest(t *testing.T) {
	skipOnWindows(t)
	projects := []uiProject{uiP("parent", "/work/graph-ops"), uiP("child", "/work/graph-ops/packages/sub")}
	if got := findProjectByLocalPath(projects, "/work/graph-ops/.claude/worktrees/DFLT-00001"); got == nil || got.ID != "parent" {
		t.Fatalf("expected parent for a subdirectory, got %+v", got)
	}
	if got := findProjectByLocalPath(projects, "/work/graph-ops/packages/sub/x"); got == nil || got.ID != "child" {
		t.Fatalf("expected the deepest (child), got %+v", got)
	}
	if got := findProjectByLocalPath(projects, "/work/graph-opsx"); got != nil {
		t.Fatalf("expected no match across a separator boundary, got %+v", got)
	}
}

// A project without a local path in the server's environment -- e.g. one
// only another team member has set up -- never matches.
func TestFindProjectByLocalPath_UnsetNeverMatches(t *testing.T) {
	skipOnWindows(t)
	projects := []uiProject{uiP("shared", "")}
	if got := findProjectByLocalPath(projects, "/work/shared"); got != nil {
		t.Fatalf("expected no match for a project without a local path, got %+v", got)
	}
	if got := resolveTargetURL("http://localhost:3001", "/work/shared", nil); got != "http://localhost:3001/?newProject=1&workDir=%2Fwork%2Fshared" {
		t.Fatalf("unexpected URL %q", got)
	}
}

func TestFindProjectByLocalPath_FirstOfDuplicatesWins(t *testing.T) {
	skipOnWindows(t)
	projects := []uiProject{uiP("first", "/Users/dev/dup"), uiP("second", "/Users/dev/dup")}
	got := findProjectByLocalPath(projects, "/Users/dev/dup")
	if got == nil || got.ID != "first" {
		t.Fatalf("expected the first duplicate to win, got %+v", got)
	}
}

func TestResolveTargetURL_MatchedProject(t *testing.T) {
	matched := &uiProject{Project: domain.Project{ID: "p1"}, LocalPath: "/Users/dev/foo"}
	got := resolveTargetURL("http://localhost:3001", "/Users/dev/foo", matched)
	want := "http://localhost:3001/"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestResolveTargetURL_NoMatch_EncodesWorkDir(t *testing.T) {
	got := resolveTargetURL("http://localhost:3001", "/Users/dev/new project", nil)
	want := "http://localhost:3001/?newProject=1&workDir=%2FUsers%2Fdev%2Fnew+project"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestUIHealthCheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	if !uiHealthCheck(ok.URL) {
		t.Fatal("expected uiHealthCheck to report healthy for a 200 response")
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer down.Close()
	if uiHealthCheck(down.URL) {
		t.Fatal("expected uiHealthCheck to report unhealthy for a 500 response")
	}

	if uiHealthCheck("http://127.0.0.1:1") {
		t.Fatal("expected uiHealthCheck to report unhealthy when nothing is listening")
	}
}

func TestWaitForUIServer_BecomesHealthyBeforeTimeout(t *testing.T) {
	var ready atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ready.Load() {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()

	go func() {
		time.Sleep(300 * time.Millisecond)
		ready.Store(true)
	}()

	if !waitForUIServer(srv.URL, 2*time.Second) {
		t.Fatal("expected waitForUIServer to succeed once the server becomes healthy")
	}
}

func TestWaitForUIServer_TimesOut(t *testing.T) {
	if waitForUIServer("http://127.0.0.1:1", 300*time.Millisecond) {
		t.Fatal("expected waitForUIServer to time out when nothing is listening")
	}
}

// TestClientBaseURL is the regression test for DFLT-00023 item N-1: cmdUI
// health checks and calls the API at this URL, against a server it starts
// itself on rc.Host, so the two must be the same address. The 192.168.1.5 row
// is the reproduced failure -- the old hardcoded "http://localhost:%d" health
// checked an address nothing was listening on, so `ui` decided the server was
// down and tried to start a second one, which then failed to bind.
func TestClientBaseURL(t *testing.T) {
	tests := []struct {
		host string
		port int
		want string
	}{
		{"127.0.0.1", 49173, "http://127.0.0.1:49173"},
		{"127.0.0.2", 49173, "http://127.0.0.2:49173"},
		{"localhost", 49173, "http://localhost:49173"},
		{"192.168.1.5", 49173, "http://192.168.1.5:49173"},
		{"::1", 49173, "http://[::1]:49173"},
		// A wildcard bind is not a connectable address; localhost is.
		{"0.0.0.0", 49173, "http://localhost:49173"},
		{"::", 49173, "http://localhost:49173"},
		{"", 49173, "http://localhost:49173"},
	}
	for _, tt := range tests {
		if got := clientBaseURL(tt.host, tt.port); got != tt.want {
			t.Errorf("clientBaseURL(%q, %d) = %q, want %q", tt.host, tt.port, got, tt.want)
		}
	}
}

// TestHumanBaseURL covers the URL `serve` prints and `ui` opens in a browser
// (DFLT-00023 item N-3): "localhost" only where it really reaches the same
// listener, the concrete bind address everywhere else.
func TestHumanBaseURL(t *testing.T) {
	tests := []struct {
		host string
		port int
		want string
	}{
		// Unchanged for the default configuration.
		{"127.0.0.1", 49173, "http://localhost:49173"},
		{"::1", 49173, "http://localhost:49173"},
		{"localhost", 49173, "http://localhost:49173"},
		{"0.0.0.0", 49173, "http://localhost:49173"},
		{"", 49173, "http://localhost:49173"},
		// localhost does NOT reach a server bound only to these, so the real
		// address has to be the one advertised.
		{"127.0.0.2", 49173, "http://127.0.0.2:49173"},
		{"192.168.1.5", 49173, "http://192.168.1.5:49173"},
	}
	for _, tt := range tests {
		if got := humanBaseURL(tt.host, tt.port); got != tt.want {
			t.Errorf("humanBaseURL(%q, %d) = %q, want %q", tt.host, tt.port, got, tt.want)
		}
	}
}

// TestBaseURLsAgreeOnHostForNonLoopbackBinds guards the invariant the Host
// header check (DFLT-00023 item S-2) now depends on: whatever host either URL
// names, it must be one the server bound to -- otherwise `ui`'s own health
// check, or the browser it opens, would be rejected by the server it just
// started.
func TestBaseURLsAgreeOnHostForNonLoopbackBinds(t *testing.T) {
	for _, host := range []string{"192.168.1.5", "127.0.0.2"} {
		if got, want := clientBaseURL(host, 49173), "http://"+host+":49173"; got != want {
			t.Errorf("clientBaseURL(%q) = %q, want %q", host, got, want)
		}
		if got, want := humanBaseURL(host, 49173), "http://"+host+":49173"; got != want {
			t.Errorf("humanBaseURL(%q) = %q, want %q", host, got, want)
		}
	}
}
