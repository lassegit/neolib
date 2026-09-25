package store

import (
	"context"
	"time"
)

// UserSettings returns the raw settings document for a user. A user without
// stored settings yields ErrNotFound, which callers treat as "use defaults".
func (s *Store) UserSettings(ctx context.Context, userID string) ([]byte, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT data FROM user_settings WHERE user_id = ?`, userID,
	).Scan(&data)
	if err != nil {
		return nil, normalize(err)
	}
	return data, nil
}

// SaveUserSettings stores the raw settings document for a user, replacing
// any previous document.
func (s *Store) SaveUserSettings(ctx context.Context, userID string, data []byte) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO user_settings (user_id, data, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`,
		userID, data, time.Now().Unix(),
	)
	return err
}
