// Interactive repeater console. Bridges a WebSerial-connected KISS modem to the
// server over a WebSocket (binary = raw KISS bytes), and exchanges JSON control
// messages for commands and status. Mirrors serial.js's bridge, plus a CLI.
//
// It also exposes a small API (window.MeshConsole) and document events so the
// separate "apply org configuration" script (console-config.js) can send commands
// and react to session state without owning the socket:
//   window.MeshConsole.ready          — is the modem connected & the session live?
//   window.MeshConsole.supported       — does this browser have WebSerial at all?
//   window.MeshConsole.connect()       — open the modem + session (needs a user gesture)
//   window.MeshConsole.send(text)      — send a CLI command; returns false if not ready
//   window.MeshConsole.getLocation()   — ask the server to fetch the repeater's coords
//   document "mesh:ready"  event       — fired when the session becomes ready
//   document "mesh:closed" event       — fired when the socket closes
//   document "mesh:status" event       — detail {state, message} for every server status

(function () {
  const connectBtn = document.getElementById("connect");
  const log = document.getElementById("log");
  const form = document.getElementById("cmdform");
  const input = document.getElementById("cmdinput");

  let port, ws, reader, writer, keepReading = false;

  // The shared API other scripts use. `ready` is the single source of truth for
  // whether a command can be sent right now.
  const api = {
    ready: false,
    supported: "serial" in navigator,
    connect() {}, // replaced with the real routine below when WebSerial is present
    send(text) {
      const t = String(text || "").trim();
      if (!t || !api.ready || !ws || ws.readyState !== WebSocket.OPEN) return false;
      ws.send(JSON.stringify({ type: "cmd", text: t }));
      return true;
    },
    getLocation() {
      if (!api.ready || !ws || ws.readyState !== WebSocket.OPEN) return false;
      ws.send(JSON.stringify({ type: "getloc" }));
      return true;
    },
  };
  window.MeshConsole = api;

  function emit(name, detail) {
    document.dispatchEvent(new CustomEvent(name, { detail: detail }));
  }

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
    const unsupportedEl = document.getElementById("unsupported");
    if (unsupportedEl) unsupportedEl.hidden = false;
    if (connectBtn) connectBtn.disabled = true;
    return; // MeshConsole stays defined but never becomes ready (no modem here)
  }

  function addLog(state, message) {
    const li = document.createElement("li");
    li.className = "ev ev-" + state;
    li.textContent = message;
    log.appendChild(li);
    log.scrollTop = log.scrollHeight;
  }

  function setReady(on) {
    api.ready = on;
    form.hidden = !on; // hide the command box until the modem is connected
    if (on) {
      input.focus();
      emit("mesh:ready", null);
    }
  }

  async function cleanup() {
    keepReading = false;
    setReady(false);
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
        if (value && value.length && ws.readyState === WebSocket.OPEN) ws.send(value);
      }
    } catch (e) {
      addLog("error", "Serial read error: " + e.message);
    }
  }

  // Chips fill the input.
  document.querySelectorAll(".chip").forEach((c) => {
    c.addEventListener("click", () => {
      // Strip the "<arg>" placeholder so the user can type the value.
      input.value = c.dataset.template.replace(/\s*<.*>$/, " ").trimEnd() +
        (/<.*>/.test(c.dataset.template) ? " " : "");
      input.focus();
    });
  });

  form.addEventListener("submit", (e) => {
    e.preventDefault();
    if (api.send(input.value)) input.value = "";
  });

  async function connect() {
    if (api.ready || connectBtn.disabled) return; // already connected / connecting
    connectBtn.disabled = true;
    log.innerHTML = "";
    try {
      addLog("info", "Requesting serial port…");
      port = await navigator.serial.requestPort();
      await port.open({ baudRate: 115200 });
      writer = port.writable.getWriter();
      reader = port.readable.getReader();
      keepReading = true;
      addLog("info", "Serial port open. Connecting to server…");

      ws = new WebSocket(wsURL());
      ws.binaryType = "arraybuffer";

      ws.onopen = () => {
        pumpSerialToWS();
        ws.send(JSON.stringify({ type: "ready" }));
      };
      ws.onmessage = async (ev) => {
        if (typeof ev.data === "string") {
          let msg = {};
          try { msg = JSON.parse(ev.data); } catch (_) {}
          addLog(msg.state || "info", msg.message || ev.data);
          emit("mesh:status", { state: msg.state || "info", message: msg.message || "" });
          if (msg.state === "info" && /ready for commands/i.test(msg.message || "")) setReady(true);
          return;
        }
        try { await writer.write(new Uint8Array(ev.data)); }
        catch (e) { addLog("error", "Serial write error: " + e.message); }
      };
      ws.onclose = () => { addLog("info", "Disconnected."); emit("mesh:closed", null); cleanup(); };
      ws.onerror = () => { addLog("error", "WebSocket error."); };
    } catch (e) {
      addLog("error", e.message);
      await cleanup();
    }
  }

  connectBtn.addEventListener("click", connect);
  api.connect = connect; // let the config modal (console-config.js) connect too
})();
