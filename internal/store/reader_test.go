package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/lassegit/neolib/internal/locator"
	"github.com/lassegit/neolib/internal/store"
)

func testLocator(href string, progression float64) locator.Locator {
	offset := int(progression * 1000)
	return locator.Locator{
		V:    1,
		Href: href,
		Type: "application/xhtml+xml",
		Locations: locator.Locations{
			Progression:      progression,
			TotalProgression: progression,
			CharRange:        &locator.Range{Start: offset, End: offset + 5},
		},
		Text: locator.TextQuote{Highlight: "a quote"},
	}
}

func TestAnnotationCRUD(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	user, err := st.CreateUser(ctx, "reader@example.com", "Reader", "hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	other, err := st.CreateUser(ctx, "other@example.com", "Other", "hash")
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}
	book, err := st.CreateBook(ctx, store.NewBook{SHA256: "sha-annotations", Title: "Annotated"})
	if err != nil {
		t.Fatalf("create book: %v", err)
	}

	highlight, err := st.CreateAnnotation(ctx, user.ID, book.ID, store.NewAnnotation{
		Kind:    store.AnnotationHighlight,
		Color:   "yellow",
		Locator: testLocator("ch1.xhtml", 0.25),
	})
	if err != nil {
		t.Fatalf("create highlight: %v", err)
	}
	if highlight.ID == "" || highlight.Kind != store.AnnotationHighlight {
		t.Fatalf("highlight = %+v", highlight)
	}

	bookmark, err := st.CreateAnnotation(ctx, user.ID, book.ID, store.NewAnnotation{
		Kind:    store.AnnotationBookmark,
		Locator: testLocator("ch2.xhtml", 0.75),
	})
	if err != nil {
		t.Fatalf("create bookmark: %v", err)
	}

	// Listing is per user and per book.
	listed, err := st.ListAnnotations(ctx, user.ID, book.ID)
	if err != nil {
		t.Fatalf("list annotations: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed %d annotations, want 2", len(listed))
	}
	if listed[0].Locator.Href != "ch1.xhtml" || listed[1].Locator.Href != "ch2.xhtml" {
		t.Fatalf("annotations out of order: %+v", listed)
	}
	if others, err := st.ListAnnotations(ctx, other.ID, book.ID); err != nil || len(others) != 0 {
		t.Fatalf("other user's annotations = %+v, %v; want none", others, err)
	}

	// Updates are scoped to the owner.
	body := "note"
	color := "blue"
	updated, err := st.UpdateAnnotation(ctx, user.ID, highlight.ID, &body, &color, nil)
	if err != nil {
		t.Fatalf("update annotation: %v", err)
	}
	if updated.Body != "note" || updated.Color != "blue" {
		t.Fatalf("updated annotation = %+v", updated)
	}
	if _, err := st.UpdateAnnotation(ctx, other.ID, highlight.ID, nil, nil, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign update error = %v, want ErrNotFound", err)
	}
	if err := st.DeleteAnnotation(ctx, other.ID, highlight.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign delete error = %v, want ErrNotFound", err)
	}

	if err := st.DeleteAnnotation(ctx, user.ID, highlight.ID); err != nil {
		t.Fatalf("delete annotation: %v", err)
	}
	if err := st.DeleteAnnotation(ctx, user.ID, highlight.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete error = %v, want ErrNotFound", err)
	}
	if listed, err := st.ListAnnotations(ctx, user.ID, book.ID); err != nil || len(listed) != 1 {
		t.Fatalf("after delete = %+v, %v; want one bookmark", listed, err)
	}
	_ = bookmark
}

func TestProgressNeverRewindsFurthest(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	user, err := st.CreateUser(ctx, "reader@example.com", "Reader", "hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	book, err := st.CreateBook(ctx, store.NewBook{SHA256: "sha-progress", Title: "Progress"})
	if err != nil {
		t.Fatalf("create book: %v", err)
	}

	if _, err := st.GetProgress(ctx, user.ID, book.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("initial progress error = %v, want ErrNotFound", err)
	}

	progress, err := st.SaveProgress(ctx, user.ID, book.ID, testLocator("ch1.xhtml", 0.4), nil)
	if err != nil {
		t.Fatalf("save progress: %v", err)
	}
	if progress.LastProgression != 0.4 || progress.FurthestProgression != 0.4 {
		t.Fatalf("progress = %+v", progress)
	}

	furthest := testLocator("ch3.xhtml", 0.9)
	progress, err = st.SaveProgress(ctx, user.ID, book.ID, testLocator("ch3.xhtml", 0.9), &furthest)
	if err != nil {
		t.Fatalf("save furthest: %v", err)
	}
	if progress.FurthestProgression != 0.9 {
		t.Fatalf("furthest = %v, want 0.9", progress.FurthestProgression)
	}

	// A stale device reports an earlier position; furthest must stay put.
	stale := testLocator("ch1.xhtml", 0.1)
	progress, err = st.SaveProgress(ctx, user.ID, book.ID, stale, &stale)
	if err != nil {
		t.Fatalf("save stale progress: %v", err)
	}
	if progress.FurthestProgression != 0.9 {
		t.Fatalf("furthest rewound to %v, want 0.9", progress.FurthestProgression)
	}
	if progress.LastProgression != 0.1 {
		t.Fatalf("last = %v, want 0.1", progress.LastProgression)
	}

	// Reading past the previous furthest moves the high-water mark.
	progress, err = st.SaveProgress(ctx, user.ID, book.ID, testLocator("ch4.xhtml", 0.95), nil)
	if err != nil {
		t.Fatalf("save past furthest: %v", err)
	}
	if progress.FurthestProgression != 0.95 || progress.Furthest.Href != "ch4.xhtml" {
		t.Fatalf("furthest did not advance: %+v", progress)
	}
}

// Concurrent devices must not lose the high-water mark: SaveProgress reads
// and writes in one transaction, so a low write that started before a high
// one cannot overwrite it afterwards.
func TestProgressConcurrentWritesKeepFurthest(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	user, err := st.CreateUser(ctx, "reader@example.com", "Reader", "hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	book, err := st.CreateBook(ctx, store.NewBook{SHA256: "sha-concurrent", Title: "Concurrent"})
	if err != nil {
		t.Fatalf("create book: %v", err)
	}

	const (
		workers    = 8
		iterations = 5
	)
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				low := testLocator("ch1.xhtml", 0.1)
				if _, err := st.SaveProgress(ctx, user.ID, book.ID, low, &low); err != nil {
					errs <- err
					return
				}
				high := testLocator("ch9.xhtml", 0.95)
				if _, err := st.SaveProgress(ctx, user.ID, book.ID, high, &high); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent save: %v", err)
	}

	got, err := st.GetProgress(ctx, user.ID, book.ID)
	if err != nil {
		t.Fatalf("get progress: %v", err)
	}
	if got.FurthestProgression != 0.95 || got.Furthest.Href != "ch9.xhtml" {
		t.Fatalf("furthest = %v (%s), want 0.95 (ch9.xhtml)", got.FurthestProgression, got.Furthest.Href)
	}
}
