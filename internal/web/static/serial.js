// Bridges a WebSerial-connected MeshCore KISS modem to the MeshTender server
// over a WebSocket. Binary WS messages carry raw KISS serial bytes; text WS
// messages carry JSON status updates from the server.

(function () {
  const connectBtn = document.getElementById("connect");
  const log = document.getElementById("log");

  // wsURL appends the optional user-entered path (#path) to the base ws URL so
  // the server routes login/commands directly (with flood fallback).
  function wsURL() {
    let url = window.MESHTENDER_WS;
    const el = document.getElementById("path");
    if (el && el.value.trim()) {
      url += (url.indexOf("?") === -1 ? "?" : "&") + "path=" + encodeURIComponent(el.value.trim());
    }
    return url;
  }

  if (!("serial" in navigator)) {
    document.getElementById("unsupported").hidden = false;
    connectBtn.disabled = true;
    return;
  }

  function addLog(state, message) {
    const li = document.createElement("li");
    li.className = "ev ev-" + state;
    li.textContent = message;
    log.appendChild(li);
    log.scrollTop = log.scrollHeight;
  }

  let port, ws, reader, writer, keepReading = false;

  async function cleanup() {
    keepReading = false;
    try { if (reader) await reader.cancel(); } catch (_) {}
    try { if (writer) writer.releaseLock(); } catch (_) {}
    try { if (port) await port.close(); } catch (_) {}
    try { if (ws && ws.readyState === WebSocket.OPEN) ws.close(); } catch (_) {}
    connectBtn.disabled = false;
  }

  async function pumpSerialToWS() {
    try {
      while (keepReading) {
        const { value, done } = await reader.read();
        if (done) break;
        if (value && value.length && ws.readyState === WebSocket.OPEN) {
          ws.send(value); // Uint8Array -> binary frame
        }
      }
    } catch (e) {
      addLog("error", "Serial read error: " + e.message);
    }
  }

  connectBtn.addEventListener("click", async () => {
    connectBtn.disabled = true;
    log.innerHTML = "";
    try {
      addLog("info", "Requesting serial port…");
      port = await navigator.serial.requestPort();
      await port.open({ baudRate: 115200 });
      writer = port.writable.getWriter();
      reader = port.readable.getReader();
      keepReading = true;
      addLog("info", "Serial port open (115200 8N1). Connecting to server…");

      ws = new WebSocket(wsURL());
      ws.binaryType = "arraybuffer";

      ws.onopen = () => {
        pumpSerialToWS();
        ws.send(JSON.stringify({ type: "ready" }));
        addLog("info", "Connected. Confirming…");
      };

      ws.onmessage = async (ev) => {
        if (typeof ev.data === "string") {
          let msg = {};
          try { msg = JSON.parse(ev.data); } catch (_) {}
          addLog(msg.state || "info", msg.message || ev.data);
          // The server keeps the session open after login to fetch the
          // location, then closes it when done (handled by ws.onclose). Don't
          // close proactively here, or the location fetch would be cut off.
          return;
        }
        // Binary from server -> write raw bytes to the modem.
        try {
          await writer.write(new Uint8Array(ev.data));
        } catch (e) {
          addLog("error", "Serial write error: " + e.message);
        }
      };

      ws.onclose = () => { addLog("info", "Disconnected."); cleanup(); };
      ws.onerror = () => { addLog("error", "WebSocket error."); };
    } catch (e) {
      addLog("error", e.message);
      await cleanup();
    }
  });
})();
