// Drives the "set up a brand-new repeater over USB" step of the add-repeater
// wizard. Unlike console.js (which bridges a KISS modem's raw bytes to the server
// over a WebSocket), this talks the repeater's own plain-text serial CLI
// DIRECTLY, through serial-cli.js.
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
  var hasSerial = !!(window.MeshSerial && window.MeshSerial.supported);
  var hasEd25519 = !!(window.crypto && window.crypto.subtle);
  if (!hasSerial || !hasEd25519) {
    var u = $("unsupported");
    if (u) u.hidden = false;
  }

  // --- identity (WebCrypto Ed25519) --------------------------------------
  // `set prv.key` takes MeshCore's 64-byte private key, which is the EXPANDED
  // Ed25519 key: SHA-512(seed) with the scalar clamped (firmware
  // lib/ed25519/keypair.c ed25519_create_keypair; meshcore-go
  // NewLocalIdentityFromExpandedKey). The firmware derives the public key from
  // its first 32 bytes. state holds the resolved keypair; pubHex is null for a
  // pasted expanded key until the repeater reports it (WebCrypto can't derive a
  // public key from a raw scalar).
  var state = { privHex: null, pubHex: null, radio: null, identityPlaceholder: null };

  // expandSeed returns the 128-hex expanded private key for a 32-byte seed.
  async function expandSeed(seed) {
    var h = new Uint8Array(await crypto.subtle.digest("SHA-512", seed));
    h[0] &= 248;
    h[31] &= 63;
    h[31] |= 64;
    return hex(h);
  }

  // The firmware refuses a key whose public key starts with 00 or ff
  // (LocalIdentity::validatePrivateKey), so never offer one.
  function usablePub(pub) { return pub[0] !== 0x00 && pub[0] !== 0xff; }

  // generateKeyPair returns {privHex(128), pubHex(64)} for a fresh random key.
  async function generateKeyPair() {
    for (;;) {
      var kp = await crypto.subtle.generateKey({ name: "Ed25519" }, true, ["sign", "verify"]);
      var jwk = await crypto.subtle.exportKey("jwk", kp.privateKey);
      var seed = b64urlToBytes(jwk.d);   // 32 bytes
      var pub = b64urlToBytes(jwk.x);    // 32 bytes
      if (usablePub(pub)) return { privHex: await expandSeed(seed), pubHex: hex(pub) };
    }
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

  // Parse a pasted key: 128 hex = MeshCore's 64-byte expanded key, the form the
  // firmware stores (its public key comes back from the repeater when it's
  // written); 64 hex = a 32-byte seed (derive both).
  async function applyPastedKey() {
    var raw = ($("privkey").value || "").trim().toLowerCase().replace(/\s+/g, "");
    if (!/^[0-9a-f]+$/.test(raw)) { clearKey(); return; }
    if (raw.length === 128) {
      state.privHex = raw; state.pubHex = null;
      setPubKey("Read from the repeater when the key is written");
    } else if (raw.length === 64) {
      var seed = hexToBytes(raw);
      var pub = await pubFromSeed(seed);
      setKeyState(await expandSeed(seed), hex(pub));
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
    $("regen").hidden = mode !== "random";
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
        $("loc-status").textContent = "Placed at " + lat.toFixed(5) + ", " + lon.toFixed(5) + ".";
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
  // maskLine hides the secrets in a command for the preview and the log: all but
  // the first 8 hex chars of the private key, and the passwords.
  function maskLine(line) {
    return line
      .replace(/(set prv\.key\s+)([0-9a-f]{8})[0-9a-f]+/i, "$1$2… (hidden)")
      .replace(/^(password |set guest\.password ).*$/, "$1•••••• (hidden)");
  }
  var commands = null; // resolved command list with the key and passwords spliced in

  // The firmware keeps each password in a 16-byte buffer and silently cuts a
  // longer one short (CommonCLI.cpp, StrHelper::strncpy), so refuse rather than
  // let the repeater end up with a password nobody typed.
  var PASSWORD_MAX_BYTES = 15;
  function passwordProblem(label, pw) {
    if (!pw) return "";
    if (new TextEncoder().encode(pw).length > PASSWORD_MAX_BYTES) return label + " can be at most " + PASSWORD_MAX_BYTES + " characters.";
    if (/[\u0000-\u001f\u007f]/.test(pw)) return label + " can't contain control characters.";
    if (/^\s|\s$/.test(pw)) return label + " can't start or end with a space.";
    return "";
  }

  // passwordCommands returns the lines that set the passwords the user typed.
  // They're spliced in here, like the key, so they never reach MeshTender.
  function passwordCommands() {
    var admin = $("admin_password") ? $("admin_password").value : "";
    var guest = $("guest_password") ? $("guest_password").value : "";
    var problem = passwordProblem("The admin password", admin) || passwordProblem("The guest password", guest);
    if (problem) return { problem: problem };
    var lines = [];
    if (admin) lines.push("password " + admin);
    if (guest) lines.push("set guest.password " + guest);
    return { lines: lines };
  }

  async function generate() {
    if (!state.privHex) {
      alert("Generate or enter a key first.");
      return;
    }
    var name = ($("name").value || "").trim();
    if (!name) { alert("Name is required."); return; }
    var passwords = passwordCommands();
    if (passwords.problem) { alert(passwords.problem); return; }
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
    // Right after MeshTender's access, before the profile's own settings.
    var after = commands.findIndex(function (c) { return /^setperm /.test(c); });
    Array.prototype.splice.apply(commands, [after + 1, 0].concat(passwords.lines));
    $("cmdlist").textContent = commands.map(maskLine).join("\n");
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

  // The firmware saves its access list 5 s after a `setperm`
  // (simple_repeater MyMesh.cpp LAZY_CONTACTS_WRITE_DELAY) and `reboot` restarts
  // at once, so rebooting sooner would lose MeshTender's admin grant.
  var ACL_SAVE_MS = 6000;

  async function run() {
    if (!commands) return;
    if (commands.some(function (c) { return /<[^>]+>/.test(c); })) {
      addLog("error", "Refusing to run: an unfilled placeholder remains in the command list.");
      return;
    }
    $("run").disabled = true;
    var cli;
    var ok = false;
    try {
      addLog("info", "Choose the repeater's USB port…");
      cli = await SerialCLI.open(await MeshSerial.requestPort());
      addLog("info", "Connected. Waiting for the repeater to answer…");
      var ready = await cli.waitReady(function (n) {
        if (n === 1) addLog("info", "No answer yet; it may be starting up.");
      });
      if (!ready) throw new Error("The repeater didn't answer. Check it's a repeater with MeshCore firmware, unplug it and plug it back in, then try again.");

      var permAt = 0;
      var problems = 0;
      for (var i = 0; i < commands.length; i++) {
        var cmd = commands[i];
        var shown = maskLine(cmd);
        if (/^reboot\b/.test(cmd)) {
          var wait = permAt ? ACL_SAVE_MS - (performance.now() - permAt) : 0;
          if (wait > 0) {
            addLog("info", "Waiting for the repeater to save MeshTender's access…");
            await new Promise(function (res) { setTimeout(res, wait); });
          }
          addLog("sent", "→ " + shown);
          await cli.send(cmd);
          addLog("info", "Rebooting to apply its new identity and radio.");
          break;
        }
        addLog("sent", "→ " + shown);
        var r = await cli.command(cmd);
        var isAdminPassword = /^password /.test(cmd);
        if (r.reply === null) addLog("noreply", "(no reply)");
        // "password now: <pw>": the firmware echoes it back; keep it out of the log.
        else if (isAdminPassword) addLog("reply", r.reply.replace(/^(password now:).*$/, "$1 (hidden)"));
        else addLog(r.error ? "error" : "reply", r.reply);

        if (/^set prv\.key\b/.test(cmd)) {
          // The firmware answers with the public key it derived; that, not ours,
          // is the identity the record must carry.
          var m = /new pubkey:\s*([0-9a-f]{64})/i.exec(r.reply || "");
          if (!m) throw new Error("The repeater didn't take the new identity, so nothing was saved.");
          var got = m[1].toLowerCase();
          if (state.pubHex && got !== state.pubHex) {
            throw new Error("The repeater derived a different public key (" + got.slice(0, 8) + "…) than expected, so nothing was saved.");
          }
          setKeyState(state.privHex, got);
        } else if (/^setperm\b/.test(cmd)) {
          if (r.reply === null || r.error) throw new Error("The repeater didn't grant MeshTender access, so nothing was saved.");
          permAt = performance.now();
        } else if (r.reply === null || r.error || (isAdminPassword && !/^password now:/.test(r.reply))) {
          problems++;
        }
      }
      if (problems) addLog("error", problems + " setting(s) didn't apply; check them in the console once it's saved.");
      ok = true;
    } catch (e) {
      addLog("error", e.message);
      $("run").disabled = false;
    } finally {
      if (cli) await cli.close();
    }
    if (ok) {
      addLog("info", "Saving the repeater…");
      await complete();
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
