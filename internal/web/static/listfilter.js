// Client-side list filtering and sorting for unpaginated in-app lists (my
// organizations, my repeaters, an org's members). Declarative and CSP-safe — no
// inline handlers.
//
// Markup contract, all within one [data-filter-root]:
//   - [data-filter-search]           an <input>; matches item [data-name] (substring, case-insensitive)
//   - [data-filter-key="foo"]        a <select>; item shown when its [data-foo] equals the selected value
//                                    (an empty value means "any")
//   - [data-filter-flag="bar"]       a checkbox; when checked, only items with [data-bar="1"] show
//   - [data-filter-item]             a list item (row/card/list-group-item); hidden when it doesn't match
//   - [data-filter-empty]            optional element shown only when nothing matches
//   - [data-filter-sort]             optional <select> reordering the items: an option's value names
//                                    an item attribute [data-sort-KEY], "KEY" ascending or "KEY:desc";
//                                    "" keeps the server's order. Numbers compare as numbers, and an
//                                    item without a value sorts last either way
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
    var sorter = root.querySelector("[data-filter-sort]");

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

    // sort reorders the items within their parent, by the chosen key; the
    // original order (as served) is the tiebreak and the "" choice.
    var served = items.slice();
    function sort() {
      var parts = String(sorter.value || "").split(":");
      var key = parts[0];
      var desc = parts[1] === "desc";
      var order = served.slice();
      if (key) {
        var val = function (item) { return item.getAttribute("data-sort-" + key) || ""; };
        order.sort(function (a, b) {
          var x = val(a), y = val(b);
          if (x === "" || y === "") return (x === "") - (y === "") || served.indexOf(a) - served.indexOf(b);
          var nx = Number(x), ny = Number(y);
          var c = isNaN(nx) || isNaN(ny) ? x.localeCompare(y) : nx - ny;
          return (desc ? -c : c) || served.indexOf(a) - served.indexOf(b);
        });
      }
      order.forEach(function (item) { item.parentNode.appendChild(item); });
    }

    if (sorter) {
      sorter.addEventListener("change", sort);
      sort();
    }
    if (search) search.addEventListener("input", apply);
    selects.forEach(function (s) { s.addEventListener("change", apply); });
    flags.forEach(function (f) { f.addEventListener("change", apply); });
    apply();
  }

  document.querySelectorAll("[data-filter-root]").forEach(setup);
})();
