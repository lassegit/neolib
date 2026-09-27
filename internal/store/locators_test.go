package store_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/lassegit/neolib/internal/locator"
	"github.com/lassegit/neolib/internal/store"
)

func TestReplaceBookIndex(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	book, err := st.CreateBook(ctx, store.NewBook{SHA256: "sha-index", Title: "Indexed"})
	if err != nil {
		t.Fatalf("create book: %v", err)
	}

	if indexed, err := st.BookIndexed(ctx, book.ID); err != nil || indexed {
		t.Fatalf("BookIndexed before = %v, %v; want false, nil", indexed, err)
	}

	idx := locator.Index{
		Chapters: []locator.Chapter{{
			SpineIndex:  0,
			Href:        "ch1.xhtml",
			MediaType:   "application/xhtml+xml",
			Projection:  locator.ProjectionVersion,
			UTF16Length: 12,
			Text:        "Hello world.",
		}},
		Positions: []locator.Position{{
			Position:         1,
			Href:             "ch1.xhtml",
			MediaType:        "application/xhtml+xml",
			Fragment:         "/4/2/1:0",
			Progression:      0,
			TotalProgression: 1,
		}},
	}
	if err := st.ReplaceBookIndex(ctx, book.ID, idx); err != nil {
		t.Fatalf("replace index: %v", err)
	}

	if indexed, err := st.BookIndexed(ctx, book.ID); err != nil || !indexed {
		t.Fatalf("BookIndexed after = %v, %v; want true, nil", indexed, err)
	}
	chapters, err := st.Chapters(ctx, book.ID)
	if err != nil {
		t.Fatalf("chapters: %v", err)
	}
	if !reflect.DeepEqual(chapters, idx.Chapters) {
		t.Fatalf("chapters = %#v, want %#v", chapters, idx.Chapters)
	}
	positions, err := st.Positions(ctx, book.ID)
	if err != nil {
		t.Fatalf("positions: %v", err)
	}
	if !reflect.DeepEqual(positions, idx.Positions) {
		t.Fatalf("positions = %#v, want %#v", positions, idx.Positions)
	}

	// Replacing replaces: the old rows must not survive.
	if err := st.ReplaceBookIndex(ctx, book.ID, locator.Index{}); err != nil {
		t.Fatalf("replace with empty index: %v", err)
	}
	if chapters, err = st.Chapters(ctx, book.ID); err != nil || len(chapters) != 0 {
		t.Fatalf("chapters after empty index = %#v, %v; want none", chapters, err)
	}
	if positions, err = st.Positions(ctx, book.ID); err != nil || len(positions) != 0 {
		t.Fatalf("positions after empty index = %#v, %v; want none", positions, err)
	}
}

func TestUnindexedBooks(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	first, err := st.CreateBook(ctx, store.NewBook{SHA256: "sha-a", Title: "A"})
	if err != nil {
		t.Fatalf("create first book: %v", err)
	}
	second, err := st.CreateBook(ctx, store.NewBook{SHA256: "sha-b", Title: "B"})
	if err != nil {
		t.Fatalf("create second book: %v", err)
	}

	books, err := st.UnindexedBooks(ctx)
	if err != nil {
		t.Fatalf("unindexed books: %v", err)
	}
	if len(books) != 2 {
		t.Fatalf("unindexed = %d, want 2", len(books))
	}

	if err := st.ReplaceBookIndex(ctx, first.ID, locator.Index{}); err != nil {
		t.Fatalf("index first book: %v", err)
	}
	books, err = st.UnindexedBooks(ctx)
	if err != nil {
		t.Fatalf("unindexed books: %v", err)
	}
	if len(books) != 1 || books[0].ID != second.ID {
		t.Fatalf("unindexed = %#v, want only %s", books, second.ID)
	}

	// A stored projection from an older algorithm version must be reindexed
	// even though the book has an index.
	stale := locator.Index{Chapters: []locator.Chapter{{
		SpineIndex: 0, Href: "ch1.xhtml", Projection: "neolib/logical-text/0", Text: "old",
	}}}
	if err := st.ReplaceBookIndex(ctx, second.ID, stale); err != nil {
		t.Fatalf("index second book: %v", err)
	}
	books, err = st.UnindexedBooks(ctx)
	if err != nil {
		t.Fatalf("unindexed books: %v", err)
	}
	if len(books) != 1 || books[0].ID != second.ID {
		t.Fatalf("unindexed = %#v, want stale projection of %s", books, second.ID)
	}
}
