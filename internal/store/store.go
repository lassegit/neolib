// Package store contains all database access. Handlers never write SQL
// themselves; they call methods on Store.
package store

import (
	"database/sql"
	"errors"
	"strings"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrDuplicate is returned when a unique constraint is violated.
var ErrDuplicate = errors.New("store: duplicate")

// ErrSignupClosed is returned when first-user signup loses a race to another
// account creation.
var ErrSignupClosed = errors.New("store: signup closed")

// Store is a thin, explicit layer over the SQLite database.
type Store struct {
	db *sql.DB
}

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// DB exposes the underlying pool for health checks and migrations.
func (s *Store) DB() *sql.DB { return s.db }

// normalize converts driver-level sentinel errors into store errors.
func normalize(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// isUnique reports whether err is a UNIQUE constraint violation. The modernc
// driver does not expose stable error codes through database/sql, so we match
// the SQLite message.
func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
