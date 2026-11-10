package store

import (
	"context"
	"errors"
	"time"

	"github.com/lassegit/neolib/internal/id"
)

// Book is catalog metadata for one EPUB.
type Book struct {
	ID             string
	SHA256         string
	Title          string
	Author         string
	Identifier     string
	Publisher      string
	Published      string
	Language       string
	ISBN           string
	CoverMediaType string
	AddedAt        int64
}

// NewBook is the input for importing a book.
type NewBook struct {
	SHA256         string
	Title          string
	Author         string
	Identifier     string
	Publisher      string
	Published      string
	Language       string
	ISBN           string
	Cover          []byte
	CoverMediaType string
}

// CreateBook inserts a new catalog entry and returns it.
func (s *Store) CreateBook(ctx context.Context, nb NewBook) (Book, error) {
	book := Book{
		ID:             id.New(),
		SHA256:         nb.SHA256,
		Title:          nb.Title,
		Author:         nb.Author,
		Identifier:     nb.Identifier,
		Publisher:      nb.Publisher,
		Published:      nb.Published,
		Language:       nb.Language,
		ISBN:           nb.ISBN,
		CoverMediaType: nb.CoverMediaType,
		AddedAt:        time.Now().Unix(),
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO books (id, sha256, title, author, identifier, publisher, published,
		                    language, isbn, cover, cover_media_type, added_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		book.ID, book.SHA256, book.Title, book.Author, book.Identifier,
		book.Publisher, book.Published, book.Language, book.ISBN,
		nb.Cover, book.CoverMediaType, book.AddedAt,
	)
	if isUnique(err) {
		return Book{}, ErrDuplicate
	}
	if err != nil {
		return Book{}, err
	}
	return book, nil
}

// UpdateBookTitleIfUnchanged replaces a catalog title only if it still has
// the value observed by the caller. This prevents a background repair from
// overwriting a concurrent correction.
func (s *Store) UpdateBookTitleIfUnchanged(ctx context.Context, bookID, previous, title string) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE books SET title = ? WHERE id = ? AND title = ? AND metadata_edited = 0`, title, bookID, previous)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

// UpdateBookMetadata saves an explicit user correction and protects it from
// later automatic metadata repairs.
func (s *Store) UpdateBookMetadata(ctx context.Context, bookID, title, author string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE books SET title = ?, author = ?, metadata_edited = 1 WHERE id = ?`, title, author, bookID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

// ListBooks returns the whole catalog, newest first, without cover bytes.
func (s *Store) ListBooks(ctx context.Context) ([]Book, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, sha256, title, author, identifier, publisher, published,
		        language, isbn, cover_media_type, added_at
		 FROM books
		 ORDER BY added_at DESC, title COLLATE NOCASE ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var books []Book
	for rows.Next() {
		var b Book
		if err := rows.Scan(&b.ID, &b.SHA256, &b.Title, &b.Author, &b.Identifier,
			&b.Publisher, &b.Published, &b.Language, &b.ISBN,
			&b.CoverMediaType, &b.AddedAt); err != nil {
			return nil, err
		}
		books = append(books, b)
	}
	return books, rows.Err()
}

// BookByID returns a single catalog entry without cover bytes.
func (s *Store) BookByID(ctx context.Context, bookID string) (Book, error) {
	return s.scanBook(s.db.QueryRowContext(ctx,
		`SELECT id, sha256, title, author, identifier, publisher, published,
		        language, isbn, cover_media_type, added_at
		 FROM books WHERE id = ?`, bookID,
	))
}

// BookBySHA returns a catalog entry by content hash.
func (s *Store) BookBySHA(ctx context.Context, sha256 string) (Book, error) {
	return s.scanBook(s.db.QueryRowContext(ctx,
		`SELECT id, sha256, title, author, identifier, publisher, published,
		        language, isbn, cover_media_type, added_at
		 FROM books WHERE sha256 = ?`, sha256,
	))
}

// DeleteBooks removes catalog entries and returns the books it deleted. IDs
// that no longer exist are ignored, and duplicate IDs are deleted once, so a
// stale bulk form still removes the remaining selections. Derived rows
// (chapters, positions, annotations, and reading progress) are removed by
// the schema's ON DELETE CASCADE. The caller owns the stored EPUB files and
// removes them after this returns.
func (s *Store) DeleteBooks(ctx context.Context, bookIDs []string) ([]Book, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	seen := make(map[string]bool, len(bookIDs))
	var books []Book
	for _, bookID := range bookIDs {
		if bookID == "" || seen[bookID] {
			continue
		}
		seen[bookID] = true

		book, err := s.scanBook(tx.QueryRowContext(ctx,
			`SELECT id, sha256, title, author, identifier, publisher, published,
			        language, isbn, cover_media_type, added_at
			 FROM books WHERE id = ?`, bookID,
		))
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM books WHERE id = ?`, book.ID); err != nil {
			return nil, err
		}
		books = append(books, book)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return books, nil
}

// DeleteBook removes one catalog entry and returns it.
func (s *Store) DeleteBook(ctx context.Context, bookID string) (Book, error) {
	books, err := s.DeleteBooks(ctx, []string{bookID})
	if err != nil {
		return Book{}, err
	}
	if len(books) == 0 {
		return Book{}, ErrNotFound
	}
	return books[0], nil
}

// BookCover returns the extracted cover image, if the book has one.
func (s *Store) BookCover(ctx context.Context, bookID string) ([]byte, string, error) {
	var (
		cover     []byte
		mediaType string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT cover, cover_media_type FROM books WHERE id = ?`, bookID,
	).Scan(&cover, &mediaType)
	if err != nil {
		return nil, "", normalize(err)
	}
	return cover, mediaType, nil
}

func (s *Store) scanBook(row interface{ Scan(...any) error }) (Book, error) {
	var b Book
	err := row.Scan(&b.ID, &b.SHA256, &b.Title, &b.Author, &b.Identifier,
		&b.Publisher, &b.Published, &b.Language, &b.ISBN,
		&b.CoverMediaType, &b.AddedAt)
	if err != nil {
		return Book{}, normalize(err)
	}
	return b, nil
}
