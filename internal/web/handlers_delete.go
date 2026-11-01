package web

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

// deleteBooksPage lists the library with bulk-delete checkboxes. It is
// linked from settings so destructive actions stay off the landing page.
func (s *Server) deleteBooksPage(w http.ResponseWriter, r *http.Request) {
	books, err := s.store.ListBooks(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page := deletePage{baseData: s.base(w, r, "Delete books"), Books: books}
	page.Notice = deletedBooksNotice(r)
	s.render(w, r, http.StatusOK, "delete", page)
}

// deleteBooks removes the selected catalog entries and their stored files.
func (s *Server) deleteBooks(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}

	bookIDs := r.PostForm["book_id"]
	if len(bookIDs) == 0 {
		s.renderDeleteBooks(w, r, http.StatusBadRequest, "Select at least one book to delete.")
		return
	}

	books, err := s.store.DeleteBooks(r.Context(), bookIDs)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if len(books) == 0 {
		s.renderDeleteBooks(w, r, http.StatusBadRequest, "None of the selected books still exist.")
		return
	}

	// The catalog entries are already gone, so a file-removal failure must
	// not turn a successful delete into an error. It leaves orphaned bytes
	// that can be cleaned up separately.
	for _, book := range books {
		name := filepath.Join(s.cfg.BooksDir(), book.SHA256+".epub")
		if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("remove deleted book file", "book", book.ID, "path", name, "error", err)
		}
	}

	s.log.Info("deleted books", "count", len(books))
	http.Redirect(w, r, "/settings/delete?deleted="+strconv.Itoa(len(books)), http.StatusSeeOther)
}

func (s *Server) renderDeleteBooks(w http.ResponseWriter, r *http.Request, status int, message string) {
	books, err := s.store.ListBooks(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page := deletePage{baseData: s.base(w, r, "Delete books"), Books: books}
	page.Error = message
	s.render(w, r, status, "delete", page)
}

func deletedBooksNotice(r *http.Request) string {
	count, err := strconv.Atoi(r.URL.Query().Get("deleted"))
	if err != nil || count <= 0 {
		return ""
	}
	if count == 1 {
		return "1 book was deleted."
	}
	return fmt.Sprintf("%d books were deleted.", count)
}
