// ui.js — small, CSP-friendly delegated handlers keyed off data-* attributes, so
// templates need no inline on*= handlers (which our strict CSP forbids). Loaded on
// every page. Supported hooks:
//   [data-confirm="msg"]          — gate a form submit (on the <form>) or a click
//                                    (on a button/link) behind window.confirm.
//   [data-copy="text"]            — copy a literal string to the clipboard.
//   [data-copy-target="#sel"]     — copy that element's value (input/textarea) or
//                                    textContent.
//   [data-copy-prev]              — copy the previous element sibling's textContent.
//   [data-autosubmit]             — submit the control's form when it changes.
//   [data-consent-target="#sel"]  — a checkbox that enables/disables the target as
//                                    it is (un)checked.
//   [data-gated]                  — a link/button whose activation is blocked while
//                                    aria-disabled="true".
//   [data-hide-target="#sel"]     — a checkbox that hides the target while checked,
//                                    for a switch that makes a section moot (the
//                                    steward toggle vs. per-command grants). The
//                                    target's inputs still submit: hiding says the
//                                    section doesn't apply right now, not that its
//                                    values are gone. Synced on load and after an
//                                    htmx swap, so a fragment rendered with the box
//                                    already checked starts collapsed.
//   [data-check-all] /            — a button that checks (all) or unchecks (none)
//   [data-check-none]               every enabled checkbox within its scope. The
//                                    scope is the closest [data-check-scope]
//                                    ancestor, or the whole document if none.
//   [data-risky] (checkbox)       — confirm before enabling; unchecks on cancel.
//                                    Delegated so it works in htmx-swapped content
//                                    (a modal fragment can't run inline script).
//   [data-tooltip]                — show this element's title= as a styled tooltip
//                                    on hover/focus, instead of the browser's slow
//                                    native one. Use it on icon-only controls,
//                                    where the icon alone doesn't say what it does.
//   [data-popover]                — like data-tooltip but for a paragraph rather
//                                    than a label: title= is the heading and
//                                    data-bs-content= the body. Put it on a
//                                    <button> so it is reachable by keyboard.
//
// It also localizes timestamps: any <time data-fmt="…"> element (emitted by the
// `ts` template func) is rewritten from its machine-readable datetime attribute
// into the viewer's locale and time zone. See formatTimes below.
(function () {
  "use strict";

  // --- tooltips ------------------------------------------------------------
  // Bootstrap tooltips are opt-in, so wire them once here rather than per page.
  // Delegating through the `selector` option means a control swapped in later by
  // htmx (a modal fragment, a re-rendered list) gets one with no re-initialization.
  //
  // It reads the element's title=, which stays as the no-JS fallback: without this,
  // hovering still surfaces the native tooltip. Note data-bs-toggle is NOT the hook
  // — an element can only carry one, and these controls already use it for modals.
  if (window.tabler && window.tabler.Tooltip) {
    new window.tabler.Tooltip(document.body, { selector: "[data-tooltip]" });
  }
  // Popovers carry an explanation too long for a tooltip. Same delegation, and
  // "hover focus" means a pointer reveals it while keyboard and touch can still
  // reach it — both dismiss on leave/blur, so there is no sticky panel to close.
  // Content is server-authored copy, never user data; Bootstrap sanitizes it anyway.
  if (window.tabler && window.tabler.Popover) {
    new window.tabler.Popover(document.body, {
      selector: "[data-popover]",
      trigger: "hover focus",
      html: true,
      placement: "left",
    });
  }

  // --- timestamp localization ---------------------------------------------
  // The server emits <time datetime="<RFC3339 UTC>" data-fmt="date|datetime|
  // time|time-seconds">UTC fallback</time>. We reformat each into the viewer's
  // locale (via Intl, with an undefined locale = the browser's) and time zone:
  // the user's saved IANA zone from <html data-tz>, or — when unset — the
  // browser's own zone (achieved by omitting timeZone so Intl uses local).
  var TS_OPTS = {
    date: { dateStyle: "medium" },
    datetime: { dateStyle: "medium", timeStyle: "short" },
    time: { timeStyle: "short" },
    "time-seconds": { timeStyle: "medium" },
  };

  var tsFormatters = {}; // cache Intl.DateTimeFormat per data-fmt kind

  function tsFormatter(kind) {
    if (tsFormatters[kind]) return tsFormatters[kind];
    var base = TS_OPTS[kind] || TS_OPTS.datetime;
    var opts = {};
    for (var k in base) if (Object.prototype.hasOwnProperty.call(base, k)) opts[k] = base[k];
    var tz = document.documentElement.getAttribute("data-tz");
    if (tz) opts.timeZone = tz; // else omit → Intl uses the browser's local zone
    var fmt;
    try {
      fmt = new Intl.DateTimeFormat(undefined, opts);
    } catch (e) {
      // A stale/invalid saved zone: fall back to the browser's local zone.
      delete opts.timeZone;
      fmt = new Intl.DateTimeFormat(undefined, opts);
    }
    tsFormatters[kind] = fmt;
    return fmt;
  }

  // formatTimes localizes every <time data-fmt> under root (default document).
  // Reading from the datetime attribute (never the current text) makes repeated
  // calls idempotent, so it's safe to re-run after htmx swaps or live inserts.
  function formatTimes(root) {
    var scope = root && root.querySelectorAll ? root : document;
    var els = scope.querySelectorAll("time[data-fmt][datetime]");
    for (var i = 0; i < els.length; i++) {
      var el = els[i];
      var d = new Date(el.getAttribute("datetime"));
      if (isNaN(d.getTime())) continue; // leave the server fallback in place
      el.textContent = tsFormatter(el.getAttribute("data-fmt")).format(d);
    }
  }

  // Exposed so views that inject timestamped nodes outside htmx (e.g. a
  // WebSocket log) can localize them: window.MeshtenderFormatTimes(node).
  window.MeshtenderFormatTimes = formatTimes;

  // A <input type="date" data-local-today> carries the server's UTC date as a
  // fallback; reset it to the viewer's local date (which can differ by a day) so
  // the default matches the day the user is actually having.
  function localTodayInputs(root) {
    var scope = root && root.querySelectorAll ? root : document;
    var inputs = scope.querySelectorAll("input[type=date][data-local-today]");
    if (!inputs.length) return;
    var now = new Date();
    var pad = function (n) { return (n < 10 ? "0" : "") + n; };
    var today = now.getFullYear() + "-" + pad(now.getMonth() + 1) + "-" + pad(now.getDate());
    for (var i = 0; i < inputs.length; i++) {
      inputs[i].max = today;
      inputs[i].value = today;
    }
  }

  // A checkbox that hides a section its state makes irrelevant. Inputs inside stay
  // enabled so they keep submitting — see the data-hide-target note at the top.
  function syncHideTargets(scope) {
    var boxes = (scope || document).querySelectorAll("input[type=checkbox][data-hide-target]");
    for (var i = 0; i < boxes.length; i++) applyHideTarget(boxes[i]);
  }

  function applyHideTarget(box) {
    var target = document.querySelector(box.getAttribute("data-hide-target"));
    if (!target) return;
    target.hidden = !!box.checked;
  }

  function onReady() {
    formatTimes(document);
    localTodayInputs(document);
    syncHideTargets(document);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", onReady);
  } else {
    onReady();
  }
  // htmx swaps in server HTML that may contain <time> elements.
  document.body && document.addEventListener("htmx:afterSwap", function (e) {
    formatTimes(e.target || document);
    syncHideTargets(e.target || document);
  });

  document.addEventListener("submit", function (e) {
    var form = e.target;
    if (form && form.matches && form.matches("[data-confirm]")) {
      if (!window.confirm(form.getAttribute("data-confirm"))) e.preventDefault();
    }
  });

  document.addEventListener("click", function (e) {
    var el = e.target;
    if (!el || !el.closest) return;

    // A gated control does nothing while disabled.
    var gated = el.closest("[data-gated]");
    if (gated && gated.getAttribute("aria-disabled") === "true") {
      e.preventDefault();
      return;
    }

    // A confirm gate on a clickable element (buttons/links). Forms are handled by
    // the submit listener above, so skip when the match is the form itself.
    var confirmEl = el.closest("[data-confirm]");
    if (confirmEl && confirmEl.tagName !== "FORM" && !window.confirm(confirmEl.getAttribute("data-confirm"))) {
      e.preventDefault();
      e.stopPropagation();
      return;
    }

    // Select-all / select-none for a group of checkboxes.
    var checkBtn = el.closest("[data-check-all], [data-check-none]");
    if (checkBtn) {
      e.preventDefault();
      var scope = checkBtn.closest("[data-check-scope]") || document;
      var checked = checkBtn.hasAttribute("data-check-all");
      var boxes = scope.querySelectorAll("input[type=checkbox]");
      for (var i = 0; i < boxes.length; i++) {
        if (!boxes[i].disabled) boxes[i].checked = checked;
      }
      return;
    }

    var copyBtn = el.closest("[data-copy], [data-copy-target], [data-copy-prev]");
    if (copyBtn) {
      var text = copyBtn.getAttribute("data-copy");
      if (text === null && copyBtn.hasAttribute("data-copy-prev")) {
        var prev = copyBtn.previousElementSibling;
        if (prev) text = prev.textContent;
      }
      if (text === null && copyBtn.hasAttribute("data-copy-target")) {
        var target = document.querySelector(copyBtn.getAttribute("data-copy-target"));
        if (target) {
          text = target.tagName === "INPUT" || target.tagName === "TEXTAREA" ? target.value : target.textContent;
        }
      }
      if (text != null && navigator.clipboard) navigator.clipboard.writeText(text);
    }
  });

  document.addEventListener("change", function (e) {
    var el = e.target;
    if (!el || !el.matches) return;

    // Confirm before enabling a risky command; revert the check if declined.
    if (el.matches("input[type=checkbox][data-risky]") && el.checked) {
      if (!window.confirm("This command can take over, lock out, or brick the node. Enable it here?")) {
        el.checked = false;
      }
      return;
    }

    if (el.matches("[data-autosubmit]") && el.form) {
      el.form.submit();
      return;
    }

    if (el.matches("input[type=checkbox][data-hide-target]")) {
      applyHideTarget(el);
      return;
    }

    if (el.matches("[data-consent-target]")) {
      var target = document.querySelector(el.getAttribute("data-consent-target"));
      if (!target) return;
      var off = !el.checked;
      target.classList.toggle("disabled", off);
      target.setAttribute("aria-disabled", off ? "true" : "false");
      if (off) target.setAttribute("tabindex", "-1");
      else target.removeAttribute("tabindex");
    }
  });
})();
