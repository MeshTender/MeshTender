// radio-preset.js — behavior for the shared "radio-fields" template: picking a
// preset fills the four radio fields, and editing a field points the picker at
// whichever preset now matches (or "Custom"). Delegated from document, so it
// also covers a form htmx swaps in after load.
(function () {
  "use strict";

  // drop float fuzz (910525000 / 1e6 must read 910.525)
  function trim(n) { return String(Math.round(n * 1e6) / 1e6); }

  function field(box, name) { return box.querySelector('[data-radio="' + name + '"]'); }

  function applyPreset(sel) {
    var box = sel.closest("[data-radio-fields]");
    var opt = sel.options[sel.selectedIndex];
    if (!box || !opt || !opt.dataset.freq) return; // "Custom" — leave the values alone
    field(box, "freq").value = trim(Number(opt.dataset.freq) / 1e6);
    field(box, "bw").value = trim(Number(opt.dataset.bw) / 1e3);
    field(box, "sf").value = String(opt.dataset.sf);
    field(box, "cr").value = String(opt.dataset.cr);
  }

  // Compare in Hz so display rounding can't cause a spurious mismatch.
  function syncPreset(box) {
    var sel = box.querySelector("[data-radio-preset]");
    if (!sel) return;
    var freqHz = Math.round(parseFloat(field(box, "freq").value) * 1e6);
    var bwHz = Math.round(parseFloat(field(box, "bw").value) * 1e3);
    var sf = String(field(box, "sf").value);
    var cr = String(field(box, "cr").value);
    for (var i = 0; i < sel.options.length; i++) {
      var o = sel.options[i];
      if (o.dataset.freq && Number(o.dataset.freq) === freqHz && Number(o.dataset.bw) === bwHz &&
          o.dataset.sf === sf && o.dataset.cr === cr) {
        sel.value = o.value;
        return;
      }
    }
    sel.value = "custom";
  }

  document.addEventListener("change", function (ev) {
    var t = ev.target;
    if (t instanceof HTMLSelectElement && t.matches("[data-radio-preset]")) applyPreset(t);
  });
  document.addEventListener("input", function (ev) {
    var t = ev.target;
    if (!(t instanceof HTMLInputElement) || !t.matches("[data-radio]")) return;
    var box = t.closest("[data-radio-fields]");
    if (box) syncPreset(box);
  });
})();
