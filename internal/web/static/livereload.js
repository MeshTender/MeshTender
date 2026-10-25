// livereload.js — dev only (loaded when MESHTENDER_LIVE_RELOAD is set). Polls the
// server's boot ID and reloads the page once a restarted server answers with a
// new one, so a rebuild from the dev watcher appears without a manual refresh.
// While the server is down the poll just fails and tries again. A hidden tab
// stops polling and checks once as soon as it's shown again.
(function () {
  "use strict";

  var url = "/_dev/live-reload";
  var interval = 1000;
  var bootID = null;
  var timer = null;
  var inFlight = false; // one poll at a time, or a tab shown mid-poll would start a second loop

  function schedule() {
    clearTimeout(timer);
    if (!document.hidden) timer = setTimeout(poll, interval);
  }

  function poll() {
    if (inFlight) return;
    inFlight = true;
    fetch(url, { cache: "no-store" })
      .then(function (res) {
        if (!res.ok) throw new Error("live reload: " + res.status);
        return res.text();
      })
      .then(function (id) {
        inFlight = false;
        if (bootID === null) {
          bootID = id;
        } else if (id !== bootID) {
          location.reload();
          return;
        }
        schedule();
      })
      .catch(function () {
        inFlight = false;
        schedule(); // server restarting (or gone): keep trying
      });
  }

  document.addEventListener("visibilitychange", function () {
    if (!document.hidden) poll();
  });
  poll();
})();
