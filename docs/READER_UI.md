# Reader UI — Design

**Status:** Draft v0.1 · **Depends on:** [TECH_SPEC.md](../TECH_SPEC.md), [LOCATORS.md](LOCATORS.md)

The reader is the product. Everything else (library, auth, import) exists to
open a book and read it comfortably. This document describes how the reader
chrome is built, what state it persists, and where plugin and theming seams
sit.

## 1. Decision: enhance the current reader, don't replace it yet

The TECH_SPEC targets a client-side SPA with a sandboxed content iframe and a
plugin host (M2). That is still the destination, but it is not the next step:

- The current server-rendered reader already concatenates the spine into one
  accessible document. Browser search, translation, reader mode, and
  extensions work on it today; an iframe would break most of that until the
  shell re-implements it.
- A single document makes selection, highlights, and position tracking
  substantially simpler (no cross-frame messaging, no iframe scroll bridge).
- The locator index (`internal/locator`) was designed for exactly this reader.
  Books are content-addressed by SHA-256, so a book ID always maps to the same
  DOM, which makes structural anchors reliable without fuzzy matching.

So we keep the server-rendered page and layer a dependency-free ES-module
reader UI on top. The seams chosen here (state JSON, JSON API, CSS custom
properties, DOM events, `window.neolib`) are the same seams the future plugin
host will expose, so no work is thrown away.

## 2. UX shape (macOS Books-like)

```
┌────────────────────────────────────────────────────────────┐
│ ←  Title · Chapter     [🔖+] [≣] [🔖] [✎] [Aa]   ────────  │  auto-hiding bar
├───────────────┬────────────────────────────────────────────┤
│ Contents      │                                            │
│ Bookmarks     │            reading column                  │
│ Notes         │                                            │
│ ─────────     │                                            │
│ About         │                                            │
└───────────────┴────────────────────────────────────────────┘
```

- **Top bar** is fixed, hides when scrolling down, reappears when scrolling
  up or near the top. It carries: back to library, title + current chapter,
  add-bookmark, Contents, Bookmarks, Notes, display settings, and a reading-
  progress line. Contents / Bookmarks / Notes are icon buttons that open the
  sidebar directly on that panel; the active icon stays highlighted.
- **Sidebar** is a single panel switched from the toolbar icons: Contents,
  Bookmarks, Notes, or About. It overlays the text (fixed drawer) so opening
  it never reflows the column or loses the reading position. It always starts
  closed, on every screen size; the toolbar icons are the only way in.
- **Selection bar** docks at the bottom of the viewport while text is
  selected: highlight colours, copy, and bookmark. Keeping it off the text
  leaves the native selection handles and browser context menu unobstructed,
  and it only appears once the selection drag ends.
- **Display sheet** (`Aa`): theme, font family, text size, line height,
  measure. Applies instantly and is persisted.
- **Resume**: reopening a book scrolls to the last locator. If the furthest
  locator is materially ahead, a toast offers "Continue at furthest (42%)"
  instead of silently rewinding.

Progressive enhancement is a hard rule: without JavaScript the page is a
plain, readable book with a table of contents and the book details. The
sidebar is hidden until `reader.js` marks the article, so it can never flash
on first paint; a `<noscript>` rule reveals it in flow when scripting is off.

## 3. State and persistence

### 3.1 Tables

`annotations` (bookmarks and highlights) and `reading_progress` are per-user
rows. Locators are stored as JSON using the envelope from
[LOCATORS.md](LOCATORS.md) §3.

### 3.2 Locators in this reader

Because a book file never changes under a book ID, the fast path always works:
`domRange` is a structural anchor into the rendered chapter and the quote is
stored for display and future cross-edition re-anchoring. Resolution order in
the client is:

1. `domRange` (selector + text node + UTF-16 offset), verified against
   `text.highlight` when present,
2. exact quote search inside the chapter,
3. `charRange` / `progression` clamp,
4. otherwise the annotation is listed but marked unanchored.

The client projection mirrors `internal/locator` (text verbatim, `<br>` → one
space, `script/style/noscript/template` skipped) so `charRange` is meaningful
even though it is only a hint.

### 3.3 Progress and sync

- The client writes progress to `localStorage` immediately (offline-safe) and
  pushes to the server, debounced, on scroll and on `pagehide` with
  `keepalive`.
- On open, whichever side has the newer `updatedAt` wins for `last`.
- `furthest` is the maximum `totalProgression`; the server also enforces the
  maximum so a stale device cannot rewind it.
- Multi-device works because both devices read the same server rows. There is
  no events log yet; adding one later (TECH_SPEC §5.4) is additive.

## 4. HTTP API

All endpoints require a session and `X-CSRF-Token` (the page embeds the
double-submit token in a `<meta>` tag; the cookie stays `HttpOnly`).

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/books/{id}/state` | settings + progress + annotations |
| PUT | `/api/books/{id}/progress` | save `last` / `furthest` locators |
| POST | `/api/books/{id}/annotations` | create bookmark/highlight |
| PATCH | `/api/annotations/{id}` | update quote/body/colour |
| DELETE | `/api/annotations/{id}` | remove annotation |
| PATCH | `/api/reader/settings` | update display preferences |

## 5. Theming

Themes are data over a versioned token contract. Core declares the tokens;
themes only set them. A theme can be a CSS file dropped into a plugin package
later — no JavaScript.

Tokens are defined in `internal/web/static/reader/reader.css`:

```
--reader-bg             page background
--reader-fg             body text
--reader-muted          secondary text and icons
--reader-border         hairlines
--reader-panel-bg       toolbar / sidebar surface
--reader-accent         links, progress, active states
--reader-highlight-yellow/green/blue/pink
--reader-font-serif     serif stack
--reader-font-sans      sans stack
--reader-font-size      root size for the reading column
--reader-line-height    line height for the reading column
--reader-measure        max width of the reading column
```

Built-in themes: `light`, `sepia`, `dark`, plus `auto` which follows
`prefers-color-scheme`. The server sets `data-reader-theme`,
`data-reader-font`, and the three sizing tokens on `<html>` for every
signed-in page, so the library and settings shell share the reader's theme;
`reader.css` is linked site-wide for that reason.

## 6. Plugins and scripting seams

Before the manifest-based host exists, the reader exposes narrow seams so
extensions are possible without a rewrite:

- **State JSON** — `<script type="application/json" id="reader-state">`
  carries settings, progress, and annotations.
- **DOM events** — `neolib:ready`, `neolib:location`, `neolib:selection`,
  `neolib:annotation` bubble on `document`.
- **`window.neolib`** — a small read-only API (`book`, `location()`,
  `goTo(locator)`, `annotations()`, `on(event, cb)`).
- **CSS layers** — author styles are wrapped in `@layer epub`; reader chrome
  lives in `@layer reader`; a future user/plugin layer can override without
  specificity fights.

Highlights use the CSS Custom Highlight API, so enabling a highlight never
mutates the content DOM. Browsers without it fall back to wrapping text
segments in `<mark>` and the API contract is unchanged.

## 7. Implementation phases

1. Persistence: migration, locator model, store methods. ✅
2. JSON API and CSRF header support. ✅
3. Reader settings: theme/typography tokens and merge-on-save. ✅
4. Reader shell: toolbar (auto-hide), sidebar panels, display sheet, toasts. ✅
5. Locators on the client: projection, selection → locator, resolve, resume. ✅
6. Highlights: Custom Highlight API, selection bar, notes list. ✅
7. Bookmarks and progress sync. ✅
8. Tests, docs, and regression fixes. ✅
