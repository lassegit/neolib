package store

import (
	"context"
	"time"

	"github.com/lassegit/neolib/internal/locator"
)

// ReplaceBookIndex atomically replaces a book's derived locator index. It is
// safe to call repeatedly; the EPUB remains the canonical source.
func (s *Store) ReplaceBookIndex(ctx context.Context, bookID string, idx locator.Index) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM chapters WHERE book_id = ?`, bookID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM positions WHERE book_id = ?`, bookID); err != nil {
		return err
	}

	chapterStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO chapters (book_id, spine_index, href, media_type, projection, char_count, text)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer chapterStmt.Close()
	for _, ch := range idx.Chapters {
		if _, err := chapterStmt.ExecContext(ctx,
			bookID, ch.SpineIndex, ch.Href, ch.MediaType, ch.Projection, ch.UTF16Length, ch.Text,
		); err != nil {
			return err
		}
	}

	positionStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO positions (book_id, position, href, media_type, fragment, progression, total_progression)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer positionStmt.Close()
	for _, pos := range idx.Positions {
		if _, err := positionStmt.ExecContext(ctx,
			bookID, pos.Position, pos.Href, pos.MediaType, pos.Fragment, pos.Progression, pos.TotalProgression,
		); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE books SET indexed_at = ? WHERE id = ?`, time.Now().Unix(), bookID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// BookIndexed reports whether a book has a stored locator index.
func (s *Store) BookIndexed(ctx context.Context, bookID string) (bool, error) {
	var indexed int
	if err := s.db.QueryRowContext(ctx,
		`SELECT indexed_at FROM books WHERE id = ?`, bookID,
	).Scan(&indexed); err != nil {
		return false, normalize(err)
	}
	return indexed > 0, nil
}

// UnindexedBooks returns catalog entries that still need an index, oldest
// first, using the same column set as the other catalog queries. A book is
// unindexed when it has never been indexed or when its stored projection was
// produced by an older algorithm version.
func (s *Store) UnindexedBooks(ctx context.Context) ([]Book, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, sha256, title, author, identifier, publisher, published,
		        language, isbn, cover_media_type, added_at
		 FROM books
		 WHERE indexed_at = 0
		    OR EXISTS (
		        SELECT 1 FROM chapters
		        WHERE chapters.book_id = books.id AND chapters.projection <> ?
		    )
		 ORDER BY added_at ASC`, locator.ProjectionVersion)
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

// Chapters returns a book's stored projections in spine order.
func (s *Store) Chapters(ctx context.Context, bookID string) ([]locator.Chapter, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT spine_index, href, media_type, projection, char_count, text
		 FROM chapters
		 WHERE book_id = ?
		 ORDER BY spine_index ASC`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chapters []locator.Chapter
	for rows.Next() {
		var ch locator.Chapter
		if err := rows.Scan(&ch.SpineIndex, &ch.Href, &ch.MediaType,
			&ch.Projection, &ch.UTF16Length, &ch.Text); err != nil {
			return nil, err
		}
		chapters = append(chapters, ch)
	}
	return chapters, rows.Err()
}

// Positions returns a book's synthetic position list ordered by position.
func (s *Store) Positions(ctx context.Context, bookID string) ([]locator.Position, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT position, href, media_type, fragment, progression, total_progression
		 FROM positions
		 WHERE book_id = ?
		 ORDER BY position ASC`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var positions []locator.Position
	for rows.Next() {
		var pos locator.Position
		if err := rows.Scan(&pos.Position, &pos.Href, &pos.MediaType,
			&pos.Fragment, &pos.Progression, &pos.TotalProgression); err != nil {
			return nil, err
		}
		positions = append(positions, pos)
	}
	return positions, rows.Err()
}
