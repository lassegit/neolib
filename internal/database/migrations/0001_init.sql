-- Users and sessions.
CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL COLLATE NOCASE UNIQUE,
    display_name  TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    created_at    INTEGER NOT NULL
);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);

CREATE INDEX sessions_user_id_idx ON sessions(user_id);
CREATE INDEX sessions_expires_at_idx ON sessions(expires_at);

-- The catalog. Book files live in the data directory, addressed by SHA-256;
-- the EPUB itself is the canonical format and is never rewritten.
CREATE TABLE books (
    id               TEXT PRIMARY KEY,
    sha256           TEXT NOT NULL UNIQUE,
    title            TEXT NOT NULL,
    author           TEXT NOT NULL DEFAULT '',
    identifier       TEXT NOT NULL DEFAULT '',
    cover            BLOB,
    cover_media_type TEXT NOT NULL DEFAULT '',
    added_at         INTEGER NOT NULL
);

CREATE INDEX books_added_at_idx ON books(added_at);
