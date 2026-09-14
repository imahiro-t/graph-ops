package artifactcontent

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/graph-ops/core-go/internal/domain"
)

// pngBytes returns a byte slice starting with the real PNG magic number
// (validateImageBytes checks the signature only, not full image structure),
// followed by an arbitrary payload so tests can still distinguish "my bytes
// round-tripped" from "some other test's bytes did".
func pngBytes(payload string) []byte {
	sig := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	return append(sig, []byte(payload)...)
}

// TestResolve_ExistingFile_HTML covers the primary DFLT-00006 path: an
// html artifact whose contentOrPath names a real file on disk (inside the
// configured artifacts directory -- see ResolveOptions/Security Review
// art-9e220d80) must end up with its bytes in Content (not only referenced
// via FilePath), so a preview works even if that file is later deleted or
// the row is read from a different machine.
func TestResolve_ExistingFile_HTML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")
	const html = "<html><body>hello</body></html>"
	if err := os.WriteFile(path, []byte(html), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resolved, err := Resolve(domain.ArtifactHTML, &path, ResolveOptions{ArtifactsDir: dir})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Content == nil || *resolved.Content != html {
		t.Fatalf("Content = %v, want %q", resolved.Content, html)
	}
	if resolved.FilePath == nil || *resolved.FilePath != path {
		t.Fatalf("FilePath = %v, want %q (informational provenance)", resolved.FilePath, path)
	}
	meta := DecodeMetadata(resolved.Metadata)
	if meta.Encoding != "" {
		t.Errorf("html Metadata.Encoding = %q, want empty (raw text)", meta.Encoding)
	}
	if meta.MimeType != "text/html; charset=utf-8" {
		t.Errorf("html Metadata.MimeType = %q", meta.MimeType)
	}
	if meta.OriginalFilename != "report.html" {
		t.Errorf("html Metadata.OriginalFilename = %q", meta.OriginalFilename)
	}
}

// TestResolve_ExistingFile_Image covers the image counterpart: bytes are
// base64-encoded into Content and Metadata.Encoding records that so Payload
// can decode it back.
func TestResolve_ExistingFile_Image(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	// Must start with the real PNG signature now that EncodeFile validates
	// image bytes (Security Review art-9e220d80 finding #2); the rest is
	// still arbitrary since Resolve/EncodeFile never fully decode the image.
	data := pngBytes("fake-png-bytes")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resolved, err := Resolve(domain.ArtifactImage, &path, ResolveOptions{ArtifactsDir: dir})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Content == nil {
		t.Fatal("Content is nil")
	}
	decoded, err := base64.StdEncoding.DecodeString(*resolved.Content)
	if err != nil {
		t.Fatalf("Content is not valid base64: %v", err)
	}
	if string(decoded) != string(data) {
		t.Errorf("decoded Content = %q, want %q", decoded, data)
	}
	meta := DecodeMetadata(resolved.Metadata)
	if meta.Encoding != "base64" {
		t.Errorf("image Metadata.Encoding = %q, want \"base64\"", meta.Encoding)
	}
	if meta.MimeType != "image/png" {
		t.Errorf("image Metadata.MimeType = %q, want image/png (from .png extension)", meta.MimeType)
	}
}

// TestResolve_ExistingFile_OutsideArtifactsDir_RejectedByDefault is the
// direct regression test for Security Review art-9e220d80 finding #1: a
// file that exists on disk but sits outside the configured artifacts
// directory must not be read, by default -- otherwise `add-artifact html
// ~/.ssh/id_rsa` would read the key into the DB, which
// GET /api/artifacts/{id}/content then serves with no authentication.
func TestResolve_ExistingFile_OutsideArtifactsDir_RejectedByDefault(t *testing.T) {
	artifactsDir := t.TempDir()
	outsideDir := t.TempDir()
	path := filepath.Join(outsideDir, "secret.html")
	if err := os.WriteFile(path, []byte("<html><body>secret</body></html>"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Resolve(domain.ArtifactHTML, &path, ResolveOptions{ArtifactsDir: artifactsDir})
	if err == nil {
		t.Fatal("expected Resolve to refuse a file outside the artifacts directory")
	}
}

// TestResolve_ExistingFile_OutsideArtifactsDir_AllowedWithOptIn covers the
// explicit escape hatch: an operator who really wants to read from anywhere
// can opt in via AllowOutsideArtifactsDir (the CLI's
// --allow-outside-artifacts-dir flag).
func TestResolve_ExistingFile_OutsideArtifactsDir_AllowedWithOptIn(t *testing.T) {
	artifactsDir := t.TempDir()
	outsideDir := t.TempDir()
	path := filepath.Join(outsideDir, "note.html")
	const html = "<html><body>not a secret</body></html>"
	if err := os.WriteFile(path, []byte(html), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	resolved, err := Resolve(domain.ArtifactHTML, &path, ResolveOptions{
		ArtifactsDir:             artifactsDir,
		AllowOutsideArtifactsDir: true,
	})
	if err != nil {
		t.Fatalf("Resolve with AllowOutsideArtifactsDir: %v", err)
	}
	if resolved.Content == nil || *resolved.Content != html {
		t.Fatalf("Content = %v, want %q", resolved.Content, html)
	}
}

// TestResolve_InlineHTML covers the fallback path used when contentOrPath is
// not an existing file: it is treated as the HTML content itself, not an
// error.
func TestResolve_InlineHTML(t *testing.T) {
	raw := "<html><body>inline</body></html>"
	resolved, err := Resolve(domain.ArtifactHTML, &raw, ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Content == nil || *resolved.Content != raw {
		t.Fatalf("Content = %v, want %q", resolved.Content, raw)
	}
	if resolved.FilePath != nil {
		t.Errorf("FilePath = %v, want nil for inline content (nothing was read from disk)", resolved.FilePath)
	}
}

// TestResolve_InlineImage_ValidBase64 covers the image counterpart: when
// contentOrPath isn't a file but does decode as base64 image bytes, it's
// accepted as already-encoded inline image content.
func TestResolve_InlineImage_ValidBase64(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString(pngBytes("some bytes"))
	resolved, err := Resolve(domain.ArtifactImage, &raw, ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Content == nil || *resolved.Content != raw {
		t.Fatalf("Content = %v, want %q (passed through as-is)", resolved.Content, raw)
	}
	meta := DecodeMetadata(resolved.Metadata)
	if meta.Encoding != "base64" {
		t.Errorf("Metadata.Encoding = %q, want base64", meta.Encoding)
	}
}

// TestResolve_InlineImage_InvalidData covers the error path: contentOrPath
// is neither a real file nor valid base64, so there is no way to interpret
// it as image content.
func TestResolve_InlineImage_InvalidData(t *testing.T) {
	raw := "not a file path and not valid base64!!"
	if _, err := Resolve(domain.ArtifactImage, &raw, ResolveOptions{}); err == nil {
		t.Fatal("expected an error for unresolvable image content")
	}
}

// TestResolve_InlineImage_ValidBase64ButNotAnImage covers Security Review
// art-9e220d80 finding #2: base64 data that decodes cleanly but whose bytes
// don't match any recognized image signature must be rejected, not stored
// as an "image" artifact (which would let a served HTML/SVG payload
// masquerade as an image and be sniffed back into an executable content
// type by GET /api/artifacts/{id}/content).
func TestResolve_InlineImage_ValidBase64ButNotAnImage(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString([]byte("<script>alert(1)</script>"))
	if _, err := Resolve(domain.ArtifactImage, &raw, ResolveOptions{}); err == nil {
		t.Fatal("expected Resolve to reject base64 data that isn't a recognized image format")
	}
}

// TestResolve_Empty covers the "no content supplied at all" case, which must
// stay legal (an artifact can be created and filled in later).
func TestResolve_Empty(t *testing.T) {
	resolved, err := Resolve(domain.ArtifactHTML, nil, ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve(nil): %v", err)
	}
	if resolved.Content != nil || resolved.Metadata != nil || resolved.FilePath != nil {
		t.Errorf("Resolve(nil) = %+v, want zero value", resolved)
	}
}

// TestPayload_RoundTrip_Image covers the serving-side inverse of
// Resolve/EncodeFile: Payload must decode base64 content back to the
// original bytes and report the stored mime type.
func TestPayload_RoundTrip_Image(t *testing.T) {
	original := pngBytes("round trip me")
	content, meta, err := EncodeFile(domain.ArtifactImage, "x.png", original)
	if err != nil {
		t.Fatalf("EncodeFile: %v", err)
	}
	data, contentType, err := Payload(domain.ArtifactImage, content, meta)
	if err != nil {
		t.Fatalf("Payload: %v", err)
	}
	if string(data) != string(original) {
		t.Errorf("Payload data = %q, want %q", data, original)
	}
	if contentType != "image/png" {
		t.Errorf("Payload contentType = %q, want image/png", contentType)
	}
}

// TestPayload_RoundTrip_HTML covers the html (raw text, no base64) case.
func TestPayload_RoundTrip_HTML(t *testing.T) {
	const html = "<html><body>x</body></html>"
	content, meta, err := EncodeFile(domain.ArtifactHTML, "x.html", []byte(html))
	if err != nil {
		t.Fatalf("EncodeFile: %v", err)
	}
	data, contentType, err := Payload(domain.ArtifactHTML, content, meta)
	if err != nil {
		t.Fatalf("Payload: %v", err)
	}
	if string(data) != html {
		t.Errorf("Payload data = %q, want %q", data, html)
	}
	if !strings.HasPrefix(contentType, "text/html") {
		t.Errorf("Payload contentType = %q, want text/html*", contentType)
	}
}

// TestEncodeFile_Image_RejectsNonImageBytes covers Security Review
// art-9e220d80 finding #2 for the file-read path: bytes read from disk that
// don't start with a recognized image signature must not be accepted as an
// image artifact (e.g. an HTML/SVG file saved with a .png extension).
func TestEncodeFile_Image_RejectsNonImageBytes(t *testing.T) {
	if _, _, err := EncodeFile(domain.ArtifactImage, "fake.png", []byte("<html><body>not an image</body></html>")); err == nil {
		t.Fatal("expected EncodeFile to reject non-image bytes for an image artifact")
	}
}

// TestDecodeMetadata_NilAndEmpty covers the "nothing stored yet" cases,
// which must yield the zero value rather than erroring -- callers treat
// every Metadata field as optional.
func TestDecodeMetadata_NilAndEmpty(t *testing.T) {
	if m := DecodeMetadata(nil); m != (Metadata{}) {
		t.Errorf("DecodeMetadata(nil) = %+v, want zero value", m)
	}
	empty := ""
	if m := DecodeMetadata(&empty); m != (Metadata{}) {
		t.Errorf("DecodeMetadata(empty) = %+v, want zero value", m)
	}
	garbage := "not json"
	if m := DecodeMetadata(&garbage); m != (Metadata{}) {
		t.Errorf("DecodeMetadata(garbage) = %+v, want zero value", m)
	}
}

// TestEncodeMetadata_ZeroValueIsNil ensures a Metadata with nothing set
// never gets written as a meaningless "{}" into artifacts.metadata.
func TestEncodeMetadata_ZeroValueIsNil(t *testing.T) {
	if got := EncodeMetadata(Metadata{}); got != nil {
		t.Errorf("EncodeMetadata(zero value) = %v, want nil", got)
	}
}

// TestPayload_TextGherkinJSON_NeverDefaultToHTML is the direct regression
// test for Security Review art-260ce0f7 (3rd security review, DFLT-00006):
// text/gherkin/json artifacts never go through EncodeFile/EncodeInline (only
// html/image do), so they are always created with a zero-value Metadata --
// exactly the "no metadata.mime_type recorded" case Payload used to default
// to "text/html; charset=utf-8" for anything other than image. That let a
// type:"text" artifact whose content was `<script>...</script>` be served by
// the unauthenticated GET /api/artifacts/{id}/content as executable HTML --
// stored XSS. Payload must now serve these types as inert text, never HTML,
// regardless of what their content looks like.
func TestPayload_TextGherkinJSON_NeverDefaultToHTML(t *testing.T) {
	const payload = `<script>alert(document.domain)</script>`
	for _, artType := range []domain.ArtifactType{domain.ArtifactText, domain.ArtifactGherkin, domain.ArtifactJSON} {
		t.Run(string(artType), func(t *testing.T) {
			data, contentType, err := Payload(artType, payload, Metadata{})
			if err != nil {
				t.Fatalf("Payload: %v", err)
			}
			if string(data) != payload {
				t.Errorf("Payload data = %q, want %q", data, payload)
			}
			if strings.HasPrefix(contentType, "text/html") {
				t.Errorf("Payload contentType = %q, must never be text/html for artifact type %q (stored XSS)", contentType, artType)
			}
			if strings.Contains(contentType, "html") {
				t.Errorf("Payload contentType = %q, must not mention html at all for artifact type %q", contentType, artType)
			}
		})
	}
}

// TestPayload_HTML_StillDefaultsToHTML guards against overcorrecting the
// art-260ce0f7 fix: "html" is the one artifact type intentionally meant to be
// served as HTML (previews render it inside a sandboxed iframe -- see
// TicketItem.tsx's sandbox="allow-scripts" -- which is the mitigation this
// type relies on), including for legacy rows that predate the metadata
// convention and so have a zero-value Metadata just like text/gherkin/json
// do.
func TestPayload_HTML_StillDefaultsToHTML(t *testing.T) {
	const html = "<html><body>legacy row, no metadata</body></html>"
	data, contentType, err := Payload(domain.ArtifactHTML, html, Metadata{})
	if err != nil {
		t.Fatalf("Payload: %v", err)
	}
	if string(data) != html {
		t.Errorf("Payload data = %q, want %q", data, html)
	}
	if !strings.HasPrefix(contentType, "text/html") {
		t.Errorf("Payload contentType = %q, want text/html*", contentType)
	}
}

// TestPayload_TextGherkinJSON_IgnoresExplicitMetadataMimeType is the direct
// regression test for Security Review art-3d521dc0 (4th security review,
// DFLT-00006): unlike TestPayload_TextGherkinJSON_NeverDefaultToHTML (which
// covers a zero-value Metadata, i.e. no mime_type recorded at all), this
// covers metadata that DOES explicitly carry a client-supplied
// mime_type:"text/html" -- exactly what handleCreateArtifact used to store
// verbatim for a type:"text" artifact whose metadata came straight from the
// request body. Payload must ignore meta.MimeType entirely for every artType
// other than html/image, regardless of what value it holds, so this can
// never again become a way to make GET /api/artifacts/{id}/content serve
// attacker-controlled content as text/html (stored XSS).
func TestPayload_TextGherkinJSON_IgnoresExplicitMetadataMimeType(t *testing.T) {
	const payload = `<script>alert(document.domain)</script>`
	spoofed := Metadata{MimeType: "text/html; charset=utf-8"}
	for _, artType := range []domain.ArtifactType{domain.ArtifactText, domain.ArtifactGherkin, domain.ArtifactJSON} {
		t.Run(string(artType), func(t *testing.T) {
			data, contentType, err := Payload(artType, payload, spoofed)
			if err != nil {
				t.Fatalf("Payload: %v", err)
			}
			if string(data) != payload {
				t.Errorf("Payload data = %q, want %q", data, payload)
			}
			if strings.Contains(contentType, "html") {
				t.Errorf("Payload contentType = %q, must ignore metadata.mime_type=%q for artifact type %q (stored XSS)", contentType, spoofed.MimeType, artType)
			}
		})
	}
}

// TestPayload_JSON_DefaultsToApplicationJSON covers the json-specific
// default distinct from the plain-text fallback text/gherkin get.
func TestPayload_JSON_DefaultsToApplicationJSON(t *testing.T) {
	const jsonBody = `{"a":1}`
	_, contentType, err := Payload(domain.ArtifactJSON, jsonBody, Metadata{})
	if err != nil {
		t.Fatalf("Payload: %v", err)
	}
	if !strings.HasPrefix(contentType, "application/json") {
		t.Errorf("Payload contentType = %q, want application/json*", contentType)
	}
}
