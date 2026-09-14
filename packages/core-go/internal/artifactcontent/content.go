// Package artifactcontent implements the DB-only artifact storage contract
// from DFLT-00006 ("about artifact storage location"): html and image artifacts must
// have their actual bytes stored in artifacts.content, never only as a
// file_path string pointing at whichever machine happened to create them --
// otherwise a remote/shared DB has rows that can't be previewed from any
// other machine, which is exactly the bug the ticket reports.
//
// Both the CLI (`add-artifact`) and the HTTP API (`POST
// /api/tickets/{id}/artifacts`) funnel html/image artifact creation through
// Resolve/EncodeFile/EncodeInline here so the two entry points agree on
// encoding; GET /api/artifacts/{id}/content decodes what was stored here via
// Payload, which funnels through the contentTypeFor Content-Type decision.
package artifactcontent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/graph-ops/core-go/internal/domain"
)

// Metadata is the JSON shape stored in artifacts.metadata for html/image
// artifacts whose bytes live in artifacts.content. Encoding says how to
// interpret Content: "base64" for image, "" (raw text) for html. MimeType is
// the Content-Type a serving endpoint should answer with. OriginalFilename
// is the basename of the file Content was read from, empty when the caller
// supplied inline content directly instead of a file path.
type Metadata struct {
	Encoding         string `json:"encoding,omitempty"`
	MimeType         string `json:"mime_type,omitempty"`
	OriginalFilename string `json:"original_filename,omitempty"`
}

// DecodeMetadata parses raw (as stored in artifacts.metadata) into a
// Metadata. A nil/empty raw, or one that isn't valid Metadata JSON (rows
// predating this feature, or a non-html/image artifact's metadata), yields
// the zero value rather than an error -- every field here is optional to the
// caller, never load-bearing for whether content can be served at all.
func DecodeMetadata(raw *string) Metadata {
	if raw == nil || *raw == "" {
		return Metadata{}
	}
	var m Metadata
	_ = json.Unmarshal([]byte(*raw), &m)
	return m
}

// EncodeMetadata is DecodeMetadata's inverse: nil for the zero value (so
// callers don't write a meaningless "{}" into artifacts.metadata), otherwise
// its JSON encoding.
func EncodeMetadata(m Metadata) *string {
	if m == (Metadata{}) {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		// Metadata is three plain strings; Marshal cannot fail on it.
		return nil
	}
	s := string(b)
	return &s
}

// Resolved holds the DB-ready fields Resolve produces for a new html/image
// artifact.
type Resolved struct {
	Content  *string
	Metadata *string
	// FilePath is populated only when contentOrPath named a real file that
	// was read into Content -- it is kept purely as informational
	// provenance (where this content originally came from on the machine
	// that created it). Nothing reads it back to serve content.
	FilePath *string
}

// ResolveOptions controls how Resolve is allowed to treat contentOrPath as a
// path to read from local disk, rather than literal inline content.
//
// This exists because of Security Review art-9e220d80 finding #1: Resolve
// used to call os.Stat/os.ReadFile on contentOrPath completely unsandboxed.
// The CLI (`add-artifact`) that is Resolve's caller is routinely invoked by
// an autonomous agent acting on ticket/plan text it did not author (and
// which may itself be adversarial, e.g. via prompt injection) -- if that
// text can talk the agent into running
// `add-artifact <ticket> <node> <name> html ~/.ssh/id_rsa`, the old code
// would read the key and store it as an artifact, which
// GET /api/artifacts/{id}/content then serves over an unauthenticated HTTP
// endpoint reachable by anyone on the network. Requiring the read to resolve
// inside ArtifactsDir by default closes that hole while keeping the normal
// case (a file the caller just wrote under the configured artifacts
// directory) working exactly as before.
type ResolveOptions struct {
	// ArtifactsDir sandboxes local file reads: contentOrPath is only read as
	// a file when its absolute path resolves inside ArtifactsDir. Empty
	// means no file may be read at all (contentOrPath is always treated as
	// inline content) unless AllowOutsideArtifactsDir is set.
	ArtifactsDir string
	// AllowOutsideArtifactsDir is an explicit, caller-opted-in escape hatch
	// (e.g. a `--allow-outside-artifacts-dir` CLI flag a human types) that
	// restores the pre-fix behavior of reading any path on disk the process
	// can see. Left false by default so an autonomous agent invocation can
	// never be tricked into it merely by what a ticket/plan says.
	AllowOutsideArtifactsDir bool
}

// Resolve turns the caller-supplied contentOrPath for a new html or image
// artifact into DB-ready content. When contentOrPath names a file that
// exists on disk *and* is permitted by opts (see ResolveOptions), its bytes
// are read and stored in Content (base64-encoded for image, verbatim for
// html, and validated -- see EncodeFile); when contentOrPath does not name a
// permitted file, it is treated as already being the content itself -- raw
// HTML text, or (for image) base64-encoded image data.
//
// A nil or empty contentOrPath returns a zero Resolved (no error): omitting
// content for a not-yet-populated artifact is legal, same as before this
// package existed.
func Resolve(artType domain.ArtifactType, contentOrPath *string, opts ResolveOptions) (Resolved, error) {
	if contentOrPath == nil || *contentOrPath == "" {
		return Resolved{}, nil
	}
	raw := *contentOrPath
	if info, err := os.Stat(raw); err == nil && !info.IsDir() {
		if !opts.AllowOutsideArtifactsDir {
			if err := RequireInsideArtifactsDir(opts.ArtifactsDir, raw); err != nil {
				return Resolved{}, err
			}
		}
		data, err := os.ReadFile(raw)
		if err != nil {
			return Resolved{}, fmt.Errorf("reading %s artifact file %q: %w", artType, raw, err)
		}
		content, meta, err := EncodeFile(artType, filepath.Base(raw), data)
		if err != nil {
			return Resolved{}, err
		}
		path := raw
		return Resolved{Content: &content, Metadata: EncodeMetadata(meta), FilePath: &path}, nil
	}
	content, meta, err := EncodeInline(artType, raw)
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{Content: &content, Metadata: EncodeMetadata(meta)}, nil
}

// RequireInsideArtifactsDir rejects reading raw as a file unless its
// absolute path resolves inside artifactsDir -- the CLI-side counterpart of
// the HTTP API's safeArtifactPath sandbox (internal/httpserver/tickets.go),
// adapted for a local caller that legitimately uses absolute/CWD-relative
// paths rather than sending a relative path over the network. An empty
// artifactsDir (no artifacts directory configured at all) refuses every
// path, since there is nothing to sandbox against. Used by Resolve
// (add-artifact).
func RequireInsideArtifactsDir(artifactsDir, raw string) error {
	if artifactsDir == "" {
		return fmt.Errorf("refusing to read %q as a local file: no artifacts directory is configured to sandbox the read against (pass --allow-outside-artifacts-dir to read files anywhere, if that is really intended)", raw)
	}
	root, err := filepath.Abs(artifactsDir)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return err
	}
	root = filepath.Clean(root)
	abs = filepath.Clean(abs)
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return fmt.Errorf("refusing to read %q: it resolves outside the artifacts directory (%s); move the file under the artifacts directory, or pass --allow-outside-artifacts-dir to read files anywhere", raw, root)
	}
	return nil
}

// EncodeFile builds DB-ready content+metadata for an html/image artifact
// whose bytes were just read from a file. filename is used only for
// MimeType/OriginalFilename bookkeeping (mime type sniffing falls back to
// the bytes themselves when the extension is unrecognized).
func EncodeFile(artType domain.ArtifactType, filename string, data []byte) (content string, meta Metadata, err error) {
	switch artType {
	case domain.ArtifactImage:
		if err := validateImageBytes(data); err != nil {
			return "", Metadata{}, err
		}
		return base64.StdEncoding.EncodeToString(data), Metadata{
			Encoding: "base64", MimeType: detectMime(filename, data), OriginalFilename: filename,
		}, nil
	case domain.ArtifactHTML:
		return string(data), Metadata{MimeType: "text/html; charset=utf-8", OriginalFilename: filename}, nil
	default:
		return "", Metadata{}, fmt.Errorf("artifactcontent.EncodeFile: unsupported artifact type %q", artType)
	}
}

// EncodeInline builds DB-ready content+metadata for html/image content
// supplied directly rather than read from a file: raw HTML text as-is, or
// (for image) data that must already be base64-encoded, since there is no
// file on disk to sniff bytes from otherwise.
func EncodeInline(artType domain.ArtifactType, raw string) (content string, meta Metadata, err error) {
	switch artType {
	case domain.ArtifactHTML:
		return raw, Metadata{MimeType: "text/html; charset=utf-8"}, nil
	case domain.ArtifactImage:
		decoded, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return "", Metadata{}, fmt.Errorf("image artifact content is neither an existing file path nor valid base64 data: %w", err)
		}
		if err := validateImageBytes(decoded); err != nil {
			return "", Metadata{}, err
		}
		return raw, Metadata{Encoding: "base64", MimeType: detectMime("", decoded)}, nil
	default:
		return "", Metadata{}, fmt.Errorf("artifactcontent.EncodeInline: unsupported artifact type %q", artType)
	}
}

// Payload decodes an artifact's stored content back into raw bytes plus the
// Content-Type a serving endpoint should answer with -- the inverse of
// EncodeFile/EncodeInline. The Content-Type decision itself is delegated to
// contentTypeFor; see that function's doc comment for the invariant it
// enforces and why.
func Payload(artType domain.ArtifactType, content string, meta Metadata) (data []byte, contentType string, err error) {
	if meta.Encoding == "base64" {
		data, err = base64.StdEncoding.DecodeString(content)
		if err != nil {
			return nil, "", fmt.Errorf("decoding base64 artifact content: %w", err)
		}
	} else {
		data = []byte(content)
	}
	return data, contentTypeFor(artType, data, meta), nil
}

// contentTypeFor is the single place that decides the Content-Type a serving
// endpoint should answer with for an artifact's final (already-decoded)
// bytes, given its artType and metadata. meta.MimeType is honored ONLY for
// artType == html/image (the only two types EncodeFile/EncodeInline ever
// populate it for); every other artType always uses the per-artifact-type
// default -- see defaultContentType -- no matter what meta.MimeType contains.
// This is a hard invariant, not a best-effort default: image/html are the
// only artifact types that may ever have their Content-Type influenced by
// anything other than defaultContentType. Payload calls this exclusively
// rather than computing its own Content-Type.
//
// Security review art-260ce0f7 (3rd security review, DFLT-00006): this used
// to default every non-image artifact to "text/html; charset=utf-8"
// regardless of artType, because text/gherkin/json artifacts never populate
// MimeType via EncodeFile/EncodeInline and so always hit the "" branch. That
// was fixed by branching on artType in defaultContentType -- but the caller
// still trusted meta.MimeType itself whenever it was non-empty, for any
// artType.
//
// Security review art-3d521dc0 (4th security review, DFLT-00006): that
// residual trust was the actual hole. handleCreateArtifact (and any other
// writer) stores body.Metadata verbatim for text/gherkin/json artifacts --
// unlike html/image, those types never get their metadata recomputed
// server-side -- so a client could POST type:"text",
// content:"<script>alert(document.domain)</script>",
// metadata:"{\"mime_type\":\"text/html; charset=utf-8\"}" and have
// GET /api/artifacts/{id}/content honor that claimed mime_type: stored XSS,
// the same bug as art-260ce0f7 but reached through metadata instead of
// through an empty MimeType. Restricting which artType values this function
// will ever read meta.MimeType from (rather than trying to enumerate every
// way a bogus MimeType could end up in metadata) closes this and any future
// variant of the same mistake at the one place that actually decides the
// served Content-Type.
//
// The allowlist is expressed as domain.IsFileBackedArtifactType rather than
// an open-coded html/image test (DFLT-00023 D-5): "may we trust the recorded
// mime_type" and "are this type's bytes file-backed" are the same question
// here, because only the file-backed types have their metadata recomputed
// server-side by EncodeFile/EncodeInline. Sharing the predicate means a
// newly added file-backed type cannot be onboarded in one place and
// forgotten in this one.
func contentTypeFor(artType domain.ArtifactType, data []byte, meta Metadata) string {
	var contentType string
	if domain.IsFileBackedArtifactType(artType) {
		contentType = meta.MimeType
	}
	if contentType == "" {
		contentType = defaultContentType(artType, data)
	}
	return contentType
}

// defaultContentType is the Content-Type Payload falls back to when an
// artifact has no metadata.mime_type recorded -- either because it predates
// the metadata convention (old html/image rows) or because its type never
// goes through EncodeFile/EncodeInline at all (text/gherkin/json, which are
// stored as plain DB text with no metadata). It is a strict per-type
// allowlist, not a sniff-and-guess: only "html" ever resolves to an HTML
// content type, and only "image" is sniffed (sniffing is safe there because
// validateImageBytes already restricted the stored bytes to real image
// signatures). Every other/unknown artType is served as text/plain, the safe
// choice for content no earlier stage has validated as renderable markup.
func defaultContentType(artType domain.ArtifactType, data []byte) string {
	switch artType {
	case domain.ArtifactImage:
		return http.DetectContentType(data)
	case domain.ArtifactHTML:
		return "text/html; charset=utf-8"
	case domain.ArtifactJSON:
		return "application/json; charset=utf-8"
	default:
		// domain.ArtifactText, domain.ArtifactGherkin, and any future/unknown
		// type all land here: never render as HTML.
		return "text/plain; charset=utf-8"
	}
}

func detectMime(filename string, data []byte) string {
	if filename != "" {
		if t := mime.TypeByExtension(filepath.Ext(filename)); t != "" {
			return t
		}
	}
	if len(data) > 0 {
		return http.DetectContentType(data)
	}
	return "application/octet-stream"
}

// imageSignatures lists the byte-prefix magic numbers of the image formats
// an "image" artifact is accepted as. validateImageBytes is the only check
// standing between arbitrary bytes and being registered as an image
// artifact: without it (Security Review art-9e220d80 finding #2), an
// attacker could base64-encode an HTML or SVG payload, submit it as
// artType == image, and have it stored and later served by
// GET /api/artifacts/{id}/content with a sniffed Content-Type a browser is
// willing to execute -- a content-type-confusion route to stored XSS that
// this feature would otherwise have opened. This is deliberately a
// permissive allowlist of common formats' signatures, not a full decode: it
// only needs to rule out non-image payloads, not validate that the image is
// well-formed.
var imageSignatures = [][]byte{
	{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, // PNG
	{0xFF, 0xD8, 0xFF},                            // JPEG
	[]byte("GIF87a"),
	[]byte("GIF89a"),
	{'B', 'M'}, // BMP
}

func validateImageBytes(data []byte) error {
	for _, sig := range imageSignatures {
		if bytes.HasPrefix(data, sig) {
			return nil
		}
	}
	// WEBP: a 12-byte RIFF container header ("RIFF" + 4-byte size + "WEBP").
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return nil
	}
	return fmt.Errorf("content does not start with a recognized image file signature (png/jpeg/gif/bmp/webp); refusing to store it as an image artifact")
}
