package web

import (
	"embed"
	"net/http"
	"strings"
)

// staticFS holds the shell's static assets, embedded so the binary stays
// self-contained. Embedded paths already include the "static/" prefix, which
// is also the URL prefix, so the file server can use it directly.
//
//go:embed static
var staticFS embed.FS

// staticFiles serves the embedded assets under /static/. The files are small,
// so responses are marked no-cache to make upgrades visible immediately.
// Directory requests are rejected so the file server never emits a listing.
func staticFiles() http.Handler {
	files := http.FileServerFS(staticFS)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}
