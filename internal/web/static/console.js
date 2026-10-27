// The Configure tab's command line: the box you type commands into and the chips
// that fill it, sent through whichever connection configure.js has chosen
// (window.MeshConsole). Also the confirm/location banners, which follow the
// session's statuses. Completion and history live in console-complete.js.
(function () {
  const form = document.getElementById("cmdform");
  const input = document.getElementById("cmdinput");
  const sendBtn = document.getElementById("cmdsend");
  const hint = document.getElementById("cmdhint");
  if (!form || !input) return;
  const conn = () => window.MeshConsole;

  // Chips fill the input (even with nothing connected: it's how you browse them).
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
    const t = conn();
    if (t && t.send(input.value)) input.value = "";
    else if (!(t && t.ready)) hint.classList.add("text-danger");
  });

  // The input works offline (completion, chips); only sending needs a connection.
  document.addEventListener("mesh:ready", () => {
    sendBtn.disabled = false;
    hint.hidden = true;
    hint.classList.remove("text-danger");
    input.focus();
  });
  document.addEventListener("mesh:closed", () => {
    sendBtn.disabled = true;
    hint.hidden = false;
  });

  // "Read it from the repeater" (location banner): if nothing's connected yet,
  // connect first and read it once the session is ready.
  let pendingGetLocation = false;
  document.querySelectorAll("[data-fetch-location]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const t = conn();
      if (!t || !t.supported) return;
      if (t.ready) { t.getLocation(); return; }
      pendingGetLocation = true;
      document.dispatchEvent(new CustomEvent("mesh:connecting"));
      t.connect();
    });
  });
  document.addEventListener("mesh:ready", () => {
    if (pendingGetLocation) { pendingGetLocation = false; conn().getLocation(); }
  });
  document.addEventListener("mesh:closed", () => { pendingGetLocation = false; });

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
