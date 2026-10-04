package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lassegit/neolib/internal/locator"
)

// Progress is a user's reading position in one book. Last is the resume
// point (latest write wins); Furthest is the high-water mark.
type Progress struct {
	Last                locator.Locator
	Furthest            locator.Locator
	LastProgression     float64
	FurthestProgression float64
	UpdatedAt           int64
}

// GetProgress returns a user's stored progress for a book.
func (s *Store) GetProgress(ctx context.Context, userID, bookID string) (Progress, error) {
	return getProgress(ctx, s.db, userID, bookID)
}

// progressQuerier is the subset of *sql.DB and *sql.Tx used by getProgress,
// so the read can join SaveProgress's transaction.
type progressQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getProgress(ctx context.Context, q progressQuerier, userID, bookID string) (Progress, error) {
	var (
		p                      Progress
		lastData, furthestData []byte
	)
	err := q.QueryRowContext(ctx,
		`SELECT last_locator, furthest_locator, last_progression, furthest_progression, updated_at
		 FROM reading_progress
		 WHERE user_id = ? AND book_id = ?`, userID, bookID,
	).Scan(&lastData, &furthestData, &p.LastProgression, &p.FurthestProgression, &p.UpdatedAt)
	if err != nil {
		return Progress{}, normalize(err)
	}
	if p.Last, err = locator.DecodeLocator(lastData); err != nil {
		return Progress{}, err
	}
	if p.Furthest, err = locator.DecodeLocator(furthestData); err != nil {
		return Progress{}, err
	}
	return p, nil
}

// SaveProgress upserts a user's reading position. last replaces the resume
// point; furthest only moves forward, so an out-of-order device write can
// never rewind the high-water mark. The read and the write share one
// transaction, so two devices writing at once cannot lose the higher mark
// between them.
func (s *Store) SaveProgress(ctx context.Context, userID, bookID string, last locator.Locator, furthest *locator.Locator) (Progress, error) {
	now := time.Now().Unix()
	lastProgression := last.ProgressionValue()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Progress{}, err
	}
	defer tx.Rollback()

	current, err := getProgress(ctx, tx, userID, bookID)
	switch {
	case errors.Is(err, ErrNotFound):
		current = Progress{
			Last:                last,
			Furthest:            last,
			LastProgression:     lastProgression,
			FurthestProgression: lastProgression,
		}
		if furthest != nil && furthest.ProgressionValue() > current.FurthestProgression {
			current.Furthest = *furthest
			current.FurthestProgression = furthest.ProgressionValue()
		}
	case err != nil:
		return Progress{}, err
	default:
		current.Last = last
		current.LastProgression = lastProgression
		if furthest != nil && furthest.ProgressionValue() > current.FurthestProgression {
			current.Furthest = *furthest
			current.FurthestProgression = furthest.ProgressionValue()
		}
		if current.FurthestProgression < lastProgression {
			current.Furthest = last
			current.FurthestProgression = lastProgression
		}
	}
	current.UpdatedAt = now

	lastData, err := current.Last.Encode()
	if err != nil {
		return Progress{}, err
	}
	furthestData, err := current.Furthest.Encode()
	if err != nil {
		return Progress{}, err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO reading_progress
		   (user_id, book_id, last_locator, furthest_locator, last_progression, furthest_progression, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, book_id) DO UPDATE SET
		   last_locator = excluded.last_locator,
		   furthest_locator = excluded.furthest_locator,
		   last_progression = excluded.last_progression,
		   furthest_progression = excluded.furthest_progression,
		   updated_at = excluded.updated_at`,
		userID, bookID, lastData, furthestData,
		current.LastProgression, current.FurthestProgression, now,
	)
	if err != nil {
		return Progress{}, err
	}
	if err := tx.Commit(); err != nil {
		return Progress{}, err
	}
	return current, nil
}
