package web

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/lassegit/neolib/internal/epub"
	"github.com/lassegit/neolib/internal/locator"
	"github.com/lassegit/neolib/internal/store"
)

// indexBook computes and stores a book's derived locator index from its
// canonical EPUB file.
func (s *Server) indexBook(ctx context.Context, book store.Book) error {
	pub, err := epub.Open(filepath.Join(s.cfg.BooksDir(), book.SHA256+".epub"))
	if err != nil {
		return fmt.Errorf("open epub: %w", err)
	}
	defer pub.Close()

	idx := locator.IndexPublication(pub)
	for _, warning := range idx.Warnings {
		s.log.Warn("chapter skipped while indexing", "book", book.ID, "chapter", warning)
	}
	if err := s.store.ReplaceBookIndex(ctx, book.ID, idx); err != nil {
		return fmt.Errorf("store index: %w", err)
	}
	return nil
}

// ensureIndexed indexes the book unless an index is already stored.
func (s *Server) ensureIndexed(ctx context.Context, book store.Book) error {
	indexed, err := s.store.BookIndexed(ctx, book.ID)
	if err != nil {
		return err
	}
	if indexed {
		return nil
	}
	return s.indexBook(ctx, book)
}

// ensureIndexedDetached indexes the book outside the caller's cancellation
// scope. The import is already committed, so a client abort must not cancel
// the derived index write. Failures are logged; the startup backfill retries
// them.
func (s *Server) ensureIndexedDetached(ctx context.Context, book store.Book) {
	if err := s.ensureIndexed(context.WithoutCancel(ctx), book); err != nil {
		s.log.Warn("index imported book", "book", book.ID, "error", err)
	}
}

// BackfillIndexes indexes every book that has no stored locator index. It is
// safe to run more than once and is intended to run in the background at
// startup, so books imported before the index existed become referenceable.
func (s *Server) BackfillIndexes(ctx context.Context) {
	books, err := s.store.UnindexedBooks(ctx)
	if err != nil {
		s.log.Error("list unindexed books", "error", err)
		return
	}
	if len(books) == 0 {
		return
	}
	s.log.Info("indexing books", "count", len(books))
	for _, book := range books {
		if ctx.Err() != nil {
			return
		}
		if err := s.indexBook(ctx, book); err != nil {
			s.log.Warn("index book", "book", book.ID, "error", err)
		}
	}
}
