// Drives the "set up a brand-new repeater over USB" step of the add-repeater
// wizard. Unlike console.js (which bridges a KISS modem's raw bytes to the server
// over a WebSocket), this talks the repeater's own plain-text serial CLI
// DIRECTLY: it writes "command\n" and reads the text the device echoes back.
//
// The repeater's identity is generated here, in the browser, with WebCrypto
// Ed25519. The private key NEVER leaves the page — only the public key is posted
// to the server (to create the repeater record). The server generates the
// ordered command list with a placeholder where the `set prv.key` line goes; we
// splice the real key in locally before running.

(function () {
  "use strict";

  var $ = function (id) { return document.getElementById(id); };
  // on wires an event handler, skipping silently if the element is absent, so a
  // trimmed-down template can omit controls without breaking setup.
  var on = function (id, event, fn) {
    var el = $(id);
    if (el) el.addEventListener(event, fn);
  };
  var hex = function (bytes) {
    return Array.prototype.map.call(bytes, function (b) {
      return b.toString(16).padStart(2, "0");
    }).join("");
  };
  var b64urlToBytes = function (s) {
    s = s.replace(/-/g, "+").replace(/_/g, "/");
    while (s.length % 4) s += "=";
    var bin = atob(s);
    var out = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out;
  };
  var hexToBytes = function (s) {
    var out = new Uint8Array(s.length / 2);
    for (var i = 0; i < out.length; i++) out[i] = parseInt(s.substr(i * 2, 2), 16);
    return out;
  };

  // --- feature detection -------------------------------------------------
  var hasSerial = "serial" in navigator;
  var hasEd25519 = !!(window.crypto && window.crypto.subtle);
  if (!hasSerial || !hasEd25519) {
    var u = $("unsupported");
    if (u) u.hidden = false;
  }

  // --- identity (WebCrypto Ed25519) --------------------------------------
  // A MeshCore private key is the 64-byte Ed25519 key (32-byte seed ‖ 32-byte
  // public key), so `set prv.key` wants 128 hex chars. We build that from the
  // JWK's `d` (seed) and `x` (public key). state holds the resolved keypair.
  var state = { privHex: null, pubHex: null, radio: null, identityPlaceholder: null };

  // generateKeyPair returns {privHex(128), pubHex(64)} for a fresh random key.
  async function generateKeyPair() {
    var kp = await crypto.subtle.generateKey({ name: "Ed25519" }, true, ["sign", "verify"]);
    var jwk = await crypto.subtle.exportKey("jwk", kp.privateKey);
    var seed = b64urlToBytes(jwk.d);   // 32 bytes
    var pub = b64urlToBytes(jwk.x);    // 32 bytes
    return { privHex: hex(seed) + hex(pub), pubHex: hex(pub) };
  }

  // pubFromSeed derives the 32-byte public key from a 32-byte seed by importing
  // it as PKCS#8 (fixed DER prefix + seed) and re-exporting the JWK.
  async function pubFromSeed(seed) {
    var prefix = hexToBytes("302e020100300506032b657004220420");
    var pkcs8 = new Uint8Array(prefix.length + seed.length);
    pkcs8.set(prefix); pkcs8.set(seed, prefix.length);
    var key = await crypto.subtle.importKey("pkcs8", pkcs8, { name: "Ed25519" }, true, ["sign"]);
    var jwk = await crypto.subtle.exportKey("jwk", key);
    return b64urlToBytes(jwk.x);
  }

  function setPubKey(text) { $("pubkey").textContent = text || "—"; }
  function setKeyState(privHex, pubHex) {
    state.privHex = privHex; state.pubHex = pubHex;
    setPubKey(pubHex);
  }
  function clearKey() { state.privHex = null; state.pubHex = null; setPubKey(null); }

  // Parse a pasted key: 128 hex = full 64-byte key (last 32 bytes are the pubkey);
  // 64 hex = a 32-byte seed (derive the pubkey).
  async function applyPastedKey() {
    var raw = ($("privkey").value || "").trim().toLowerCase().replace(/\s+/g, "");
    if (!/^[0-9a-f]+$/.test(raw)) { clearKey(); return; }
    if (raw.length === 128) {
      setKeyState(raw, raw.slice(64));
    } else if (raw.length === 64) {
      var pub = await pubFromSeed(hexToBytes(raw));
      setKeyState(raw + hex(pub), hex(pub));
    } else {
      clearKey();
    }
  }

  // --- key mode toggles --------------------------------------------------
  var grinding = false;
  function selectedKeyMode() {
    var el = document.querySelector('input[name="keymode"]:checked');
    return el ? el.value : "random";
  }
  function refreshKeyMode() {
    var mode = selectedKeyMode();
    $("key-random").hidden = mode !== "random";
    $("key-prefix").hidden = mode !== "prefix";
    $("key-paste").hidden = mode !== "paste";
    grinding = false; $("grind-stop").hidden = true;
    $("grind-status").textContent = "";
    if (mode === "random") {
      generateKeyPair().then(function (k) { setKeyState(k.privHex, k.pubHex); });
    } else if (mode === "paste") {
      applyPastedKey();
    } else {
      clearKey();
    }
  }

  async function grind() {
    if (grinding) return;
    var prefix = ($("prefix").value || "").trim().toLowerCase();
    if (!/^[0-9a-f]*$/.test(prefix)) {
      $("grind-status").textContent = "Prefix must be hex (0-9, a-f).";
      return;
    }
    if (prefix === "") { refreshKeyMode(); return; }
    grinding = true;
    $("grind-stop").hidden = false;
    clearKey();
    var tries = 0;
    while (grinding) {
      var k = await generateKeyPair();
      tries++;
      if (k.pubHex.startsWith(prefix)) {
        setKeyState(k.privHex, k.pubHex);
        $("grind-status").textContent = "Found after " + tries + " tries.";
        break;
      }
      if (tries % 200 === 0) $("grind-status").textContent = "Searching… " + tries + " tries";
    }
    grinding = false;
    $("grind-stop").hidden = true;
  }

  // --- org config / preset toggle ----------------------------------------
  function populateOrgs() {
    var sel = $("org");
    (window.SETUP_ORGS || []).forEach(function (o) {
      var opt = document.createElement("option");
      opt.value = String(o.ID); opt.textContent = o.Name;
      sel.appendChild(opt);
    });
  }
  function currentOrg() {
    var id = parseInt($("org").value, 10) || 0;
    if (!id) return null;
    return (window.SETUP_ORGS || []).find(function (o) { return o.ID === id; }) || null;
  }
  function refreshOrg() {
    var org = currentOrg();
    var profiles = (org && org.Profiles) || [];
    $("preset-wrap").hidden = !!org;
    $("profile-wrap").hidden = !(org && profiles.length);
    var psel = $("profile");
    psel.innerHTML = "";
    profiles.forEach(function (name) {
      var opt = document.createElement("option");
      opt.value = name; opt.textContent = name;
      psel.appendChild(opt);
    });
  }

  // --- location ----------------------------------------------------------
  var loc = { lat: null, lon: null };
  function initMap() {
    if (!window.regionMapView) return;
    regionMapView("map", {
      onPick: function (lat, lon) {
        loc.lat = lat; loc.lon = lon;
        $("loc-status").textContent = "Location: " + lat.toFixed(5) + ", " + lon.toFixed(5);
      },
    });
  }

  // --- generate command list ---------------------------------------------
  function readRadio() {
    return {
      freqMhz: parseFloat($("radio_freq_mhz").value),
      bwKhz: parseFloat($("radio_bw_khz").value),
      sf: parseInt($("radio_sf").value, 10),
      cr: parseInt($("radio_cr").value, 10),
    };
  }
  function maskKeyLine(line) {
    // Show only the first 8 hex chars of the private key in the preview.
    return line.replace(/(set prv\.key\s+)([0-9a-f]{8})[0-9a-f]+/i, "$1$2… (hidden)");
  }
  var commands = null; // resolved command list with the real key spliced in

  async function generate() {
    if (!state.privHex || !state.pubHex) {
      alert("Generate or enter a key first.");
      return;
    }
    var name = ($("name").value || "").trim();
    if (!name) { alert("Name is required."); return; }
    var org = currentOrg();
    var body = {
      name: name,
      orgId: org ? org.ID : 0,
      profile: org && !$("profile-wrap").hidden ? $("profile").value : "",
      lat: loc.lat, lon: loc.lon,
    };
    if (!org) {
      var r = readRadio();
      body.freqMhz = r.freqMhz; body.bwKhz = r.bwKhz; body.sf = r.sf; body.cr = r.cr;
    }
    var resp;
    try {
      resp = await fetch("/repeaters/setup/commands", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
    } catch (e) { alert("Could not reach the server: " + e.message); return; }
    if (!resp.ok) { alert("Could not build commands: " + (await resp.text())); return; }
    var data = await resp.json();
    state.radio = data.radio;
    var idLine = "set prv.key " + state.privHex;
    commands = data.commands.map(function (c) {
      return c === data.identityPlaceholder ? idLine : c;
    });
    $("cmdlist").textContent = commands.map(maskKeyLine).join("\n");
    $("review-card").hidden = false;
    $("review-card").scrollIntoView({ behavior: "smooth" });
  }

  // --- run over USB serial ------------------------------------------------
  function addLog(stateName, message) {
    var li = document.createElement("li");
    li.className = "ev ev-" + stateName;
    li.textContent = message;
    $("log").appendChild(li);
    $("log").scrollTop = $("log").scrollHeight;
  }

  // readForDevice reads decoded text from the port until it goes quiet for
  // `idleMs` or `maxMs` elapses — repeater CLI replies are short and terminated
  // by a prompt/newline, but we don't depend on an exact terminator.
  async function readForDevice(reader, decoder, idleMs, maxMs) {
    var out = "";
    var deadline = performance.now() + maxMs;
    while (performance.now() < deadline) {
      var timer;
      var idle = new Promise(function (res) { timer = setTimeout(function () { res("__idle__"); }, idleMs); });
      var chunk;
      try {
        chunk = await Promise.race([reader.read(), idle]);
      } finally { clearTimeout(timer); }
      if (chunk === "__idle__") break;
      if (chunk.done) break;
      if (chunk.value) out += decoder.decode(chunk.value, { stream: true });
    }
    return out.trim();
  }

  async function run() {
    if (!commands) return;
    if (commands.some(function (c) { return /<[^>]+>/.test(c); })) {
      addLog("error", "Refusing to run: an unfilled placeholder remains in the command list.");
      return;
    }
    $("run").disabled = true;
    var port, writer, reader;
    var encoder = new TextEncoder();
    var decoder = new TextDecoder();
    try {
      addLog("info", "Requesting serial port…");
      port = await navigator.serial.requestPort();
      await port.open({ baudRate: 115200 });
      writer = port.writable.getWriter();
      reader = port.readable.getReader();
      addLog("info", "Connected (115200 8N1). Running setup…");

      for (var i = 0; i < commands.length; i++) {
        var cmd = commands[i];
        var shown = maskKeyLine(cmd);
        var isReboot = /^reboot\b/.test(cmd);
        addLog("sent", "→ " + shown);
        await writer.write(encoder.encode(cmd + "\n"));
        if (isReboot) {
          addLog("info", "Reboot sent — the device will restart to apply its new identity and radio.");
          break;
        }
        var reply = await readForDevice(reader, decoder, 400, 3000);
        if (reply) addLog("reply", reply);
        else addLog("noreply", "(no reply)");
      }
      addLog("info", "Setup commands sent. Saving repeater…");
      await complete();
    } catch (e) {
      addLog("error", e.message);
      $("run").disabled = false;
    } finally {
      try { if (reader) { await reader.cancel(); reader.releaseLock(); } } catch (_) {}
      try { if (writer) writer.releaseLock(); } catch (_) {}
      try { if (port) await port.close(); } catch (_) {}
    }
  }

  async function complete() {
    var form = new URLSearchParams();
    form.set("name", ($("name").value || "").trim());
    form.set("public_key", state.pubHex);
    form.set("radio_freq_mhz", String(state.radio.freqMhz));
    form.set("radio_bw_khz", String(state.radio.bwKhz));
    form.set("radio_sf", String(state.radio.sf));
    form.set("radio_cr", String(state.radio.cr));
    if (loc.lat != null && loc.lon != null) {
      form.set("lat", loc.lat.toFixed(6));
      form.set("lon", loc.lon.toFixed(6));
    }
    // Optional visibility toggles — guarded so a trimmed-down template can omit them.
    var showOrg = $("show_on_public_org");
    if (showOrg && showOrg.checked) form.set("show_on_public_org", "1");
    var exposePage = $("expose_public_page");
    if (exposePage && exposePage.checked) form.set("expose_public_page", "1");
    var resp = await fetch("/repeaters/setup/complete", {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: form.toString(),
    });
    if (!resp.ok) {
      addLog("error", "Could not save repeater: " + (await resp.text()));
      return;
    }
    var data = await resp.json();
    addLog("info", "Saved. Redirecting…");
    window.location = data.redirect;
  }

  // --- wire up -----------------------------------------------------------
  if (!hasSerial || !hasEd25519) return;
  populateOrgs();
  refreshOrg();
  refreshKeyMode();
  initMap();

  document.querySelectorAll('input[name="keymode"]').forEach(function (el) {
    el.addEventListener("change", refreshKeyMode);
  });
  on("regen", "click", function () {
    generateKeyPair().then(function (k) { setKeyState(k.privHex, k.pubHex); });
  });
  on("privkey", "input", function () {
    if (selectedKeyMode() === "paste") applyPastedKey();
  });
  on("grind", "click", grind);
  on("grind-stop", "click", function () { grinding = false; });
  on("org", "change", refreshOrg);
  on("generate", "click", generate);
  on("run", "click", run);
})();
