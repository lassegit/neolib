package store

import (
	"context"
	"time"

	"github.com/lassegit/neolib/internal/id"
	"github.com/lassegit/neolib/internal/locator"
)

// Annotation kinds.
const (
	AnnotationBookmark  = "bookmark"
	AnnotationHighlight = "highlight"
)

// Highlight colors accepted by the UI. An empty color is treated as the
// default highlight color at render time.
var annotationColors = map[string]bool{
	"yellow": true,
	"green":  true,
	"blue":   true,
	"pink":   true,
}

// Annotation is a user-owned bookmark or highlight anchored by a locator.
type Annotation struct {
	ID        string
	UserID    string
	BookID    string
	Kind      string
	Color     string
	Body      string
	Locator   locator.Locator
	CreatedAt int64
	UpdatedAt int64
}

// NewAnnotation is the input for creating an annotation.
type NewAnnotation struct {
	Kind    string
	Color   string
	Body    string
	Locator locator.Locator
}

// ValidAnnotationKind reports whether kind is a supported annotation kind.
func ValidAnnotationKind(kind string) bool {
	return kind == AnnotationBookmark || kind == AnnotationHighlight
}

// ValidAnnotationColor reports whether color is a supported highlight color.
func ValidAnnotationColor(color string) bool {
	return color == "" || annotationColors[color]
}

// CreateAnnotation inserts a bookmark or highlight for a user's book.
func (s *Store) CreateAnnotation(ctx context.Context, userID, bookID string, na NewAnnotation) (Annotation, error) {
	encoded, err := na.Locator.Encode()
	if err != nil {
		return Annotation{}, err
	}
	now := time.Now().Unix()
	a := Annotation{
		ID:        id.New(),
		UserID:    userID,
		BookID:    bookID,
		Kind:      na.Kind,
		Color:     na.Color,
		Body:      na.Body,
		Locator:   na.Locator,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO annotations (id, user_id, book_id, kind, color, body, locator, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.UserID, a.BookID, a.Kind, a.Color, a.Body, encoded, a.CreatedAt, a.UpdatedAt,
	); err != nil {
		return Annotation{}, err
	}
	return a, nil
}

// ListAnnotations returns a user's annotations for one book, oldest first.
func (s *Store) ListAnnotations(ctx context.Context, userID, bookID string) ([]Annotation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, book_id, kind, color, body, locator, created_at, updated_at
		 FROM annotations
		 WHERE user_id = ? AND book_id = ?
		 ORDER BY created_at ASC, id ASC`, userID, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var annotations []Annotation
	for rows.Next() {
		a, err := scanAnnotation(rows)
		if err != nil {
			return nil, err
		}
		annotations = append(annotations, a)
	}
	return annotations, rows.Err()
}

// AnnotationByID returns one annotation owned by userID.
func (s *Store) AnnotationByID(ctx context.Context, userID, annotationID string) (Annotation, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, book_id, kind, color, body, locator, created_at, updated_at
		 FROM annotations
		 WHERE id = ? AND user_id = ?`, annotationID, userID)
	return scanAnnotation(row)
}

// UpdateAnnotation applies a body, color, and locator correction. Zero-value
// fields are left unchanged; a correction replaces the whole locator because
// a partial update cannot preserve anchor coherence.
func (s *Store) UpdateAnnotation(ctx context.Context, userID, annotationID string, body, color *string, loc *locator.Locator) (Annotation, error) {
	a, err := s.AnnotationByID(ctx, userID, annotationID)
	if err != nil {
		return Annotation{}, err
	}
	if body != nil {
		a.Body = *body
	}
	if color != nil {
		a.Color = *color
	}
	var encoded []byte
	if loc != nil {
		a.Locator = *loc
	}
	encoded, err = a.Locator.Encode()
	if err != nil {
		return Annotation{}, err
	}
	a.UpdatedAt = time.Now().Unix()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE annotations SET color = ?, body = ?, locator = ?, updated_at = ?
		 WHERE id = ? AND user_id = ?`,
		a.Color, a.Body, encoded, a.UpdatedAt, a.ID, a.UserID,
	); err != nil {
		return Annotation{}, err
	}
	return a, nil
}

// DeleteAnnotation removes one annotation owned by userID.
func (s *Store) DeleteAnnotation(ctx context.Context, userID, annotationID string) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM annotations WHERE id = ? AND user_id = ?`, annotationID, userID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

type annotationScanner interface{ Scan(...any) error }

func scanAnnotation(row annotationScanner) (Annotation, error) {
	var (
		a       Annotation
		encoded []byte
	)
	if err := row.Scan(&a.ID, &a.UserID, &a.BookID, &a.Kind, &a.Color, &a.Body, &encoded, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return Annotation{}, normalize(err)
	}
	loc, err := locator.DecodeLocator(encoded)
	if err != nil {
		return Annotation{}, err
	}
	a.Locator = loc
	return a, nil
}
