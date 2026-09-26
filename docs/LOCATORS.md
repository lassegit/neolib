# Locators — Design Specification

**Status:** Draft v0.1 · **Depends on:** [TECH_SPEC.md](../TECH_SPEC.md) §5.3

A locator is a stable, shareable reference to a point or range inside a book. Locators are the universal currency for bookmarks, highlights, reading progress, search results, share links, and the plugin API.

## 1. Requirements

- **Cross-device:** the same book on a phone and a laptop resolves to the same text. Viewport, font, column count, and pagination are irrelevant.
- **Across time:** app updates and renderer rewrites must not invalidate stored locators.
- **Across editions where possible:** an updated or different EPUB of the same work should re-anchor, degrading gracefully.
- **Storable and shareable:** one JSON object; a URL can carry it without server state.
- **Composable:** point and range locators, usable by third-party plugins (translation, TTS, SRS, export).
- **No external JS locator libraries.** Algorithms are implemented in-house using platform APIs only (`String.prototype.normalize`, `Intl.Segmenter`, `BigInt`, `CompressionStream`), mirroring the Go implementation in `internal/locator`.

## 2. Design summary

A locator is a **composite**. No single anchor is sufficient:

| Anchor | Role | Fails when |
|---|---|---|
| `domRange` / `partialCfi` | Exact fast path for the same file | DOM/file changed |
| `text` quote | Durable, human-meaningful re-anchor | Quote is short/ambiguous |
| `charRange` | Cheap hint, ordering, fuzzy-match offset | Text edited before it |
| `progression` / `position` | Last-resort fallback, progress UI | Location precision needed |

Structural anchors are always **verified against the quote** before they are trusted. Resolution is an ordered fallback with confidence scoring; unresolvable locators are kept and marked `unanchored`, never dropped.

## 3. Canonical locator JSON

The envelope follows the [Readium Locator](https://readium.org/architecture/models/locators/) model; `v`, `book`, `projection`, and `charRange` are neolib extensions.

```json
{
  "v": 1,
  "book": {
    "hash": "sha256:…",
    "uuid": "urn:uuid:…",
    "isbn": "978…",
    "workId": "openlibrary:OL…W"
  },
  "href": "OEBPS/chapter05.xhtml",
  "type": "application/xhtml+xml",
  "title": "Chapter 5",
  "projection": "neolib/logical-text/1",
  "locations": {
    "partialCfi": "/4/10[para05]/2/1:3[yyy,zzz;s=b]",
    "domRange": {
      "start": { "cssSelector": "#para05 > p:nth-of-type(3)", "textNodeIndex": 0, "charOffset": 15 },
      "end":   { "cssSelector": "#para05 > p:nth-of-type(3)", "textNodeIndex": 0, "charOffset": 43 }
    },
    "charRange": { "start": 10432, "end": 10460 },
    "progression": 0.41,
    "totalProgression": 0.28,
    "position": 419
  },
  "text": { "before": "…", "highlight": "…", "after": "…" }
}
```

Field rules:

- `href` + `type` are required; `href` is a resource path relative to the EPUB root, **without fragment**.
- `title` is advisory display metadata (best chapter/section title for the location).
- `locations.domRange` follows the Readium HTML extension: `textNodeIndex` is zero-based among child text nodes, `charOffset` is optional for element containers.
- `charRange` is `[start, end)`; a collapsed range has `start == end`.
- All offsets (`charRange`, CFI character offsets) are **UTF-16 code units**, zero-based. Rationale: this matches DOM Range, EPUB CFI 1.1 §3.1.4, and JavaScript string indexing exactly. Go converts with `unicode/utf16`. When exporting to W3C `TextPositionSelector` (which counts Unicode code points), convert explicitly.
- `text` slices are **raw, unnormalized** text from the projection (see §4). Normalization is applied only at match time, so stored locators stay deterministic per file.
- A range within one XHTML resource is a single locator with `domRange`; a range spanning resources is a `{ "start": Locator, "end": Locator }` pair. Point locators omit `end`.
- Requirements per use case:

| Use case | Required | Should have |
|---|---|---|
| Progress | `href`, `type`, `locations.progression` | `totalProgression`, `position`, `charRange`, `text.before/after` |
| Bookmark | `href`, `type`, `locations.progression` | `totalProgression`, `position`, `text`, `partialCfi`/`domRange` |
| Highlight | `href`, `type`, `text.highlight`, `locations.progression`, one of `domRange`/`partialCfi`/`charRange` | `totalProgression`, `position`, `text.before/after` |

### 3.1 Readium compatibility

Field names in `locations` and `text` match Readium. Mapping to a Readium-only locator = drop `v`, `book`, `projection`, `charRange`; mapping from one = add them. The `book` context is stored alongside a locator, not inside it, when handing locators to Readium-style APIs.

## 4. Logical-text projection (`neolib/logical-text/1`)

Locators index into a deterministic projection of XHTML, so every device and the server agree byte-for-byte.

Algorithm:

1. Start at (or inside) `body`; walk child nodes in document order.
2. For each text node, append its `data` verbatim (no whitespace collapsing).
3. For each `<br>`, append one `" "` (U+0020).
4. Skip entire subtrees: `script`, `style`, `noscript`, `template`. Skip `<img>` (text lives in `alt`, which CFI can address but the projection does not).
5. Ignore CSS entirely: `display:none` content is included, and styling changes can never move anchors. Plugins must not rely on hidden text being absent.
6. No Unicode normalization in the projection: raw UTF-16 code units only.
7. `projection` versions this algorithm. Any change that alters output requires a new version; old locators remain resolvable because resolution falls back to quotes.

Consistency:

- The server computes the authoritative projection at import, stores it per chapter, and returns each chapter's `char_count`.
- Clients compute the projection with the same algorithm. On `char_count` mismatch they log a drift metric and still resolve using their local projection (quotes are authoritative).
- Go and TS implementations are checked against shared golden vectors in `testdata/locators/projection/`.

### 4.1 Match-time normalization

Applied to both projection text and quote when comparing; never stored:

1. NFC normalize.
2. Collapse whitespace runs (`\s+`) to a single space and trim.
3. Case-fold with `toLowerCase()`.
4. Strip diacritics: NFD, remove `\p{M}` combining marks, NFC again (tunable; disabled for scripts where it harms matching).
5. Map a small punctuation set (curly quotes ↔ straight, en/em dash ↔ hyphen) — optional and corpus-gated.

Because normalization changes lengths, build a `normalized offset → raw offset` map while normalizing. Returned matches are converted back to raw UTF-16 offsets before they are stored or rendered.

## 5. CFI minting and correction (subset)

We mint `partialCfi` (Readium HTML extension: CFI fragment without the wrapping `epubcfi(...)` and without the OPF spine prefix). We do not need a full CFI engine:

- Path steps: element child steps are even numbers; odd steps reference character data. IDs are recorded as `[id]` assertions whenever the element has one.
- Character offsets: `:n`, UTF-16 code units.
- Text-location assertions: `[pre,post]` around the target offset, capped at 32 code units per side.
- Side bias: `[;s=b]` or `[;s=a]` to preserve which side of a break the point attaches to. Range endpoints use `b` for start, `a` for end.
- Correction (`resolveCfi`): walk steps, verifying ID assertions; if an assertion fails, find the element by ID elsewhere in the document and recompute the path. Then verify `[pre,post]` around the char offset; if it fails, search ±256 code units for the assertion pair and correct the offset. If correction fails, return `null` — the quote fallback takes over.
- Interop output: a full CFI is emitted only when sharing (`epubcfi(/<spineStep>!/<partialCfi>)`), where `spineStep` is derived from the spine index. CFI range notation is not consumed in v1; ranges live in `domRange`/`charRange` and in a start/end locator pair.

## 6. Resolution

```
resolve(loc, doc):
  1. structural: domRange → Range
     verify: normalized(projected(range)) ≈ normalized(text.highlight)
     return (1.00) if quote similarity ≥ 0.99
  2. structural: partialCfi (with correction) → Range
     verify as above; return (0.99)
  3. exact quote: indexOf over normalized projection; rank all hits by
     prefix/suffix agreement and distance to charRange hint
  4. fuzzy quote: in-house Bitap (see below) when no exact hit
  5. charRange: clamp to projection, snap forward to a grapheme boundary
  6. progression × chapter length → nearest block boundary
  7. cross-edition: remap href via chapter title/id, else search the quote
     across the whole book
  8. else: mark unanchored, keep the locator, surface the quote to the user
```

Verification is mandatory: an anchor that resolves but whose text does not match is a wrong location, not a stale one. Step 1/2 only win if the quote agrees.

### 6.1 In-house approximate matcher

Exact matching is the common path. When it fails, use a Bitap (Baeza-Yates–Gonnet) matcher implemented with `BigInt` bitmasks over Unicode code points:

- Quote (`exact`) capped at 256 code points for matching.
- Complexity `O(n · m / 64)`, no dependencies.
- Two passes: high-precision `maxErrors = ⌊m/8⌋ + 1`, then `maxErrors = ⌊m/4⌋` with a stricter acceptance threshold.
- Matches are scored with the same weighting scheme as the quote scorer below; a fuzzy match must beat the threshold on both quote and total score.

### 6.2 Scoring and thresholds

Initial defaults (constants in `packages/core/src/locator/quote-match.ts`, tuned by the CI anchor corpus):

```
score = 50·quoteSim + 20·prefixSim + 20·suffixSim + 2·positionSim
        normalized by 92
positionSim = 1 − |matchOffset − hint| / projectionLength
```

- Accept exact: `quoteSim == 1` and `score ≥ 0.86`.
- Accept fuzzy: `quoteSim ≥ 0.85` and `score ≥ 0.82`.
- Ambiguity: if the top two candidates differ by `< 0.02`, return unresolved unless the position hint separates them. Prefer no highlight over a wrong highlight.
- Grapheme safety: never return a range boundary inside a grapheme cluster (`Intl.Segmenter` with a surrogate/combining-mark fallback).

`Resolved` results carry `{ range, confidence, method, candidates }` so callers can debug and plugins can make choices.

## 7. Positions list

Generated at import from the authoritative projection and cached:

- One synthetic position per 1024 UTF-16 code units per resource, positions numbered globally from 1.
- Each entry: `href`, `type`, `locations.fragment` (partial CFI of the position start), `locations.position`, `locations.progression` (within resource), `locations.totalProgression`.
- Served as `application/vnd.readium.position-list+json` (Readium-compatible), with `total`.

Positions give stable "page 123 of 456" UI across viewports and a last-resort resume target that never requires rendering.

## 8. Sharing

### 8.1 Stateless link

```
https://host/b/<bookId>#loc=1.<base64url(deflateRaw(JSON))>
```

- `#loc=` fragment keeps the payload out of server logs; the link works offline once the book is local.
- `1.` is the link-format version; the decoder validates and rejects unknown versions.
- Encode with native `CompressionStream('deflate-raw')`; Go decodes with `compress/flate`.
- The payload contains the full locator including `book.hash` so the recipient can detect a different copy.
- For server-rendered previews (quote card, Open Graph) the same payload may be passed as `?loc=`; document the privacy trade-off.

### 8.2 Interop link

When the start point is expressible, the share UI also offers the bare interop fragment `#epubcfi(/6/14!/4/10[para05]/2/1:3[yyy,zzz;s=b])` plus the source URL. Other EPUB readers can use it directly.

### 8.3 Share card and limits

- Server preview shows title, author, chapter, quote, and context, so the link is meaningful without the book.
- Quote caps applied **only when exporting/sharing**: `highlight` ≤ 256 UTF-16 units, `before`/`after` ≤ 64. Local storage keeps full text. Ranges remain fully described by `charRange`/`domRange`.
- Shared payload cap: 8 KB compressed; reject and validate strictly. Quotes are rendered as text, never HTML.

## 9. W3C Web Annotation interop

Locators are exported/imported as Web Annotation Data Model JSON. Mapping:

| Locator field | W3C selector |
|---|---|
| `partialCfi` | `FragmentSelector` (`conformsTo` epubcfi) |
| `domRange` / `cssSelector` | `RangeSelector` + `CssSelector` |
| `text.before/highlight/after` | `TextQuoteSelector` prefix/exact/suffix |
| `charRange` | `TextPositionSelector` (UTF-16 → code points) |
| `progression` / `position` | extension property (no direct equivalent) |

```json
{
  "@context": "http://www.w3.org/ns/anno.jsonld",
  "id": "https://host/api/annotations/01H…",
  "type": "Annotation",
  "motivation": "highlighting",
  "target": {
    "source": "https://host/api/books/…/file?resource=OEBPS/chapter05.xhtml",
    "selector": {
      "type": "FragmentSelector",
      "conformsTo": "http://www.idpf.org/epub/linking/cfi/epub-cfi/",
      "value": "epubcfi(/6/14!/4/10[para05]/2/1:3)",
      "refinedBy": {
        "type": "RangeSelector",
        "startSelector": { "type": "TextQuoteSelector", "exact": "…", "prefix": "…", "suffix": "…" },
        "endSelector": { "type": "TextQuoteSelector", "exact": "…" }
      }
    }
  }
}
```

The adapter lives in `packages/core/src/locator/w3c.ts` (client) and `internal/locator/w3c.go` (server, for API responses).

## 10. Book identity and edition drift

Three identity levels, strongest first:

1. **Exact file** — `book.hash` (SHA-256). Same file ⇒ structural anchors are trustworthy.
2. **Edition** — `book.uuid` (EPUB `dc:identifier`) and `book.isbn`. Same edition after a file replacement ⇒ `href`/CFI may need correction; quotes re-anchor.
3. **Work** — `book.workId` (Open Library / Wikidata) or user-confirmed clustering by normalized title+author+language. Cross-edition resolution maps chapter via title/id and searches quotes book-wide (or via an optional alignment plugin); `totalProgression` is the last resort.

When an edition is replaced, keep the old hash and locator history; never rewrite stored locators silently. Corrections are explicit re-anchor events (§11).

## 11. Sync rules

- Append-only `events`; the **server-assigned monotonic `seq` is the only ordering authority**. Client events carry `clientId`, `opId` (idempotency key), and `createdAt` (device clock, display only). Offline queues replay with their `opId`s and are deduplicated server-side.
- Annotations: `add` / `update` / `delete` (tombstone) events. A materialized annotation is the latest event by `seq`; updates create revisions rather than mutating history. Locator corrections are ordinary update events.
- Progress stores two locators:
  - `last` — resume point; latest `seq` wins.
  - `furthest` — max `totalProgression`; ties broken by latest `seq`.
- On open, resume at `last`. If `furthest` is materially ahead (different chapter or > 2% progression), offer "continue at furthest" rather than silently rewinding.
- SSE notifies connected clients; polling is the fallback. No CRDTs in v1.

## 12. Plugin API

Locators are the only anchoring currency exposed to plugins:

```ts
interface LocatorService {
  current(): Promise<Locator>;
  fromSelection(): Promise<Locator>;        // point or range
  fromRange(range: Range): Promise<Locator>;
  fromViewport(): Promise<Locator>;         // progress point
  resolve(loc: Locator): Promise<Resolved | null>;
  goTo(loc: Locator): Promise<void>;
  observe(cb: (loc: Locator) => void): Disposable;
}
```

- Annotations carry `{ locator, body, motivation }`. **A translation is an annotation with `motivation: "translating"` and a body** — shareable and re-anchorable like any other annotation.
- Plugin cache keys must derive from `sha256(bookHash + href + normalizedQuote + projection + plugin-specific inputs)` so caches survive restarts and map across editions.
- `viewTransforms` declare `textPreserving: true|false`. In v1 only text-preserving transforms are allowed; text-altering transforms must provide a `mapRange()` and are disabled when it is absent. The canonical content DOM is never mutated.

## 13. Implementation layout

```
packages/core/src/locator/
  projection.ts    # logical-text walker (browser)
  normalize.ts     # match-time normalization + offset maps
  bitap.ts         # approximate matcher
  quote-match.ts   # scoring, ambiguity policy
  cfi.ts           # mint / parse / correct subset
  resolve.ts       # ordered fallback pipeline
  share.ts         # deflate + base64url codec
  w3c.ts           # Web Annotation adapter
internal/locator/
  projection.go    # authoritative import-time text + char counts
  positions.go     # position list generation
  cfi.go           # parse/correct for server-side resolution
  resolve.go       # position/progression resolver
  share.go         # codec for links/previews
testdata/locators/
  projection/      # Go+TS golden vectors
  mutations/       # anchor regression corpus
```

## 14. Testing

- **Golden projection vectors:** shared fixtures run by both Go and TS; must match exactly, including emoji, surrogate pairs, combining marks, RTL, CJK, and soft hyphens.
- **Round-trip fuzzing:** random selection → locator → resolve must return the identical range on the unmodified file.
- **Mutation corpus:** insert a paragraph, split/merge chapters, reorder spine entries, change whitespace/entities, update metadata, replace edition. Track re-anchor success rate and character drift; thresholds in CI (start: ≥ 98% re-anchor, ≤ 64 UTF-16 units drift on same-edition edits).
- **Matcher tests:** Bitap results compared against a naive Levenshtein implementation on small inputs.
- **Share codec:** cross-language encode/decode test between TS and Go, malformed-payload rejection.

## 15. Non-goals and decisions

- **No external JS libraries.** Projection, normalization, Bitap, CFI subset, and share codec are in-house; design credit to Hypothesis anchoring, Readium locators, and EPUB CFI 1.1.
- No full CFI engine, no CFI ranges in v1; no PDF/audio locators yet (the envelope has `fragments`/time/spatial fields reserved for later).
- No CRDTs, no E2E encryption.
- Offsets stored as UTF-16 code units; code-point conversion only at the W3C boundary.
- Quotes are capped on export, never silently truncated in local storage.
