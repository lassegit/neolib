/* Side table of contents: mark the chapter currently under the reading line
 * and fill the progress rail as the book is read. Progressive enhancement:
 * without this script the list is still a plain table of contents. */
(function () {
  "use strict";

  var nav = document.querySelector(".toc-side");
  if (!nav) {
    return;
  }

  var rail = nav.querySelector(".toc-rail");
  var progress = nav.querySelector(".toc-progress");
  var list = nav.querySelector("ol");
  var links = Array.prototype.slice.call(nav.querySelectorAll('a[href^="#"]'));
  if (!rail || !progress || !list || links.length === 0) {
    return;
  }

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

  var active = -1;
  var frame = 0;

  function update() {
    frame = 0;

    var scrollY = window.scrollY;
    var viewport = window.innerHeight;

    // The chapter whose heading has passed the reading line is current.
    var marker = scrollY + viewport * 0.25;
    var current = 0;
    var tops = [];
    var bottoms = [];
    for (var i = 0; i < sections.length; i++) {
      var rect = sections[i].section.getBoundingClientRect();
      tops[i] = rect.top + scrollY;
      bottoms[i] = rect.bottom + scrollY;
      if (tops[i] <= marker) {
        current = i;
      }
    }

    if (current !== active) {
      for (var j = 0; j < sections.length; j++) {
        sections[j].link.removeAttribute("aria-current");
      }
      var link = sections[current].link;
      link.setAttribute("aria-current", "location");
      active = current;

      // Keep the active item in view when the list is taller than the nav.
      var linkRect = link.getBoundingClientRect();
      var listRect = list.getBoundingClientRect();
      if (linkRect.top < listRect.top || linkRect.bottom > listRect.bottom) {
        list.scrollTop += linkRect.top - listRect.top - list.clientHeight / 2 + linkRect.height / 2;
      }
    }

    // Align the fill with the list rather than raw document length: it
    // grows through the active chapter's entry, so the end of the bar
    // always sits next to the bold item.
    var railRect = rail.getBoundingClientRect();
    var itemRect = sections[current].link.getBoundingClientRect();
    var itemTop = itemRect.top - railRect.top;

    var sectionEnd = current + 1 < sections.length ? tops[current + 1] : bottoms[current];
    if (current + 1 === sections.length) {
      // Finish the last entry at the end of the scroll range, which sits a
      // fraction of a viewport above the document bottom.
      var maxMarker = document.documentElement.scrollHeight - viewport * 0.75;
      sectionEnd = Math.min(sectionEnd, Math.max(tops[current] + 1, maxMarker));
    }
    var within = (marker - tops[current]) / Math.max(1, sectionEnd - tops[current]);
    within = Math.max(0, Math.min(1, within));

    var fill = itemTop + within * itemRect.height;
    fill = Math.max(0, Math.min(rail.clientHeight, fill));
    progress.style.height = fill.toFixed(2) + "px";
  }

  function schedule() {
    if (frame === 0) {
      frame = window.requestAnimationFrame(update);
    }
  }

  window.addEventListener("scroll", schedule, { passive: true });
  window.addEventListener("resize", schedule);
  window.addEventListener("load", schedule);
  window.addEventListener("hashchange", schedule);

  // A fragment jump moves the page without a user scroll and may settle
  // after the click handler returns, so refresh once now and once after the
  // browser has finished scrolling.
  nav.addEventListener("click", function (event) {
    var target = event.target;
    if (!target || !target.closest || !target.closest('a[href^="#"]')) {
      return;
    }
    window.setTimeout(schedule, 0);
    window.setTimeout(schedule, 150);
  });

  update();
})();
