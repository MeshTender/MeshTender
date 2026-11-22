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

  // The route in Options: flood, flood in a region or direct through
  // repeaters (the #path field). Only the chosen route's field shows. The
  // region is picked from the #scope-region list, whose "Another region…"
  // option (value "") reveals the #scope text field; a page with no regions
  // has just the text field. The route and region are remembered; without a
  // remembered route, the page's default applies (flood in the org's default
  // scope region, if it recommends one). The region is only remembered when a
  // session starts with it, so a half-typed name never is. (The key changed
  // from "meshtender.scope" when that was learned the hard way.)
  var ROUTE_KEY = "meshtender.route";
  var SCOPE_KEY = "meshtender.region";
  var routes = Array.prototype.slice.call(document.querySelectorAll("[data-route]"));
  var routeOnly = Array.prototype.slice.call(document.querySelectorAll("[data-route-only]"));
  var scopeList = document.getElementById("scope-region");
  var scopeField = document.getElementById("scope");
  var routeBox = document.querySelector('[data-testid="route"]');
  // scopeName is the region chosen, from the list or typed.
  function scopeName() {
    if (scopeList && scopeList.value) return scopeList.value;
    return scopeField ? scopeField.value.trim() : "";
  }
  function showScopeField() {
    if (scopeList && scopeField) scopeField.hidden = scopeList.value !== "";
  }
  // rememberScope saves the region a session is starting with.
  function rememberScope() {
    if (!routes.length || route() !== "scope" || !scopeName()) return;
    try { localStorage.setItem(SCOPE_KEY, scopeName()); } catch (_) { /* private mode */ }
  }
  function route() {
    var r = routes.find(function (i) { return i.checked; });
    return r ? r.value : "flood";
  }
  function showRoute() {
    routeOnly.forEach(function (el) { el.hidden = el.getAttribute("data-route-only") !== route(); });
  }
  if (routes.length) {
    var savedRoute = null, savedScope = null;
    try {
      savedRoute = localStorage.getItem(ROUTE_KEY);
      savedScope = localStorage.getItem(SCOPE_KEY);
    } catch (_) { /* private mode */ }
    var initial = savedRoute || (routeBox && routeBox.getAttribute("data-default-route")) || "flood";
    routes.forEach(function (r) {
      r.checked = r.value === initial;
      r.addEventListener("change", function () {
        try { localStorage.setItem(ROUTE_KEY, route()); } catch (_) { /* private mode */ }
        showRoute();
      });
    });
    if (!routes.some(function (r) { return r.checked; })) routes[0].checked = true;
    // A remembered region is picked from the list if it's there, and typed in
    // otherwise.
    if (savedScope) {
      var listed = scopeList && Array.prototype.slice.call(scopeList.options).some(function (o) { return o.value === savedScope; });
      if (listed) {
        scopeList.value = savedScope;
      } else {
        if (scopeList) scopeList.value = "";
        if (scopeField) scopeField.value = savedScope;
      }
    }
    if (scopeList) {
      scopeList.addEventListener("change", function () {
        showScopeField();
        if (!scopeList.value && scopeField) scopeField.focus();
      });
    }
    showScopeField();
    showRoute();
  }

  button.addEventListener("click", function () {
    var t = window.MeshConsole;
    if (!t) return;
    if (t.ready) { t.disconnect(); return; }
    busy = true;
    rememberScope();
    render();
    t.connect();
  });
  // Something else on the page (the location banner) may start a connection.
  document.addEventListener("mesh:connecting", function () { busy = true; rememberScope(); render(); });
  document.addEventListener("mesh:ready", function () { busy = false; render(); });
  document.addEventListener("mesh:closed", function () { busy = false; render(); });
})();
