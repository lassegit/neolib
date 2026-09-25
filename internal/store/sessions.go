package store

import "context"

// CreateSession stores a hashed session token. The raw token never touches
// the database.
func (s *Store) CreateSession(ctx context.Context, tokenHash, userID string, createdAt, expiresAt int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at)
		 VALUES (?, ?, ?, ?)`,
		tokenHash, userID, createdAt, expiresAt,
	)
	return err
}

// SessionUser returns the user owning a live session, if any.
func (s *Store) SessionUser(ctx context.Context, tokenHash string, now int64) (User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT u.id, u.email, u.display_name, u.password_hash, u.created_at
		 FROM sessions s
		 JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = ? AND s.expires_at > ?`,
		tokenHash, now,
	))
}

// DeleteSession removes a single session.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteUserSessions removes every session belonging to a user.
func (s *Store) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// DeleteExpiredSessions removes sessions whose expiry has passed and returns
// how many rows were deleted.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now int64) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
