// Package web contains neolib's HTTP routing and server-rendered pages.
//
// The navigation shell (library, book details, settings, auth) is rendered by
// the server with html/template. Book pages add a dependency-free ES-module
// reader UI (toolbar, contents sidebar, highlights, progress) via progressive
// enhancement; without JavaScript the page remains a readable book. See
// docs/READER_UI.md.
package web

import (
	"html/template"
	"log/slog"
	"net/http"

	"github.com/lassegit/neolib/internal/auth"
	"github.com/lassegit/neolib/internal/config"
	"github.com/lassegit/neolib/internal/store"
)

// Deps are the collaborators a Server needs.
type Deps struct {
	Config config.Config
	Store  *store.Store
	Auth   *auth.Manager
	Logger *slog.Logger
}

// Server holds the HTTP handlers and parsed templates.
type Server struct {
	cfg   config.Config
	store *store.Store
	auth  *auth.Manager
	log   *slog.Logger
	pages map[string]*template.Template
}

// New builds a Server from its dependencies.
func New(deps Deps) (*Server, error) {
	pages, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:   deps.Config,
		store: deps.Store,
		auth:  deps.Auth,
		log:   deps.Logger,
		pages: pages,
	}, nil
}

// Handler returns the complete HTTP handler, middleware included.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public routes.
	mux.HandleFunc("GET /healthz", s.health)
	mux.Handle("GET /static/", staticFiles())
	mux.HandleFunc("GET /signin", s.getSignin)
	mux.HandleFunc("POST /signin", s.postSignin)
	mux.HandleFunc("GET /signup", s.getSignup)
	mux.HandleFunc("POST /signup", s.postSignup)

	// Authenticated routes.
	mux.Handle("GET /{$}", s.requireAuth(s.library))
	mux.Handle("POST /books", s.requireAuth(s.importBook))
	mux.Handle("GET /books/{id}", s.requireAuth(s.book))
	mux.Handle("GET /books/{id}/resource/{path...}", s.requireAuth(s.bookResource))
	mux.Handle("GET /books/{id}/cover", s.requireAuth(s.bookCover))
	mux.Handle("GET /books/{id}/file", s.requireAuth(s.bookFile))
	mux.Handle("GET /settings", s.requireAuth(s.settings))
	mux.Handle("POST /settings/profile", s.requireAuth(s.updateProfile))
	mux.Handle("POST /settings/reader", s.requireAuth(s.updateReaderSettings))
	mux.Handle("POST /settings/password", s.requireAuth(s.updatePassword))
	mux.Handle("POST /signout", s.requireAuth(s.signout))

	// Reader JSON API. These answer with JSON errors and validate the
	// double-submit token from the X-CSRF-Token header.
	mux.Handle("GET /api/books/{id}/state", s.requireAPIAuth(s.apiBookState))
	mux.Handle("PUT /api/books/{id}/progress", s.requireAPIAuth(s.apiSaveProgress))
	mux.Handle("POST /api/books/{id}/annotations", s.requireAPIAuth(s.apiCreateAnnotation))
	mux.Handle("PATCH /api/annotations/{id}", s.requireAPIAuth(s.apiUpdateAnnotation))
	mux.Handle("DELETE /api/annotations/{id}", s.requireAPIAuth(s.apiDeleteAnnotation))
	mux.Handle("PATCH /api/reader/settings", s.requireAPIAuth(s.apiReaderSettings))

	// Catch-all page for unknown URLs.
	mux.HandleFunc("/", s.notFound)

	return s.recoverer(s.logRequests(s.limitBody(s.securityHeaders(mux))))
}
