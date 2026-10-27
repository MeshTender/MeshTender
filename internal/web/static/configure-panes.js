// configure-panes.js — keeps the Configure tab's open pane in the URL (?pane=),
// so a reload or a shared link opens the same one. Bootstrap switches the panes
// itself ([data-bs-toggle="tab"]); this only rewrites the address, in place, so
// the connection on the page is untouched.
(function () {
  "use strict";
  document.querySelectorAll("[data-pane]").forEach(function (tab) {
    tab.addEventListener("shown.bs.tab", function () {
      var url = new URL(window.location.href);
      url.searchParams.set("pane", String(tab.dataset.pane || ""));
      url.searchParams.delete("before"); // a history page belongs to History
      window.history.replaceState(null, "", url.toString());
    });
  });
})();
