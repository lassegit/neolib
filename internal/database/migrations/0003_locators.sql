-- Derived locator index. This is a cache of what is computable from the
-- EPUB: the logical-text projection of each spine document (the coordinate
-- system locator offsets index into) and the deterministic synthetic
-- position list. Books are re-indexed from their file when the projection
-- version changes; the EPUB itself stays canonical.
ALTER TABLE books ADD COLUMN indexed_at INTEGER NOT NULL DEFAULT 0;

CREATE TABLE chapters (
    book_id      TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    spine_index  INTEGER NOT NULL,
    href         TEXT NOT NULL,
    media_type   TEXT NOT NULL DEFAULT '',
    projection   TEXT NOT NULL,
    char_count   INTEGER NOT NULL,
    text         TEXT NOT NULL,
    PRIMARY KEY (book_id, spine_index)
);

CREATE TABLE positions (
    book_id           TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    position          INTEGER NOT NULL,
    href              TEXT NOT NULL,
    media_type        TEXT NOT NULL DEFAULT '',
    fragment          TEXT NOT NULL DEFAULT '',
    progression       REAL NOT NULL,
    total_progression REAL NOT NULL,
    PRIMARY KEY (book_id, position)
);

CREATE INDEX positions_book_id_idx ON positions(book_id);
