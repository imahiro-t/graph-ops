package httpserver

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
	"github.com/graph-ops/core-go/internal/engine"
)

func getArtifactContent(t *testing.T, s *Server, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/artifacts/"+id+"/content", nil)
	req.Host = testHost
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	return rec
}

// TestHandleGetArtifactContent_ServesHTMLFromDBContent is the core DFLT-00006
// regression: once an html artifact has its bytes in the DB (via file_path
// converted on create, see handleCreateArtifact), the content endpoint must
// serve those bytes directly -- no local file needs to exist for this to
// work, which is the point of storing artifacts in a DB a team can share.
func TestHandleGetArtifactContent_ServesHTMLFromDBContent(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := repo.CreateNode(domain.GraphNode{
		ID: "node-" + engine.NewArtifactID(), TicketID: ticket.ID, Name: "Impl",
		Type: domain.NodeTypeImplementation, Status: domain.NodeTODO,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	// Create via file_path, exactly like a subagent invoking add-artifact
	// with a file on disk -- handleCreateArtifact must convert this into
	// stored Content.
	if err := os.WriteFile(filepath.Join(s.cfg.ArtifactsDir, "note.html"), []byte("<html><body>hi</body></html>"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	rec := postArtifact(t, s, ticket.ID, map[string]any{
		"node_id": node.ID, "name": "Note", "type": "html", "file_path": "note.html",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("postArtifact: %d %s", rec.Code, rec.Body.String())
	}
	var created domain.Artifact
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decoding created artifact: %v", err)
	}

	// Delete the local file entirely -- the whole point is that content
	// serving must not depend on it once it's in the DB.
	if err := os.Remove(filepath.Join(s.cfg.ArtifactsDir, "note.html")); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	contentRec := getArtifactContent(t, s, created.ID)
	if contentRec.Code != http.StatusOK {
		t.Fatalf("GET content: %d %s", contentRec.Code, contentRec.Body.String())
	}
	if got := contentRec.Body.String(); got != "<html><body>hi</body></html>" {
		t.Errorf("body = %q, want the original html", got)
	}
	if ct := contentRec.Header().Get("Content-Type"); ct == "" {
		t.Error("expected a Content-Type header")
	}
}

// TestHandleGetArtifactContent_DecodesBase64Image covers the image path:
// base64 Content must be decoded back to raw bytes with an image
// Content-Type.
func TestHandleGetArtifactContent_DecodesBase64Image(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := repo.CreateNode(domain.GraphNode{
		ID: "node-" + engine.NewArtifactID(), TicketID: ticket.ID, Name: "Impl",
		Type: domain.NodeTypeImplementation, Status: domain.NodeTODO,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	raw := []byte("fake-png-bytes")
	content := base64.StdEncoding.EncodeToString(raw)
	metadata := `{"encoding":"base64","mime_type":"image/png"}`
	created, err := repo.CreateArtifact(domain.Artifact{
		ID: engine.NewArtifactID(), TicketID: ticket.ID, NodeID: node.ID, Name: "Shot",
		Type: domain.ArtifactImage, Content: &content, Metadata: &metadata,
	})
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	rec := getArtifactContent(t, s, created.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET content: %d %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != string(raw) {
		t.Errorf("body = %q, want %q", rec.Body.String(), raw)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
}

// TestHandleCreateArtifact_ImageFilePathRejectsNonImageBytes covers Security
// Review art-9e220d80 finding #2 at the HTTP layer: a file_path submitted as
// type "image" whose bytes don't match a recognized image signature (e.g.
// an SVG/HTML payload saved with a .png name) must be rejected at creation
// time, not stored -- storing it would let GET /api/artifacts/{id}/content
// later serve it with a sniffed Content-Type a browser could execute as
// script (content-type confusion / stored XSS).
func TestHandleCreateArtifact_ImageFilePathRejectsNonImageBytes(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := repo.CreateNode(domain.GraphNode{
		ID: "node-" + engine.NewArtifactID(), TicketID: ticket.ID, Name: "Impl",
		Type: domain.NodeTypeImplementation, Status: domain.NodeTODO,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	if err := os.WriteFile(filepath.Join(s.cfg.ArtifactsDir, "not-really.png"), []byte("<script>alert(document.domain)</script>"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rec := postArtifact(t, s, ticket.ID, map[string]any{
		"node_id": node.ID, "name": "Shot", "type": "image", "file_path": "not-really.png",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-image bytes submitted as image, got %d: %s", rec.Code, rec.Body.String())
	}

	artifacts, err := repo.ListArtifactsByTicket(ticket.ID)
	if err != nil {
		t.Fatalf("ListArtifactsByTicket: %v", err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("expected no artifact row to be created for the rejected image, got %d", len(artifacts))
	}
}

// TestHandleCreateArtifact_InlineImageContentRejectsNonImageBytes covers
// Security Review art-0e621b48: submitting image bytes via the JSON body's
// "content" field directly (rather than file_path) used to skip
// validateImageBytes entirely, letting a caller register arbitrary
// (non-image) bytes as an "image" artifact. It must be rejected at creation
// time exactly like the file_path path already is.
func TestHandleCreateArtifact_InlineImageContentRejectsNonImageBytes(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := repo.CreateNode(domain.GraphNode{
		ID: "node-" + engine.NewArtifactID(), TicketID: ticket.ID, Name: "Impl",
		Type: domain.NodeTypeImplementation, Status: domain.NodeTODO,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	// Not valid base64 image bytes at all -- an attacker-controlled payload
	// the client falsely claims is a PNG via metadata.mime_type.
	payload := base64.StdEncoding.EncodeToString([]byte("<script>alert(document.domain)</script>"))
	rec := postArtifact(t, s, ticket.ID, map[string]any{
		"node_id": node.ID, "name": "Shot", "type": "image", "content": payload,
		"metadata": `{"mime_type":"text/html"}`,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-image inline content submitted as image, got %d: %s", rec.Code, rec.Body.String())
	}

	artifacts, err := repo.ListArtifactsByTicket(ticket.ID)
	if err != nil {
		t.Fatalf("ListArtifactsByTicket: %v", err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("expected no artifact row to be created for the rejected image, got %d", len(artifacts))
	}
}

// TestHandleCreateArtifact_InlineContentIgnoresClientClaimedMimeType covers
// the other half of Security Review art-0e621b48: even when inline content
// legitimately matches its declared artifact type, the client's claimed
// metadata.mime_type must never be trusted verbatim -- the server always
// derives/fixes it itself (detected from the image's magic bytes, or the
// fixed text/html value for html), so a spoofed mime_type in the request
// body cannot make GET /api/artifacts/{id}/content serve stored bytes with
// an attacker-chosen Content-Type.
func TestHandleCreateArtifact_InlineContentIgnoresClientClaimedMimeType(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := repo.CreateNode(domain.GraphNode{
		ID: "node-" + engine.NewArtifactID(), TicketID: ticket.ID, Name: "Impl",
		Type: domain.NodeTypeImplementation, Status: domain.NodeTODO,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	// A genuine (1x1) PNG, but the request falsely claims it's HTML.
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 'r', 'e', 's', 't'}
	rec := postArtifact(t, s, ticket.ID, map[string]any{
		"node_id": node.ID, "name": "Shot", "type": "image",
		"content":  base64.StdEncoding.EncodeToString(png),
		"metadata": `{"mime_type":"text/html","encoding":"base64"}`,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("postArtifact: %d %s", rec.Code, rec.Body.String())
	}
	var created domain.Artifact
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decoding created artifact: %v", err)
	}

	contentRec := getArtifactContent(t, s, created.ID)
	if contentRec.Code != http.StatusOK {
		t.Fatalf("GET content: %d %s", contentRec.Code, contentRec.Body.String())
	}
	if ct := contentRec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want server-detected image/png (client-claimed text/html must be ignored)", ct)
	}
	if got := contentRec.Body.Bytes(); string(got) != string(png) {
		t.Errorf("body = %q, want the original png bytes", got)
	}
}

// TestWriteArtifactContent_EscapesFilenameInContentDisposition covers
// Security Review art-9e220d80 finding #3: OriginalFilename is untrusted
// (derived from filepath.Base() of whatever a caller supplied as
// file_path). Building `inline; filename="` + filename + `"` directly, as
// an earlier version of writeArtifactContent did, let a filename containing
// a double quote break out of the quoted-string and inject additional
// header directives.
func TestWriteArtifactContent_EscapesFilenameInContentDisposition(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := repo.CreateNode(domain.GraphNode{
		ID: "node-" + engine.NewArtifactID(), TicketID: ticket.ID, Name: "Impl",
		Type: domain.NodeTypeImplementation, Status: domain.NodeTODO,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	content := "<html><body>x</body></html>"
	metadata := `{"mime_type":"text/html; charset=utf-8","original_filename":"evil\".html; attachment; filename=\"pwned.html"}`
	created, err := repo.CreateArtifact(domain.Artifact{
		ID: engine.NewArtifactID(), TicketID: ticket.ID, NodeID: node.ID, Name: "Evil",
		Type: domain.ArtifactHTML, Content: &content, Metadata: &metadata,
	})
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	rec := getArtifactContent(t, s, created.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET content: %d %s", rec.Code, rec.Body.String())
	}
	cd := rec.Header().Get("Content-Disposition")
	if strings.Contains(cd, `"; attachment; filename="pwned`) {
		t.Fatalf("Content-Disposition = %q, filename broke out of its quoted-string", cd)
	}
	if strings.Count(cd, `"`) != 2 {
		t.Errorf("Content-Disposition = %q, want exactly one quoted-string (2 quote characters)", cd)
	}
}

// TestHandleGetArtifactContent_TextArtifactNeverServedAsHTML is the direct
// regression test for Security Review art-260ce0f7 (3rd security review,
// DFLT-00006): Payload used to default every non-image artifact type to
// "text/html; charset=utf-8" whenever metadata.mime_type was unset, and
// text/gherkin/json artifacts never get a mime_type (only html/image go
// through EncodeFile/EncodeInline -- see handleCreateArtifact's switch,
// which leaves content/metadata untouched for every other type). A caller
// could therefore add-artifact a type:"text" artifact containing
// `<script>...</script>` and have the unauthenticated
// GET /api/artifacts/{id}/content serve it as text/html -- a browser
// visiting that URL directly (or via an <iframe>/redirect the CSRF header
// check on state-changing APIs does nothing to stop, since this is a GET)
// would execute it: stored XSS. This posts exactly that payload through the
// same HTTP path add-artifact/a subagent would use and asserts the served
// Content-Type can never be interpreted as HTML by a browser.
func TestHandleGetArtifactContent_TextArtifactNeverServedAsHTML(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := repo.CreateNode(domain.GraphNode{
		ID: "node-" + engine.NewArtifactID(), TicketID: ticket.ID, Name: "Impl",
		Type: domain.NodeTypeImplementation, Status: domain.NodeTODO,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	const payload = `<script>alert(document.domain)</script>`
	rec := postArtifact(t, s, ticket.ID, map[string]any{
		"node_id": node.ID, "name": "Finding", "type": "text", "content": payload,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("postArtifact: %d %s", rec.Code, rec.Body.String())
	}
	var created domain.Artifact
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decoding created artifact: %v", err)
	}

	contentRec := getArtifactContent(t, s, created.ID)
	if contentRec.Code != http.StatusOK {
		t.Fatalf("GET content: %d %s", contentRec.Code, contentRec.Body.String())
	}
	if got := contentRec.Body.String(); got != payload {
		t.Errorf("body = %q, want the original content %q", got, payload)
	}
	ct := contentRec.Header().Get("Content-Type")
	if strings.HasPrefix(ct, "text/html") || strings.Contains(ct, "xml") {
		t.Fatalf("Content-Type = %q, a browser could render/execute the stored <script> as this type (stored XSS)", ct)
	}
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain*", ct)
	}
}

// TestHandleCreateArtifact_TextMetadataMimeTypeRejected is the direct
// regression test for Security Review art-3d521dc0 (4th security review,
// DFLT-00006): handleCreateArtifact only ran client-supplied content/metadata
// through EncodeInline (which fixes/overwrites mime_type server-side) for
// type html/image; for text/gherkin/json it stored body.Metadata verbatim,
// so POSTing type:"text", content:"<script>...</script>",
// metadata:{"mime_type":"text/html; charset=utf-8"} made
// GET /api/artifacts/{id}/content serve the stored script as HTML -- stored
// XSS (confirmed against the live server per the finding). The request must
// now be rejected outright at creation time, and no artifact row created.
func TestHandleCreateArtifact_TextMetadataMimeTypeRejected(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := repo.CreateNode(domain.GraphNode{
		ID: "node-" + engine.NewArtifactID(), TicketID: ticket.ID, Name: "Impl",
		Type: domain.NodeTypeImplementation, Status: domain.NodeTODO,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	for _, artType := range []string{"text", "gherkin", "json"} {
		t.Run(artType, func(t *testing.T) {
			rec := postArtifact(t, s, ticket.ID, map[string]any{
				"node_id": node.ID, "name": "Finding", "type": artType,
				"content":  `<script>alert(document.domain)</script>`,
				"metadata": `{"mime_type":"text/html; charset=utf-8"}`,
			})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for metadata.mime_type on artifact type %q, got %d: %s", artType, rec.Code, rec.Body.String())
			}
		})
	}

	artifacts, err := repo.ListArtifactsByTicket(ticket.ID)
	if err != nil {
		t.Fatalf("ListArtifactsByTicket: %v", err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("expected no artifact row to be created for any rejected request, got %d", len(artifacts))
	}
}

// TestHandleCreateArtifact_TextMetadataWithoutMimeTypeStillAllowed guards
// against overcorrecting the art-3d521dc0 fix: metadata is still allowed for
// text/gherkin/json artifacts as long as it doesn't smuggle a mime_type (e.g.
// any other bookkeeping field a future caller might add), and the served
// Content-Type must still be the safe text/plain default, never HTML.
func TestHandleCreateArtifact_TextMetadataWithoutMimeTypeStillAllowed(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := repo.CreateNode(domain.GraphNode{
		ID: "node-" + engine.NewArtifactID(), TicketID: ticket.ID, Name: "Impl",
		Type: domain.NodeTypeImplementation, Status: domain.NodeTODO,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	rec := postArtifact(t, s, ticket.ID, map[string]any{
		"node_id": node.ID, "name": "Finding", "type": "text", "content": "plain finding text",
		"metadata": `{"original_filename":"note.txt"}`,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("postArtifact: %d %s", rec.Code, rec.Body.String())
	}
	var created domain.Artifact
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decoding created artifact: %v", err)
	}

	contentRec := getArtifactContent(t, s, created.ID)
	if contentRec.Code != http.StatusOK {
		t.Fatalf("GET content: %d %s", contentRec.Code, contentRec.Body.String())
	}
	if ct := contentRec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain*", ct)
	}
}

// TestHandleCreateArtifact_TextFilePathRejected is the direct regression test
// for Security Review art-65fe221a (5th security review, DFLT-00006):
// handleCreateArtifact's file_path->Content conversion switch only ever
// covers html/image, so a type:"text" (or gherkin/json) POST with only
// file_path (no inline content) must be rejected outright rather than
// created with Content left nil and FilePath stored as-is.
func TestHandleCreateArtifact_TextFilePathRejected(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := repo.CreateNode(domain.GraphNode{
		ID: "node-" + engine.NewArtifactID(), TicketID: ticket.ID, Name: "Impl",
		Type: domain.NodeTypeImplementation, Status: domain.NodeTODO,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	if err := os.WriteFile(filepath.Join(s.cfg.ArtifactsDir, "finding.txt"), []byte(`<script>alert(document.domain)</script>`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	for _, artType := range []string{"text", "gherkin", "json"} {
		t.Run(artType, func(t *testing.T) {
			rec := postArtifact(t, s, ticket.ID, map[string]any{
				"node_id": node.ID, "name": "Finding", "type": artType, "file_path": "finding.txt",
			})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for file_path on artifact type %q, got %d: %s", artType, rec.Code, rec.Body.String())
			}
		})
	}

	artifacts, err := repo.ListArtifactsByTicket(ticket.ID)
	if err != nil {
		t.Fatalf("ListArtifactsByTicket: %v", err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("expected no artifact row to be created for any rejected request, got %d", len(artifacts))
	}
}

// TestHandleGetArtifactContent_SetsSandboxCSPAndNosniff is the direct
// regression test for DFLT-00053: opening an artifact's content URL directly
// (e.g. the Web UI's "open in new tab" link, which -- unlike the sandboxed
// <iframe> used for inline preview -- has no isolation of its own) used to
// render agent-authored HTML as a same-origin top-level document, giving any
// script in it same-origin access to the whole unauthenticated API surface
// and defeating the Origin-based CSRF check. Every response from this
// endpoint must now carry Content-Security-Policy: sandbox allow-scripts
// (forces an opaque origin, so isLoopbackOrigin rejects the CSRF-relevant
// Origin header it sends) and X-Content-Type-Options: nosniff, regardless of
// artifact type -- writeArtifactContent applies both unconditionally.
func TestHandleGetArtifactContent_SetsSandboxCSPAndNosniff(t *testing.T) {
	s, repo, projectID := newTestServer(t)
	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := repo.CreateNode(domain.GraphNode{
		ID: "node-" + engine.NewArtifactID(), TicketID: ticket.ID, Name: "Impl",
		Type: domain.NodeTypeImplementation, Status: domain.NodeTODO,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	const wantCSP = "sandbox allow-scripts"
	const wantNosniff = "nosniff"

	cases := []struct {
		name    string
		artType string
		content string
	}{
		{"html", "html", "<html><body>hi</body></html>"},
		{"text", "text", "plain finding text"},
		{"gherkin", "gherkin", "Feature: x\n"},
		{"json", "json", `{"a":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postArtifact(t, s, ticket.ID, map[string]any{
				"node_id": node.ID, "name": "Finding", "type": tc.artType, "content": tc.content,
			})
			if rec.Code != http.StatusCreated {
				t.Fatalf("postArtifact: %d %s", rec.Code, rec.Body.String())
			}
			var created domain.Artifact
			if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
				t.Fatalf("decoding created artifact: %v", err)
			}

			contentRec := getArtifactContent(t, s, created.ID)
			if contentRec.Code != http.StatusOK {
				t.Fatalf("GET content: %d %s", contentRec.Code, contentRec.Body.String())
			}
			if got := contentRec.Header().Get("Content-Security-Policy"); got != wantCSP {
				t.Errorf("Content-Security-Policy = %q, want %q", got, wantCSP)
			}
			if got := contentRec.Header().Get("X-Content-Type-Options"); got != wantNosniff {
				t.Errorf("X-Content-Type-Options = %q, want %q", got, wantNosniff)
			}
		})
	}

	// The image path goes through a different branch of Payload/metadata but
	// must get the same unconditional headers.
	t.Run("image", func(t *testing.T) {
		raw := []byte("fake-png-bytes")
		content := base64.StdEncoding.EncodeToString(raw)
		metadata := `{"encoding":"base64","mime_type":"image/png"}`
		created, err := repo.CreateArtifact(domain.Artifact{
			ID: engine.NewArtifactID(), TicketID: ticket.ID, NodeID: node.ID, Name: "Shot",
			Type: domain.ArtifactImage, Content: &content, Metadata: &metadata,
		})
		if err != nil {
			t.Fatalf("CreateArtifact: %v", err)
		}
		rec := getArtifactContent(t, s, created.ID)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET content: %d %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Security-Policy"); got != wantCSP {
			t.Errorf("Content-Security-Policy = %q, want %q", got, wantCSP)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != wantNosniff {
			t.Errorf("X-Content-Type-Options = %q, want %q", got, wantNosniff)
		}
	})

	// download=1 (the Web UI's Download button) must carry the same headers
	// as inline preview -- the isolation is unconditional on the attachment
	// flag, not just the default inline path.
	t.Run("download=1", func(t *testing.T) {
		rec := postArtifact(t, s, ticket.ID, map[string]any{
			"node_id": node.ID, "name": "Finding", "type": "html",
			"content": "<html><body>dl</body></html>",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("postArtifact: %d %s", rec.Code, rec.Body.String())
		}
		var created domain.Artifact
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatalf("decoding created artifact: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/artifacts/"+created.ID+"/content?download=1", nil)
		req.Host = testHost
		dlRec := httptest.NewRecorder()
		s.Routes().ServeHTTP(dlRec, req)
		if dlRec.Code != http.StatusOK {
			t.Fatalf("GET content?download=1: %d %s", dlRec.Code, dlRec.Body.String())
		}
		if got := dlRec.Header().Get("Content-Security-Policy"); got != wantCSP {
			t.Errorf("Content-Security-Policy = %q, want %q", got, wantCSP)
		}
		if got := dlRec.Header().Get("X-Content-Type-Options"); got != wantNosniff {
			t.Errorf("X-Content-Type-Options = %q, want %q", got, wantNosniff)
		}
		if cd := dlRec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
			t.Errorf("Content-Disposition = %q, want an attachment (download=1)", cd)
		}
	})
}

// TestHandleGetArtifactContent_NotFound covers both an unknown artifact ID
// and a real artifact with neither Content nor FilePath -- both must be a
// 404 with the ArtifactNotFound error code, not a 500 or empty 200.
func TestHandleGetArtifactContent_NotFound(t *testing.T) {
	s, repo, projectID := newTestServer(t)

	rec := getArtifactContent(t, s, "art-doesnotexist")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	ticket, err := repo.CreateTicket(projectID, domain.Ticket{Title: "t", Status: domain.TicketTODO})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	node, err := repo.CreateNode(domain.GraphNode{
		ID: "node-" + engine.NewArtifactID(), TicketID: ticket.ID, Name: "Impl",
		Type: domain.NodeTypeImplementation, Status: domain.NodeTODO,
	})
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	empty, err := repo.CreateArtifact(domain.Artifact{
		ID: engine.NewArtifactID(), TicketID: ticket.ID, NodeID: node.ID, Name: "Empty",
		Type: domain.ArtifactHTML,
	})
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	rec = getArtifactContent(t, s, empty.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("content-less artifact: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}
