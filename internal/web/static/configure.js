// configure.js — the Configure tab's connection bar.
//
// The tab talks to the repeater through one of the connections loaded on the
// page: usb-console.js (the repeater plugged into this computer) and
// air-console.js (a KISS modem, or an observer MeshTender sends through). This
// script owns the choice between them (the #transport select, remembered in
// localStorage), makes the chosen one window.MeshConsole, and runs the
// Connect/Disconnect button and the status beside it. Everything else on the
// tab — the command line (console.js), the organization configuration
// (console-config.js) — just uses window.MeshConsole, and hears about a switch
// from the "mesh:transport" event. The choice is locked while connecting or
// connected.
//
// The select's values are "usb", "air", or "observer:<public key>" for each
// observer the user can send through.
(function () {
  "use strict";

  var select = document.getElementById("transport");
  var button = document.getElementById("connect");
  var label = document.getElementById("connect-label");
  var status = document.getElementById("conn-status");
  var unsupported = document.getElementById("unsupported");
  var airOnly = Array.prototype.slice.call(document.querySelectorAll("[data-air-only]"));
  if (!select || !button || !window.MeshTransports) return;

  var KEY = "meshtender.transport";
  var OBSERVER = "observer:";
  var DEVICES = { usb: "repeater", air: "modem", observer: "observer" };
  var busy = false; // a connect is under way

  // chosen is the connection the selected option uses.
  function chosen() {
    var v = String(select.value || "");
    return v.indexOf(OBSERVER) === 0 ? "observer" : v;
  }
  function device() { return DEVICES[chosen()] || "repeater"; }

  function render() {
    var t = window.MeshConsole;
    var ready = !!(t && t.ready);
    select.disabled = ready || busy;
    airOnly.forEach(function (el) { el.hidden = chosen() === "usb"; });
    if (unsupported) unsupported.hidden = !!(t && t.supported);
    button.disabled = !(t && t.supported) || (busy && !ready);
    button.classList.toggle("btn-primary", !ready);
    label.textContent = ready ? "Disconnect" : busy ? "Connecting…" : "Connect " + device();
    status.textContent = ready
      ? (chosen() === "usb" ? "Connected over USB" : "Connected through the " + device())
      : busy ? "Connecting…" : "Not connected";
    status.classList.toggle("text-green", ready);
  }

  // pick makes the selected option's connection window.MeshConsole.
  function pick() {
    var t = window.MeshTransports[chosen()];
    if (!t) return null;
    if (chosen() === "observer") t.observer = select.value.slice(OBSERVER.length);
    window.MeshConsole = t;
    return t;
  }

  // Restore the remembered choice, unless the page preselected an observer
  // (?via=). Only an option still on the list is restored: an observer that's
  // gone falls back to the first option.
  if (!select.hasAttribute("data-preselected")) {
    var saved = null;
    try { saved = localStorage.getItem(KEY); } catch (_) { /* private mode */ }
    var options = Array.prototype.slice.call(select.options);
    if (saved && options.some(function (o) { return o.value === saved; })) select.value = saved;
  }
  pick();
  render();

  select.addEventListener("change", function () {
    var t = pick();
    if (!t) return;
    try { localStorage.setItem(KEY, select.value); } catch (_) { /* private mode */ }
    render();
    document.dispatchEvent(new CustomEvent("mesh:transport", { detail: { name: t.name } }));
  });

  button.addEventListener("click", function () {
    var t = window.MeshConsole;
    if (!t) return;
    if (t.ready) { t.disconnect(); return; }
    busy = true;
    render();
    t.connect();
  });
  // Something else on the page (the location banner) may start a connection.
  document.addEventListener("mesh:connecting", function () { busy = true; render(); });
  document.addEventListener("mesh:ready", function () { busy = false; render(); });
  document.addEventListener("mesh:closed", function () { busy = false; render(); });
})();
