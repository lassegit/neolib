// Locator engine for the concatenated reader.
//
// The logical-text projection mirrors internal/locator: text nodes verbatim,
// <br> becomes one space, script/style/noscript/template are skipped. All
// offsets are UTF-16 code units, matching DOM Range. Highlights and reading
// position are anchored with a DomRange (selector + text node + offset) plus
// a quote for verification and display.

const SKIP_TAGS = new Set(["script", "style", "noscript", "template"]);
const CONTEXT_CHARS = 64;
const HIGHLIGHT_CAP = 4096;

const projectionCache = new WeakMap();

export function chapters() {
  return Array.from(document.querySelectorAll("[data-reader-chapter]"));
}

export function chapterFor(node) {
  if (!node) {
    return null;
  }
  const element =
    node.nodeType === Node.ELEMENT_NODE ? node : node.parentElement;
  return element ? element.closest("[data-reader-chapter]") : null;
}

export function chapterByHref(href) {
  return (
    chapters().find((chapter) => chapter.dataset.href === href) || null
  );
}

// projectionText returns the chapter's logical text, cached because the
// rendered content never changes while the page is open.
export function projectionText(root) {
  let text = projectionCache.get(root);
  if (text !== undefined) {
    return text;
  }
  let out = "";
  const walk = (node) => {
    for (let child = node.firstChild; child; child = child.nextSibling) {
      if (child.nodeType === Node.TEXT_NODE) {
        out += child.data;
        continue;
      }
      if (child.nodeType !== Node.ELEMENT_NODE) {
        continue;
      }
      const tag = child.localName;
      if (SKIP_TAGS.has(tag)) {
        continue;
      }
      if (tag === "br") {
        out += " ";
        continue;
      }
      if (tag === "img") {
        continue;
      }
      walk(child);
    }
  };
  walk(root);
  projectionCache.set(root, out);
  return out;
}

function* textItems(root) {
  for (let child = root.firstChild; child; child = child.nextSibling) {
    if (child.nodeType === Node.TEXT_NODE) {
      yield { node: child };
      continue;
    }
    if (child.nodeType !== Node.ELEMENT_NODE) {
      continue;
    }
    const tag = child.localName;
    if (SKIP_TAGS.has(tag) || tag === "img") {
      continue;
    }
    if (tag === "br") {
      yield { br: true };
      continue;
    }
    yield* textItems(child);
  }
}

// projectionOffset maps a DOM text position to an offset in the chapter
// projection.
export function projectionOffset(root, textNode, offset) {
  let acc = 0;
  for (const item of textItems(root)) {
    if (item.br) {
      acc += 1;
      continue;
    }
    if (item.node === textNode) {
      return acc + offset;
    }
    acc += item.node.data.length;
  }
  return acc;
}

// rangeFromOffsets is the reverse mapping, used only as a fallback when a
// DomRange cannot be resolved.
export function rangeFromOffsets(root, start, end) {
  const range = document.createRange();
  let acc = 0;
  let started = false;
  let finished = false;
  const walk = (node) => {
    for (let child = node.firstChild; child && !finished; child = child.nextSibling) {
      if (child.nodeType === Node.TEXT_NODE) {
        const length = child.data.length;
        if (!started && start <= acc + length) {
          range.setStart(child, clamp(start - acc, 0, length));
          started = true;
        }
        if (started && end <= acc + length) {
          range.setEnd(child, clamp(end - acc, 0, length));
          finished = true;
          return;
        }
        acc += length;
        continue;
      }
      if (child.nodeType !== Node.ELEMENT_NODE) {
        continue;
      }
      const tag = child.localName;
      if (SKIP_TAGS.has(tag) || tag === "img") {
        continue;
      }
      if (tag === "br") {
        acc += 1;
        continue;
      }
      walk(child);
    }
  };
  walk(root);
  if (!started) {
    return null;
  }
  if (!finished) {
    range.setEnd(root, root.childNodes.length);
  }
  return range;
}

// pointFromOffset returns the text node and offset for a projection offset.
function pointFromOffset(root, offset) {
  const range = rangeFromOffsets(root, offset, offset);
  if (!range) {
    return null;
  }
  return { node: range.startContainer, offset: range.startOffset };
}

function clamp(value, min, max) {
  return Math.max(min, Math.min(max, value));
}

function textNodeAt(element, index) {
  if (!element) {
    return null;
  }
  let count = 0;
  for (const child of element.childNodes) {
    if (child.nodeType !== Node.TEXT_NODE) {
      continue;
    }
    if (count === index) {
      return child;
    }
    count += 1;
  }
  return null;
}

function textIndexIn(element, textNode) {
  let count = 0;
  for (const child of element.childNodes) {
    if (child === textNode) {
      return count;
    }
    if (child.nodeType === Node.TEXT_NODE) {
      count += 1;
    }
  }
  return count;
}

// selectorFor builds a structural CSS selector from root to element.
function selectorFor(element, root) {
  if (element === root) {
    return ":scope";
  }
  const parts = [];
  let current = element;
  while (current && current !== root) {
    if (current.id) {
      parts.unshift("#" + CSS.escape(current.id));
      break;
    }
    const parent = current.parentElement;
    if (!parent) {
      break;
    }
    let index = 1;
    for (let sibling = current.previousElementSibling; sibling; sibling = sibling.previousElementSibling) {
      index += 1;
    }
    parts.unshift(`${current.localName}:nth-child(${index})`);
    current = parent;
  }
  return parts.join(" > ");
}

function elementFor(root, selector) {
  if (!selector || selector === ":scope") {
    return root;
  }
  try {
    return root.querySelector(selector);
  } catch {
    return null;
  }
}

// normalizePoint turns a DOM Range endpoint into a text node position. An
// element endpoint is snapped to the nearest descendant text node.
function normalizePoint(node, offset) {
  if (node.nodeType === Node.TEXT_NODE) {
    return { node, offset };
  }
  if (node.nodeType !== Node.ELEMENT_NODE && node.nodeType !== Node.DOCUMENT_NODE) {
    return null;
  }
  const children = node.childNodes;
  const next = children[offset];
  if (next) {
    const text = firstTextInSubtree(next) || firstTextAfter(next);
    if (text) {
      return { node: text, offset: 0 };
    }
  }
  const previous = children[offset - 1];
  if (previous) {
    const text = lastTextNode(previous);
    if (text) {
      return { node: text, offset: text.data.length };
    }
  }
  return null;
}

function firstTextInSubtree(node) {
  if (!node) {
    return null;
  }
  if (node.nodeType === Node.TEXT_NODE) {
    return node;
  }
  for (let child = node.firstChild; child; child = child.nextSibling) {
    const found = firstTextInSubtree(child);
    if (found) {
      return found;
    }
  }
  return null;
}

function firstTextAfter(node) {
  let current = node;
  while (current) {
    for (let sibling = current.nextSibling; sibling; sibling = sibling.nextSibling) {
      const found = firstTextInSubtree(sibling);
      if (found) {
        return found;
      }
    }
    current = current.parentNode;
  }
  return null;
}

function lastTextNode(node) {
  if (!node) {
    return null;
  }
  if (node.nodeType === Node.TEXT_NODE) {
    return node;
  }
  for (let child = node.lastChild; child; child = child.previousSibling) {
    const found = lastTextNode(child);
    if (found) {
      return found;
    }
  }
  return null;
}

// anchorFor converts a point into the stored DomRange anchor shape.
export function anchorFor(root, textNode, offset) {
  const element = textNode.parentElement || root;
  return {
    cssSelector: selectorFor(element, root),
    textNodeIndex: textIndexIn(element, textNode),
    charOffset: offset,
  };
}

// bookProgression returns the publication-wide progression of an offset in
// a chapter using projection lengths, which are viewport-independent. The
// denominator is the whole book, so progression never decreases between
// chapters.
export function bookProgression(chapter, offset) {
  const all = chapters();
  let before = 0;
  let total = 0;
  let passed = false;
  for (const item of all) {
    const length = projectionText(item).length;
    total += length;
    if (item === chapter) {
      passed = true;
      continue;
    }
    if (!passed) {
      before += length;
    }
  }
  return total > 0 ? clamp((before + offset) / total, 0, 1) : 0;
}

// locatorFromRange builds a full locator from a DOM range inside a chapter.
export function locatorFromRange(range, { book, chapter, type = "application/xhtml+xml" }) {
  const root = chapterFor(range.startContainer) || chapter;
  if (!root || !root.contains(range.startContainer)) {
    return null;
  }
  const start = normalizePoint(range.startContainer, range.startOffset);
  const end = normalizePoint(range.endContainer, range.endOffset);
  if (!start || !end) {
    return null;
  }

  const text = projectionText(root);
  const charStart = projectionOffset(root, start.node, start.offset);
  const charEnd = projectionOffset(root, end.node, end.offset);
  const from = Math.min(charStart, charEnd);
  const to = Math.max(charStart, charEnd);
  const highlight = text.slice(from, to).slice(0, HIGHLIGHT_CAP);
  const progression = text.length > 0 ? clamp(to / text.length, 0, 1) : 0;

  return {
    v: 1,
    book: { hash: `sha256:${book.sha256}` },
    href: root.dataset.href || "",
    type,
    title: root.dataset.title || "",
    projection: "neolib/logical-text/1",
    locations: {
      domRange: {
        start: anchorFor(root, start.node, start.offset),
        end: anchorFor(root, end.node, end.offset),
      },
      charRange: { start: from, end: to },
      progression,
      totalProgression: bookProgression(root, to),
    },
    text: {
      before: text.slice(Math.max(0, from - CONTEXT_CHARS), from),
      highlight,
      after: text.slice(to, to + CONTEXT_CHARS),
    },
  };
}

// caretPoint returns a text point at a viewport coordinate, across engines.
export function caretPoint(x, y) {
  let node = null;
  let offset = 0;
  if (document.caretPositionFromPoint) {
    const position = document.caretPositionFromPoint(x, y);
    if (position) {
      node = position.offsetNode;
      offset = position.offset;
    }
  } else if (document.caretRangeFromPoint) {
    const range = document.caretRangeFromPoint(x, y);
    if (range) {
      node = range.startContainer;
      offset = range.startOffset;
    }
  }
  if (!node) {
    return null;
  }
  return normalizePoint(node, offset);
}

// locatorAtPoint builds a collapsed locator at a viewport coordinate.
export function locatorAtPoint(x, y, book) {
  const point = caretPoint(x, y);
  if (!point) {
    return null;
  }
  const root = chapterFor(point.node);
  if (!root) {
    return null;
  }
  const range = document.createRange();
  range.setStart(point.node, point.offset);
  range.collapse(true);
  return locatorFromRange(range, { book, chapter: root });
}

// normalizeForMatch applies the match-time normalization from LOCATORS.md
// §4.1: NFC, whitespace collapse, lowercase. It returns the normalized text
// and an index map back to raw UTF-16 offsets.
function normalizeForMatch(text) {
  let normalized = "";
  const map = [];
  let pendingSpace = false;
  for (let i = 0; i < text.length; i += 1) {
    const ch = text[i];
    if (/\s/.test(ch)) {
      pendingSpace = normalized.length > 0;
      continue;
    }
    if (pendingSpace) {
      normalized += " ";
      map.push(i - 1);
      pendingSpace = false;
    }
    const lower = ch.normalize("NFC").toLowerCase();
    for (const out of lower) {
      normalized += out;
      map.push(i);
    }
  }
  return { normalized, map };
}

// quoteMatches verifies that a resolved range still contains the stored
// quote, so a stale structural anchor can never move a highlight silently.
// The comparison is made against the range's own text, not the locator's
// charRange hint: the hint was stored next to the quote, so trusting it
// would accept a range that resolved to the wrong place.
export function quoteMatches(range, quote) {
  if (!quote) {
    return true;
  }
  const slice = range.toString();
  return normalizeForMatch(slice).normalized === normalizeForMatch(quote).normalized;
}

// quoteSearch finds a quote in the chapter, preferring the candidate closest
// to the charRange hint.
export function quoteSearch(root, locator) {
  const quote = locator.text && locator.text.highlight;
  if (!quote) {
    return null;
  }
  const text = projectionText(root);
  const { normalized, map } = normalizeForMatch(text);
  const needle = normalizeForMatch(quote).normalized;
  if (!needle) {
    return null;
  }

  const hint = locator.locations && locator.locations.charRange
    ? locator.locations.charRange.start
    : 0;
  let best = -1;
  let bestDistance = Infinity;
  let index = normalized.indexOf(needle);
  while (index !== -1) {
    const raw = map[index] !== undefined ? map[index] : 0;
    const distance = Math.abs(raw - hint);
    if (distance < bestDistance) {
      best = index;
      bestDistance = distance;
    }
    index = normalized.indexOf(needle, index + 1);
  }
  if (best === -1) {
    return null;
  }
  const start = map[best] !== undefined ? map[best] : 0;
  const lastIndex = best + needle.length - 1;
  const lastRaw = map[lastIndex] !== undefined ? map[lastIndex] : start;
  const end = Math.min(text.length, lastRaw + 1);
  return rangeFromOffsets(root, start, Math.max(end, start));
}

// resolveRange resolves a locator to a DOM range. Returns null when no
// anchor can be trusted; the annotation is then listed but unanchored.
export function resolveRange(locator) {
  if (!locator || !locator.href) {
    return null;
  }
  const root = chapterByHref(locator.href);
  if (!root) {
    return null;
  }

  const dom = locator.locations && locator.locations.domRange;
  if (dom) {
    const startElement = elementFor(root, dom.start.cssSelector);
    const endElement = elementFor(root, dom.end.cssSelector);
    const startNode = textNodeAt(startElement, dom.start.textNodeIndex);
    const endNode = textNodeAt(endElement, dom.end.textNodeIndex);
    if (startNode && endNode) {
      try {
        const range = document.createRange();
        range.setStart(startNode, clamp(dom.start.charOffset, 0, startNode.data.length));
        range.setEnd(endNode, clamp(dom.end.charOffset, 0, endNode.data.length));
        if (quoteMatches(range, locator.text && locator.text.highlight)) {
          return { range, chapter: root };
        }
      } catch {
        // Fall through to the quote and offset fallbacks.
      }
    }
  }

  const quoteRange = quoteSearch(root, locator);
  if (quoteRange) {
    return { range: quoteRange, chapter: root };
  }

  const chars = locator.locations && locator.locations.charRange;
  if (chars) {
    const range = rangeFromOffsets(root, chars.start, Math.max(chars.start, chars.end));
    if (range) {
      return { range, chapter: root };
    }
  }

  const progression =
    locator.locations && locator.locations.progression
      ? locator.locations.progression
      : 0;
  const offset = Math.floor(projectionText(root).length * progression);
  const range = rangeFromOffsets(root, offset, offset);
  if (range) {
    return { range, chapter: root };
  }
  return null;
}

// pointLocatorFromOffset builds a collapsed locator at a projection offset;
// used for resume fallbacks.
export function locatorFromOffset(root, offset, book) {
  const point = pointFromOffset(root, offset);
  if (!point) {
    return null;
  }
  const range = document.createRange();
  range.setStart(point.node, point.offset);
  range.collapse(true);
  return locatorFromRange(range, { book, chapter: root });
}
