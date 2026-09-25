package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lassegit/neolib/internal/epub"
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

// importBook accepts a multipart EPUB upload, stores it content-addressed,
// and adds it to the catalog. Importing the same file twice is a no-op.
func (s *Server) importBook(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		s.renderLibraryError(w, r, http.StatusBadRequest, "The upload could not be read.")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		s.renderLibraryError(w, r, http.StatusBadRequest, "Choose an EPUB file to import.")
		return
	}
	defer file.Close()

	tmp, err := os.CreateTemp(s.cfg.TempDir(), "import-*.epub")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		s.serverError(w, r, err)
		return
	}
	if err := tmp.Close(); err != nil {
		s.serverError(w, r, err)
		return
	}

	// Some downloads deliver the EPUB inside an outer ZIP (for example
	// "book.epub.zip"); store the EPUB itself in that case.
	canonical, err := epub.Canonicalize(tmpName)
	if err != nil {
		s.log.Warn("rejected import", "filename", header.Filename, "error", err)
		s.renderLibraryError(w, r, http.StatusBadRequest,
			"That file could not be read as an EPUB.")
		return
	}
	if canonical != tmpName {
		defer os.Remove(canonical)
	}

	sum, err := fileSHA256(canonical)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if existing, err := s.store.BookBySHA(r.Context(), sum); err == nil {
		http.Redirect(w, r, "/books/"+existing.ID, http.StatusSeeOther)
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}

	meta, err := epub.Read(canonical)
	if err != nil {
		s.log.Warn("rejected import", "filename", header.Filename, "error", err)
		s.renderLibraryError(w, r, http.StatusBadRequest,
			"That file could not be read as an EPUB.")
		return
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
		s.serverError(w, r, err)
		return
	}

	book, err := s.store.CreateBook(r.Context(), store.NewBook{
		SHA256:         sum,
		Title:          title,
		Author:         meta.Author,
		Identifier:     meta.Identifier,
		Cover:          meta.Cover,
		CoverMediaType: meta.CoverMediaType,
	})
	if errors.Is(err, store.ErrDuplicate) {
		if book, err = s.store.BookBySHA(r.Context(), sum); err != nil {
			s.serverError(w, r, err)
			return
		}
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}

	s.log.Info("imported book", "id", book.ID, "title", book.Title, "sha256", sum)
	http.Redirect(w, r, "/books/"+book.ID, http.StatusSeeOther)
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
	page := bookPage{baseData: s.base(w, r, book.Title), Book: book}
	s.render(w, r, http.StatusOK, "book", page)
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

func (s *Server) renderLibraryError(w http.ResponseWriter, r *http.Request, status int, message string) {
	books, err := s.store.ListBooks(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page := libraryPage{baseData: s.base(w, r, "Library"), Books: books}
	page.Error = message
	s.render(w, r, status, "library", page)
}
