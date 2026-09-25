package web

import (
	"embed"
	"net/http"
)

// staticFS holds the shell's static assets, embedded so the binary stays
// self-contained. Embedded paths already include the "static/" prefix, which
// is also the URL prefix, so the file server can use it directly.
//
//go:embed static
var staticFS embed.FS

// staticFiles serves the embedded assets under /static/. The files are small,
// so responses are marked no-cache to make upgrades visible immediately.
func staticFiles() http.Handler {
	files := http.FileServerFS(staticFS)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}
