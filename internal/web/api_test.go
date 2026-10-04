package web_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// importTestBook uploads the standard test EPUB and returns its catalog id.
func importTestBook(t *testing.T, app *testApp) string {
	t.Helper()
	payload, contentType := multipartUploads(t, csrfFrom(t, body(t, app.get(t, "/"))),
		uploadFile{name: "test.epub", data: buildTestEPUB(t)},
	)
	resp, err := app.client.Post(app.server.URL+"/books", contentType, payload)
	if err != nil {
		t.Fatalf("POST /books: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("import status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	return strings.TrimPrefix(resp.Header.Get("Location"), "/books/")
}

// jsonRequest performs a JSON request with an optional CSRF header.
func (a *testApp) jsonRequest(t *testing.T, method, path string, payload any, csrf string) *http.Response {
	t.Helper()
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, a.server.URL+path, body)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func decodeBody[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

type annotationResponse struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Color     string          `json:"color"`
	Body      string          `json:"body"`
	Locator   json.RawMessage `json:"locator"`
	CreatedAt int64           `json:"createdAt"`
	UpdatedAt int64           `json:"updatedAt"`
}

type progressResponse struct {
	Last                json.RawMessage `json:"last"`
	Furthest            json.RawMessage `json:"furthest"`
	LastProgression     float64         `json:"lastProgression"`
	FurthestProgression float64         `json:"furthestProgression"`
	UpdatedAt           int64           `json:"updatedAt"`
}

type stateResponse struct {
	Settings    map[string]any       `json:"settings"`
	Progress    *progressResponse    `json:"progress"`
	Annotations []annotationResponse `json:"annotations"`
}

func testLocatorPayload(href string, progression float64) map[string]any {
	return map[string]any{
		"v":    1,
		"href": href,
		"type": "application/xhtml+xml",
		"locations": map[string]any{
			"progression":      progression,
			"totalProgression": progression,
		},
		"text": map[string]any{"highlight": "Hello"},
	}
}

// Anonymous API calls answer with 401 JSON, not an HTML redirect.
func TestAPIRequiresAuth(t *testing.T) {
	app := newTestApp(t)
	resp := app.jsonRequest(t, http.MethodGet, "/api/books/whatever/state", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous state status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
	payload := decodeBody[map[string]string](t, resp)
	if payload["error"] == "" {
		t.Errorf("error payload = %v", payload)
	}
}

func TestAnnotationAPI(t *testing.T) {
	app := newTestApp(t)
	signup(t, app, "reader@example.com", "correct horse battery")
	bookID := importTestBook(t, app)
	csrf := csrfFrom(t, body(t, app.get(t, "/settings")))

	locator := testLocatorPayload("OEBPS/chapter1.xhtml", 0.2)
	create := map[string]any{"kind": "highlight", "color": "yellow", "locator": locator}

	// State-changes require the CSRF header.
	resp := app.jsonRequest(t, http.MethodPost, "/api/books/"+bookID+"/annotations", create, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
	resp.Body.Close()

	// Unknown kinds are rejected.
	resp = app.jsonRequest(t, http.MethodPost, "/api/books/"+bookID+"/annotations",
		map[string]any{"kind": "scrawl", "locator": locator}, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown kind status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	resp.Body.Close()

	// Creating a highlight returns the stored annotation.
	resp = app.jsonRequest(t, http.MethodPost, "/api/books/"+bookID+"/annotations", create, csrf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create annotation status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	created := decodeBody[annotationResponse](t, resp)
	if created.ID == "" || created.Kind != "highlight" || created.Color != "yellow" {
		t.Fatalf("created annotation = %+v", created)
	}
	if created.Locator == nil || created.CreatedAt == 0 {
		t.Fatalf("created annotation is missing fields: %+v", created)
	}

	// Unknown books are a 404, not an annotation on a ghost book.
	resp = app.jsonRequest(t, http.MethodPost, "/api/books/nope/annotations", create, csrf)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown book status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
	resp.Body.Close()

	// The state endpoint lists the annotation.
	state := decodeBody[stateResponse](t, app.jsonRequest(t, http.MethodGet, "/api/books/"+bookID+"/state", nil, ""))
	if len(state.Annotations) != 1 || state.Annotations[0].ID != created.ID {
		t.Fatalf("state annotations = %+v", state.Annotations)
	}

	// Updates patch the colour.
	resp = app.jsonRequest(t, http.MethodPatch, "/api/annotations/"+created.ID,
		map[string]any{"color": "blue"}, csrf)
	updated := decodeBody[annotationResponse](t, resp)
	if updated.Color != "blue" {
		t.Fatalf("updated colour = %q, want blue", updated.Color)
	}

	// A malformed locator is rejected.
	resp = app.jsonRequest(t, http.MethodPost, "/api/books/"+bookID+"/annotations",
		map[string]any{
			"kind": "bookmark",
			"locator": map[string]any{
				"v":         1,
				"href":      "OEBPS/chapter1.xhtml",
				"locations": map[string]any{"progression": 3.5},
			},
		}, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("out-of-range progression status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	resp.Body.Close()

	// Deleting is idempotent from the client's point of view: the second
	// delete reports not found.
	resp = app.jsonRequest(t, http.MethodDelete, "/api/annotations/"+created.ID, nil, csrf)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	resp.Body.Close()
	resp = app.jsonRequest(t, http.MethodDelete, "/api/annotations/"+created.ID, nil, csrf)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
	resp.Body.Close()
}

func TestProgressAPI(t *testing.T) {
	app := newTestApp(t)
	signup(t, app, "reader@example.com", "correct horse battery")
	bookID := importTestBook(t, app)
	csrf := csrfFrom(t, body(t, app.get(t, "/settings")))

	resp := app.jsonRequest(t, http.MethodPut, "/api/books/"+bookID+"/progress",
		map[string]any{
			"last":     testLocatorPayload("OEBPS/chapter1.xhtml", 0.4),
			"furthest": testLocatorPayload("OEBPS/chapter1.xhtml", 0.7),
		}, csrf)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save progress status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	progress := decodeBody[progressResponse](t, resp)
	if progress.LastProgression != 0.4 || progress.FurthestProgression != 0.7 {
		t.Fatalf("progress = %+v", progress)
	}

	// A stale device cannot rewind the high-water mark.
	resp = app.jsonRequest(t, http.MethodPut, "/api/books/"+bookID+"/progress",
		map[string]any{
			"last":     testLocatorPayload("OEBPS/chapter1.xhtml", 0.2),
			"furthest": testLocatorPayload("OEBPS/chapter1.xhtml", 0.1),
		}, csrf)
	progress = decodeBody[progressResponse](t, resp)
	if progress.LastProgression != 0.2 {
		t.Fatalf("last = %v, want 0.2", progress.LastProgression)
	}
	if progress.FurthestProgression != 0.7 {
		t.Fatalf("furthest rewound to %v, want 0.7", progress.FurthestProgression)
	}

	// The state endpoint returns the stored progress.
	state := decodeBody[stateResponse](t, app.jsonRequest(t, http.MethodGet, "/api/books/"+bookID+"/state", nil, ""))
	if state.Progress == nil || state.Progress.FurthestProgression != 0.7 {
		t.Fatalf("state progress = %+v", state.Progress)
	}
}

func TestReaderDisplaySettingsAPI(t *testing.T) {
	app := newTestApp(t)
	signup(t, app, "reader@example.com", "correct horse battery")
	bookID := importTestBook(t, app)
	csrf := csrfFrom(t, body(t, app.get(t, "/settings")))

	// Display preferences are clamped and stored.
	resp := app.jsonRequest(t, http.MethodPatch, "/api/reader/settings", map[string]any{
		"theme":       "dark",
		"font_family": "sans",
		"font_size":   1.4,
		"line_height": 1.8,
		"measure":     72,
	}, csrf)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("display settings status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	settings := decodeBody[map[string]any](t, resp)
	if settings["theme"] != "dark" || settings["font_family"] != "sans" {
		t.Fatalf("settings = %+v", settings)
	}

	// The book page server-renders the display attributes, so the first
	// paint has the right theme.
	page := body(t, app.get(t, "/books/"+bookID))
	for _, want := range []string{
		`data-reader-theme="dark"`,
		`data-reader-font="sans"`,
		"--reader-font-size:1.4rem",
		"--reader-line-height:1.8",
		"--reader-measure:72ch",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("book page missing %q:\n%s", want, page)
		}
	}

	// The theme reaches the rest of the shell too, so navigating between
	// pages does not flash the default background.
	for _, path := range []string{"/", "/settings"} {
		shell := body(t, app.get(t, path))
		if !strings.Contains(shell, `data-reader-theme="dark"`) {
			t.Errorf("%s missing the reader theme attribute:\n%s", path, shell)
		}
	}

	// Saving the behavioral form must preserve display preferences.
	formCSRF := csrfFrom(t, body(t, app.get(t, "/settings")))
	resp = app.postForm(t, "/settings/reader", url.Values{
		"csrf_token":     {formCSRF},
		"external_links": {"same_tab"},
		"images":         {"plain"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("behavior save status = %d", resp.StatusCode)
	}
	state := decodeBody[stateResponse](t, app.jsonRequest(t, http.MethodGet, "/api/books/"+bookID+"/state", nil, ""))
	if state.Settings["theme"] != "dark" || state.Settings["font_family"] != "sans" {
		t.Fatalf("display preferences were reset: %+v", state.Settings)
	}
	if state.Settings["external_links"] != "same_tab" {
		t.Fatalf("behavior preferences were not saved: %+v", state.Settings)
	}

	// Out-of-range or unknown display values are rejected.
	for _, invalid := range []map[string]any{
		{"theme": "neon"},
		{"font_size": 9},
		{"measure": 1000},
	} {
		resp = app.jsonRequest(t, http.MethodPatch, "/api/reader/settings", invalid, csrf)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("invalid settings %v status = %d, want %d", invalid, resp.StatusCode, http.StatusBadRequest)
		}
		resp.Body.Close()
	}
}
