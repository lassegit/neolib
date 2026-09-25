package web_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lassegit/neolib/internal/auth"
	"github.com/lassegit/neolib/internal/config"
	"github.com/lassegit/neolib/internal/database"
	"github.com/lassegit/neolib/internal/store"
	"github.com/lassegit/neolib/internal/web"
)

var csrfPattern = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

type testApp struct {
	server *httptest.Server
	client *http.Client
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	return newTestAppWithConfig(t, config.Config{
		Addr:           ":0",
		DataDir:        t.TempDir(),
		MaxUploadBytes: 8 << 20,
	})
}

func newTestAppWithConfig(t *testing.T, cfg config.Config) *testApp {
	t.Helper()

	for _, dir := range []string{cfg.BooksDir(), cfg.TempDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}

	db, err := database.Open(context.Background(), cfg.DBPath())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	st := store.New(db)
	manager := auth.NewManager(st, auth.Options{
		SecureCookies: config.SecureNever,
		SessionTTL:    time.Hour,
	})
	handler, err := web.New(web.Deps{
		Config: cfg,
		Store:  st,
		Auth:   manager,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(handler.Handler())
	t.Cleanup(ts.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return &testApp{
		server: ts,
		client: &http.Client{
			Jar: jar,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (a *testApp) get(t *testing.T, path string) *http.Response {
	t.Helper()
	resp, err := a.client.Get(a.server.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func (a *testApp) postForm(t *testing.T, path string, values url.Values) *http.Response {
	t.Helper()
	resp, err := a.client.PostForm(a.server.URL+path, values)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}

func csrfFrom(t *testing.T, page string) string {
	t.Helper()
	match := csrfPattern.FindStringSubmatch(page)
	if match == nil {
		t.Fatalf("no CSRF token found in page")
	}
	return match[1]
}

func signup(t *testing.T, app *testApp, email, password string) {
	t.Helper()
	csrf := csrfFrom(t, body(t, app.get(t, "/signup")))
	resp := app.postForm(t, "/signup", url.Values{
		"csrf_token":       {csrf},
		"email":            {email},
		"display_name":     {"Reader"},
		"password":         {password},
		"password_confirm": {password},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("signup status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
}

func TestAuthFlow(t *testing.T) {
	app := newTestApp(t)

	resp := app.get(t, "/")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("anonymous GET / status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	if loc := resp.Header.Get("Location"); loc != "/signin?next=%2F" {
		t.Fatalf("anonymous GET / redirect = %q", loc)
	}

	// The signup page sets a CSRF cookie and embeds the matching token.
	signup(t, app, "reader@example.com", "correct horse battery")

	resp = app.get(t, "/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if page := body(t, resp); !strings.Contains(page, "Library") {
		t.Fatalf("library page does not contain heading: %s", page)
	}

	resp = app.get(t, "/settings")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /settings status = %d", resp.StatusCode)
	}
	settingsPage := body(t, resp)
	if !strings.Contains(settingsPage, "reader@example.com") {
		t.Fatalf("settings page does not show the account email")
	}

	// Sign out ends the session.
	resp = app.postForm(t, "/signout", url.Values{"csrf_token": {csrfFrom(t, settingsPage)}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/signin" {
		t.Fatalf("signout = %d %q, want 303 /signin", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Registration closes after the first account (the default policy).
	resp = app.get(t, "/signup")
	signupPage := body(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(signupPage, "Registration is closed") {
		t.Fatalf("signup page after first account = %d: %s", resp.StatusCode, signupPage)
	}
	resp = app.postForm(t, "/signup", url.Values{
		"csrf_token":       {csrfFrom(t, body(t, app.get(t, "/signin")))},
		"email":            {"other@example.com"},
		"password":         {"correct horse battery"},
		"password_confirm": {"correct horse battery"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("second signup status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}

	resp = app.get(t, "/")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET / after signout status = %d, want redirect", resp.StatusCode)
	}
}

func TestSignin(t *testing.T) {
	app := newTestApp(t)
	signup(t, app, "reader@example.com", "correct horse battery")

	resp := app.get(t, "/")
	resp.Body.Close()

	resp = app.postForm(t, "/signout", url.Values{
		"csrf_token": {csrfFrom(t, body(t, app.get(t, "/settings")))},
	})
	resp.Body.Close()

	signinPage := body(t, app.get(t, "/signin"))
	csrf := csrfFrom(t, signinPage)

	resp = app.postForm(t, "/signin", url.Values{
		"csrf_token": {csrf},
		"email":      {"reader@example.com"},
		"password":   {"wrong password"},
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	if page := body(t, resp); !strings.Contains(page, "incorrect") {
		t.Fatalf("wrong password message missing: %s", page)
	}

	resp = app.postForm(t, "/signin", url.Values{
		"csrf_token": {csrf},
		"email":      {"reader@example.com"},
		"password":   {"correct horse battery"},
		"next":       {"/settings"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/settings" {
		t.Fatalf("signin = %d %q, want 303 /settings", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestCSRFRequired(t *testing.T) {
	app := newTestApp(t)
	resp := app.postForm(t, "/signup", url.Values{
		"email":            {"reader@example.com"},
		"password":         {"correct horse battery"},
		"password_confirm": {"correct horse battery"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("signup without CSRF token status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestImportEPUB(t *testing.T) {
	app := newTestApp(t)
	signup(t, app, "reader@example.com", "correct horse battery")

	epubData := buildTestEPUB(t)

	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	if err := writer.WriteField("csrf_token", csrfFrom(t, body(t, app.get(t, "/")))); err != nil {
		t.Fatalf("write csrf field: %v", err)
	}
	part, err := writer.CreateFormFile("file", "test.epub")
	if err != nil {
		t.Fatalf("create file field: %v", err)
	}
	if _, err := part.Write(epubData); err != nil {
		t.Fatalf("write epub: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	resp, err := app.client.Post(app.server.URL+"/books", writer.FormDataContentType(), &payload)
	if err != nil {
		t.Fatalf("POST /books: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("import status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	location := resp.Header.Get("Location")
	if !strings.HasPrefix(location, "/books/") {
		t.Fatalf("import redirect = %q", location)
	}

	// The catalog now lists the book.
	if page := body(t, app.get(t, "/")); !strings.Contains(page, "Test Book") {
		t.Fatalf("library does not list imported book: %s", page)
	}

	// The detail page and cover are served.
	resp = app.get(t, location)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d", location, resp.StatusCode)
	}
	if page := body(t, resp); !strings.Contains(page, "Ada Lovelace") {
		t.Fatalf("detail page missing author: %s", page)
	}

	resp = app.get(t, location+"/cover")
	cover := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET cover status = %d", resp.StatusCode)
	}
	if !strings.HasPrefix(cover, "\xff\xd8\xff") {
		t.Fatalf("cover is not a JPEG")
	}
}

// The book page shows every chapter as one scrollable document and serves
// the resources embedded in the EPUB.
func TestBookContent(t *testing.T) {
	app := newTestApp(t)
	signup(t, app, "reader@example.com", "correct horse battery")

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
	location := resp.Header.Get("Location")

	page := body(t, app.get(t, location))
	for _, want := range []string{
		`<nav aria-label="Table of contents">`,
		`href="#c0"`,
		`<section id="c0" aria-labelledby="c0-chapter">`,
		`<h1 id="c0-chapter">Chapter One</h1>`,
		"Hello from the chapter.",
		`src="/books/`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("book page missing %q:\n%s", want, page)
		}
	}

	// An embedded image is served with an immutable cache header.
	resp = app.get(t, location+"/resource/OEBPS/images/cover.jpg")
	image := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET resource status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !strings.HasPrefix(image, "\xff\xd8\xff") {
		t.Fatalf("resource is not a JPEG: %q", image)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable", cc)
	}

	// Only archive members are reachable.
	resp = app.get(t, location+"/resource/OEBPS/nope.png")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing resource status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestImportWrappedEPUB(t *testing.T) {
	app := newTestApp(t)
	signup(t, app, "reader@example.com", "correct horse battery")

	encoded := wrapTestEPUB(t, "Composing Software.epub", buildTestEPUB(t))

	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	if err := writer.WriteField("csrf_token", csrfFrom(t, body(t, app.get(t, "/")))); err != nil {
		t.Fatalf("write csrf field: %v", err)
	}
	part, err := writer.CreateFormFile("file", "Composing Software.epub.zip")
	if err != nil {
		t.Fatalf("create file field: %v", err)
	}
	if _, err := part.Write(encoded); err != nil {
		t.Fatalf("write epub: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	resp, err := app.client.Post(app.server.URL+"/books", writer.FormDataContentType(), &payload)
	if err != nil {
		t.Fatalf("POST /books: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("import status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	location := resp.Header.Get("Location")
	if !strings.HasPrefix(location, "/books/") {
		t.Fatalf("import redirect = %q", location)
	}

	// The stored book must be the inner EPUB, not the outer wrapper.
	resp = app.get(t, location+"/file")
	stored := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s/file status = %d", location, resp.StatusCode)
	}
	reader, err := zip.NewReader(strings.NewReader(stored), int64(len(stored)))
	if err != nil {
		t.Fatalf("stored book is not a zip: %v", err)
	}
	if _, err := reader.Open("META-INF/container.xml"); err != nil {
		t.Fatalf("stored book is missing META-INF/container.xml: %v", err)
	}
}

func TestImportPrefixedEPUB(t *testing.T) {
	app := newTestApp(t)
	signup(t, app, "reader@example.com", "correct horse battery")

	encoded := prefixTestEPUB(t, "Composing Software.epub/", buildTestEPUB(t))

	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	if err := writer.WriteField("csrf_token", csrfFrom(t, body(t, app.get(t, "/")))); err != nil {
		t.Fatalf("write csrf field: %v", err)
	}
	part, err := writer.CreateFormFile("file", "Composing Software.epub.zip")
	if err != nil {
		t.Fatalf("create file field: %v", err)
	}
	if _, err := part.Write(encoded); err != nil {
		t.Fatalf("write epub: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	resp, err := app.client.Post(app.server.URL+"/books", writer.FormDataContentType(), &payload)
	if err != nil {
		t.Fatalf("POST /books: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("import status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	location := resp.Header.Get("Location")

	// The stored book must be repackaged without the leading directory,
	// with a conforming mimetype member.
	resp = app.get(t, location+"/file")
	stored := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s/file status = %d", location, resp.StatusCode)
	}
	reader, err := zip.NewReader(strings.NewReader(stored), int64(len(stored)))
	if err != nil {
		t.Fatalf("stored book is not a zip: %v", err)
	}
	if len(reader.File) == 0 || reader.File[0].Name != "mimetype" || reader.File[0].Method != zip.Store {
		t.Fatalf("stored book does not start with a stored mimetype member")
	}
	if _, err := reader.Open("META-INF/container.xml"); err != nil {
		t.Fatalf("stored book is missing META-INF/container.xml: %v", err)
	}
}

// The form must let users pick several EPUBs at once.
func TestLibraryFormAllowsMultiple(t *testing.T) {
	app := newTestApp(t)
	signup(t, app, "reader@example.com", "correct horse battery")

	page := body(t, app.get(t, "/"))
	if !strings.Contains(page, `type="file"`) || !strings.Contains(page, "multiple") {
		t.Fatalf("library form does not allow multiple files: %s", page)
	}
}

func TestImportMultipleEPUBs(t *testing.T) {
	app := newTestApp(t)
	signup(t, app, "reader@example.com", "correct horse battery")

	payload, contentType := multipartUploads(t, csrfFrom(t, body(t, app.get(t, "/"))),
		uploadFile{name: "first.epub", data: buildTestEPUBWithTitle(t, "First Book")},
		uploadFile{name: "second.epub", data: buildTestEPUBWithTitle(t, "Second Book")},
	)
	resp, err := app.client.Post(app.server.URL+"/books", contentType, payload)
	if err != nil {
		t.Fatalf("POST /books: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("import = %d %q, want 303 /", resp.StatusCode, resp.Header.Get("Location"))
	}

	page := body(t, app.get(t, "/"))
	for _, title := range []string{"First Book", "Second Book"} {
		if !strings.Contains(page, title) {
			t.Errorf("library does not list %q: %s", title, page)
		}
	}
}

func TestImportMultiplePartialFailure(t *testing.T) {
	app := newTestApp(t)
	signup(t, app, "reader@example.com", "correct horse battery")

	payload, contentType := multipartUploads(t, csrfFrom(t, body(t, app.get(t, "/"))),
		uploadFile{name: "good.epub", data: buildTestEPUBWithTitle(t, "Good Book")},
		uploadFile{name: "bad.epub", data: []byte("this is not an epub")},
	)
	resp, err := app.client.Post(app.server.URL+"/books", contentType, payload)
	if err != nil {
		t.Fatalf("POST /books: %v", err)
	}
	status := resp.StatusCode
	page := body(t, resp)
	if status != http.StatusBadRequest {
		t.Fatalf("import status = %d, want %d", status, http.StatusBadRequest)
	}
	if !strings.Contains(page, "Imported 1 of 2 books.") {
		t.Errorf("partial import notice missing: %s", page)
	}
	if !strings.Contains(page, "bad.epub") {
		t.Errorf("rejected filename missing: %s", page)
	}
	if !strings.Contains(page, "Good Book") {
		t.Errorf("imported book not listed: %s", page)
	}
}

func TestUploadTooLarge(t *testing.T) {
	app := newTestAppWithConfig(t, config.Config{
		Addr:           ":0",
		DataDir:        t.TempDir(),
		MaxUploadBytes: 1 << 10,
	})
	signup(t, app, "reader@example.com", "correct horse battery")

	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	if err := writer.WriteField("csrf_token", csrfFrom(t, body(t, app.get(t, "/")))); err != nil {
		t.Fatalf("write csrf field: %v", err)
	}
	part, err := writer.CreateFormFile("file", "big.epub")
	if err != nil {
		t.Fatalf("create file field: %v", err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("x"), 4<<10)); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	resp, err := app.client.Post(app.server.URL+"/books", writer.FormDataContentType(), &payload)
	if err != nil {
		t.Fatalf("POST /books: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload status = %d, want %d", resp.StatusCode, http.StatusRequestEntityTooLarge)
	}
}

func buildTestEPUB(t *testing.T) []byte {
	t.Helper()
	return buildTestEPUBWithTitle(t, "Test Book")
}

func buildTestEPUBWithTitle(t *testing.T, title string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	add("mimetype", "application/epub+zip")
	add("META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
<rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`)
	add("OEBPS/content.opf", strings.ReplaceAll(`<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
<dc:title>Test Book</dc:title><dc:creator>Ada Lovelace</dc:creator>
<meta name="cover" content="cover-img"/>
</metadata>
<manifest>
<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
<item id="chapter" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
<item id="cover-img" href="images/cover.jpg" media-type="image/jpeg"/>
</manifest>
<spine><itemref idref="chapter"/></spine>
</package>`, "Test Book", title))
	add("OEBPS/nav.xhtml", `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body><nav epub:type="toc"><ol>
<li><a href="chapter1.xhtml#chapter">Chapter One</a></li>
</ol></nav></body></html>`)
	add("OEBPS/chapter1.xhtml", `<?xml version="1.0"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body>
<h1 id="chapter">Chapter One</h1>
<p>Hello from the chapter.</p>
<img src="images/cover.jpg" alt="cover">
</body></html>`)
	cover, err := zw.Create("OEBPS/images/cover.jpg")
	if err != nil {
		t.Fatalf("create cover: %v", err)
	}
	if _, err := cover.Write([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00}); err != nil {
		t.Fatalf("write cover: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func wrapTestEPUB(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func prefixTestEPUB(t *testing.T, prefix string, data []byte) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open epub: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, file := range reader.File {
		w, err := zw.Create(prefix + file.Name)
		if err != nil {
			t.Fatalf("create %s: %v", file.Name, err)
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", file.Name, err)
		}
		if _, err := io.Copy(w, rc); err != nil {
			t.Fatalf("copy %s: %v", file.Name, err)
		}
		rc.Close()
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

type uploadFile struct {
	name string
	data []byte
}

// multipartUploads builds a form body carrying one or more "file" parts.
func multipartUploads(t *testing.T, csrf string, files ...uploadFile) (*bytes.Buffer, string) {
	t.Helper()
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	if err := writer.WriteField("csrf_token", csrf); err != nil {
		t.Fatalf("write csrf field: %v", err)
	}
	for _, file := range files {
		part, err := writer.CreateFormFile("file", file.name)
		if err != nil {
			t.Fatalf("create file field: %v", err)
		}
		if _, err := part.Write(file.data); err != nil {
			t.Fatalf("write %s: %v", file.name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return &payload, writer.FormDataContentType()
}
