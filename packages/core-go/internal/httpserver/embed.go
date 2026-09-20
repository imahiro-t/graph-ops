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

// staticWebHandler serves the embedded frontend build, with an SPA fallback:
// a request path that isn't a real file in the build (e.g. /artifacts/{id}/
// preview, the artifact-preview deep link opened by "open in new tab" for
// gherkin, text and html artifacts -- see TicketItem.tsx's
// openInNewTabLink) is served
// index.html instead of a 404, so the app's own tiny path-based switch in
// main.tsx can take over client-side. Vite's dev server already does this by
// default (its default appType is "spa"); this makes the built single-binary
// `serve` path behave the same way.
//
// fs.Sub only fails if "webdist" isn't present in webdistFS, which can't
// happen: go:embed guarantees the directory exists at compile time.
func staticWebHandler() http.Handler {
	sub, err := fs.Sub(webdistFS, "webdist")
	if err != nil {
		panic("internal/httpserver: embedded webdist is missing, this should be impossible: " + err.Error())
	}
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
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}
