# neolib

Self-hosted book reader. See [TECH_SPEC.md](TECH_SPEC.md) for the technical
specification and [docs/READER_UI.md](docs/READER_UI.md) for the reader UI
design.

## Reader

Opening a book gives you a reading view with:

- an auto-hiding top bar (back, bookmark, contents, display settings),
- a sidebar with the table of contents, bookmarks, notes, and book details,
- text selection highlights in four colors, painted with the CSS Custom
  Highlight API (no content DOM mutation),
- reading progress that resumes where you left off and syncs across devices,
- themes (auto, light, sepia, dark) and typography controls that are applied
  before first paint and saved to your account.

Everything is progressive enhancement: with JavaScript disabled the page is a
plain, readable book with its table of contents.

## Running

```
go run ./cmd/neolib
```

Then open <http://localhost:8080>. The first visit is `/signup`, which creates the account; afterwards registration is closed by default.

Data lands in `./data`. Useful environment variables:

| Variable                | Default  | Purpose                                      |
| ----------------------- | -------- | -------------------------------------------- |
| `NEOLIB_ADDR`           | `:8080`  | HTTP listen address                          |
| `NEOLIB_DATA_DIR`       | `./data` | Database, books, and temporary uploads       |
| `NEOLIB_ALLOW_SIGNUP`   | `auto`   | `auto` (first user only), `true`, or `false` |
| `NEOLIB_SECURE_COOKIES` | `auto`   | `auto` (HTTPS requests), `true`, or `false`  |
| `NEOLIB_MAX_UPLOAD_MB`  | `512`    | Maximum request body size for imports        |

## Links

- https://www.gutenberg.org/
