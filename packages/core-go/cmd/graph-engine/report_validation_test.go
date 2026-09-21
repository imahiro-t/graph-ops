package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
)

func newTestRepo(t *testing.T) store.GraphRepository {
	t.Helper()
	repo, err := store.NewSQLiteRepository(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	if err := repo.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return repo
}

// newTestRepoWithProject is newTestRepo plus a ready-to-use project (and
// current-project selected), for tests that need CreateTicket's now-required
// projectID or exercise CLI commands (like create-ticket) that default to
// the current project.
func newTestRepoWithProject(t *testing.T) (store.GraphRepository, string) {
	t.Helper()
	repo := newTestRepo(t)
	proj, err := repo.CreateProject("Test Project", "TEST")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := repo.SetCurrentProjectID(proj.ID); err != nil {
		t.Fatalf("SetCurrentProjectID: %v", err)
	}
	return repo, proj.ID
}

func mustCreateTicketAndNode(t *testing.T, repo store.GraphRepository, projectID string, nodeType domain.NodeType) (ticketID, nodeID string) {
	t.Helper()
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	// approval_gate and release are manual node types, and ExpandGraph
	// forces is_manual on them in a real graph; the fixture has to do the
	// same or it produces a node that could not exist. It matters since
	// DFLT-00102: a manual node may be completed straight from TODO (that
	// is where a human judges it, since get-executable never claims one),
	// while an automatic one at TODO is refused.
	node, err := repo.CreateNode(domain.GraphNode{
		TicketID: ticket.ID, Name: "n", Type: nodeType, Status: domain.NodeTODO,
		IsManual: nodeType == domain.NodeTypeApprovalGate || nodeType == domain.NodeTypeRelease,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	return ticket.ID, node.ID
}

func writeTempHTML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact.html")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it, for asserting on a CLI command's exact stdout
// output. Shared by every cmd/graph-engine test file that checks printJSON
// output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return string(buf)
}

func contains(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) && indexOfCLI(s, substr) >= 0)
}

func indexOfCLI(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// TestValidateReportArtifactIfNeeded_RejectsNonConformingReportHTML covers
// the "add-artifact rejects a report HTML file that does not match the fixed
// template" Gherkin scenario at the CLI-wiring level (the marker checks
// themselves are covered by internal/config's ValidateReportHTML tests).
func TestValidateReportArtifactIfNeeded_RejectsNonConformingReportHTML(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	_, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeReport)
	path := writeTempHTML(t, "<html><body>free-form report</body></html>")

	if err := validateReportArtifactIfNeeded(repo, nodeID, &path); err == nil {
		t.Fatal("expected a validation error for non-conforming report HTML")
	}
}

// TestValidateReportArtifactIfNeeded_AcceptsConformingReportHTML covers the
// happy-path counterpart: a file that carries the fixed template's markers
// passes.
func TestValidateReportArtifactIfNeeded_AcceptsConformingReportHTML(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	_, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeReport)
	path := writeTempHTML(t, config.ResolveReportTemplate(config.Roots{}))

	if err := validateReportArtifactIfNeeded(repo, nodeID, &path); err != nil {
		t.Fatalf("expected the default template itself to validate cleanly, got %v", err)
	}
}

// TestValidateReportArtifactIfNeeded_DoesNotEnforceForNonReportNodes covers
// "add-artifact does not enforce the report template shape for non-report
// nodes" -- arbitrary HTML must be accepted for e.g. an implementation node.
func TestValidateReportArtifactIfNeeded_DoesNotEnforceForNonReportNodes(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	_, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeImplementation)
	path := writeTempHTML(t, "<html><body>anything goes here</body></html>")

	if err := validateReportArtifactIfNeeded(repo, nodeID, &path); err != nil {
		t.Errorf("expected no validation for a non-report node, got %v", err)
	}
}

// TestLoadRuntimeConfig_ExtensionsDirs mirrors the existing
// TestLoadRuntimeConfig_TerminalWorkDir precedence-table pattern for the two
// new extension-directory settings: env var > the home config file > default
// (empty, which for the user tier means $HOME/.graph-ops and for the team
// tier means no team tier at all -- see internal/config.ResolveRoots).
func TestLoadRuntimeConfig_ExtensionsDirs(t *testing.T) {
	// stubHome is mandatory for every loadRuntimeConfig test, including the
	// ones that only care about unrelated settings: the call unconditionally
	// creates artifactsDir, which now defaults to $HOME/.graph-ops/artifacts,
	// so without a stubbed home `go test` silently creates that directory in
	// the developer's real home and leaves it behind. See stubHome's doc
	// comment in runtime_config_test.go.
	home := stubHome(t)
	tempCwd(t)

	t.Setenv("GRAPH_USER_EXTENSIONS_DIR", "/tmp/env-user-ext")
	t.Setenv("GRAPH_TEAM_EXTENSIONS_DIR", "")
	writeHomeConfig(t, home, runtimeconfig.FileConfig{
		UserExtensionsDir: "/tmp/json-user-ext",
		TeamExtensionsDir: "/tmp/json-team-ext",
	})

	rc, err := loadRuntimeConfig()
	if err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if rc.UserExtensionsDir != "/tmp/env-user-ext" {
		t.Errorf("UserExtensionsDir = %q, want env var to win", rc.UserExtensionsDir)
	}
	if rc.TeamExtensionsDir != "/tmp/json-team-ext" {
		t.Errorf("TeamExtensionsDir = %q, want the home config's value since env is unset", rc.TeamExtensionsDir)
	}
}
