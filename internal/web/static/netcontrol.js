// netcontrol.js — net control's modem panel on a live net session. It connects
// the user's KISS modem through mesh-bridge.js to the session's modem socket
// (the path is the panel's data-ws-path) and drives the announcement buttons;
// everything on the air happens server-side.
(function () {
  "use strict";
  var panel = document.querySelector("[data-net-modem]");
  if (!panel) return;
  var connectBtn = panel.querySelector("[data-nc-connect]");
  var log = panel.querySelector("[data-nc-log]");
  var controls = panel.querySelector("[data-nc-controls]");
  var sayForm = panel.querySelector("[data-nc-say]");
  var sayInput = sayForm ? sayForm.querySelector("input") : null;
  var bridge = null;

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
    log.scrollTop = log.scrollHeight;
  }

  function setReady(on) {
    controls.hidden = !on;
    connectBtn.hidden = on;
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
