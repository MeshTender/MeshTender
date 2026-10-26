// air-console.js — the over-the-air connection to a repeater: the user's KISS
// modem, piped to MeshTender by mesh-bridge.js, which logs in and relays CLI
// commands over LoRa (the server checks each one against the user's
// permissions). usb-console.js is the other connection, to a repeater plugged
// into this computer; both have the same shape:
//
//   .ready        — connected, and commands can be sent
//   .supported    — can this browser reach a serial device at all?
//   .connect()    — open the port and the session (needs a user gesture)
//   .send(text)   — send a CLI command; false if it can't be sent now
//   .getLocation()— have the repeater's coordinates read and saved
//   document events: "mesh:ready", "mesh:closed", "mesh:status" {state, message}
//
// Each registers itself in window.MeshTransports under its name ("air", "usb")
// and becomes window.MeshConsole if nothing else has: the console page loads only
// this one, and the Configuration tab loads both and picks.
//
// Activity goes to #log. window.MESHTENDER_WS is the session's WebSocket URL; a
// #path field, if the page has one, routes it directly instead of by flood.
(function () {
  "use strict";

  var log = document.getElementById("log");
  var bridge = null;
  var connecting = false;

  var api = {
    name: "air",
    ready: false,
    supported: !!(window.MeshBridge && window.MeshBridge.supported),
    connect: connect,
    send: function (text) {
      var t = String(text || "").trim();
      if (!t || !api.ready || !bridge) return false;
      if (!bridge.send({ type: "cmd", text: t })) return false;
      window.dispatchEvent(new CustomEvent("console:sent", { detail: { text: t } }));
      return true;
    },
    getLocation: function () {
      if (!api.ready || !bridge) return false;
      return bridge.send({ type: "getloc" });
    },
  };
  window.MeshTransports = window.MeshTransports || {};
  window.MeshTransports.air = api;
  if (!window.MeshConsole) window.MeshConsole = api;

  function emit(name, detail) {
    document.dispatchEvent(new CustomEvent(name, { detail: detail }));
  }

  function addLog(state, message) {
    if (!log) return;
    var li = document.createElement("li");
    li.className = "ev ev-" + state;
    li.textContent = message;
    log.appendChild(li);
    log.scrollTop = log.scrollHeight;
  }

  function wsURL() {
    var url = window.MESHTENDER_WS;
    var el = document.getElementById("path");
    if (el && el.value.trim()) {
      url += (url.indexOf("?") === -1 ? "?" : "&") + "path=" + encodeURIComponent(el.value.trim());
    }
    return url;
  }

  async function connect() {
    if (api.ready || bridge || connecting || !api.supported) return;
    connecting = true;
    if (log) log.textContent = "";
    try {
      bridge = await MeshBridge.open(wsURL(), {
        onLog: addLog,
        onStatus: function (state, message) {
          addLog(state, message);
          emit("mesh:status", { state: state, message: message });
          if (state === "ready") {
            api.ready = true;
            emit("mesh:ready", null);
          }
        },
        onClose: function () {
          bridge = null;
          connecting = false;
          api.ready = false;
          emit("mesh:closed", null);
        },
      });
    } catch (_) {
      // MeshBridge logged the error and closed (onClose ran).
    }
    connecting = false;
  }
})();
