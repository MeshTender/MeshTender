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
//   [data-check-all] /            — a button that checks (all) or unchecks (none)
//   [data-check-none]               every enabled checkbox within its scope. The
//                                    scope is the closest [data-check-scope]
//                                    ancestor, or the whole document if none.
(function () {
  "use strict";

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

    if (el.matches("[data-autosubmit]") && el.form) {
      el.form.submit();
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
