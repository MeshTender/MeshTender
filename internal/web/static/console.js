// The repeater console page: a CLI over the air-console.js connection
// (window.MeshConsole). This file is only the page: the Connect button, the
// command box and its chips, and the confirm/location banners, which follow the
// session's statuses. Completion and history live in console-complete.js.
(function () {
  const api = window.MeshConsole;
  const connectBtn = document.getElementById("connect");
  const form = document.getElementById("cmdform");
  const input = document.getElementById("cmdinput");
  const sendBtn = document.getElementById("cmdsend");
  const hint = document.getElementById("cmdhint");
  if (!api || !connectBtn || !form) return;

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
    connectBtn.disabled = true;
    document.querySelectorAll("[data-fetch-location]").forEach((b) => { b.disabled = true; });
    return; // never becomes ready: no modem here
  }

  connectBtn.addEventListener("click", () => {
    connectBtn.disabled = true;
    api.connect();
  });

  document.addEventListener("mesh:ready", () => {
    // The input works offline (completion, chips); only sending needs the modem.
    sendBtn.disabled = false;
    hint.hidden = true;
    hint.classList.remove("text-danger");
    input.focus();
  });
  document.addEventListener("mesh:closed", () => {
    sendBtn.disabled = true;
    hint.hidden = false;
    connectBtn.disabled = false;
  });

  // "Fetch location" banner button: query the repeater's coordinates. If the
  // modem isn't connected yet, connect first and fetch once the session is ready.
  let pendingGetLocation = false;
  document.querySelectorAll("[data-fetch-location]").forEach((btn) => {
    btn.addEventListener("click", () => {
      if (api.ready) { api.getLocation(); return; }
      pendingGetLocation = true;
      connectBtn.disabled = true;
      api.connect();
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
