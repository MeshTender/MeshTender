// Interactive repeater console. Bridges a WebSerial-connected KISS modem to the
// server over a WebSocket (binary = raw KISS bytes), and exchanges JSON control
// messages for commands and status. Mirrors serial.js's bridge, plus a CLI.

(function () {
  const connectBtn = document.getElementById("connect");
  const log = document.getElementById("log");
  const form = document.getElementById("cmdform");
  const input = document.getElementById("cmdinput");
  const sendBtn = document.getElementById("cmdsend");

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

  let port, ws, reader, writer, keepReading = false, ready = false;

  function setReady(on) {
    ready = on;
    form.hidden = !on; // hide the command box until the modem is connected
    if (on) input.focus();
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
    const text = input.value.trim();
    if (!text || !ready || !ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify({ type: "cmd", text }));
    input.value = "";
  });

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
      addLog("info", "Serial port open. Connecting to server…");

      ws = new WebSocket(window.MESHTENDER_WS);
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
          if (msg.state === "info" && /ready for commands/i.test(msg.message || "")) setReady(true);
          return;
        }
        try { await writer.write(new Uint8Array(ev.data)); }
        catch (e) { addLog("error", "Serial write error: " + e.message); }
      };
      ws.onclose = () => { addLog("info", "Disconnected."); cleanup(); };
      ws.onerror = () => { addLog("error", "WebSocket error."); };
    } catch (e) {
      addLog("error", e.message);
      await cleanup();
    }
  });
})();
