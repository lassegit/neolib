// Reader chrome: auto-hiding toolbar, panel sidebar, display sheet, and
// toasts. Everything here is progressive enhancement; the server-rendered
// page is readable without it.

import { api } from "./api.js";

export function setupToolbar({ article, onToggleDisplay, onAddBookmark }) {
  const bar = article.querySelector("[data-reader-bar]");
  const progressFill = article.querySelector("[data-reader-progress]");
  const location = article.querySelector("[data-reader-location]");
  const displayToggle = article.querySelector("[data-reader-toggle-display]");
  const bookmarkButton = article.querySelector("[data-reader-add-bookmark]");

  let lastY = Math.max(0, window.scrollY);
  let ticking = false;
  let forced = false;

  function show() {
    bar.classList.remove("is-hidden");
  }

  function hide() {
    // Never hide the bar while it holds focus.
    if (bar.contains(document.activeElement)) {
      return;
    }
    bar.classList.add("is-hidden");
  }

  function update() {
    ticking = false;
    const y = Math.max(0, window.scrollY);
    if (forced || y < 24) {
      show();
      lastY = y;
      return;
    }
    const delta = y - lastY;
    if (delta > 10) {
      hide();
    } else if (delta < -10) {
      show();
    }
    lastY = y;
  }

  window.addEventListener(
    "scroll",
    () => {
      if (!ticking) {
        ticking = true;
        window.requestAnimationFrame(update);
      }
    },
    { passive: true },
  );

  displayToggle.addEventListener("click", () => onToggleDisplay(displayToggle));
  bookmarkButton.addEventListener("click", () => onAddBookmark());

  return {
    setProgress(fraction) {
      const clamped = Math.max(0, Math.min(1, fraction || 0));
      progressFill.style.width = `${(clamped * 100).toFixed(2)}%`;
    },
    setLocation(text) {
      location.textContent = text;
    },
    setForced(value) {
      forced = value;
      if (value) {
        show();
      }
    },
    show,
  };
}

// Sidebar panels are opened from the toolbar icon buttons via the
// data-reader-open-panel attribute, so the bar carries Contents, Bookmarks,
// Notes, and About directly (macOS Books style). One panel is visible at a
// time; the active button reflects the sidebar state.
export function setupSidebar({ article, onOpenChange }) {
  const sidebar = article.querySelector("[data-reader-sidebar]");
  const scrim = article.querySelector("[data-reader-scrim]");
  const closeButton = sidebar.querySelector("[data-reader-close-sidebar]");
  const title = sidebar.querySelector("[data-reader-panel-title]");
  const openButtons = Array.from(
    article.querySelectorAll("[data-reader-open-panel]"),
  );
  const panels = new Map(
    Array.from(sidebar.querySelectorAll("[data-reader-panel]")).map((panel) => [
      panel.dataset.readerPanel,
      panel,
    ]),
  );
  const labels = {
    toc: "Contents",
    bookmarks: "Bookmarks",
    notes: "Notes",
    about: "About this book",
  };

  sidebar.inert = true;
  let open = false;
  let active = "toc";

  function refreshButtons() {
    for (const button of openButtons) {
      const selected = button.dataset.readerOpenPanel === active;
      button.setAttribute("aria-expanded", String(open && selected));
      button.classList.toggle("is-active", open && selected);
    }
  }

  function showPanel(name) {
    if (!panels.has(name)) {
      return;
    }
    active = name;
    for (const [key, panel] of panels) {
      panel.hidden = key !== name;
    }
    if (title) {
      title.textContent = labels[name] || name;
    }
    refreshButtons();
  }

  function setOpen(value, { focus = false } = {}) {
    if (open === value) {
      return;
    }
    open = value;
    sidebar.toggleAttribute("data-open", value);
    sidebar.inert = !value;
    scrim.hidden = !value;
    syncLock();
    refreshButtons();
    onOpenChange(value);
    if (value && focus) {
      closeButton.focus({ preventScroll: true });
    }
  }

  function openPanel(name, options = {}) {
    showPanel(name);
    setOpen(true, options);
  }

  function togglePanel(name) {
    if (open && active === name) {
      setOpen(false);
      return;
    }
    openPanel(name, { focus: true });
  }

  // Lock background scrolling only where the sidebar is a full-screen
  // overlay; on wide screens it is a drawer and the book stays scrollable.
  function syncLock() {
    const overlay = window.matchMedia("(max-width: 52rem)").matches;
    document.body.classList.toggle("reader-locked", open && overlay);
  }

  window.addEventListener("resize", syncLock);
  scrim.addEventListener("click", () => setOpen(false));
  closeButton.addEventListener("click", () => setOpen(false));
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && open) {
      setOpen(false);
      closeButton.focus({ preventScroll: true });
    }
  });
  for (const button of openButtons) {
    button.addEventListener("click", () =>
      togglePanel(button.dataset.readerOpenPanel),
    );
  }

  showPanel(active);

  return {
    setOpen,
    openPanel,
    togglePanel,
    showPanel,
    activePanel: () => active,
    isOpen: () => open,
  };
}

export function setupDisplay({ article, settings, onChange, onBeforeLayout, onOpenChange, notify }) {
  const panel = article.querySelector("[data-reader-display]");
  const toggle = article.querySelector("[data-reader-toggle-display]");
  const themeOptions = Array.from(
    article.querySelectorAll("[data-reader-theme]"),
  );
  const fontOptions = Array.from(article.querySelectorAll("[data-reader-font]"));
  const fontSizes = article.querySelector("[data-reader-font-size]");
  const lineHeights = article.querySelector("[data-reader-line-height]");
  const measures = article.querySelector("[data-reader-measure]");

  let current = normalizeDisplay(settings);
  let saveTimer = 0;

  function apply() {
    const root = document.documentElement;
    root.dataset.readerTheme = current.theme;
    root.dataset.readerFont = current.font_family;
    root.style.setProperty("--reader-font-size", `${current.font_size}rem`);
    root.style.setProperty("--reader-line-height", current.line_height);
    root.style.setProperty("--reader-measure", `${current.measure}ch`);

    for (const option of themeOptions) {
      option.setAttribute(
        "aria-pressed",
        String(option.dataset.readerTheme === current.theme),
      );
    }
    for (const option of fontOptions) {
      option.setAttribute(
        "aria-pressed",
        String(option.dataset.readerFont === current.font_family),
      );
    }
    fontSizes.value = current.font_size;
    lineHeights.value = current.line_height;
    measures.value = current.measure;
  }

  function commit(partial, { layout = false } = {}) {
    const before = layout && onBeforeLayout ? onBeforeLayout() : null;
    current = { ...current, ...partial };
    apply();
    if (onChange) {
      onChange(current, { layout, before });
    }
    scheduleSave();
  }

  // applyRemote adopts settings fetched from the server without writing
  // them back (used by the background state refresh).
  function applyRemote(remote) {
    current = normalizeDisplay(remote);
    apply();
  }

  function scheduleSave() {
    window.clearTimeout(saveTimer);
    saveTimer = window.setTimeout(async () => {
      try {
        const saved = await api("/api/reader/settings", {
          method: "PATCH",
          body: current,
        });
        current = normalizeDisplay(saved);
        apply();
      } catch (error) {
        if (notify) {
          notify(error.message);
        }
      }
    }, 500);
  }

  function setOpen(value, { focus = false } = {}) {
    panel.hidden = !value;
    toggle.setAttribute("aria-expanded", String(value));
    if (onOpenChange) {
      onOpenChange(value);
    }
    if (value && focus) {
      panel.querySelector("button")?.focus({ preventScroll: true });
    }
  }

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !panel.hidden) {
      setOpen(false);
      toggle.focus({ preventScroll: true });
    }
  });
  document.addEventListener("pointerdown", (event) => {
    if (panel.hidden) {
      return;
    }
    if (panel.contains(event.target) || toggle.contains(event.target)) {
      return;
    }
    setOpen(false);
  });

  for (const option of themeOptions) {
    option.addEventListener("click", () =>
      commit({ theme: option.dataset.readerTheme }),
    );
  }
  for (const option of fontOptions) {
    option.addEventListener("click", () =>
      commit({ font_family: option.dataset.readerFont }, { layout: true }),
    );
  }
  fontSizes.addEventListener("input", () =>
    commit({ font_size: Number(fontSizes.value) }, { layout: true }),
  );
  lineHeights.addEventListener("input", () =>
    commit({ line_height: Number(lineHeights.value) }, { layout: true }),
  );
  measures.addEventListener("input", () =>
    commit({ measure: Number(measures.value) }, { layout: true }),
  );

  apply();

  return {
    current: () => ({ ...current }),
    open: () => setOpen(true, { focus: true }),
    close: () => setOpen(false),
    toggle: () => setOpen(panel.hidden, { focus: panel.hidden }),
    isOpen: () => !panel.hidden,
    applyRemote,
  };
}

export function createToast(container) {
  let timer = 0;
  function close() {
    window.clearTimeout(timer);
    container.hidden = true;
    container.textContent = "";
  }
  function toast(message, { action, onAction, duration = 5000 } = {}) {
    window.clearTimeout(timer);
    container.textContent = "";
    container.hidden = false;

    const box = document.createElement("div");
    box.className = "toast";
    const text = document.createElement("span");
    text.textContent = message;
    box.appendChild(text);

    if (action) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "toast-action";
      button.textContent = action;
      button.addEventListener("click", () => {
        close();
        if (onAction) {
          onAction();
        }
      });
      box.appendChild(button);
    }

    const dismiss = document.createElement("button");
    dismiss.type = "button";
    dismiss.className = "toast-dismiss";
    dismiss.setAttribute("aria-label", "Dismiss");
    dismiss.textContent = "×";
    dismiss.addEventListener("click", close);
    box.appendChild(dismiss);

    container.appendChild(box);
    timer = window.setTimeout(close, duration);
  }
  return { toast, close };
}

function normalizeDisplay(settings) {
  return {
    theme: settings.theme || "auto",
    font_family: settings.font_family || "serif",
    font_size: settings.font_size || 1.125,
    line_height: settings.line_height || 1.65,
    measure: settings.measure || 65,
  };
}
