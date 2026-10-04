package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/lassegit/neolib/internal/reader"
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
	Title     string
	User      *store.User
	CSRF      string
	Error     string
	Notice    string
	BodyClass string

	// Display preferences apply to every signed-in page so the shell does
	// not flash the default colors when moving between the library, the
	// reader, and settings. The reader page surfaces decode failures itself;
	// elsewhere the defaults keep the page usable.
	Theme      string
	FontFamily string
	FontSize   float64
	LineHeight float64
	Measure    int
}

// Page view models.

type libraryPage struct {
	baseData
	Books []store.Book
}

type bookChapter struct {
	ID      string
	Href    string
	Title   string
	LabelID string
	HTML    template.HTML
	Notice  string
}

type bookPage struct {
	baseData
	Book         store.Book
	Reader       reader.Settings
	Chapters     []bookChapter
	TOC          []reader.TOCEntry
	ContentError string
	StateJSON    template.JS
}

// readerPageState is embedded in the book page and returned (minus the
// book block) by GET /api/books/{id}/state. It is the reader's initial,
// offline-capable state.
type readerPageState struct {
	Book        readerPageBook      `json:"book"`
	Settings    reader.Settings     `json:"settings"`
	Progress    *progressPayload    `json:"progress"`
	Annotations []annotationPayload `json:"annotations"`
}

type readerPageBook struct {
	ID       string `json:"id"`
	SHA256   string `json:"sha256"`
	Title    string `json:"title"`
	Language string `json:"language,omitempty"`
}

// encodeReaderState marshals state for the inline JSON script. Escaping
// "</" keeps the payload from closing the script element early.
func encodeReaderState(state any) template.JS {
	data, err := json.Marshal(state)
	if err != nil {
		return template.JS("{}")
	}
	return template.JS(strings.ReplaceAll(string(data), "</", `<\/`))
}

type settingsPage struct {
	baseData
	Reader reader.Settings
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
	base := baseData{
		Title: title,
		User:  userFrom(r),
		CSRF:  s.auth.CSRFToken(w, r),
	}
	if base.User != nil {
		if settings, err := s.readerSettings(r); err == nil {
			base.Theme = settings.Theme
			base.FontFamily = settings.FontFamily
			base.FontSize = settings.FontSize
			base.LineHeight = settings.LineHeight
			base.Measure = settings.Measure
		} else {
			s.log.Warn("reader settings unavailable for page chrome", "error", err)
		}
	}
	return base
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
