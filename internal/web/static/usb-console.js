// usb-console.js — the connection to a repeater plugged into this computer,
// through its own serial CLI (serial-cli.js): no modem, nothing over the air.
// It has the same shape as air-console.js (see there) and registers as
// window.MeshTransports.usb, so console-config.js can run an organization's
// configuration through either.
//
// Connecting checks the repeater's public key against the one MeshTender has
// (#usb-console[data-pubkey]) and refuses any other device, so a configuration
// never lands on the wrong repeater. A reply saying a change needs a reboot
// (the firmware's "OK - reboot to apply", e.g. for `set radio`) shows
// #reboot-prompt, whose #reboot button sends `reboot` and closes the port.
(function () {
  "use strict";

  var root = document.getElementById("usb-console");
  if (!root) return;
  var log = document.getElementById("log");
  var rebootPrompt = document.getElementById("reboot-prompt");
  var rebootBtn = document.getElementById("reboot");
  var panel = document.getElementById("config-panel");
  var expected = String(root.dataset.pubkey || "").toLowerCase();

  var cli = null;
  var connecting = false;
  var queue = Promise.resolve();

  var api = {
    name: "usb",
    ready: false,
    supported: !!(window.MeshSerial && window.MeshSerial.supported && window.SerialCLI),
    connect: connect,
    disconnect: function () { queue = queue.then(close); },
    send: send,
    getLocation: getLocation,
  };
  window.MeshTransports = window.MeshTransports || {};
  window.MeshTransports.usb = api;

  function emit(name, detail) {
    document.dispatchEvent(new CustomEvent(name, { detail: detail }));
  }
  function status(state, message) {
    emit("mesh:status", { state: state, message: message || "" });
  }
  function addLog(state, message) {
    var li = document.createElement("li");
    li.className = "ev ev-" + state;
    li.textContent = message;
    log.appendChild(li);
    log.scrollTop = log.scrollHeight;
  }

  async function close() {
    var c = cli;
    cli = null;
    api.ready = false;
    if (c) await c.close();
    emit("mesh:closed", null);
  }

  async function connect() {
    if (api.ready || cli || connecting || !api.supported) return;
    connecting = true;
    try {
      addLog("info", "Choose the repeater's USB port…");
      cli = await SerialCLI.open(await MeshSerial.requestPort());
      addLog("info", "Connected. Waiting for the repeater to answer…");
      var ok = await cli.waitReady(function (n) {
        if (n === 1) addLog("info", "No answer yet; it may be starting up.");
      });
      if (!ok) throw new Error("The repeater didn't answer. Check it's a repeater with MeshCore firmware, unplug it and plug it back in, then try again.");
      var r = await cli.command("get public.key");
      var m = /^>\s*([0-9a-f]{64})/i.exec(r.reply || "");
      if (!m) throw new Error("The repeater didn't say which one it is.");
      var got = m[1].toLowerCase();
      if (got !== expected) {
        throw new Error("This is a different repeater (" + got.slice(0, 8) + "…), so nothing was sent. Plug in the one whose key starts " + expected.slice(0, 8) + ".");
      }
      addLog("reply", "It's " + (root.dataset.name || "this repeater") + " (" + got.slice(0, 8) + "…).");
      api.ready = true;
      status("ready", "");
      emit("mesh:ready", null);
    } catch (e) {
      // A cancelled port picker is the user's choice, not a failure.
      if (!e || e.name !== "NotFoundError") addLog("error", e && e.message ? e.message : String(e));
      await close();
    }
    connecting = false;
  }

  // send queues a command and reports its result as a mesh:status, the way the
  // over-the-air console does: "reply", "error" or "noreply".
  function send(text) {
    var t = String(text || "").trim();
    if (!t || !api.ready || !cli) return false;
    queue = queue.then(async function () {
      if (!cli) { status("error", "Not connected."); return; }
      addLog("sent", "→ " + t);
      window.dispatchEvent(new CustomEvent("console:sent", { detail: { text: t } }));
      var r = await cli.command(t);
      if (r.reply === null) {
        addLog("noreply", "(no reply)");
        status("noreply", "");
      } else if (r.error) {
        addLog("error", r.reply);
        status("error", r.reply);
      } else {
        addLog("reply", r.reply);
        status("reply", r.reply);
      }
      if (r.reboot && rebootPrompt) rebootPrompt.hidden = false;
    });
    return true;
  }

  // getLocation reads the coordinates the repeater has and saves them, then
  // reports "location" so the panel reloads its region commands.
  function getLocation() {
    if (!api.ready || !cli) return false;
    queue = queue.then(async function () {
      if (!cli) return;
      var lat = parseFloat(((await cli.command("get lat")).reply || "").replace(/^>\s*/, ""));
      var lon = parseFloat(((await cli.command("get lon")).reply || "").replace(/^>\s*/, ""));
      // An unset location reads as 0, 0.
      if (!isFinite(lat) || !isFinite(lon) || (lat === 0 && lon === 0)) {
        addLog("error", "The repeater doesn't have a location set. Pick it on the map instead.");
        status("error", "no location");
        return;
      }
      var resp = await fetch(panel.dataset.locationUrl, {
        method: "POST",
        body: new URLSearchParams({ lat: String(lat), lon: String(lon) }),
      });
      if (!resp.ok) {
        addLog("error", "Couldn't save the location.");
        status("error", "save failed");
        return;
      }
      addLog("reply", "Location " + lat.toFixed(5) + ", " + lon.toFixed(5) + " saved.");
      status("location", "");
    });
    return true;
  }

  if (rebootBtn) {
    rebootBtn.addEventListener("click", function () {
      if (!cli) return;
      rebootBtn.disabled = true;
      queue = queue.then(async function () {
        if (!cli) return;
        addLog("sent", "→ reboot");
        await cli.send("reboot");
        addLog("info", "Rebooting. Unplug it whenever you like.");
        rebootPrompt.hidden = true;
        await close();
      });
    });
  }
})();
