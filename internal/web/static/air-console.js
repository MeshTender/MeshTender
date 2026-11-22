// air-console.js — the over-the-air connections to a repeater, both piped to
// MeshTender by mesh-bridge.js, which logs in and relays CLI commands over LoRa
// (the server checks each one against the user's permissions):
//
//   "air"      — the user's KISS modem, on a serial port
//   "observer" — an observer MeshTender sends through; no serial port. Its
//                .observer is the observer's public key, set by configure.js.
//
// usb-console.js is the other connection, to a repeater plugged into this
// computer. All have the same shape:
//
//   .ready        — connected, and commands can be sent
//   .supported    — can this browser use it at all?
//   .connect()    — open the port (if any) and the session (needs a user gesture)
//   .disconnect() — close them
//   .send(text)   — send a CLI command; false if it can't be sent now
//   .getLocation()— have the repeater's coordinates read and saved
//   document events: "mesh:ready", "mesh:closed", "mesh:status" {state, message}
//
// Each registers itself in window.MeshTransports under its name; configure.js
// picks which is window.MeshConsole.
//
// Activity goes to #log. window.MESHTENDER_WS is the session's WebSocket URL.
// The route chosen in the page's [data-route] radios goes with it: the region
// (picked in #scope-region, or typed in #scope when that's "Another region…"
// or absent) floods in that region, the #path field routes directly instead
// of by flood (configure.js keeps it to one or the other).
(function () {
  "use strict";

  var log = document.getElementById("log");

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

  function withParam(url, key, value) {
    return url + (url.indexOf("?") === -1 ? "?" : "&") + key + "=" + encodeURIComponent(value);
  }

  // transport builds one over-the-air connection. With serial false it opens
  // no port, and the session goes through the observer in api.observer.
  function transport(name, serial) {
    var bridge = null;
    var connecting = false;

    var api = {
      name: name,
      ready: false,
      supported: !!window.MeshBridge && (!serial || !!window.MeshBridge.supported),
      observer: "",
      connect: connect,
      disconnect: function () {
        if (bridge) bridge.close();
      },
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

    function wsURL() {
      var url = String(window.MESHTENDER_WS || "");
      if (!serial) url = withParam(url, "via", String(api.observer || ""));
      var chosen = document.querySelector("[data-route]:checked");
      var kind = chosen ? chosen.value : "";
      var value = "";
      if (kind === "scope") {
        var list = document.getElementById("scope-region");
        var typed = document.getElementById("scope");
        value = list && list.value ? list.value : (typed ? typed.value : "");
      } else if (kind === "path") {
        var path = document.getElementById("path");
        value = path ? path.value : "";
      }
      if (String(value).trim()) url = withParam(url, kind, String(value).trim());
      return url;
    }

    async function connect() {
      if (api.ready || bridge || connecting || !api.supported) return;
      if (!serial && !api.observer) return;
      connecting = true;
      try {
        bridge = await MeshBridge.open(wsURL(), {
          serial: serial,
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

    return api;
  }

  window.MeshTransports = window.MeshTransports || {};
  window.MeshTransports.air = transport("air", true);
  window.MeshTransports.observer = transport("observer", false);
})();
