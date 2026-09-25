package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/lassegit/neolib/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

// pageNames lists every page template. Adding a page means adding a file in
// templates/ and its name here.
var pageNames = []string{"library", "book", "settings", "signin", "signup", "error"}

var templateFuncs = template.FuncMap{
	"humanDate": func(unix int64) string {
		return time.Unix(unix, 0).UTC().Format("2 Jan 2006")
	},
}

// parseTemplates compiles each page together with the shared layout. Parsing
// per page keeps template names ("content") from colliding.
func parseTemplates() (map[string]*template.Template, error) {
	pages := make(map[string]*template.Template, len(pageNames))
	for _, name := range pageNames {
		tmpl, err := template.New(name).Funcs(templateFuncs).ParseFS(
			templateFS, "templates/layout.html", "templates/"+name+".html",
		)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", name, err)
		}
		pages[name] = tmpl
	}
	return pages, nil
}

// render executes a page into a buffer first, so a template error can never
// leave a half-written response.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page string, data any) {
	tmpl, ok := s.pages[page]
	if !ok {
		s.log.Error("unknown page template", "page", page)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
		s.log.Error("render failed", "page", page, "error", err, "path", r.URL.Path)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

// baseData is embedded in every page view model.
type baseData struct {
	Title  string
	User   *store.User
	CSRF   string
	Error  string
	Notice string
}

// Page view models.

type libraryPage struct {
	baseData
	Books []store.Book
}

type bookChapter struct {
	ID      string
	Title   string
	LabelID string
	HTML    template.HTML
	Notice  string
}

type bookPage struct {
	baseData
	Book         store.Book
	Chapters     []bookChapter
	ContentError string
}

type settingsPage struct {
	baseData
}

type signinPage struct {
	baseData
	Email string
	Next  string
}

type signupPage struct {
	baseData
	Email       string
	DisplayName string
	Closed      bool
}

func (s *Server) base(w http.ResponseWriter, r *http.Request, title string) baseData {
	return baseData{
		Title: title,
		User:  userFrom(r),
		CSRF:  s.auth.CSRFToken(w, r),
	}
}

// renderError renders the generic error page.
func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, title, message string) {
	page := struct {
		baseData
		Message string
	}{
		baseData: s.base(w, r, title),
		Message:  message,
	}
	s.render(w, r, status, "error", page)
}

// serverError reports an unexpected failure and shows a safe message.
func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "error", err, "method", r.Method, "path", r.URL.Path)
	s.renderError(w, r, http.StatusInternalServerError,
		"Something went wrong", "The error has been logged. Please try again.")
}
