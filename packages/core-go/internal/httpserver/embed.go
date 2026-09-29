package httpserver

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// webdist holds the built React app (packages/web/dist), copied in here by
// the root `npm run embed:web` script before `go build`. Only .gitkeep is
// tracked in git; `all:` is required so go:embed doesn't choke on a
// directory that (before that copy step runs) contains nothing else.
//
//go:embed all:webdist
var webdistFS embed.FS

// staticWebHandler serves the embedded frontend build; see
// newStaticWebHandler for the routing rules.
//
// fs.Sub only fails if "webdist" isn't present in webdistFS, which can't
// happen: go:embed guarantees the directory exists at compile time.
func staticWebHandler() http.Handler {
	sub, err := fs.Sub(webdistFS, "webdist")
	if err != nil {
		panic("internal/httpserver: embedded webdist is missing, this should be impossible: " + err.Error())
	}
	return newStaticWebHandler(sub)
}

// assetsPrefix is where Vite writes the build's hashed output files (its
// default build.assetsDir, which vite.config.ts leaves unchanged). If that
// setting ever changes, this prefix has to change with it.
const assetsPrefix = "assets/"

// newStaticWebHandler serves the frontend build in sub, with an SPA
// fallback: a request path that isn't a real file in the build (e.g.
// /artifacts/{id}/preview, the artifact-preview deep link opened by "open in
// new tab" for gherkin, text and html artifacts -- see TicketItem.tsx's
// openInNewTabLink) is served index.html instead of a 404, so the app's own
// tiny path-based switch in main.tsx can take over client-side. Vite's dev
// server already does this by default (its default appType is "spa"); this
// makes the built single-binary `serve` path behave the same way.
//
// Three prefixes deliberately never reach this fallback, because answering
// them with the SPA and a 200 would hide a mistake:
//   - /api/ and the removed /artifacts-static/ route, both of which Routes()
//     answers with an explicit JSON 404 before this handler is reached;
//   - /assets/, answered here with a plain 404 when the file is missing.
//     Everything under it is a hashed build output, so a missing one is
//     almost always an old chunk that a page left open across a deploy is
//     still asking for. Serving index.html there would show up in the
//     browser as a confusing MIME-type error instead of a 404.
//
// It takes the file system as a parameter so tests can pass an
// fstest.MapFS instead of the embedded build.
func newStaticWebHandler(sub fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqPath := strings.TrimPrefix(r.URL.Path, "/")
		if reqPath == "" {
			reqPath = "index.html"
		}
		if f, err := sub.Open(reqPath); err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(reqPath, assetsPrefix) {
			http.NotFound(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}
