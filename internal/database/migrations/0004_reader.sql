-- Reader state: annotations (bookmarks and highlights) and per-user
-- reading progress. Locators are stored as JSON using the envelope in
-- docs/LOCATORS.md; the EPUB stays canonical and no book text is duplicated.
CREATE TABLE annotations (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id    TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('bookmark', 'highlight')),
    color      TEXT NOT NULL DEFAULT '',
    body       TEXT NOT NULL DEFAULT '',
    locator    TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX annotations_user_book_idx ON annotations(user_id, book_id, created_at);

CREATE TABLE reading_progress (
    user_id              TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id              TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    last_locator         TEXT NOT NULL,
    furthest_locator     TEXT NOT NULL,
    last_progression     REAL NOT NULL DEFAULT 0,
    furthest_progression REAL NOT NULL DEFAULT 0,
    updated_at           INTEGER NOT NULL,
    PRIMARY KEY (user_id, book_id)
);
