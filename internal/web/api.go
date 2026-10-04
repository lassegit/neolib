package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lassegit/neolib/internal/locator"
	"github.com/lassegit/neolib/internal/reader"
	"github.com/lassegit/neolib/internal/store"
)

// maxAPIBody caps JSON request bodies. Locators with full quotes are small;
// anything larger is a client bug or an attack.
const maxAPIBody = 256 << 10

// annotationPayload is the wire shape of a bookmark or highlight.
type annotationPayload struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Color     string          `json:"color,omitempty"`
	Body      string          `json:"body,omitempty"`
	Locator   locator.Locator `json:"locator"`
	CreatedAt int64           `json:"createdAt"`
	UpdatedAt int64           `json:"updatedAt"`
}

type progressPayload struct {
	Last                locator.Locator `json:"last"`
	Furthest            locator.Locator `json:"furthest"`
	LastProgression     float64         `json:"lastProgression"`
	FurthestProgression float64         `json:"furthestProgression"`
	UpdatedAt           int64           `json:"updatedAt"`
}

type bookStatePayload struct {
	Settings    reader.Settings     `json:"settings"`
	Progress    *progressPayload    `json:"progress"`
	Annotations []annotationPayload `json:"annotations"`
	ServerTime  int64               `json:"serverTime"`
}

type annotationInput struct {
	Kind    string          `json:"kind"`
	Color   string          `json:"color"`
	Body    string          `json:"body"`
	Locator locator.Locator `json:"locator"`
}

type annotationPatch struct {
	Color   *string          `json:"color"`
	Body    *string          `json:"body"`
	Locator *locator.Locator `json:"locator"`
}

type progressInput struct {
	Last     locator.Locator  `json:"last"`
	Furthest *locator.Locator `json:"furthest"`
}

// requireAPIAuth behaves like requireAuth but answers anonymous requests
// with 401 JSON instead of an HTML redirect, so fetch clients can tell an
// expired session apart from a network failure.
func (s *Server) requireAPIAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := s.auth.User(r)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "Not signed in.")
			return
		}
		next(w, r.WithContext(withUser(r.Context(), user)))
	})
}

// checkAPICSRF validates the double-submit token supplied in the
// X-CSRF-Token header. The CSRF cookie itself stays HttpOnly.
func (s *Server) checkAPICSRF(w http.ResponseWriter, r *http.Request) bool {
	if s.auth.ValidCSRF(r, r.Header.Get("X-CSRF-Token")) {
		return true
	}
	writeJSONError(w, http.StatusForbidden, "Your session has expired. Reload the page and try again.")
	return false
}

// decodeJSON reads and validates one JSON request body.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAPIBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "The request is too large.")
			return false
		}
		writeJSONError(w, http.StatusBadRequest, "The request body is not valid JSON.")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeJSONError(w, http.StatusBadRequest, "The request must contain one JSON object.")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func toAnnotationPayload(a store.Annotation) annotationPayload {
	return annotationPayload{
		ID:        a.ID,
		Kind:      a.Kind,
		Color:     a.Color,
		Body:      a.Body,
		Locator:   a.Locator,
		CreatedAt: a.CreatedAt,
		UpdatedAt: a.UpdatedAt,
	}
}

func toProgressPayload(p store.Progress) progressPayload {
	return progressPayload{
		Last:                p.Last,
		Furthest:            p.Furthest,
		LastProgression:     p.LastProgression,
		FurthestProgression: p.FurthestProgression,
		UpdatedAt:           p.UpdatedAt,
	}
}

// apiBookState returns everything the reader needs after the initial page
// render: display settings, reading progress, and annotations.
func (s *Server) apiBookState(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r)
	book, err := s.store.BookByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "Book not found.")
		return
	}
	if err != nil {
		s.apiServerError(w, err)
		return
	}

	settings, err := s.readerSettings(r)
	if err != nil {
		s.apiServerError(w, err)
		return
	}
	annotations, err := s.store.ListAnnotations(r.Context(), user.ID, book.ID)
	if err != nil {
		s.apiServerError(w, err)
		return
	}
	payload := bookStatePayload{
		Settings:    settings,
		Annotations: make([]annotationPayload, 0, len(annotations)),
		ServerTime:  time.Now().Unix(),
	}
	for _, a := range annotations {
		payload.Annotations = append(payload.Annotations, toAnnotationPayload(a))
	}
	if progress, err := s.store.GetProgress(r.Context(), user.ID, book.ID); err == nil {
		p := toProgressPayload(progress)
		payload.Progress = &p
	} else if !errors.Is(err, store.ErrNotFound) {
		s.apiServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

// apiSaveProgress upserts the reader position for a book.
func (s *Server) apiSaveProgress(w http.ResponseWriter, r *http.Request) {
	if !s.checkAPICSRF(w, r) {
		return
	}
	user := userFrom(r)
	book, err := s.store.BookByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "Book not found.")
		return
	}
	if err != nil {
		s.apiServerError(w, err)
		return
	}

	var input progressInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Last = input.Last.Normalize()
	if err := input.Last.Validate(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "The reading position is malformed.")
		return
	}
	if input.Furthest != nil {
		furthest := input.Furthest.Normalize()
		if err := furthest.Validate(); err != nil {
			writeJSONError(w, http.StatusBadRequest, "The reading position is malformed.")
			return
		}
		input.Furthest = &furthest
	}

	progress, err := s.store.SaveProgress(r.Context(), user.ID, book.ID, input.Last, input.Furthest)
	if err != nil {
		s.apiServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toProgressPayload(progress))
}

// apiCreateAnnotation creates a bookmark or highlight.
func (s *Server) apiCreateAnnotation(w http.ResponseWriter, r *http.Request) {
	if !s.checkAPICSRF(w, r) {
		return
	}
	user := userFrom(r)
	book, err := s.store.BookByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "Book not found.")
		return
	}
	if err != nil {
		s.apiServerError(w, err)
		return
	}

	var input annotationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	loc := input.Locator.Normalize()
	if !store.ValidAnnotationKind(input.Kind) {
		writeJSONError(w, http.StatusBadRequest, "Unknown annotation kind.")
		return
	}
	if !store.ValidAnnotationColor(input.Color) {
		writeJSONError(w, http.StatusBadRequest, "Unknown highlight color.")
		return
	}
	if err := loc.Validate(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "The annotation location is malformed.")
		return
	}
	if len([]rune(input.Body)) > 10000 {
		writeJSONError(w, http.StatusBadRequest, "The note is too long.")
		return
	}

	annotation, err := s.store.CreateAnnotation(r.Context(), user.ID, book.ID, store.NewAnnotation{
		Kind:    input.Kind,
		Color:   input.Color,
		Body:    strings.TrimSpace(input.Body),
		Locator: loc,
	})
	if err != nil {
		s.apiServerError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toAnnotationPayload(annotation))
}

// apiUpdateAnnotation updates an annotation's colour, note, or locator.
func (s *Server) apiUpdateAnnotation(w http.ResponseWriter, r *http.Request) {
	if !s.checkAPICSRF(w, r) {
		return
	}
	user := userFrom(r)
	var patch annotationPatch
	if !decodeJSON(w, r, &patch) {
		return
	}
	if patch.Color != nil && !store.ValidAnnotationColor(*patch.Color) {
		writeJSONError(w, http.StatusBadRequest, "Unknown highlight color.")
		return
	}
	if patch.Body != nil && len([]rune(*patch.Body)) > 10000 {
		writeJSONError(w, http.StatusBadRequest, "The note is too long.")
		return
	}
	if patch.Locator != nil {
		loc := patch.Locator.Normalize()
		if err := loc.Validate(); err != nil {
			writeJSONError(w, http.StatusBadRequest, "The annotation location is malformed.")
			return
		}
		patch.Locator = &loc
	}

	annotation, err := s.store.UpdateAnnotation(r.Context(), user.ID, r.PathValue("id"), patch.Body, patch.Color, patch.Locator)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "Annotation not found.")
		return
	}
	if err != nil {
		s.apiServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toAnnotationPayload(annotation))
}

// apiDeleteAnnotation removes an annotation.
func (s *Server) apiDeleteAnnotation(w http.ResponseWriter, r *http.Request) {
	if !s.checkAPICSRF(w, r) {
		return
	}
	user := userFrom(r)
	err := s.store.DeleteAnnotation(r.Context(), user.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "Annotation not found.")
		return
	}
	if err != nil {
		s.apiServerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// apiReaderSettings merges display preferences into the stored settings.
func (s *Server) apiReaderSettings(w http.ResponseWriter, r *http.Request) {
	if !s.checkAPICSRF(w, r) {
		return
	}
	var submitted reader.Settings
	if !decodeJSON(w, r, &submitted) {
		return
	}
	if !submitted.ValidateDisplay() {
		writeJSONError(w, http.StatusBadRequest, "Unknown or out-of-range display settings.")
		return
	}
	current, err := s.readerSettings(r)
	if err != nil {
		s.apiServerError(w, err)
		return
	}
	merged := current.Overlay(submitted)
	data, err := merged.Encode()
	if err != nil {
		s.apiServerError(w, err)
		return
	}
	if err := s.store.SaveUserSettings(r.Context(), userFrom(r).ID, data); err != nil {
		s.apiServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, merged)
}

// apiServerError reports an internal failure without leaking details.
func (s *Server) apiServerError(w http.ResponseWriter, err error) {
	s.log.Error("api request failed", "error", err)
	writeJSONError(w, http.StatusInternalServerError, "Something went wrong.")
}
