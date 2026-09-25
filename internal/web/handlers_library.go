package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/lassegit/neolib/internal/epub"
	"github.com/lassegit/neolib/internal/reader"
	"github.com/lassegit/neolib/internal/store"
)

func (s *Server) library(w http.ResponseWriter, r *http.Request) {
	books, err := s.store.ListBooks(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page := libraryPage{baseData: s.base(w, r, "Library"), Books: books}
	s.render(w, r, http.StatusOK, "library", page)
}

// errNotEPUB marks an upload that is not a readable EPUB, as opposed to
// an internal storage or database failure.
var errNotEPUB = errors.New("not an EPUB")

// importBook accepts multipart EPUB uploads, stores them content-addressed,
// and adds them to the catalog. Importing the same file twice is a no-op.
func (s *Server) importBook(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		s.renderLibraryError(w, r, http.StatusBadRequest, "The upload could not be read.")
		return
	}

	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		s.renderLibraryError(w, r, http.StatusBadRequest, "Choose one or more EPUB files to import.")
		return
	}

	// A single file keeps the original behavior: the reader lands on the
	// imported book. Batches report back on the library page.
	if len(files) == 1 {
		book, err := s.importOne(r, files[0])
		if errors.Is(err, errNotEPUB) {
			s.log.Warn("rejected import", "filename", files[0].Filename, "error", err)
			s.renderLibraryError(w, r, http.StatusBadRequest,
				"That file could not be read as an EPUB.")
			return
		}
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		http.Redirect(w, r, "/books/"+book.ID, http.StatusSeeOther)
		return
	}

	imported := 0
	var rejected []string
	for _, header := range files {
		_, err := s.importOne(r, header)
		switch {
		case errors.Is(err, errNotEPUB):
			s.log.Warn("rejected import", "filename", header.Filename, "error", err)
			rejected = append(rejected, header.Filename)
		case err != nil:
			s.serverError(w, r, err)
			return
		default:
			imported++
		}
	}

	if len(rejected) == 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	notice := ""
	if imported > 0 {
		notice = fmt.Sprintf("Imported %d of %d books.", imported, len(files))
	}
	s.renderLibrary(w, r, http.StatusBadRequest, notice,
		fmt.Sprintf("%d of %d files could not be read as EPUBs: %s",
			len(rejected), len(files), strings.Join(rejected, ", ")))
}

// importOne canonicalizes, stores, and catalogs one uploaded file.
func (s *Server) importOne(r *http.Request, header *multipart.FileHeader) (store.Book, error) {
	file, err := header.Open()
	if err != nil {
		return store.Book{}, err
	}
	defer file.Close()

	tmp, err := os.CreateTemp(s.cfg.TempDir(), "import-*.epub")
	if err != nil {
		return store.Book{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		return store.Book{}, err
	}
	if err := tmp.Close(); err != nil {
		return store.Book{}, err
	}

	// Some downloads deliver the EPUB inside an outer ZIP (for example
	// "book.epub.zip"); store the EPUB itself in that case.
	canonical, err := epub.Canonicalize(tmpName)
	if err != nil {
		return store.Book{}, fmt.Errorf("%w: %v", errNotEPUB, err)
	}
	if canonical != tmpName {
		defer os.Remove(canonical)
	}

	sum, err := fileSHA256(canonical)
	if err != nil {
		return store.Book{}, err
	}
	if existing, err := s.store.BookBySHA(r.Context(), sum); err == nil {
		return existing, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.Book{}, err
	}

	meta, err := epub.Read(canonical)
	if err != nil {
		return store.Book{}, fmt.Errorf("%w: %v", errNotEPUB, err)
	}

	title := meta.Title
	if title == "" {
		base := filepath.Base(header.Filename)
		title = strings.TrimSpace(strings.TrimSuffix(base, filepath.Ext(base)))
	}
	if title == "" {
		title = "Untitled"
	}

	dest := filepath.Join(s.cfg.BooksDir(), sum+".epub")
	if err := os.Rename(canonical, dest); err != nil {
		return store.Book{}, err
	}

	book, err := s.store.CreateBook(r.Context(), store.NewBook{
		SHA256:         sum,
		Title:          title,
		Author:         meta.Author,
		Identifier:     meta.Identifier,
		Publisher:      meta.Publisher,
		Published:      meta.Published,
		Language:       meta.Language,
		ISBN:           meta.ISBN,
		Cover:          meta.Cover,
		CoverMediaType: meta.CoverMediaType,
	})
	if errors.Is(err, store.ErrDuplicate) {
		if book, err = s.store.BookBySHA(r.Context(), sum); err != nil {
			return store.Book{}, err
		}
	} else if err != nil {
		return store.Book{}, err
	}

	s.log.Info("imported book", "id", book.ID, "title", book.Title, "sha256", sum)
	return book, nil
}

// fileSHA256 hashes a file's contents for content-addressed storage.
func fileSHA256(name string) (string, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func (s *Server) book(w http.ResponseWriter, r *http.Request) {
	book, err := s.store.BookByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "Book not found",
			"No book with that identifier exists in this library.")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	settings, err := s.readerSettings(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page := bookPage{baseData: s.base(w, r, book.Title), Book: book}
	pub, err := epub.Open(filepath.Join(s.cfg.BooksDir(), book.SHA256+".epub"))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		page.ContentError = "The EPUB file for this book is missing."
	case err != nil:
		s.log.Error("open book content", "book", book.ID, "error", err)
		page.ContentError = "The content of this EPUB could not be read."
	default:
		defer pub.Close()
		doc, err := reader.Build(pub, book.ID, settings)
		if err != nil {
			s.log.Error("build book content", "book", book.ID, "error", err)
			page.ContentError = "The content of this EPUB could not be read."
			break
		}
		for _, chapter := range doc.Chapters {
			page.Chapters = append(page.Chapters, bookChapter{
				ID:      chapter.ID,
				Title:   chapter.Title,
				LabelID: chapter.LabelID,
				HTML:    template.HTML(chapter.HTML),
				Notice:  chapter.Err,
			})
		}
	}
	s.render(w, r, http.StatusOK, "book", page)
}

// maxResourceBytes caps a single embedded resource served from an EPUB.
const maxResourceBytes = 64 << 20

// bookResource serves an image, stylesheet, font, or other file embedded in
// the book. Only members of the EPUB archive are reachable, never the
// filesystem, and the content-addressed book makes the response immutable.
func (s *Server) bookResource(w http.ResponseWriter, r *http.Request) {
	book, err := s.store.BookByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	pub, err := epub.Open(filepath.Join(s.cfg.BooksDir(), book.SHA256+".epub"))
	if errors.Is(err, fs.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	defer pub.Close()

	name := pub.ResolvePath(r.PathValue("path"))
	data, err := pub.Read(name, maxResourceBytes)
	if errors.Is(err, fs.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	mediaType := pub.MediaType(name)
	if mediaType == "" {
		mediaType = mime.TypeByExtension(path.Ext(name))
	}
	if mediaType == "" {
		mediaType = http.DetectContentType(data)
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, path.Base(name), time.Unix(book.AddedAt, 0), bytes.NewReader(data))
}

func (s *Server) bookCover(w http.ResponseWriter, r *http.Request) {
	book, err := s.store.BookByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	cover, mediaType, err := s.store.BookCover(r.Context(), book.ID)
	if errors.Is(err, store.ErrNotFound) || len(cover) == 0 {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	if mediaType == "" {
		mediaType = http.DetectContentType(cover)
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, "", time.Unix(book.AddedAt, 0), bytes.NewReader(cover))
}

// bookFile streams the stored EPUB. http.ServeContent provides Range support,
// which readers use to fetch individual resources.
func (s *Server) bookFile(w http.ResponseWriter, r *http.Request) {
	book, err := s.store.BookByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	file, err := os.Open(filepath.Join(s.cfg.BooksDir(), book.SHA256+".epub"))
	if os.IsNotExist(err) {
		s.renderError(w, r, http.StatusNotFound, "Book file missing",
			"The catalog entry exists, but the EPUB file is not on disk.")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Type", "application/epub+zip")
	if disposition := mime.FormatMediaType("attachment", map[string]string{
		"filename": book.Title + ".epub",
	}); disposition != "" {
		w.Header().Set("Content-Disposition", disposition)
	}
	http.ServeContent(w, r, "", time.Unix(book.AddedAt, 0), file)
}

func (s *Server) renderLibrary(w http.ResponseWriter, r *http.Request, status int, notice, message string) {
	books, err := s.store.ListBooks(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page := libraryPage{baseData: s.base(w, r, "Library"), Books: books}
	page.Notice = notice
	page.Error = message
	s.render(w, r, status, "library", page)
}

func (s *Server) renderLibraryError(w http.ResponseWriter, r *http.Request, status int, message string) {
	s.renderLibrary(w, r, status, "", message)
}
