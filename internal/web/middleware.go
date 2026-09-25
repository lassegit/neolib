package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"runtime/debug"
	"time"

	"github.com/lassegit/neolib/internal/auth"
	"github.com/lassegit/neolib/internal/store"
)

type contextKey int

const userContextKey contextKey = iota

// requireAuth resolves the session user and redirects anonymous visitors to
// the sign-in page, preserving where they were headed.
func (s *Server) requireAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := s.auth.User(r)
		if err != nil {
			if errors.Is(err, auth.ErrNoSession) {
				target := "/signin?next=" + url.QueryEscape(r.URL.RequestURI())
				http.Redirect(w, r, target, http.StatusSeeOther)
				return
			}
			s.serverError(w, r, err)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
	})
}

// userFrom returns the authenticated user placed on the context by requireAuth.
func userFrom(r *http.Request) *store.User {
	user, _ := r.Context().Value(userContextKey).(*store.User)
	return user
}

// maxMultipartMemory is how much of a multipart body is buffered in memory;
// the rest spills to disk. It matches net/http's own default.
const maxMultipartMemory = 32 << 20

// checkCSRF validates the double-submit token on state-changing requests. It
// must run after authentication so that expired sessions redirect to sign-in
// instead of failing with 403.
func (s *Server) checkCSRF(w http.ResponseWriter, r *http.Request) bool {
	// Reject requests that cannot possibly pass validation before parsing the
	// body: without the cookie, a cross-site submission could otherwise make
	// the server spool a large upload to disk only to discard it.
	if !s.auth.HasCSRFCookie(r) {
		s.renderError(w, r, http.StatusForbidden, "Forbidden",
			"Your form session has expired. Reload the page and try again.")
		return false
	}
	if r.Form == nil {
		// ParseMultipartForm parses multipart bodies and, via ParseForm,
		// urlencoded ones. It reports ErrNotMultipart for the latter after
		// ParseForm has already populated the form, so ignore that error.
		if err := r.ParseMultipartForm(maxMultipartMemory); err != nil && !errors.Is(err, http.ErrNotMultipart) {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				s.renderError(w, r, http.StatusRequestEntityTooLarge, "Upload too large",
					"The request exceeded the maximum upload size.")
				return false
			}
			s.renderError(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
			return false
		}
	}
	token := r.PostFormValue("csrf_token")
	if s.auth.ValidCSRF(r, token) {
		return true
	}
	s.renderError(w, r, http.StatusForbidden, "Forbidden",
		"Your form session has expired. Reload the page and try again.")
	return false
}

// limitBody caps every request body to the configured upload size.
func (s *Server) limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes)
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the response status for request logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (rec *statusRecorder) WriteHeader(status int) {
	rec.status = status
	rec.ResponseWriter.WriteHeader(status)
}

func (rec *statusRecorder) Write(data []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return rec.ResponseWriter.Write(data)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		s.log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start).Round(time.Millisecond),
		)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic",
					"panic", rec,
					"path", r.URL.Path,
					"stack", string(debug.Stack()),
				)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
