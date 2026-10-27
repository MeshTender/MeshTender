// configure.js — the Configure tab's connection bar.
//
// The tab talks to the repeater through one of two connections, both loaded on
// the page: usb-console.js (the repeater plugged into this computer) and
// air-console.js (a KISS modem, over the air). This script owns the choice
// between them ([data-transport] radios, remembered in localStorage), makes the
// chosen one window.MeshConsole, and runs the Connect/Disconnect button and the
// status beside it. Everything else on the tab — the command line (console.js),
// the organization configuration (console-config.js) — just uses
// window.MeshConsole, and hears about a switch from the "mesh:transport" event.
// The choice is locked while connecting or connected.
(function () {
  "use strict";

  var radios = Array.prototype.slice.call(document.querySelectorAll("[data-transport]"));
  var button = document.getElementById("connect");
  var label = document.getElementById("connect-label");
  var status = document.getElementById("conn-status");
  var unsupported = document.getElementById("unsupported");
  var airOnly = Array.prototype.slice.call(document.querySelectorAll("[data-air-only]"));
  if (!radios.length || !button || !window.MeshTransports) return;

  var KEY = "meshtender.transport";
  var busy = false; // a connect is under way

  function chosen() {
    var r = radios.find(function (i) { return i.checked; });
    return r ? r.value : "usb";
  }
  function device() { return chosen() === "usb" ? "repeater" : "modem"; }

  function render() {
    var t = window.MeshConsole;
    var ready = !!(t && t.ready);
    radios.forEach(function (r) { r.disabled = ready || busy; });
    airOnly.forEach(function (el) { el.hidden = chosen() !== "air"; });
    if (unsupported) unsupported.hidden = !!(t && t.supported);
    button.disabled = !(t && t.supported) || (busy && !ready);
    button.classList.toggle("btn-primary", !ready);
    label.textContent = ready ? "Disconnect" : busy ? "Connecting…" : "Connect " + device();
    status.textContent = ready
      ? (chosen() === "usb" ? "Connected over USB" : "Connected through the modem")
      : busy ? "Connecting…" : "Not connected";
    status.classList.toggle("text-green", ready);
  }

  function use(name) {
    var t = window.MeshTransports[name];
    if (!t) return;
    window.MeshConsole = t;
    try { localStorage.setItem(KEY, name); } catch (_) { /* private mode */ }
    render();
    document.dispatchEvent(new CustomEvent("mesh:transport", { detail: { name: name } }));
  }

  var saved = null;
  try { saved = localStorage.getItem(KEY); } catch (_) { /* private mode */ }
  radios.forEach(function (r) {
    if (saved && r.value === saved) r.checked = true;
    r.addEventListener("change", function () { use(chosen()); });
  });
  window.MeshConsole = window.MeshTransports[chosen()];
  render();

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
