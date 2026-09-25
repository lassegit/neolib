# Book Reader — Technical Specification

**Status:** Draft v0.1 · **Date:** 2026-09-24

Core technology: **Go + SQLite** server, embedded **TypeScript** web app, and a **JS/TS plugin system** for extending the reading experience.

## 1. Purpose

A self-hosted, browser-first reading environment for books. The reader owns the text: every book is standard EPUB (HTML + CSS), references are portable, and themes, annotations, translation, TTS, and study tools are plugins rather than features baked into a closed app.

## 2. Goals

1. Fast browser reading — all browser tooling (search, translation, a11y, extensions) applies.
2. Stable, shareable references (locators) across devices and editions where possible.
3. Local-first: works offline; state syncs when the server is reachable.
4. Extensible without rebuilds: third-party plugins and CSS-only themes, browsable and installable from registries.
5. Minimal self-hosting: one binary, one data directory.

## 3. Non-goals

- DRM support/removal or redistribution of copyrighted books.
- Multi-tenant SaaS; self-hosting is the distribution model.
- A custom EPUB rendering engine (reuse epub.js/Readium-class tech).
- Postgres/MySQL support. SQLite is a feature, not a limitation.
- Server-side plugin sandboxing in v1 (WASM/wazero is the reserved path).

## 4. Architecture

```
browser (PWA: TS shell · plugin host · reader iframe)
   │  HTTP + SSE
Go server (single binary, embedded UI)
   │
data/
  db.sqlite
  books/<sha256>.epub
  plugins/<id>/<version>/
```

- **Server:** Go `net/http`, SQLite via `modernc.org/sqlite` (pure Go, no CGO), `sqlc` + `goose`, argon2id auth, SSE for change notification.
- **Web app:** Vite/TypeScript SPA embedded via `go:embed`; framework-agnostic core with an optional custom-element UI kit.
- **Plugins:** ESM modules served by the server; declarative manifests; CSS-only themes require no JS.
- **Content:** EPUB rendered in a sandboxed iframe. **The content DOM is never mutated** — reference stability is a hard invariant.

## 5. Server

### 5.1 Storage

- Books are immutable files addressed by SHA-256; re-upload is a no-op.
- SQLite holds all mutable state: WAL mode, `busy_timeout`, foreign keys, single writer.
- Backup: `VACUUM INTO` + books tar. Optional Litestream replication to S3/R2.

### 5.2 Data model (initial)

| Table | Purpose |
|---|---|
| `users`, `sessions` | auth (single-user by default) |
| `books` | id, sha256, title, author, publisher, published, language, ISBN/UUID, added_at |
| `chapters` | book_id, href, spine_index, title |
| `search_fts` | FTS5 index for cross-book search |
| `annotations` | id, user, book, locator JSON, body, plugin data |
| `progress` | user, book, locator JSON, progression, updated_at |
| `events` | append-only sync log: id, user, device, kind, payload, created_at |
| `plugins` | installed plugins/themes + enabled state |
| `settings` | user- and plugin-scoped config |

### 5.3 Locators

The universal reference type, stored as JSON, compatible with W3C Web Annotation selectors:

```json
{
  "bookId": "urn:isbn:...", "bookHash": "sha256:...",
  "href": "chapter005.xhtml",
  "locations": { "cfi": "epubcfi(/6/14!/4/10/2:15)", "position": 41927, "progression": 0.41 },
  "text": { "before": "…", "highlight": "…", "after": "…" }
}
```

- CFI is exact but **edition-scoped**; quote + position survive edition drift and are used to re-anchor.
- Server generates a char-offset index at import; clients mint CFIs in the renderer.

### 5.4 Sync

- Append-only `events` table. Clients push local events and pull `GET /api/sync?since=<cursor>`.
- Merge: union ordered by ID; annotations immutable + tombstones; progress = max progression per book.
- SSE notifies connected clients; polling is the fallback. No CRDTs in v1.

### 5.5 HTTP API (v1 sketch)

| Method | Path | Purpose |
|---|---|---|
| POST | `/api/books` | import (multipart) |
| GET | `/api/books`, `/api/books/:id` | catalog, metadata |
| GET | `/api/books/:id/file` | EPUB stream (Range supported) |
| GET/POST/PATCH | `/api/annotations` | CRUD |
| GET/POST | `/api/sync` | cursor pull / event push |
| GET | `/api/events` | SSE change notifications |
| GET | `/opds` | OPDS 2.0 catalog |
| GET | `/api/plugins`, `/api/plugins/:id` | registry + install |

## 6. Web app layout

```
packages/contracts   # types only → published as @bookreader/plugin-api
packages/core        # services: library, reader, sync, annotations, commands, events, slots, plugins
packages/ui          # shell, slot renderer, custom-element kit
packages/plugin-kit  # mocks, fixtures, scaffolding, contract tests
apps/reader          # SPA entry; registers first-party plugins
```

`contracts` is runtime-free and semver'd — the single dependency for plugin authors. Plugins interact only through `PluginContext`.

## 7. Plugin system

### 7.1 Primitives

1. **Services** — capabilities: `books`, `reader`, `sync`, `annotations`, `ui`, `storage`, `net`, `commands`.
2. **Commands** — named, typed actions invocable from palette, menus, keybindings, or other plugins.
3. **Contribution points** — declarative UI/extensions declared in the manifest.

Events are **notifications only**, never request/response.

### 7.2 Manifest

```json
{
  "id": "translate", "version": "1.2.0", "kind": "plugin",
  "engines": { "app": "^1" },
  "entry": "index.js", "styles": ["styles.css"],
  "permissions": ["net:https://api.example.com", "annotations:write", "storage:sync"],
  "activationEvents": ["onCommand:translate.selection", "onBook:*"],
  "contributes": {
    "commands": [{ "id": "translate.selection", "title": "Translate selection" }],
    "menus": [{ "slot": "selection.menu", "command": "translate.selection", "order": 10 }],
    "settings": { "targetLang": { "type": "string", "default": "en" } }
  }
}
```

- `kind`: `plugin` | `theme` | `style` — one package format, one registry, three loaders.
- `theme`/`style` packs omit `entry` and `permissions`: they are inert data (token maps + CSS), validated against the token contract, and load even when JS plugins are disabled.
- Lazy activation: a plugin module loads only when an activation event fires.
- All registrations go through `ctx.subscriptions` for leak-free unload.

### 7.3 Context API

```ts
interface PluginContext {
  app: { version, apiVersion };
  books: { list, get, import, open, importers, exporters };
  sources: { register, list, refresh };
  reader: { navigator, selection, highlighters, decorators, viewTransforms, renderers, progress };
  sync: { collection<T>(name): ObservableCollection<T> };
  annotations: { add, update, remove, query, onDidChange };
  commands: { register, execute };
  events: { on(name, cb) };
  ui: { slots, dialogs, notifications, settings };
  storage: { sync, local };
  net: { fetch };              // permission-gated, logged
  log; subscriptions; pluginId;
}
```

Rules that keep the API future-proof:

- All service methods async; arguments structured-clone-safe; disposables ID-based — so plugins can later run behind a worker/iframe without a breaking change.
- `storage.sync` (replicated, small) vs `storage.local` (device cache) is explicit and enforced by types.
- Storage, settings, and logs are scoped by plugin ID.

### 7.4 Reader extension points

| API | Use |
|---|---|
| `reader.highlighters.register` | Annotation categories via CSS Custom Highlight API; no DOM mutation |
| `reader.decorators.register` | Gutter/overlay marks (SRS status, read-along position) |
| `reader.viewTransforms.register` | Structural transforms (bilingual, bionic) on a disposable derived view |
| `reader.renderers.register` | Formats as engines: EPUB first, PDF/audio later |
| `annotations.exportFormats.register` | Export to Markdown, CSV, Anki |

### 7.5 Trust and versioning

- Plugins run same-realm (browser-extension trust model); permissions are consent UX, not a sandbox.
- Worker/iframe execution is a reserved tier; the API is written to survive the move.
- `engines.app` semver checked at load; breaking changes require an API major. Freeze v1 before promoting third-party plugins.
- HACS-style registry with signed entries; installs land in `data/plugins/<id>/<version>/` and are served with SRI.

### 7.6 Dependencies (external JS libraries)

- **Bundling is the supported path.** `plugin-kit build` (esbuild/rollup/Bun) emits a single ESM entry with npm dependencies inlined — offline-friendly, conflict-free, no runtime resolver.
- **Host modules** are imported via the `app:` scheme (`app:contracts`, `app:ui`), versioned with the plugin API. These are the only non-bundled imports allowed at runtime.
- **Runtime imports are restricted to the plugin's own package and `app:*`**, which eliminates import-map conflicts, CDN outages, and load-time supply-chain surprises.
- **Remote ESM is an exceptional opt-in**: pinned URL + SHA-256 in the manifest, `net:` permission, fetched and cached by the server at install. Localhost imports are a dev-only exception.
- The server verifies package hashes (and signatures when present) at install; packages are served same-origin with immutable caching. CSP stays `script-src 'self'`.

### 7.7 Sources (external catalogs)

OPDS 2.0 and WebDAV are core. Non-standard APIs (scrapers, proprietary services) are plugins registering `ctx.sources` entries that resolve to OPDS-shaped acquisition records; the server downloads, hashes, and indexes through the normal import pipeline. Fetched books are untrusted: content scripts disabled, rendering confined to the sandboxed iframe.

### 7.8 Plugin library (registries)

Users browse and install plugins/themes from one or more **registries** — static JSON indexes plus package archives. The official registry ships by default; users can add any registry URL.

- Registry index schema (cacheable, versioned):
  `{ schema, name, homepage, plugins: [{ id, name, kind, description, author, license, repo, screenshots, tags, verified, versions: [{ version, engines, permissions, url, sha256, signature, size, publishedAt }] }] }`
- **Server-side fetching** avoids CORS, caches indexes, verifies hashes/signatures, and installs atomically to `data/plugins/<id>/<version>/`. The app talks to `/api/registries` and `/api/plugins/*`.
- **Discover UI**: search + filters (kind, category, verified); detail page with README, screenshots, changelog, permissions, engine compatibility, size, and version history.
- **Install flow**: permission consent → download/verify → enable. Per-plugin enable/disable, version pin, rollback, uninstall, and auto-update (off by default).
- **Publishing**: `plugin-kit publish` bundles, validates manifest/token/engine constraints, creates the release, and emits the registry entry. The official registry can be generated from GitHub releases/topics (HACS model) and mirrored by self-hosters.
- **Trust tiers**: official (signed), verified (reviewed), community (self-signed/none). Signatures checked when present; permissionless theme/style packs can come from plain CSS hosts.

## 8. Styling

- Domains: **chrome** (shadow DOM + `::part`), **content** (book typography), **highlights** (`::highlight`), **export**.
- CSS never affects CFIs; it can require re-pagination, so registrations declare `layoutAffecting` and core re-anchors.
- Themes are data: a versioned **token contract** of CSS custom properties. `kind: "theme"` = tokens (+ optional CSS); `kind: "style"` = arbitrary scoped CSS. Static assets, no JS required — they share the plugin package format, registry, and update path, but carry no `entry` and no permissions. Single-file CSS themes are droppable as-is.
- Cascade: core declares `@layer epub, theme, plugin, user, highlights`. At ingest, author CSS files are wrapped in `@layer epub` — a text-only change, so the DOM tree (and CFIs) stay intact.
- Application via constructable stylesheets + `adoptedStyleSheets` in the content iframe: no `<style>` injection, CFI-safe, CSP-friendly.
- `appliesTo` rules (per book/tag) select themes; `@layer user` always wins; the token contract is semver'd.

## 9. Deployment

- Single static binary (no CGO) with embedded UI; goreleaser for linux/darwin/windows × amd64/arm64.
- Channels: curl installer, Homebrew, Scoop/winget, `.deb`/`.rpm`, Docker + compose.
- First-run wizard: setup token, LAN URL + QR, admin account, library directory.
- `bookserver doctor`, `bookserver upgrade` (signed), `bookserver backup`; built-in ACME or Tailscale/Cloudflare Tunnel for remote access.

## 10. Security and legal

- Users host their own copies; no DRM handling. Copyrighted editions stay private.
- Shared bundles contain **layers only** — quotes and locators, never full text indexes or book files.
- The locator index contains book text; treat it as book content.
- Auth: argon2id + signed cookies; OIDC optional. TLS via built-in ACME or a reverse proxy.
- Plugin network access is proxied via `ctx.net` and permission-gated.

## 11. Milestones

1. **M0** — import EPUB, serve content, catalog, read one book in the browser.
2. **M1** — locators, annotations, progress, sync event log, PWA offline.
3. **M2** — plugin/theme host + contracts v1 + first-party theme, TTS, and translate plugins.
4. **M3** — packaging (goreleaser, Docker), wizard, doctor/backup, OPDS.

## 12. Open questions

1. Single-user only at v1, or multi-user self-hosting?
2. EPUB-only at launch, or EPUB + PDF?
3. Translation/TTS: client-side with user API keys, or server integrations?
4. Is realtime sync needed, or is sync-on-open + polling enough?
5. Official registry: who curates/signs it, and what is the bar for "verified"?
6. Where does the initial token contract live — a fixed list, or per-theme contributions?
7. Book identity across editions: EPUB UUID, ISBN, content hash, or all three?
8. Study layer (objectives, questions, SRS): core or first-party plugins?
9. Freeze plugin API v1 at M2?
10. Remote ESM imports: allow pinned CDN imports at all, or is "bundle everything + `app:*`" the only supported path?
11. Which external sources ship in core via OPDS/WebDAV vs. as first-party plugin sources?
12. Project name and license.
