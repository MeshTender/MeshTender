// Interactive repeater console: a CLI on top of the modem bridge
// (mesh-bridge.js), which pipes the user's KISS modem to the server and back.
//
// It also exposes a small API (window.MeshConsole) and document events so the
// separate "apply org configuration" script (console-config.js) can send commands
// and react to session state without owning the socket:
//   window.MeshConsole.ready          — is the modem connected & the session live?
//   window.MeshConsole.supported       — can this browser reach a serial device at all?
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
  const sendBtn = document.getElementById("cmdsend");
  const hint = document.getElementById("cmdhint");

  let bridge = null;

  // The shared API other scripts use. `ready` is the single source of truth for
  // whether a command can be sent right now.
  const api = {
    ready: false,
    supported: !!(window.MeshBridge && window.MeshBridge.supported),
    connect() {}, // replaced with the real routine below when a serial transport is present
    send(text) {
      const t = String(text || "").trim();
      if (!t || !api.ready || !bridge) return false;
      if (!bridge.send({ type: "cmd", text: t })) return false;
      window.dispatchEvent(new CustomEvent("console:sent", { detail: { text: t } }));
      return true;
    },
    getLocation() {
      if (!api.ready || !bridge) return false;
      return bridge.send({ type: "getloc" });
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

  // Chips fill the input (even with no modem here: it's how you browse them).
  document.querySelectorAll(".chip").forEach((c) => {
    c.addEventListener("click", () => {
      // Strip the "<arg>" placeholder so the user can type the value.
      input.value = c.dataset.template.replace(/\s*<.*>$/, " ").trimEnd() +
        (/<.*>/.test(c.dataset.template) ? " " : "");
      input.focus();
      input.dispatchEvent(new Event("input")); // show its arguments
    });
  });

  form.addEventListener("submit", (e) => {
    e.preventDefault();
    if (api.send(input.value)) input.value = "";
    else if (!api.ready) hint.classList.add("text-danger");
  });

  if (!api.supported) {
    const unsupportedEl = document.getElementById("unsupported");
    if (unsupportedEl) unsupportedEl.hidden = false;
    if (connectBtn) connectBtn.disabled = true;
    document.querySelectorAll("[data-fetch-location]").forEach((b) => { b.disabled = true; });
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
    // The input works offline (completion, chips); only sending needs the modem.
    sendBtn.disabled = !on;
    hint.hidden = on;
    hint.classList.remove("text-danger");
    if (on) {
      input.focus();
      emit("mesh:ready", null);
    }
  }

  async function connect() {
    if (api.ready || connectBtn.disabled) return; // already connected / connecting
    connectBtn.disabled = true;
    log.innerHTML = "";
    try {
      bridge = await MeshBridge.open(wsURL(), {
        onLog: addLog,
        onStatus: (state, message) => {
          addLog(state, message);
          emit("mesh:status", { state: state, message: message });
          if (state === "ready") setReady(true);
        },
        onClose: () => {
          bridge = null;
          setReady(false);
          connectBtn.disabled = false;
          emit("mesh:closed", null);
        },
      });
    } catch (_) {
      // MeshBridge reported the error to the log and cleaned up.
    }
  }

  connectBtn.addEventListener("click", connect);
  api.connect = connect; // let the config modal (console-config.js) connect too

  // "Fetch location" banner button: query the repeater's coordinates. If the
  // modem isn't connected yet, connect first and fetch once the session is ready.
  let pendingGetLocation = false;
  document.querySelectorAll("[data-fetch-location]").forEach((btn) => {
    btn.addEventListener("click", () => {
      if (api.ready) { api.getLocation(); return; }
      pendingGetLocation = true;
      connect();
    });
  });
  document.addEventListener("mesh:ready", () => {
    if (pendingGetLocation) { pendingGetLocation = false; api.getLocation(); }
  });

  // Reflect confirm/location progress live: once the server reports the repeater
  // confirmed or its location stored, the matching page-load banner is stale.
  document.addEventListener("mesh:status", (ev) => {
    const state = ev.detail && ev.detail.state;
    if (state === "confirmed") {
      // Admin login just confirmed the repeater: drop the "not confirmed" prompt
      // and reveal the location prompt (rendered hidden when no location is
      // stored). If a location is already known the prompt isn't in the DOM.
      const confirmBanner = document.querySelector('[data-testid="confirm-banner"]');
      if (confirmBanner) confirmBanner.hidden = true;
      const locationBanner = document.querySelector('[data-testid="location-banner"]');
      if (locationBanner) locationBanner.hidden = false;
    }
    if (state === "location") {
      const b = document.querySelector('[data-testid="location-banner"]');
      if (b) b.hidden = true;
    }
  });
})();
