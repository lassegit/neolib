// Bookmarks and highlights.
//
// Highlights are painted with the CSS Custom Highlight API, so the content
// DOM is never mutated. Browsers without it fall back to wrapping text
// segments in <mark>. Both paths share the same locator state, so switching
// paths never changes what is stored.

import { api } from "./api.js";
import {
  chapterFor,
  locatorFromRange,
  resolveRange,
} from "./locator.js";

const COLORS = ["yellow", "green", "blue", "pink"];

export class AnnotationLayer {
  constructor({ book, content, selectionBar, bookmarkList, bookmarkEmpty, highlightList, highlightEmpty, onCounts, notify }) {
    this.book = book;
    this.content = content;
    this.selectionBar = selectionBar;
    this.bookmarkList = bookmarkList;
    this.bookmarkEmpty = bookmarkEmpty;
    this.highlightList = highlightList;
    this.highlightEmpty = highlightEmpty;
    this.onCounts = onCounts || (() => {});
    this.notify = notify || (() => {});

    this.annotations = [];
    this.ranges = new Map();
    this.selectionRange = null;
    this.pointerSelecting = false;
    // HighlightRegistry is maplike but is not an instanceof Map, so feature
    // detect on the registry itself.
    this.customHighlights =
      typeof Highlight === "function" &&
      typeof CSS !== "undefined" &&
      CSS.highlights != null;

    this.onSelectionChange = this.onSelectionChange.bind(this);
    this.onPointerDown = this.onPointerDown.bind(this);
    this.onSelectStart = this.onSelectStart.bind(this);
    this.onSelectEnd = this.onSelectEnd.bind(this);
  }

  init(annotations) {
    this.annotations = Array.isArray(annotations) ? annotations.slice() : [];
    this.paintAll();
    this.renderLists();
    document.addEventListener("selectionchange", this.onSelectionChange);
    this.content.addEventListener("pointerdown", this.onSelectStart);
    document.addEventListener("pointerup", this.onSelectEnd, true);
    document.addEventListener("pointerdown", this.onPointerDown, true);
    this.selectionBar.addEventListener("click", (event) => {
      const color = event.target.closest("[data-reader-highlight]");
      if (color) {
        this.createHighlight(color.dataset.readerHighlight);
        return;
      }
      if (event.target.closest("[data-reader-copy-selection]")) {
        this.copySelection();
        return;
      }
      if (event.target.closest("[data-reader-bookmark-selection]")) {
        this.createBookmark(this.selectionRange);
        return;
      }
    });
  }

  // replaceAll adopts a fresh annotation list from the server without
  // rebinding events.
  replaceAll(annotations) {
    for (const annotation of this.annotations) {
      this.unpaint(annotation);
    }
    this.annotations = Array.isArray(annotations) ? annotations.slice() : [];
    this.paintAll();
    this.renderLists();
    this.onCounts(this.counts());
  }

  list() {
    return this.annotations.slice();
  }

  byId(id) {
    return this.annotations.find((annotation) => annotation.id === id) || null;
  }

  paintAll() {
    if (this.customHighlights) {
      CSS.highlights.clear();
      this.ranges.clear();
    }
    for (const annotation of this.annotations) {
      this.paint(annotation);
    }
  }

  paint(annotation) {
    if (annotation.kind !== "highlight") {
      // Bookmarks still need a resolved range for navigation, but are not
      // painted into the text.
      const resolved = resolveRange(annotation.locator);
      if (resolved) {
        this.ranges.set(annotation.id, resolved.range);
      }
      return;
    }
    const resolved = resolveRange(annotation.locator);
    if (!resolved) {
      return;
    }
    this.ranges.set(annotation.id, resolved.range);
    const color = COLORS.includes(annotation.color) ? annotation.color : "yellow";
    if (this.customHighlights) {
      const name = `neolib-${color}`;
      let registry = CSS.highlights.get(name);
      if (!registry) {
        registry = new Highlight();
        CSS.highlights.set(name, registry);
      }
      registry.add(resolved.range);
    } else {
      this.wrapFallback(annotation.id, resolved.range, color);
    }
  }

  unpaint(annotation) {
    const range = this.ranges.get(annotation.id);
    if (this.customHighlights && range && annotation.kind === "highlight") {
      const color = COLORS.includes(annotation.color) ? annotation.color : "yellow";
      const registry = CSS.highlights.get(`neolib-${color}`);
      if (registry) {
        registry.delete(range);
        if (registry.size === 0) {
          CSS.highlights.delete(`neolib-${color}`);
        }
      }
    }
    if (!this.customHighlights && annotation.kind === "highlight") {
      this.unwrapFallback(annotation.id);
    }
    this.ranges.delete(annotation.id);
  }

  wrapFallback(id, range, color) {
    for (const entry of textNodesInRange(range)) {
      const mark = document.createElement("mark");
      mark.className = `neolib-hl neolib-hl-${color}`;
      mark.dataset.annotationId = id;
      // Split the node so the middle segment [start, end) can be wrapped.
      entry.node.splitText(entry.end);
      const middle = entry.node.splitText(entry.start);
      middle.parentNode.insertBefore(mark, middle);
      mark.appendChild(middle);
    }
  }

  unwrapFallback(id) {
    const selector = `mark.neolib-hl[data-annotation-id="${CSS.escape(id)}"]`;
    for (const mark of Array.from(document.querySelectorAll(selector))) {
      const parent = mark.parentNode;
      while (mark.firstChild) {
        parent.insertBefore(mark.firstChild, mark);
      }
      parent.removeChild(mark);
      parent.normalize();
    }
  }

  async createHighlight(color) {
    if (!this.selectionRange) {
      return;
    }
    await this.create("highlight", color, this.selectionRange);
    this.hideSelectionBar();
    window.getSelection().removeAllRanges();
  }

  async createBookmark(range) {
    const target = range || null;
    await this.create("bookmark", "", target);
    this.hideSelectionBar();
  }

  async create(kind, color, range) {
    let locator = null;
    if (range) {
      locator = locatorFromRange(range, {
        book: this.book,
        chapter: chapterFor(range.startContainer),
      });
    }
    if (!locator) {
      this.notify("That location could not be identified.");
      return;
    }
    try {
      const annotation = await api(
        `/api/books/${encodeURIComponent(this.book.id)}/annotations`,
        { method: "POST", body: { kind, color, body: "", locator } },
      );
      this.annotations.push(annotation);
      this.paint(annotation);
      this.renderLists();
      this.onCounts(this.counts());
      document.dispatchEvent(
        new CustomEvent("neolib:annotation", { detail: { annotation } }),
      );
      if (kind === "bookmark") {
        this.notify("Bookmark added.");
      }
    } catch (error) {
      this.notify(error.message);
    }
  }

  async remove(id) {
    const annotation = this.byId(id);
    if (!annotation) {
      return;
    }
    try {
      await api(`/api/annotations/${encodeURIComponent(id)}`, {
        method: "DELETE",
      });
    } catch (error) {
      this.notify(error.message);
      return;
    }
    this.unpaint(annotation);
    this.annotations = this.annotations.filter((item) => item.id !== id);
    this.renderLists();
    this.onCounts(this.counts());
    document.dispatchEvent(
      new CustomEvent("neolib:annotation", { detail: { annotation, deleted: true } }),
    );
  }

  navigate(id, { smooth = true } = {}) {
    const annotation = this.byId(id);
    if (!annotation) {
      return;
    }
    const resolved = resolveRange(annotation.locator);
    if (!resolved) {
      this.notify("This location could not be found in the book.");
      return;
    }
    scrollToRange(resolved.range, smooth);
    this.flash(annotation, resolved.range);
    document.dispatchEvent(
      new CustomEvent("neolib:location", {
        detail: { locator: annotation.locator, annotation },
      }),
    );
  }

  flash(annotation, range) {
    const element = closestBlock(range.startContainer);
    if (!element) {
      return;
    }
    element.classList.remove("reader-flash");
    // Force a restart of the CSS animation.
    void element.offsetWidth;
    element.classList.add("reader-flash");
    window.setTimeout(() => element.classList.remove("reader-flash"), 1200);
    void annotation;
  }

  copySelection() {
    const selection = window.getSelection();
    if (!selection || selection.isCollapsed) {
      return;
    }
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(selection.toString()).then(
        () => this.notify("Copied."),
        () => this.notify("Copy failed."),
      );
    } else {
      this.notify("Copy is not available in this browser.");
    }
  }

  counts() {
    return {
      bookmarks: this.annotations.filter((a) => a.kind === "bookmark").length,
      highlights: this.annotations.filter((a) => a.kind === "highlight").length,
    };
  }

  renderLists() {
    this.renderList("bookmark", this.bookmarkList, this.bookmarkEmpty);
    this.renderList("highlight", this.highlightList, this.highlightEmpty);
  }

  renderList(kind, list, empty) {
    list.textContent = "";
    const items = this.annotations
      .filter((annotation) => annotation.kind === kind)
      .sort(
        (a, b) =>
          progressionOf(a.locator) - progressionOf(b.locator) ||
          a.createdAt - b.createdAt,
      );
    empty.hidden = items.length > 0;
    for (const annotation of items) {
      const item = document.createElement("li");
      item.className = "annotation-item";
      item.dataset.annotationId = annotation.id;

      const main = document.createElement("button");
      main.type = "button";
      main.className = "annotation-main";
      main.addEventListener("click", () => this.navigate(annotation.id));

      const quote = document.createElement("span");
      quote.className = "annotation-quote";
      if (kind === "highlight" && annotation.color) {
        const swatch = document.createElement("span");
        swatch.className = "annotation-swatch";
        swatch.dataset.color = annotation.color;
        quote.appendChild(swatch);
      }
      quote.appendChild(
        document.createTextNode(displayText(annotation)),
      );

      const meta = document.createElement("span");
      meta.className = "annotation-meta";
      meta.textContent = metaText(annotation);

      main.appendChild(quote);
      main.appendChild(meta);

      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "annotation-delete";
      remove.setAttribute("aria-label", `Delete ${kind}`);
      remove.title = `Delete ${kind}`;
      remove.textContent = "×";
      remove.addEventListener("click", () => this.remove(annotation.id));

      item.appendChild(main);
      item.appendChild(remove);
      list.appendChild(item);
    }
  }

  onPointerDown(event) {
    if (this.selectionBar.hidden) {
      return;
    }
    if (event.target.closest("[data-reader-selection]")) {
      // Keep the text selection alive until the click handler runs.
      event.preventDefault();
      return;
    }
    this.hideSelectionBar();
  }

  onSelectStart() {
    this.pointerSelecting = true;
  }

  // The bar only appears once the pointer is released, so showing it never
  // interrupts a native selection drag.
  onSelectEnd() {
    if (!this.pointerSelecting) {
      return;
    }
    this.pointerSelecting = false;
    this.onSelectionChange();
  }

  onSelectionChange() {
    const selection = window.getSelection();
    if (!selection || selection.rangeCount === 0 || selection.isCollapsed) {
      this.hideSelectionBar();
      return;
    }
    const range = selection.getRangeAt(0);
    if (!this.content.contains(range.commonAncestorContainer)) {
      this.hideSelectionBar();
      return;
    }
    this.selectionRange = range.cloneRange();
    document.dispatchEvent(
      new CustomEvent("neolib:selection", {
        detail: { text: selection.toString() },
      }),
    );
    if (this.pointerSelecting) {
      return;
    }
    this.showSelectionBar();
  }

  // The actions are docked at the bottom of the viewport rather than
  // floating over the selection: the native selection handles and context
  // menu stay unobstructed, and the bar stays thumb-reachable on touch
  // screens.
  showSelectionBar() {
    this.selectionBar.hidden = false;
    this.selectionBar.classList.add("is-open");
  }

  hideSelectionBar() {
    this.selectionBar.classList.remove("is-open");
    this.selectionBar.hidden = true;
    this.selectionRange = null;
  }
}

function progressionOf(locator) {
  const locations = (locator && locator.locations) || {};
  return locations.totalProgression || locations.progression || 0;
}

function displayText(annotation) {
  const text = annotation.locator && annotation.locator.text;
  if (text && text.highlight) {
    return text.highlight.length > 240
      ? `${text.highlight.slice(0, 240)}…`
      : text.highlight;
  }
  if (annotation.locator && annotation.locator.title) {
    return annotation.locator.title;
  }
  return annotation.kind === "bookmark" ? "Bookmark" : "Highlight";
}

function metaText(annotation) {
  const bits = [];
  const title = annotation.locator && annotation.locator.title;
  if (title) {
    bits.push(title);
  }
  const location = progressionOf(annotation.locator);
  if (location > 0) {
    bits.push(`${Math.round(location * 100)}%`);
  }
  if (annotation.body) {
    bits.push(annotation.body);
  }
  return bits.join(" · ");
}

function textNodesInRange(range) {
  const root =
    range.commonAncestorContainer.nodeType === Node.TEXT_NODE
      ? range.commonAncestorContainer.parentNode
      : range.commonAncestorContainer;
  if (!root) {
    return [];
  }
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      return range.intersectsNode(node)
        ? NodeFilter.FILTER_ACCEPT
        : NodeFilter.FILTER_REJECT;
    },
  });
  const entries = [];
  while (walker.nextNode()) {
    const node = walker.currentNode;
    const start = node === range.startContainer ? range.startOffset : 0;
    const end = node === range.endContainer ? range.endOffset : node.data.length;
    if (end > start) {
      entries.push({ node, start, end });
    }
  }
  return entries;
}

function closestBlock(node) {
  const element =
    node.nodeType === Node.ELEMENT_NODE ? node : node.parentElement;
  if (!element) {
    return null;
  }
  return element.closest("p, li, blockquote, td, h1, h2, h3, h4, h5, h6, div") || element;
}

export function scrollToRange(range, smooth) {
  const rect = rectForRange(range);
  const barHeight = readBarHeight();
  const top = window.scrollY + rect.top - barHeight - 16;
  const reduceMotion =
    window.matchMedia &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  window.scrollTo({
    top: Math.max(0, top),
    behavior: smooth && !reduceMotion ? "smooth" : "auto",
  });
}

// rectForRange returns the viewport rect to align with the reading column.
// A collapsed range is a caret: Blink reports a caret rect for it, but
// Gecko reports an empty rect, which would otherwise send the reader back to
// the top of the book instead of the stored position. Probe the character
// next to the caret in that case, falling back to the containing element.
function rectForRange(range) {
  const rect = range.getBoundingClientRect();
  if (!isZeroRect(rect) || !range.collapsed) {
    return rect;
  }
  const node = range.startContainer;
  if (node && node.nodeType === Node.TEXT_NODE && node.data.length > 0) {
    const offset = Math.min(Math.max(range.startOffset, 0), node.data.length);
    const probe = document.createRange();
    if (offset < node.data.length) {
      probe.setStart(node, offset);
      probe.setEnd(node, offset + 1);
    } else {
      probe.setStart(node, offset - 1);
      probe.setEnd(node, offset);
    }
    const probeRect = probe.getBoundingClientRect();
    if (!isZeroRect(probeRect)) {
      return probeRect;
    }
  }
  const element =
    node && (node.nodeType === Node.ELEMENT_NODE ? node : node.parentElement);
  return element ? element.getBoundingClientRect() : rect;
}

function isZeroRect(rect) {
  return (
    rect.top === 0 &&
    rect.left === 0 &&
    rect.right === 0 &&
    rect.bottom === 0
  );
}

export function readBarHeight() {
  const bar = document.querySelector("[data-reader-bar]");
  return bar ? bar.getBoundingClientRect().height : 48;
}
