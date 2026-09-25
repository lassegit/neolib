package store

import (
	"context"
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
