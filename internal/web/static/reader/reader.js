// neolib reader bootstrap.
//
// Wires the chrome, annotation layer, and progress tracker together. The
// state embedded by the server is authoritative on first paint; a background
// refresh picks up changes made on other devices.

import { api } from "./api.js";
import { AnnotationLayer } from "./annotations.js";
import { ProgressTracker } from "./progress.js";
import {
  createToast,
  setupDisplay,
  setupSidebar,
  setupToolbar,
} from "./chrome.js";
import { chapterByHref } from "./locator.js";

const article = document.querySelector("[data-reader]");

if (article) {
  boot(article);
}

function parseState(article) {
  const script = document.getElementById("reader-state");
  if (script) {
    try {
      return JSON.parse(script.textContent || "{}");
    } catch {
      // Fall through to the data attributes on the article.
    }
  }
  return {
    book: {
      id: article.dataset.bookId,
      sha256: article.dataset.bookSha,
      title: article.dataset.bookTitle,
    },
    settings: {},
    progress: null,
    annotations: [],
  };
}

function boot(article) {
  const state = parseState(article);
  const book = state.book || {
    id: article.dataset.bookId,
    sha256: article.dataset.bookSha,
    title: article.dataset.bookTitle,
  };

  // Switch on the JS chrome as early as possible; the sidebar and toolbar
  // become overlays from here on.
  article.classList.add("reader-js");

  const content = article.querySelector("[data-reader-content]");
  if (!content) {
    return;
  }

  const toastHost = article.querySelector("[data-reader-toast]");
  const { toast } = createToast(toastHost);
  const tocLinks = Array.from(
    article.querySelectorAll("[data-reader-panel='toc'] a[href^='#']"),
  );

  let toolbar = null;
  let currentChapter = null;

  const sidebar = setupSidebar({
    article,
    onOpenChange: (open) => {
      if (toolbar) {
        toolbar.setForced(open || display.isOpen());
      }
      if (open) {
        markToc(currentChapter);
      }
    },
  });

  const display = setupDisplay({
    article,
    settings: state.settings || {},
    notify: toast,
    onOpenChange: () => {
      if (toolbar) {
        toolbar.setForced(sidebar.isOpen() || display.isOpen());
      }
    },
    onBeforeLayout: () => tracker.current(),
    onChange: (_settings, { layout, before }) => {
      if (layout && before) {
        window.requestAnimationFrame(() => tracker.restore(before));
      }
    },
  });

  const tracker = new ProgressTracker({
    book,
    content,
    notify: toast,
    onLocation: (locator) => {
      const fraction =
        (locator.locations && locator.locations.totalProgression) ||
        (locator.locations && locator.locations.progression) ||
        0;
      toolbar.setProgress(fraction);
      const chapter = chapterByHref(locator.href);
      currentChapter = chapter;
      const title = chapter ? chapter.dataset.title : "";
      toolbar.setLocation(
        title ? `${title} · ${Math.round(fraction * 100)}%` : `${Math.round(fraction * 100)}%`,
      );
      markToc(chapter);
    },
  });

  const annotations = new AnnotationLayer({
    book,
    content,
    selectionBar: article.querySelector("[data-reader-selection]"),
    bookmarkList: article.querySelector("[data-reader-bookmark-list]"),
    bookmarkEmpty: article.querySelector("[data-reader-bookmark-empty]"),
    highlightList: article.querySelector("[data-reader-highlight-list]"),
    highlightEmpty: article.querySelector("[data-reader-highlight-empty]"),
    notify: toast,
  });

  toolbar = setupToolbar({
    article,
    onToggleDisplay: () => display.toggle(),
    onAddBookmark: () => {
      const range = tracker.currentRange();
      if (range) {
        annotations.createBookmark(range);
      } else {
        toast("There is no text at this position to bookmark.");
      }
    },
  });

  tracker.load(state.progress);
  annotations.init(state.annotations || []);

  restoreProgress(tracker);
  offerFurthest(tracker, toast);
  tracker.start();

  // Close the sidebar after following a table of contents link on narrow
  // screens, where the overlay would otherwise hide the destination.
  for (const link of tocLinks) {
    link.addEventListener("click", () => {
      if (window.matchMedia("(max-width: 52rem)").matches) {
        sidebar.setOpen(false);
      }
    });
  }

  window.addEventListener("resize", () => {
    if (window.matchMedia("(max-width: 52rem)").matches && sidebar.isOpen()) {
      sidebar.setOpen(false);
    }
  });

  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") {
      refreshState(book, annotations, tracker, display);
    }
  });

  exposeAPI({ book, tracker, annotations, sidebar, display });
  document.dispatchEvent(new CustomEvent("neolib:ready", { detail: { book } }));
}

// restoreProgress scrolls to the stored resume point and re-anchors after
// late layout (fonts, lazy images), but only while the reader has not
// started scrolling themselves.
function restoreProgress(tracker) {
  const locator = tracker.resumeLocator();
  if (!locator) {
    return;
  }
  let moved = false;
  const markMoved = () => {
    moved = true;
  };
  for (const type of ["wheel", "touchstart", "keydown"]) {
    window.addEventListener(type, markMoved, { passive: true });
  }
  const restore = () => {
    if (!moved) {
      tracker.restore(locator);
    }
  };
  restore();
  window.requestAnimationFrame(restore);
  if (document.fonts && document.fonts.ready) {
    document.fonts.ready.then(restore);
  }
  window.addEventListener("load", restore);
  window.setTimeout(restore, 600);
}

// offerFurthest surfaces the high-water mark when resuming would rewind.
function offerFurthest(tracker, toast) {
  if (!tracker.shouldOfferFurthest()) {
    return;
  }
  const percent = Math.round(
    (tracker.persisted().furthestProgression || 0) * 100,
  );
  toast(`Furthest reading point: ${percent}%.`, {
    action: "Continue there",
    onAction: () => tracker.restore(tracker.furthest(), { smooth: true }),
    duration: 8000,
  });
}

// markToc maps chapters to their first TOC link once, then highlights the
// current chapter's link. Rebuilding the map every scroll frame would make
// large tables of contents visible in profiles.
let tocLinkByChapter = null;
let currentTocChapterId = null;

function markToc(chapter) {
  const panel = document.querySelector("[data-reader-panel='toc']");
  if (!panel || !chapter) {
    return;
  }
  if (chapter.id === currentTocChapterId) {
    return;
  }
  currentTocChapterId = chapter.id;

  if (!tocLinkByChapter) {
    tocLinkByChapter = new Map();
    for (const link of panel.querySelectorAll("a[href^='#']")) {
      let id = link.hash.slice(1);
      try {
        id = decodeURIComponent(id);
      } catch {
        // Keep the raw hash; a malformed fragment just will not match.
      }
      const target = document.getElementById(id);
      const root = target && target.closest("[data-reader-chapter]");
      if (root && !tocLinkByChapter.has(root.id)) {
        tocLinkByChapter.set(root.id, link);
      }
    }
  }

  for (const link of tocLinkByChapter.values()) {
    link.removeAttribute("aria-current");
  }
  const chosen = tocLinkByChapter.get(chapter.id);
  if (chosen) {
    chosen.setAttribute("aria-current", "location");
    chosen.scrollIntoView({ block: "nearest" });
  }
}

// refreshState pulls annotations, progress, and settings again so a device
// that was left open catches up with edits made elsewhere.
async function refreshState(book, annotations, tracker, display) {
  try {
    const state = await api(`/api/books/${encodeURIComponent(book.id)}/state`);
    if (state.annotations) {
      annotations.replaceAll(state.annotations);
    }
    if (state.settings) {
      display.applyRemote(state.settings);
    }
    if (state.progress) {
      tracker.mergeRemote(state.progress);
    }
  } catch {
    // Offline or signed out: local state remains usable.
  }
}

function exposeAPI({ book, tracker, annotations, sidebar, display }) {
  window.neolib = {
    book,
    location: () => tracker.current(),
    goTo: (locator) => tracker.restore(locator, { smooth: true }),
    annotations: () => annotations.list(),
    sidebar,
    display,
    on: (event, callback) =>
      document.addEventListener(`neolib:${event}`, callback),
  };
}
