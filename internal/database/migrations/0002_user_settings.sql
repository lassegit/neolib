-- Per-user preferences. The value is a JSON document so new settings can be
-- added without schema changes; readers overlay stored values on defaults.
CREATE TABLE user_settings (
    user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    data       BLOB NOT NULL,
    updated_at INTEGER NOT NULL
);
