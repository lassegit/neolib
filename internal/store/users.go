package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/lassegit/neolib/internal/id"
)

// User is a neolib account.
type User struct {
	ID           string
	Email        string
	DisplayName  string
	PasswordHash string
	CreatedAt    int64
}

// CreateUser inserts a new user and returns it.
func (s *Store) CreateUser(ctx context.Context, email, displayName, passwordHash string) (User, error) {
	user := newUser(email, displayName, passwordHash)
	if err := insertUser(ctx, s.db, user); err != nil {
		return User{}, err
	}
	return user, nil
}

// CreateUserIfEmpty inserts the first account and atomically rejects the
// insert when any user already exists. It closes the check-then-insert race
// in first-run signup.
func (s *Store) CreateUserIfEmpty(ctx context.Context, email, displayName, passwordHash string) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()

	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return User{}, err
	}
	if count > 0 {
		return User{}, ErrSignupClosed
	}

	user := newUser(email, displayName, passwordHash)
	if err := insertUser(ctx, tx, user); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return user, nil
}

func newUser(email, displayName, passwordHash string) User {
	return User{
		ID:           id.New(),
		Email:        email,
		DisplayName:  displayName,
		PasswordHash: passwordHash,
		CreatedAt:    time.Now().Unix(),
	}
}

// execer is satisfied by both *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertUser(ctx context.Context, db execer, user User) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, email, display_name, password_hash, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		user.ID, user.Email, user.DisplayName, user.PasswordHash, user.CreatedAt,
	)
	if isUnique(err) {
		return ErrDuplicate
	}
	return err
}

// UserByEmail looks up a user by email address (case-insensitive).
func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, email, display_name, password_hash, created_at
		 FROM users WHERE email = ?`, email,
	))
}

// UserByID looks up a user by primary key.
func (s *Store) UserByID(ctx context.Context, userID string) (User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, email, display_name, password_hash, created_at
		 FROM users WHERE id = ?`, userID,
	))
}

// CountUsers returns the number of accounts on this server.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// UpdateUserName changes a user's display name.
func (s *Store) UpdateUserName(ctx context.Context, userID, displayName string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET display_name = ? WHERE id = ?`, displayName, userID,
	)
	return err
}

// UpdateUserPassword replaces a user's password hash.
func (s *Store) UpdateUserPassword(ctx context.Context, userID, passwordHash string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, userID,
	)
	return err
}

func (s *Store) scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		return User{}, normalize(err)
	}
	return u, nil
}
