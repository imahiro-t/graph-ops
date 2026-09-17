package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// pngBytes returns bytes starting with the real PNG magic number (image
// artifacts are now validated against a magic-byte allowlist -- see
// artifactcontent.validateImageBytes / Security Review art-9e220d80 finding
// #2), followed by an arbitrary payload.
func pngBytes(payload string) []byte {
	sig := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	return append(sig, []byte(payload)...)
}

// TestCmdAddArtifact_HTMLFilePathStoresContentInDB is the CLI-level
// regression test for DFLT-00006: add-artifact used to store an html
// artifact's contentOrPath argument only as FilePath, never reading the
// file into the DB. A shared/remote DB with only that path string cannot be
// previewed from any machine but the one that ran this command. After the
// fix, the file's bytes must end up in the artifact's Content column.
//
// The file is written inside a dedicated artifacts directory and that same
// directory is passed to cmdAddArtifact, matching how `serve`/the CLI are
// actually configured (GRAPH_ARTIFACTS_DIR) -- see
// TestCmdAddArtifact_HTMLFilePathOutsideArtifactsDirRejectedByDefault for
// the sandboxing behavior itself.
func TestCmdAddArtifact_HTMLFilePathStoresContentInDB(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	ticketID, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeImplementation)

	artifactsDir := t.TempDir()
	const html = "<html><body>hello from disk</body></html>"
	path := filepath.Join(artifactsDir, "notes.html")
	if err := os.WriteFile(path, []byte(html), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := cmdAddArtifact(repo, artifactsDir, []string{ticketID, nodeID, "Notes", "html", path}); err != nil {
		t.Fatalf("cmdAddArtifact: %v", err)
	}

	// ListArtifactsByTicket omits Content for html/image rows (see
	// artifactSummaryCols) to keep GET /api/tickets/{id} and `get-ticket`
	// payloads from growing unbounded as artifacts accumulate -- it only
	// promises HasContent here. The actual bytes are fetched by ID via
	// GetArtifact (what GET /api/artifacts/{id}/content does).
	summaries, err := repo.ListArtifactsByTicket(ticketID)
	if err != nil || len(summaries) != 1 {
		t.Fatalf("ListArtifactsByTicket: %d artifacts, err=%v", len(summaries), err)
	}
	if summaries[0].Content != nil {
		t.Errorf("summary Content = %v, want nil (html content must not be inlined in the ticket list)", summaries[0].Content)
	}
	if !summaries[0].HasContent {
		t.Error("summary HasContent = false, want true")
	}

	a, err := repo.GetArtifact(summaries[0].ID)
	if err != nil || a == nil {
		t.Fatalf("GetArtifact: %v, %+v", err, a)
	}
	if a.Content == nil || *a.Content != html {
		t.Fatalf("Content = %v, want the file's bytes (%q)", a.Content, html)
	}
	if a.FilePath == nil || *a.FilePath != path {
		t.Errorf("FilePath = %v, want %q kept as informational provenance", a.FilePath, path)
	}
}

// TestCmdAddArtifact_HTMLFilePathOutsideArtifactsDirRejectedByDefault is the
// CLI-level regression test for Security Review art-9e220d80 finding #1:
// add-artifact used to call os.Stat/os.ReadFile on contentOrPath completely
// unsandboxed, so an autonomous agent acting on adversarial ticket/plan text
// could be talked into reading an arbitrary local file (e.g. ~/.ssh/id_rsa)
// into the DB, which GET /api/artifacts/{id}/content then serves with no
// authentication. A file that exists but sits outside the configured
// artifacts directory must be refused, not read.
func TestCmdAddArtifact_HTMLFilePathOutsideArtifactsDirRejectedByDefault(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	ticketID, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeImplementation)

	artifactsDir := t.TempDir()
	outsideDir := t.TempDir()
	secret := filepath.Join(outsideDir, "id_rsa.html")
	if err := os.WriteFile(secret, []byte("-----BEGIN OPENSSH PRIVATE KEY-----"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := cmdAddArtifact(repo, artifactsDir, []string{ticketID, nodeID, "Notes", "html", secret}); err == nil {
		t.Fatal("expected cmdAddArtifact to refuse a file outside the artifacts directory")
	}

	artifacts, err := repo.ListArtifactsByTicket(ticketID)
	if err != nil {
		t.Fatalf("ListArtifactsByTicket: %v", err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("expected no artifact row to be created for the refused read, got %d", len(artifacts))
	}
}

// TestCmdAddArtifact_HTMLFilePathOutsideArtifactsDirAllowedWithOptIn covers
// the explicit escape hatch: --allow-outside-artifacts-dir restores the
// pre-fix behavior for a caller (a human operator, not an autonomous agent
// silently following ticket text) who really means to read from anywhere.
func TestCmdAddArtifact_HTMLFilePathOutsideArtifactsDirAllowedWithOptIn(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	ticketID, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeImplementation)

	artifactsDir := t.TempDir()
	outsideDir := t.TempDir()
	const html = "<html><body>outside but opted in</body></html>"
	path := filepath.Join(outsideDir, "notes.html")
	if err := os.WriteFile(path, []byte(html), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := cmdAddArtifact(repo, artifactsDir, []string{ticketID, nodeID, "Notes", "html", path, "--allow-outside-artifacts-dir"}); err != nil {
		t.Fatalf("cmdAddArtifact: %v", err)
	}

	summaries, err := repo.ListArtifactsByTicket(ticketID)
	if err != nil || len(summaries) != 1 {
		t.Fatalf("ListArtifactsByTicket: %d artifacts, err=%v", len(summaries), err)
	}
	a, err := repo.GetArtifact(summaries[0].ID)
	if err != nil || a == nil {
		t.Fatalf("GetArtifact: %v, %+v", err, a)
	}
	if a.Content == nil || *a.Content != html {
		t.Fatalf("Content = %v, want %q", a.Content, html)
	}
}

// TestCmdAddArtifact_ImageFilePathStoresBase64ContentInDB mirrors the HTML
// case for image artifacts: bytes must be base64-encoded into Content.
func TestCmdAddArtifact_ImageFilePathStoresBase64ContentInDB(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	ticketID, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeImplementation)

	artifactsDir := t.TempDir()
	data := pngBytes("fake-image-bytes")
	path := filepath.Join(artifactsDir, "capture.png")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := cmdAddArtifact(repo, artifactsDir, []string{ticketID, nodeID, "Screenshot", "image", path}); err != nil {
		t.Fatalf("cmdAddArtifact: %v", err)
	}

	summaries, err := repo.ListArtifactsByTicket(ticketID)
	if err != nil || len(summaries) != 1 {
		t.Fatalf("ListArtifactsByTicket: %d artifacts, err=%v", len(summaries), err)
	}
	if summaries[0].Content != nil {
		t.Errorf("summary Content = %v, want nil (image content must not be inlined in the ticket list)", summaries[0].Content)
	}

	a, err := repo.GetArtifact(summaries[0].ID)
	if err != nil || a == nil {
		t.Fatalf("GetArtifact: %v, %+v", err, a)
	}
	if a.Content == nil {
		t.Fatal("Content is nil")
	}
	decoded, err := base64.StdEncoding.DecodeString(*a.Content)
	if err != nil {
		t.Fatalf("Content is not valid base64: %v", err)
	}
	if string(decoded) != string(data) {
		t.Errorf("decoded Content = %q, want %q", decoded, data)
	}
	if a.Metadata == nil {
		t.Fatal("Metadata is nil")
	}
	var meta struct {
		Encoding string `json:"encoding"`
	}
	if err := json.Unmarshal([]byte(*a.Metadata), &meta); err != nil {
		t.Fatalf("Metadata is not valid JSON: %v", err)
	}
	if meta.Encoding != "base64" {
		t.Errorf("Metadata encoding = %q, want base64", meta.Encoding)
	}
}

// TestCmdAddArtifact_ImageFilePathRejectsNonImageBytes covers Security
// Review art-9e220d80 finding #2 at the CLI level: a file registered as
// type "image" whose bytes don't match a recognized image signature (e.g.
// HTML/SVG saved with a .png name) must be rejected, not stored -- storing
// it would let GET /api/artifacts/{id}/content later serve it with a
// sniffed Content-Type a browser could execute.
func TestCmdAddArtifact_ImageFilePathRejectsNonImageBytes(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	ticketID, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeImplementation)

	artifactsDir := t.TempDir()
	path := filepath.Join(artifactsDir, "not-really.png")
	if err := os.WriteFile(path, []byte("<script>alert(document.domain)</script>"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := cmdAddArtifact(repo, artifactsDir, []string{ticketID, nodeID, "Screenshot", "image", path}); err == nil {
		t.Fatal("expected cmdAddArtifact to reject non-image bytes for an image artifact")
	}

	artifacts, err := repo.ListArtifactsByTicket(ticketID)
	if err != nil {
		t.Fatalf("ListArtifactsByTicket: %v", err)
	}
	if len(artifacts) != 0 {
		t.Fatalf("expected no artifact row to be created for the rejected image, got %d", len(artifacts))
	}
}

// TestCmdAddArtifact_HTMLInlineContentAccepted covers the fallback: when the
// given string isn't an existing file, it is stored as the html content
// itself rather than erroring or being (mis)treated as a path.
func TestCmdAddArtifact_HTMLInlineContentAccepted(t *testing.T) {
	repo, projectID := newTestRepoWithProject(t)
	ticketID, nodeID := mustCreateTicketAndNode(t, repo, projectID, domain.NodeTypeImplementation)

	const html = "<html><body>inline, no file</body></html>"
	if err := cmdAddArtifact(repo, t.TempDir(), []string{ticketID, nodeID, "Notes", "html", html}); err != nil {
		t.Fatalf("cmdAddArtifact: %v", err)
	}

	summaries, err := repo.ListArtifactsByTicket(ticketID)
	if err != nil || len(summaries) != 1 {
		t.Fatalf("ListArtifactsByTicket: %d artifacts, err=%v", len(summaries), err)
	}

	a, err := repo.GetArtifact(summaries[0].ID)
	if err != nil || a == nil {
		t.Fatalf("GetArtifact: %v, %+v", err, a)
	}
	if a.Content == nil || *a.Content != html {
		t.Fatalf("Content = %v, want %q", a.Content, html)
	}
}
