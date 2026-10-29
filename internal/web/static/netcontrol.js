// netcontrol.js — net control's rail on a live net session. The modem panel connects
// the user's KISS modem through mesh-bridge.js to the session's modem socket
// (the path is the panel's data-ws-path) and drives the announcement buttons;
// everything on the air happens server-side.
(function () {
  "use strict";

  // "Add a check-in": Enter in the member search shouldn't submit the form
  // without a member picked (each result's Add button submits it).
  var search = document.querySelector("[data-testid='add-search']");
  if (search) {
    search.addEventListener("keydown", function (e) {
      if (e.key === "Enter") e.preventDefault();
    });
  }
  // The form posts with htmx (a page load would drop the modem), so it isn't
  // cleared by navigating: once a member is added, start the search over.
  var addForm = document.querySelector("[data-nc-add]");
  if (addForm && search) {
    addForm.addEventListener("htmx:afterRequest", function (e) {
      if (e.target !== addForm || !e.detail || !e.detail.successful) return;
      addForm.reset();
      if (window.htmx) window.htmx.trigger(search, "search");
    });
  }

  var MAX_LOG = 100; // the modem log is a scrolling tail, not a history
  var panel = document.querySelector("[data-net-modem]");
  if (!panel) return;
  var connectBtn = panel.querySelector("[data-nc-connect]");
  var log = panel.querySelector("[data-nc-log]");
  var controls = panel.querySelector("[data-nc-controls]");
  var sayForm = panel.querySelector("[data-nc-say]");
  var sayInput = sayForm ? sayForm.querySelector("input") : null;
  var stateBadge = panel.querySelector("[data-nc-state]");
  var bridge = null;

  // Net control's actions swap the session's live part with htmx. A failed one
  // (server error, lost connection, expired sign-in) changes nothing on the
  // page, so say so in the modem log (absent until the org's radio is set).
  // The live part's own 10-second refresh is left out: offline, it would fill
  // the log.
  function actionFailed(e) {
    var elt = e.detail && e.detail.elt;
    if (!log || !elt || elt.id === "session-live" || elt.getAttribute("hx-target") !== "#session-live") return;
    addLog("error", "That didn't go through. Check your connection and try again.");
  }
  document.body.addEventListener("htmx:responseError", actionFailed);
  document.body.addEventListener("htmx:sendError", actionFailed);

  if (!window.MeshBridge || !window.MeshBridge.supported) {
    var unsupported = panel.querySelector("[data-nc-unsupported]");
    if (unsupported) unsupported.hidden = false;
    if (connectBtn) connectBtn.disabled = true;
    return;
  }

  function addLog(state, message) {
    var li = document.createElement("li");
    li.className = "ev ev-" + String(state);
    li.textContent = String(message);
    log.appendChild(li);
    while (log.children.length > MAX_LOG) log.removeChild(log.firstChild);
    log.scrollTop = log.scrollHeight;
  }

  function setReady(on) {
    controls.hidden = !on;
    connectBtn.hidden = on;
    if (stateBadge) {
      stateBadge.textContent = on ? "Connected" : "Not connected";
      stateBadge.className = "badge " + (on ? "bg-green-lt" : "bg-secondary-lt");
    }
  }

  function send(obj) {
    if (bridge) bridge.send(obj);
  }

  connectBtn.addEventListener("click", async function () {
    connectBtn.disabled = true;
    var url = (location.protocol === "https:" ? "wss://" : "ws://") + location.host + String(panel.dataset.wsPath || "");
    try {
      bridge = await window.MeshBridge.open(url, {
        onLog: addLog,
        onStatus: function (state, message) {
          addLog(state, message);
          if (state === "ready") setReady(true);
        },
        onClose: function () {
          bridge = null;
          setReady(false);
          connectBtn.disabled = false;
        },
      });
    } catch (_) {
      connectBtn.disabled = false; // MeshBridge logged why
    }
  });

  panel.querySelectorAll("[data-nc-send]").forEach(function (btn) {
    btn.addEventListener("click", function () { send({ type: String(btn.dataset.ncSend) }); });
  });

  if (sayForm && sayInput) {
    sayForm.addEventListener("submit", function (e) {
      e.preventDefault();
      var text = String(sayInput.value || "").trim();
      if (!text) return;
      send({ type: "say", text: text });
      sayInput.value = "";
    });
  }
})();
