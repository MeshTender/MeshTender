// timezone-picker.js — fills the account time-zone <select> from the browser's
// own IANA database (Intl.supportedValuesOf), grouped by region, so we don't
// maintain a zone list server-side. Enhances any <select data-tz-picker>:
//   data-current="America/New_York"  — the saved zone ("" = auto-detect)
//   [data-tz-detected] sibling       — a hint element for the detected zone
// Falls back to the server-rendered options if Intl.supportedValuesOf is absent.
(function () {
  "use strict";

  function detectedZone() {
    try {
      return new Intl.DateTimeFormat().resolvedOptions().timeZone || "";
    } catch (e) {
      return "";
    }
  }

  function buildPicker(sel) {
    if (typeof Intl === "undefined" || typeof Intl.supportedValuesOf !== "function") {
      return; // leave the server-rendered options in place
    }
    var zones;
    try {
      zones = Intl.supportedValuesOf("timeZone");
    } catch (e) {
      return;
    }
    if (!zones || !zones.length) return;

    var current = sel.getAttribute("data-current") || "";
    var detected = detectedZone();

    // Rebuild: an "Auto-detect" option, then one <optgroup> per region.
    sel.innerHTML = "";
    var auto = document.createElement("option");
    auto.value = "";
    auto.textContent = detected ? "Auto-detect (" + detected + ")" : "Auto-detect (browser)";
    if (!current) auto.selected = true;
    sel.appendChild(auto);

    var groups = {}; // region -> <optgroup>
    for (var i = 0; i < zones.length; i++) {
      var name = zones[i];
      var slash = name.indexOf("/");
      var region = slash >= 0 ? name.slice(0, slash) : name;
      var group = groups[region];
      if (!group) {
        group = document.createElement("optgroup");
        group.label = region;
        groups[region] = group;
        sel.appendChild(group);
      }
      var opt = document.createElement("option");
      opt.value = name;
      opt.textContent = (slash >= 0 ? name.slice(slash + 1) : name).replace(/_/g, " ");
      if (name === current) opt.selected = true;
      group.appendChild(opt);
    }

    // The saved zone may not be in the browser's list (older browser): keep it as
    // a selectable option so saving doesn't silently drop it.
    if (current && sel.value !== current) {
      var kept = document.createElement("option");
      kept.value = current;
      kept.textContent = current;
      kept.selected = true;
      sel.insertBefore(kept, sel.firstChild.nextSibling);
    }

    // Hint: when on auto-detect, show which zone the browser reports.
    var hint = sel.parentNode && sel.parentNode.querySelector("[data-tz-detected]");
    if (hint && detected) {
      hint.textContent = current ? "" : "Detected: " + detected;
    }
  }

  function init() {
    var pickers = document.querySelectorAll("select[data-tz-picker]");
    for (var i = 0; i < pickers.length; i++) buildPicker(pickers[i]);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
