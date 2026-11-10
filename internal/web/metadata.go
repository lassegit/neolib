package web

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/lassegit/neolib/internal/epub"
	"github.com/lassegit/neolib/internal/store"
)

// repairBookTitle updates only an obvious placeholder, and only when the
// stored EPUB now supplies a better title. The canonical file, book ID, and
// all reading state remain untouched.
func (s *Server) repairBookTitle(ctx context.Context, book store.Book) (store.Book, error) {
	if !epub.PlaceholderTitle(book.Title) {
		return book, nil
	}
	pub, err := epub.Open(filepath.Join(s.cfg.BooksDir(), book.SHA256+".epub"))
	if err != nil {
		return book, fmt.Errorf("open epub: %w", err)
	}
	title := pub.Title()
	pub.Close()
	if epub.PlaceholderTitle(title) {
		return book, nil
	}
	updated, err := s.store.UpdateBookTitleIfUnchanged(ctx, book.ID, book.Title, title)
	if err != nil {
		return book, err
	}
	if updated {
		book.Title = title
	} else {
		// Another request changed or removed the book while it was inspected.
		return s.store.BookByID(ctx, book.ID)
	}
	return book, nil
}

// BackfillBookTitles repairs placeholder titles imported by older versions.
// It is safe to repeat and never changes meaningful catalog metadata.
func (s *Server) BackfillBookTitles(ctx context.Context) {
	books, err := s.store.ListBooks(ctx)
	if err != nil {
		s.log.Error("list books for title repair", "error", err)
		return
	}
	for _, book := range books {
		if ctx.Err() != nil {
			return
		}
		if !epub.PlaceholderTitle(book.Title) {
			continue
		}
		if _, err := s.repairBookTitle(ctx, book); err != nil {
			s.log.Warn("repair book title", "book", book.ID, "error", err)
		}
	}
}
