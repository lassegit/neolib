/* Side table of contents: mark the chapter currently under the reading line
 * and fill the progress rail as the book is read. Progressive enhancement:
 * without this script the list is still a plain table of contents.
 *
 * The rail is a continuous function of scroll position, so it cannot be
 * driven by IntersectionObserver alone. Instead of measuring headings on
 * every frame, section offsets and rail-relative geometry are cached and
 * recomputed only when layout actually changes, so an ordinary scroll frame
 * performs no layout reads at all. */
(function () {
  "use strict";

  var nav = document.querySelector(".toc-side");
  if (!nav) {
    return;
  }

  var rail = nav.querySelector(".toc-rail");
  var progress = nav.querySelector(".toc-progress");
  // `.toc-body > ol` only matches the top-level list (`:scope` throws in
  // browsers without it, such as IE11, which are otherwise supported).
  var list = nav.querySelector(".toc-body > ol");
  var links = Array.prototype.slice.call(nav.querySelectorAll('a[href^="#"]'));
  if (!rail || !progress || !list || links.length === 0) {
    return;
  }

  // The fraction of the viewport above which a heading counts as current.
  var READING_LINE = 0.25;

  var sections = [];
  for (var i = 0; i < links.length; i++) {
    var raw = links[i].hash.slice(1);
    var id = raw;
    try {
      id = decodeURIComponent(raw);
    } catch (error) {
      // A malformed percent escape in an EPUB-authored fragment throws
      // URIError. Keeping the raw hash merely leaves this entry untracked
      // instead of aborting the whole enhancement; the link still navigates
      // natively.
      id = raw;
    }
    var section = document.getElementById(id);
    if (section) {
      sections.push({ link: links[i], section: section });
    }
  }
  if (sections.length === 0) {
    return;
  }

  // TOC order is not guaranteed to match reading order (front and back
  // matter can be listed out of place), so track sections in document
  // order before using their positions.
  sections.sort(function (a, b) {
    if (a.section === b.section) {
      return 0;
    }
    var position = a.section.compareDocumentPosition(b.section);
    if (position & Node.DOCUMENT_POSITION_FOLLOWING) {
      return -1;
    }
    if (position & Node.DOCUMENT_POSITION_PRECEDING) {
      return 1;
    }
    return 0;
  });

  var count = sections.length;
  var tops = new Array(count);
  var bottoms = new Array(count);
  var maxMarker = 0;
  var boundsDirty = true;

  var active = -1;
  var geometryDirty = true;
  var itemTop = 0;
  var itemHeight = 0;
  var railHeight = 0;

  var frame = 0;
  var canObserveLayout = typeof window.ResizeObserver === "function";

  // Document-space offsets of every section, plus the furthest the reading
  // line can travel. Only called after a resize, a document height change,
  // or on browsers without ResizeObserver.
  function measureBounds() {
    var scrollY = window.scrollY;
    for (var i = 0; i < count; i++) {
      var rect = sections[i].section.getBoundingClientRect();
      tops[i] = rect.top + scrollY;
      bottoms[i] = rect.bottom + scrollY;
    }
    // At maximum scroll the reading line sits (1 - READING_LINE) of a
    // viewport above the document bottom.
    maxMarker =
      document.documentElement.scrollHeight -
      window.innerHeight * (1 - READING_LINE);
    boundsDirty = false;
    // Cached offsets moved, so rail-relative geometry is suspect too.
    geometryDirty = true;
  }

  // Index of the last section whose heading has crossed the marker. `tops`
  // is in document order, so it is sorted.
  function sectionAt(marker) {
    var low = 0;
    var high = count - 1;
    var found = 0;
    while (low <= high) {
      var mid = (low + high) >> 1;
      if (tops[mid] <= marker) {
        found = mid;
        low = mid + 1;
      } else {
        high = mid - 1;
      }
    }
    return found;
  }

  function update() {
    frame = 0;
    if (boundsDirty) {
      measureBounds();
    }

    var viewport = window.innerHeight;
    var marker = window.scrollY + viewport * READING_LINE;
    var current = sectionAt(marker);
    var link = sections[current].link;

    if (current !== active) {
      if (active >= 0) {
        sections[active].link.removeAttribute("aria-current");
      }
      // Set the highlight before measuring: the bold weight can wrap text
      // and change the item's height, which the rail fill depends on.
      link.setAttribute("aria-current", "location");

      // Keep the active item in view when the list is taller than the nav,
      // and measure rail-relative geometry in the same read block. Writing
      // `scrollTop` afterwards leaves the frame with a single forced layout.
      var listRect = list.getBoundingClientRect();
      var linkRect = link.getBoundingClientRect();
      var railRect = rail.getBoundingClientRect();
      var centered =
        list.scrollTop +
        (linkRect.top - listRect.top) -
        list.clientHeight / 2 +
        linkRect.height / 2;
      var target = Math.max(
        0,
        Math.min(centered, list.scrollHeight - list.clientHeight),
      );
      var delta = target - list.scrollTop;
      list.scrollTop = target;

      // The rail does not scroll with the list, so scrolling the list by
      // `delta` moves the active item the other way by the same amount.
      itemTop = linkRect.top - railRect.top - delta;
      itemHeight = linkRect.height;
      railHeight = railRect.height;
      active = current;
      geometryDirty = false;
    } else if (geometryDirty) {
      var railBox = rail.getBoundingClientRect();
      var itemBox = link.getBoundingClientRect();
      itemTop = itemBox.top - railBox.top;
      itemHeight = itemBox.height;
      railHeight = railBox.height;
      geometryDirty = false;
    }

    // Align the fill with the list rather than raw document length: it
    // grows through the active chapter's entry, so the end of the bar
    // always sits next to the bold item.
    var sectionEnd =
      current + 1 < count ? tops[current + 1] : bottoms[current];
    if (current + 1 === count) {
      // Finish the last entry at the end of the scroll range, which sits a
      // fraction of a viewport above the document bottom.
      sectionEnd = Math.min(
        sectionEnd,
        Math.max(tops[current] + 1, maxMarker),
      );
    }
    var within =
      (marker - tops[current]) / Math.max(1, sectionEnd - tops[current]);
    within = Math.max(0, Math.min(1, within));

    var fill = itemTop + within * itemHeight;
    fill = Math.max(0, Math.min(railHeight, fill));
    progress.style.height = fill.toFixed(2) + "px";

    if (!canObserveLayout) {
      // No ResizeObserver means late layout shifts (lazy images) cannot be
      // detected; fall back to measuring on every frame.
      boundsDirty = true;
    }
  }

  function schedule() {
    if (frame === 0) {
      frame = window.requestAnimationFrame(update);
    }
  }

  function invalidate() {
    boundsDirty = true;
    schedule();
  }

  if (canObserveLayout) {
    // Lazy-loaded images change the document height after `load`, so watch
    // the body rather than measuring once.
    new ResizeObserver(invalidate).observe(document.body);
  }
  if (document.fonts && document.fonts.ready) {
    document.fonts.ready.then(invalidate);
  }
  nav.classList.add("toc-enhanced");

  window.addEventListener("scroll", schedule, { passive: true });
  window.addEventListener("resize", invalidate);
  window.addEventListener("load", invalidate);
  window.addEventListener("hashchange", schedule);

  // The rail's position relative to the active item changes when the reader
  // scrolls the TOC list itself (it is a scroll container on wide screens).
  list.addEventListener(
    "scroll",
    function () {
      geometryDirty = true;
      schedule();
    },
    { passive: true },
  );

  update();
})();
