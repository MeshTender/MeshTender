// mesh-bridge.js — connects a MeshCore KISS modem on this computer (WebSerial,
// or WebUSB via serial-port.js) to a MeshTender modem WebSocket, as a byte pipe:
// everything the modem sends goes to the server unchanged, and binary messages
// from the server go to the modem unchanged. KISS framing and every MeshCore
// packet are handled server-side. Text messages are JSON: the page sends small
// control messages, the server sends {state, message} statuses. Shared by the
// repeater console and Configuration tab (air-console.js) and the net control
// session (netcontrol.js).
//
//   MeshBridge.supported                      — can this browser reach a serial device?
//   MeshBridge.open(url, {onStatus, onLog, onClose}) → Promise<bridge>
//       onStatus(state, message) — every server status
//       onLog(state, message)    — local progress and errors, for the page's log
//       onClose()                — the session ended (either side)
//   bridge.send(obj)  — send a JSON control message; false if the socket isn't open
//   bridge.close()    — close the session and release the port
// Opening needs a user gesture (the browser's port picker).
(function () {
  "use strict";

  function open(url, handlers) {
    var h = handlers || {};
    var onStatus = h.onStatus || function () {};
    var onLog = h.onLog || function () {};
    var onClose = h.onClose || function () {};
    var port, ws, reader, writer;
    var reading = false;
    var closed = false;

    async function cleanup() {
      if (closed) return;
      closed = true;
      reading = false;
      try { if (reader) await reader.cancel(); } catch (_) {}
      try { if (writer) writer.releaseLock(); } catch (_) {}
      try { if (port) await port.close(); } catch (_) {}
      try { if (ws && ws.readyState === WebSocket.OPEN) ws.close(); } catch (_) {}
      onClose();
    }

    async function pumpSerialToWS() {
      try {
        while (reading) {
          var r = await reader.read();
          if (r.done) break;
          if (r.value && r.value.length && ws.readyState === WebSocket.OPEN) ws.send(r.value);
        }
      } catch (e) {
        onLog("error", "Serial read error: " + e.message);
      }
    }

    var bridge = {
      send: function (obj) {
        if (!ws || ws.readyState !== WebSocket.OPEN) return false;
        ws.send(JSON.stringify(obj));
        return true;
      },
      close: cleanup,
    };

    return (async function () {
      try {
        onLog("info", "Requesting serial port…");
        port = await window.MeshSerial.requestPort();
        await port.open({ baudRate: 115200 });
        writer = port.writable.getWriter();
        reader = port.readable.getReader();
        reading = true;
        onLog("info", "Serial port open. Connecting to server…");

        ws = new WebSocket(String(url));
        ws.binaryType = "arraybuffer";
        ws.onopen = function () {
          pumpSerialToWS();
          ws.send(JSON.stringify({ type: "ready" }));
        };
        ws.onmessage = async function (ev) {
          if (typeof ev.data === "string") {
            var msg = {};
            try { msg = JSON.parse(ev.data); } catch (_) {}
            onStatus(String(msg.state || "info"), String(msg.message || ev.data));
            return;
          }
          try { await writer.write(new Uint8Array(ev.data)); }
          catch (e) { onLog("error", "Serial write error: " + e.message); }
        };
        ws.onclose = function () { onLog("info", "Disconnected."); cleanup(); };
        ws.onerror = function () { onLog("error", "WebSocket error."); };
        return bridge;
      } catch (e) {
        onLog("error", e.message);
        await cleanup();
        throw e;
      }
    })();
  }

  window.MeshBridge = {
    supported: !!(window.MeshSerial && window.MeshSerial.supported),
    open: open,
  };
})();
