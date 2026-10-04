// Reading progress: computes a locator for the current reading line, keeps
// it in localStorage for offline use, and syncs it to the server. The server
// keeps two locators: last (resume, latest wins) and furthest (high-water
// mark). On open, whichever side has the newer timestamp wins for last.

import { api } from "./api.js";
import {
  caretPoint,
  chapterAtFraction,
  chapterFor,
  locatorAtPoint,
  locatorFromOffset,
  resolveRange,
} from "./locator.js";
import { readBarHeight, scrollToRange } from "./annotations.js";

const SAVE_DEBOUNCE_MS = 1200;
const FURTHEST_THRESHOLD = 0.02;

export class ProgressTracker {
  constructor({ book, content, onLocation, notify }) {
    this.book = book;
    this.content = content;
    this.onLocation = onLocation || (() => {});
    this.notify = notify || (() => {});
    this.state = null;
    this.pending = false;
    this.revision = 0;
    this.lastSaved = null;
    this.timer = 0;
    this.frame = 0;
    this.onScroll = this.onScroll.bind(this);
    this.onPageHide = this.onPageHide.bind(this);
    this.onVisibility = this.onVisibility.bind(this);
  }

  load(serverProgress) {
    const local = readLocal(this.book.id);
    this.state = chooseState(serverProgress, local);
    this.lastSaved = this.state.last ? JSON.stringify(this.state.last) : null;
  }

  start() {
    window.addEventListener("scroll", this.onScroll, { passive: true });
    window.addEventListener("pagehide", this.onPageHide);
    document.addEventListener("visibilitychange", this.onVisibility);
    this.onScroll();
  }

  persisted() {
    return this.state;
  }

  furthest() {
    return this.state ? this.state.furthest : null;
  }

  resumeLocator() {
    return this.state ? this.state.last : null;
  }

  // current returns a collapsed locator at the reading line, or null when
  // the page has no readable text yet.
  current() {
    const barHeight = readBarHeight();
    const y = barHeight + Math.max(24, (window.innerHeight - barHeight) * 0.25);
    const rect = this.content.getBoundingClientRect();
    const xs = [
      rect.left + rect.width / 2,
      rect.left + rect.width / 4,
      rect.left + (rect.width * 3) / 4,
    ];
    for (const x of xs) {
      const locator = locatorAtPoint(x, y, this.book);
      if (locator) {
        return locator;
      }
    }
    return this.fallbackLocator();
  }

  // currentRange returns the text point at the reading line as a Range, so
  // the toolbar bookmark button can bookmark exactly where the reader is.
  currentRange() {
    const barHeight = readBarHeight();
    const y = barHeight + Math.max(24, (window.innerHeight - barHeight) * 0.25);
    const rect = this.content.getBoundingClientRect();
    const xs = [
      rect.left + rect.width / 2,
      rect.left + rect.width / 4,
      rect.left + (rect.width * 3) / 4,
    ];
    for (const x of xs) {
      const point = caretPoint(x, y);
      if (!point || !chapterFor(point.node)) {
        continue;
      }
      const range = document.createRange();
      range.setStart(point.node, point.offset);
      range.collapse(true);
      return range;
    }
    return null;
  }

  // fallbackLocator maps the scroll fraction onto the nearest chapter, used
  // when the caret API is unavailable (e.g. inside empty space).
  fallbackLocator() {
    const scrollable = Math.max(
      1,
      document.documentElement.scrollHeight - window.innerHeight,
    );
    const fraction = Math.min(1, Math.max(0, window.scrollY / scrollable));
    const found = chapterAtFraction(fraction);
    return found
      ? locatorFromOffset(found.chapter, found.offset, this.book)
      : null;
  }

  onScroll() {
    if (this.frame) {
      return;
    }
    this.frame = window.requestAnimationFrame(() => {
      this.frame = 0;
      const locator = this.current();
      if (!locator) {
        return;
      }
      const serialized = JSON.stringify(locator);
      if (serialized === this.lastSaved) {
        return;
      }
      this.lastSaved = serialized;
      this.updateState(locator);
      this.onLocation(locator);
      this.pending = true;
      writeLocal(this.book.id, this.state);
      this.schedulePush();
    });
  }

  updateState(locator) {
    const progression = progressionOf(locator);
    this.state.last = locator;
    this.state.lastProgression = progression;
    if (!this.state.furthest || progression > this.state.furthestProgression) {
      this.state.furthest = locator;
      this.state.furthestProgression = progression;
    }
    this.state.updatedAt = Date.now() / 1000;
    this.revision += 1;
  }

  schedulePush() {
    window.clearTimeout(this.timer);
    this.timer = window.setTimeout(() => this.push(), SAVE_DEBOUNCE_MS);
  }

  async push({ keepalive = false } = {}) {
    if (!this.pending || !this.state || !this.state.last) {
      return;
    }
    const revision = this.revision;
    const body = {
      last: this.state.last,
      furthest: this.state.furthest,
    };
    try {
      const progress = await api(
        `/api/books/${encodeURIComponent(this.book.id)}/progress`,
        { method: "PUT", body, keepalive },
      );
      // A scroll during the request makes this response stale: keep pending
      // set so the already scheduled push sends the newer position, and do
      // not adopt the older server fields.
      if (this.revision !== revision) {
        return;
      }
      this.pending = false;
      if (progress) {
        this.state.lastProgression = progress.lastProgression;
        this.state.furthest = progress.furthest;
        this.state.furthestProgression = progress.furthestProgression;
        this.state.updatedAt = progress.updatedAt;
        writeLocal(this.book.id, this.state);
      }
    } catch (error) {
      // Offline or signed out: the local copy is the queue. A later scroll
      // retries; the error is not worth interrupting reading for.
      if (error.status !== 401) {
        this.notify("Reading position could not be saved.");
      }
    }
  }

  onPageHide() {
    this.push({ keepalive: true });
  }

  onVisibility() {
    if (document.visibilityState === "hidden") {
      this.push({ keepalive: true });
    }
  }

  restore(locator, { smooth = false } = {}) {
    if (!locator) {
      return false;
    }
    const resolved = resolveRange(locator);
    if (!resolved) {
      return false;
    }
    scrollToRange(resolved.range, smooth);
    this.onLocation(locator);
    return true;
  }

  // shouldOfferFurthest reports whether the high-water mark is far enough
  // ahead that silently resuming at `last` would rewind the reader.
  shouldOfferFurthest() {
    if (!this.state || !this.state.furthest || !this.state.last) {
      return false;
    }
    if (this.state.last.href === this.state.furthest.href) {
      return (
        this.state.furthestProgression - this.state.lastProgression >
        FURTHEST_THRESHOLD
      );
    }
    return (
      this.state.furthestProgression - this.state.lastProgression >=
      FURTHEST_THRESHOLD
    );
  }

  // mergeRemote adopts newer progress from another device without moving
  // the reader's viewport.
  mergeRemote(serverProgress) {
    this.state = chooseState(serverProgress, this.state);
    writeLocal(this.book.id, this.state);
    if (this.state.last) {
      this.lastSaved = JSON.stringify(this.state.last);
    }
  }
}

function progressionOf(locator) {
  const locations = (locator && locator.locations) || {};
  return locations.totalProgression || locations.progression || 0;
}

function chooseState(serverProgress, local) {
  const state = {
    last: null,
    furthest: null,
    lastProgression: 0,
    furthestProgression: 0,
    updatedAt: 0,
  };
  const remote = serverProgress
    ? {
        last: serverProgress.last,
        furthest: serverProgress.furthest,
        lastProgression: serverProgress.lastProgression || 0,
        furthestProgression: serverProgress.furthestProgression || 0,
        updatedAt: serverProgress.updatedAt || 0,
      }
    : null;

  if (remote && local) {
    state.last = local.updatedAt > remote.updatedAt ? local.last : remote.last;
  } else {
    state.last = (local && local.last) || (remote && remote.last) || null;
  }

  const candidates = [remote, local].filter(Boolean);
  for (const candidate of candidates) {
    if (
      candidate.furthest &&
      candidate.furthestProgression >= state.furthestProgression
    ) {
      state.furthest = candidate.furthest;
      state.furthestProgression = candidate.furthestProgression;
    }
  }
  if (!state.furthest && state.last) {
    state.furthest = state.last;
    state.furthestProgression = progressionOf(state.last);
  }
  if (state.last) {
    state.lastProgression = progressionOf(state.last);
  }
  state.updatedAt = Math.max(
    (remote && remote.updatedAt) || 0,
    (local && local.updatedAt) || 0,
  );
  return state;
}

function storageKey(bookId) {
  return `neolib:progress:${bookId}`;
}

function readLocal(bookId) {
  try {
    const raw = localStorage.getItem(storageKey(bookId));
    return raw ? JSON.parse(raw) : null;
  } catch {
    return null;
  }
}

function writeLocal(bookId, state) {
  try {
    localStorage.setItem(storageKey(bookId), JSON.stringify(state));
  } catch {
    // Storage may be full or blocked in private mode; the server copy still
    // receives the update.
  }
}
