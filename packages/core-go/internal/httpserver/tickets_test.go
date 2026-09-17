package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/graph-ops/core-go/internal/config"
	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
	"github.com/graph-ops/core-go/internal/runtimeconfig"
	"github.com/graph-ops/core-go/internal/store"
)

// testHost is the Host header every test request carries. httptest.NewRequest
// defaults it to "example.com", which withAllowedHost now (correctly)
// rejects with 403 -- that is the whole point of the DNS rebinding defense
// added for DFLT-00023 item S-2. A loopback host:port pair is what a real
// browser, the Vite dev proxy and `graph-engine ui` all send.
const testHost = "127.0.0.1:49173"

// newTestServer sets up a Server backed by a fresh SQLite DB and a temp
// ArtifactsDir, mirroring internal/engine's newTestEngine helper. It also
// creates a project and selects it as current, so tests that POST to
// /api/tickets without an explicit project_id (as every test here predates
// project support and does) keep working unchanged; the project's ID is
// returned for tests that call repo.CreateTicket directly.
func newTestServer(t *testing.T) (*Server, store.GraphRepository, string) {
	t.Helper()
	repo, err := store.NewSQLiteRepository(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteRepository: %v", err)
	}
	if err := repo.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	proj, err := repo.CreateProject("Test Project", "TEST")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := repo.SetCurrentProjectID(proj.ID); err != nil {
		t.Fatalf("SetCurrentProjectID: %v", err)
	}
	eng := engine.New(repo)
	// HomeDir is sandboxed so graph-config.json (projectPaths) is written
	// under a temp dir; the project gets its own temp local path, standing in
	// for the DB work_dir it had before DFLT-00080.
	cfg := Config{ArtifactsDir: t.TempDir(), HomeDir: t.TempDir()}
	s := New(repo, eng, cfg)
	if _, err := runtimeconfig.SetProjectPath(cfg.WorkDir, cfg.HomeDir, proj.ID, t.TempDir()); err != nil {
		t.Fatalf("SetProjectPath: %v", err)
	}
	return s, repo, proj.ID
}

// testProjectLocalPath returns projectID's local path in s's graph-config.json,
// failing the test if none is set.
func testProjectLocalPath(t *testing.T, s *Server, projectID string) string {
	t.Helper()
	p := s.projectLocalPath(projectID)
	if p == "" {
		t.Fatalf("project %s has no local path in the test server's graph-config.json", projectID)
	}
	return p
}

// doJSON issues an HTTP request against s's routes and returns the recorded
// response, marshaling body (if non-nil) as the request's JSON payload and
// stamping csrfHeaderName so non-GET requests aren't rejected by withCORS
// before the handler ever runs. Shared by every httpserver test file that
// exercises handlers through Routes() rather than calling them directly.
func doJSON(t *testing.T, s *Server, method, path string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Host = testHost
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeaderName, "1")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	return rec
}

// decodeError unmarshals rec's body as the standard {"error": {...}} envelope
// (see writeError) into its apiErrorPayload.
func decodeError(t *testing.T, rec *httptest.ResponseRecorder) apiErrorPayload {
	t.Helper()
	var body struct {
		Error apiErrorPayload `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v (body: %s)", err, rec.Body.String())
	}
	return body.Error
}

// newReportNode creates a ticket with a single "report"-type node and
// returns (ticketID, nodeID).
func newReportNode(t *testing.T, repo store.GraphRepository, projectID string) (string, string) {
	t.Helper()
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Description: "d", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := repo.CreateNode(domain.GraphNode{
		ID:       "node-" + engine.NewArtifactID(),
		TicketID: ticket.ID,
		Name:     "Report",
		Type:     domain.NodeTypeReport,
		Status:   domain.NodeTODO,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	return ticket.ID, node.ID
}

const validReportHTML = `<html data-report-template="t" data-report-version="1">
<header data-report-section="header">h</header>
<section data-report-section="summary">s</section>
<section data-report-section="results">r</section>
<footer data-report-section="footer">f</footer>
</html>`

const invalidReportHTML = `<html><body>free-form, no markers</body></html>`

func postArtifact(t *testing.T, s *Server, ticketID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/tickets/"+ticketID+"/artifacts", bytes.NewReader(raw))
	req.Host = testHost
	req.Header.Set("Content-Type", "application/json")
	// Every non-GET request must carry this (see server.go's csrfHeaderName)
	// or withCORS rejects it with 403 before the handler ever runs.
	req.Header.Set(csrfHeaderName, "1")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	return rec
}

// (a) An absolute file_path must never be read directly off the local
// filesystem by the HTTP artifact-creation endpoint -- see Security Review
// node-47c93a46, blocking finding #1. Point it at a real, readable file
// outside ArtifactsDir containing valid report markers; if the old
// unsandboxed os.ReadFile(path) behavior were still in place this would
// succeed (200/201). It must instead be rejected (400) without ever being
// read as an absolute path.
func TestHandleCreateArtifact_AbsoluteFilePathRejected(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticketID, nodeID := newReportNode(t, repo, projectID)

	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "sneaky.html")
	if err := os.WriteFile(outsidePath, []byte(validReportHTML), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rec := postArtifact(t, s, ticketID, map[string]any{
		"node_id":   nodeID,
		"name":      "Report",
		"type":      "html",
		"file_path": outsidePath,
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for absolute file_path, got %d: %s", rec.Code, rec.Body.String())
	}

	nodes, err := repo.ListArtifactsByTicket(ticketID)
	if err != nil {
		t.Fatalf("ListArtifactsByTicket: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("expected no artifact row to be created, got %d", len(nodes))
	}
}

// A relative file_path that resolves (after Join+Clean) outside
// ArtifactsDir via "../" traversal must be rejected the same way.
func TestHandleCreateArtifact_RelativeTraversalFilePathRejected(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticketID, nodeID := newReportNode(t, repo, projectID)

	// Plant a file one level above ArtifactsDir and try to reach it with "..".
	outsidePath := filepath.Join(filepath.Dir(s.cfg.ArtifactsDir), "sneaky.html")
	if err := os.WriteFile(outsidePath, []byte(validReportHTML), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	defer os.Remove(outsidePath)

	rec := postArtifact(t, s, ticketID, map[string]any{
		"node_id":   nodeID,
		"name":      "Report",
		"type":      "html",
		"file_path": "../sneaky.html",
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for traversal file_path, got %d: %s", rec.Code, rec.Body.String())
	}
}

// A relative file_path that resolves inside ArtifactsDir and contains valid
// report markers is still accepted -- the fix must not break the legitimate
// case.
func TestHandleCreateArtifact_RelativeFilePathWithinArtifactsDirAccepted(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticketID, nodeID := newReportNode(t, repo, projectID)

	if err := os.WriteFile(filepath.Join(s.cfg.ArtifactsDir, "good.html"), []byte(validReportHTML), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rec := postArtifact(t, s, ticketID, map[string]any{
		"node_id":   nodeID,
		"name":      "Report",
		"type":      "html",
		"file_path": "good.html",
	})

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for valid in-sandbox report html, got %d: %s", rec.Code, rec.Body.String())
	}
}

// (c) Inline `content` (no file_path) for a report-node html artifact must
// be validated against the fixed template just like file_path-sourced
// content -- see QA Review node-5ff1dd13's non-blocking gap.
func TestHandleCreateArtifact_InlineContentValidatedForReportNode(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticketID, nodeID := newReportNode(t, repo, projectID)

	rec := postArtifact(t, s, ticketID, map[string]any{
		"node_id": nodeID,
		"name":    "Report",
		"type":    "html",
		"content": invalidReportHTML,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-conforming inline content, got %d: %s", rec.Code, rec.Body.String())
	}

	artifacts, err := repo.ListArtifactsByTicket(ticketID)
	if err != nil {
		t.Fatalf("ListArtifactsByTicket: %v", err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("expected no artifact row for rejected inline content, got %d", len(artifacts))
	}

	rec = postArtifact(t, s, ticketID, map[string]any{
		"node_id": nodeID,
		"name":    "Report",
		"type":    "html",
		"content": validReportHTML,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for conforming inline content, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Inline content on a non-report node is unaffected -- validation stays
// scoped to report-type nodes only.
func TestHandleCreateArtifact_InlineContentNotValidatedForNonReportNode(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Description: "d", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := repo.CreateNode(domain.GraphNode{
		ID:       "node-" + engine.NewArtifactID(),
		TicketID: ticket.ID,
		Name:     "Impl",
		Type:     domain.NodeTypeImplementation,
		Status:   domain.NodeTODO,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	rec := postArtifact(t, s, ticket.ID, map[string]any{
		"node_id": node.ID,
		"name":    "Notes",
		"type":    "html",
		"content": invalidReportHTML,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 (no report-format enforcement on non-report nodes), got %d: %s", rec.Code, rec.Body.String())
	}
}

// Scenario (DFLT-00051 execution plan, section 3.1/4 item 8): a ticket's
// first-ever GET /api/tickets/{id}/executable-nodes poll -- the Web UI's
// seeding path (handleExecutableNodes -> loadCatalogForTicket ->
// engine.GetExecutableNodes -> EnsureGraphStarted) -- must localize the
// seeded plan/plan_review node names exactly the way the CLI's
// get-executable/expand-graph would for the same team-tier language
// setting, since both ultimately resolve through the same
// config.LoadWithRoots. The project's local path (graph-config.json's
// projectPaths) is what loadCatalogForTicket resolves the team tier from
// here (s.cfg has no TeamExtensionsDir override -- see newTestServer),
// matching how the settings UI always writes project-scoped overrides under
// that local path.
func TestHandleExecutableNodes_SeedsLocalizedNamesFromProjectLanguage(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	project, err := repo.GetProject(projectID)
	if err != nil || project == nil {
		t.Fatalf("GetProject: %v", err)
	}

	teamDir := filepath.Join(testProjectLocalPath(t, s, project.ID), ".graph-ops")
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "workflow.yaml"), []byte("version: 1\nlanguage: ja\n"), 0o644); err != nil {
		t.Fatalf("write workflow.yaml: %v", err)
	}

	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO, AutoExecutable: true})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	rec := doJSON(t, s, http.MethodGet, "/api/tickets/"+ticket.ID+"/executable-nodes", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET executable-nodes expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var nodes []domain.GraphNode
	if err := json.Unmarshal(rec.Body.Bytes(), &nodes); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
	}
	if len(nodes) == 0 {
		t.Fatalf("expected at least one executable (seed) node, got none")
	}
	var planName string
	for _, n := range nodes {
		if n.ConfigID != nil && *n.ConfigID == "plan" {
			planName = n.Name
		}
	}
	if planName != "計画作成" {
		t.Errorf("seeded plan node Name = %q, want 計画作成 (localized via the project's team-tier language)", planName)
	}

	// Cross-check against what the CLI's own resolution path
	// (config.LoadWithRoots, no --language override) would produce for the
	// same project local path -- the two entry points must never disagree.
	cliCat, err := config.LoadWithRoots(testProjectLocalPath(t, s, project.ID), "", "", "")
	if err != nil {
		t.Fatalf("config.LoadWithRoots: %v", err)
	}
	var cliPlanName string
	for _, n := range cliCat.Nodes {
		if n.ID == "plan" {
			cliPlanName = n.Name
		}
	}
	if cliPlanName != planName {
		t.Errorf("CLI-path plan name %q must match HTTP-path plan name %q", cliPlanName, planName)
	}
}
