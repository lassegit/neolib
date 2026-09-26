# neolib

Self-hosted book reader. See [TECH_SPEC.md](TECH_SPEC.md) for the technical specification.

## Running

```
go run ./cmd/neolib
```

Then open <http://localhost:3000>. The first visit is `/signup`, which creates the account; afterwards registration is closed by default.

Data lands in `./data`. Useful environment variables:

| Variable                | Default  | Purpose                                      |
| ----------------------- | -------- | -------------------------------------------- |
| `NEOLIB_ADDR`           | `:3000`  | HTTP listen address                          |
| `NEOLIB_DATA_DIR`       | `./data` | Database, books, and temporary uploads       |
| `NEOLIB_ALLOW_SIGNUP`   | `auto`   | `auto` (first user only), `true`, or `false` |
| `NEOLIB_SECURE_COOKIES` | `auto`   | `auto` (HTTPS requests), `true`, or `false`  |
| `NEOLIB_MAX_UPLOAD_MB`  | `512`    | Maximum request body size for imports        |

## Links

- https://www.gutenberg.org/
