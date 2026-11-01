package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lassegit/neolib/internal/locator"
	"github.com/lassegit/neolib/internal/store"
)

// Deleting a book removes its catalog row, derived index, and all per-user
// reader state through the schema's foreign-key cascades.
func TestDeleteBookRemovesReaderState(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	user, err := st.CreateUser(ctx, "reader@example.com", "Reader", "hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	book, err := st.CreateBook(ctx, store.NewBook{SHA256: "sha-delete", Title: "Delete Me"})
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	other, err := st.CreateBook(ctx, store.NewBook{SHA256: "sha-keep", Title: "Keep Me"})
	if err != nil {
		t.Fatalf("create other book: %v", err)
	}

	idx := locator.Index{
		Chapters: []locator.Chapter{{
			SpineIndex:  0,
			Href:        "ch1.xhtml",
			MediaType:   "application/xhtml+xml",
			Projection:  locator.ProjectionVersion,
			UTF16Length: 5,
			Text:        "hello",
		}},
		Positions: []locator.Position{{
			Position:         1,
			Href:             "ch1.xhtml",
			MediaType:        "application/xhtml+xml",
			Progression:      0,
			TotalProgression: 0,
		}},
	}
	if err := st.ReplaceBookIndex(ctx, book.ID, idx); err != nil {
		t.Fatalf("replace index: %v", err)
	}
	if _, err := st.CreateAnnotation(ctx, user.ID, book.ID, store.NewAnnotation{
		Kind:    store.AnnotationHighlight,
		Color:   "yellow",
		Locator: testLocator("ch1.xhtml", 0.25),
	}); err != nil {
		t.Fatalf("create annotation: %v", err)
	}
	if _, err := st.SaveProgress(ctx, user.ID, book.ID, testLocator("ch1.xhtml", 0.25), nil); err != nil {
		t.Fatalf("save progress: %v", err)
	}

	deleted, err := st.DeleteBook(ctx, book.ID)
	if err != nil {
		t.Fatalf("delete book: %v", err)
	}
	if deleted.ID != book.ID || deleted.SHA256 != book.SHA256 || deleted.Title != book.Title {
		t.Fatalf("deleted book = %+v, want %+v", deleted, book)
	}

	if _, err := st.BookByID(ctx, book.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("BookByID after delete error = %v, want ErrNotFound", err)
	}
	if chapters, err := st.Chapters(ctx, book.ID); err != nil || len(chapters) != 0 {
		t.Fatalf("chapters after delete = %+v, %v; want none", chapters, err)
	}
	if positions, err := st.Positions(ctx, book.ID); err != nil || len(positions) != 0 {
		t.Fatalf("positions after delete = %+v, %v; want none", positions, err)
	}
	if annotations, err := st.ListAnnotations(ctx, user.ID, book.ID); err != nil || len(annotations) != 0 {
		t.Fatalf("annotations after delete = %+v, %v; want none", annotations, err)
	}
	if _, err := st.GetProgress(ctx, user.ID, book.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("progress after delete error = %v, want ErrNotFound", err)
	}

	// The other catalog entry is untouched.
	if kept, err := st.BookByID(ctx, other.ID); err != nil || kept.Title != other.Title {
		t.Fatalf("other book after delete = %+v, %v", kept, err)
	}

	// Deleting twice reports not found instead of succeeding silently.
	if _, err := st.DeleteBook(ctx, book.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete error = %v, want ErrNotFound", err)
	}
}

// Bulk deletion removes every selected book once, ignores stale IDs, and
// leaves unselected books alone.
func TestDeleteBooks(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	first, err := st.CreateBook(ctx, store.NewBook{SHA256: "sha-bulk-first", Title: "First"})
	if err != nil {
		t.Fatalf("create first book: %v", err)
	}
	second, err := st.CreateBook(ctx, store.NewBook{SHA256: "sha-bulk-second", Title: "Second"})
	if err != nil {
		t.Fatalf("create second book: %v", err)
	}
	keep, err := st.CreateBook(ctx, store.NewBook{SHA256: "sha-bulk-keep", Title: "Keep"})
	if err != nil {
		t.Fatalf("create kept book: %v", err)
	}

	deleted, err := st.DeleteBooks(ctx, []string{first.ID, "missing", first.ID, second.ID})
	if err != nil {
		t.Fatalf("delete books: %v", err)
	}
	if len(deleted) != 2 {
		t.Fatalf("deleted %d books, want 2", len(deleted))
	}
	deletedIDs := map[string]bool{}
	for _, book := range deleted {
		deletedIDs[book.ID] = true
	}
	if !deletedIDs[first.ID] || !deletedIDs[second.ID] {
		t.Fatalf("deleted books = %+v, want first and second", deleted)
	}

	for _, book := range []store.Book{first, second} {
		if _, err := st.BookByID(ctx, book.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("BookByID(%s) error = %v, want ErrNotFound", book.Title, err)
		}
	}
	if kept, err := st.BookByID(ctx, keep.ID); err != nil || kept.Title != keep.Title {
		t.Fatalf("kept book = %+v, %v", kept, err)
	}
}
