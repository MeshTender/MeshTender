// Client-side list filtering for unpaginated in-app lists (my organizations, my
// repeaters). Declarative and CSP-safe — no inline handlers.
//
// Markup contract, all within one [data-filter-root]:
//   - [data-filter-search]           an <input>; matches item [data-name] (substring, case-insensitive)
//   - [data-filter-key="foo"]        a <select>; item shown when its [data-foo] equals the selected value
//                                    (an empty value means "any")
//   - [data-filter-flag="bar"]       a checkbox; when checked, only items with [data-bar="1"] show
//   - [data-filter-item]             a list item (row/card/list-group-item); hidden when it doesn't match
//   - [data-filter-empty]            optional element shown only when nothing matches
//
// Filters combine with AND. Everything is coerced/guarded since attributes and
// control values come straight from the DOM.
(function () {
  "use strict";

  function setup(root) {
    var search = root.querySelector("[data-filter-search]");
    var selects = Array.prototype.slice.call(root.querySelectorAll("[data-filter-key]"));
    var flags = Array.prototype.slice.call(root.querySelectorAll("[data-filter-flag]"));
    var items = Array.prototype.slice.call(root.querySelectorAll("[data-filter-item]"));
    var empty = root.querySelector("[data-filter-empty]");

    function matches(item) {
      var q = search ? String(search.value || "").trim().toLowerCase() : "";
      if (q && String(item.getAttribute("data-name") || "").toLowerCase().indexOf(q) === -1) {
        return false;
      }
      for (var i = 0; i < selects.length; i++) {
        var key = selects[i].getAttribute("data-filter-key");
        var want = selects[i].value;
        if (want && item.getAttribute("data-" + key) !== want) return false;
      }
      for (var j = 0; j < flags.length; j++) {
        if (flags[j].checked && item.getAttribute("data-" + flags[j].getAttribute("data-filter-flag")) !== "1") {
          return false;
        }
      }
      return true;
    }

    function apply() {
      var visible = 0;
      items.forEach(function (item) {
        var show = matches(item);
        item.hidden = !show;
        if (show) visible++;
      });
      if (empty) empty.hidden = visible !== 0;
    }

    if (search) search.addEventListener("input", apply);
    selects.forEach(function (s) { s.addEventListener("change", apply); });
    flags.forEach(function (f) { f.addEventListener("change", apply); });
    apply();
  }

  document.querySelectorAll("[data-filter-root]").forEach(setup);
})();
