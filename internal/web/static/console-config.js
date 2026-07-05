// Drives the "Apply organization configuration" panel on the repeater console.
// The panel is an inline Bootstrap collapse (hidden until the user opens it) so
// the console log above stays visible while commands run. It fetches the
// recommended configuration for a chosen org/profile from
// /repeaters/{id}/config.json, lists every command (marking which the user may
// run), and runs them over the live console session via window.MeshConsole
// (owned by console.js), showing a spinner/✓/✗ next to each as it runs. Location
// is handled here too: fetch-from-device (a getloc request over the session) or
// pick-on-map (POST to /location).

(function () {
  const panel = document.getElementById("config-panel");
  if (!panel) return;

  const configURL = panel.dataset.configUrl;
  const locationURL = panel.dataset.locationUrl;
  const q = (sel) => panel.querySelector(sel);
  const orgSel = q('[data-cfg="org"]');
  const profileSel = q('[data-cfg="profile"]');
  const locBox = q('[data-cfg="location"]');
  const mapBox = q('[data-cfg="map"]');
  const cmdBox = q('[data-cfg="commands"]');
  const runAllBtn = q('[data-cfg="run-all"]');
  const connectBtn = q('[data-cfg="connect"]');
  const connectLabel = q('[data-cfg="connect-label"]');
  const hint = q('[data-cfg="hint"]');

  let data = null; // last config payload
  let rows = []; // per-command row state: { line, runnable, statusEl }
  let mapView = null; // Leaflet map instance (created lazily when the picker is shown)
  let running = false; // a run (single or batch) is in progress

  const consoleReady = () => !!(window.MeshConsole && window.MeshConsole.ready);

  function queryURL() {
    const p = new URLSearchParams();
    if (orgSel.value) p.set("org", orgSel.value);
    if (profileSel.value) p.set("profile", profileSel.value);
    const qs = p.toString();
    return qs ? configURL + "?" + qs : configURL;
  }

  async function load(useSelectors) {
    try {
      const resp = await fetch(useSelectors ? queryURL() : configURL, {
        headers: { Accept: "application/json" },
      });
      if (!resp.ok) throw new Error("HTTP " + resp.status);
      data = await resp.json();
    } catch (e) {
      cmdBox.textContent = "Could not load the configuration.";
      return;
    }
    render();
  }

  function render() {
    const orgs = (data && data.orgs) || [];
    if (!orgs.length) {
      orgSel.innerHTML = "";
      profileSel.innerHTML = "";
      locBox.innerHTML = "";
      cmdBox.innerHTML =
        '<p class="text-secondary mb-0">This repeater isn\'t in an organization with a saved configuration.</p>';
      rows = [];
      updateRunState();
      return;
    }

    orgSel.innerHTML = "";
    orgs.forEach((o) => {
      const opt = document.createElement("option");
      opt.value = String(o.orgId);
      opt.textContent = o.orgName;
      if (o.orgId === data.selectedOrg) opt.selected = true;
      orgSel.appendChild(opt);
    });

    const org = orgs.find((o) => o.orgId === data.selectedOrg) || orgs[0];
    const profiles = (org && org.profiles) || [];
    profileSel.innerHTML = "";
    if (!profiles.length) {
      const opt = document.createElement("option");
      opt.value = "";
      opt.textContent = "(regions only)";
      profileSel.appendChild(opt);
      profileSel.disabled = true;
    } else {
      profileSel.disabled = false;
      profiles.forEach((name) => {
        const opt = document.createElement("option");
        opt.value = name;
        opt.textContent = name;
        if (name === data.selectedProfile) opt.selected = true;
        profileSel.appendChild(opt);
      });
    }

    renderLocation();
    renderCommands();
    refreshHint();
  }

  function renderLocation() {
    locBox.innerHTML = "";
    const loc = (data && data.location) || {};
    if (loc.known) {
      const el = document.createElement("div");
      el.className = "small text-secondary mb-2";
      el.textContent = "Location: " + fmt(loc.lat) + ", " + fmt(loc.lon);
      locBox.appendChild(el);
      if (!loc.regionsCover) {
        locBox.appendChild(
          alertEl(
            "warning",
            "This location isn't inside any of this organization's regions, so no regional settings will be applied — check the repeater's location.",
          ),
        );
      }
    } else if (loc.needsLocation) {
      locBox.appendChild(
        alertEl(
          "info",
          "This organization's region settings depend on the repeater's location, which isn't known yet. Connect the modem and fetch it, or pick it on the map.",
        ),
      );
    }

    if (loc.needsLocation || !loc.known || (loc.known && !loc.regionsCover)) {
      const actions = document.createElement("div");
      actions.className = "btn-list mb-3";

      const fetchBtn = document.createElement("button");
      fetchBtn.type = "button";
      fetchBtn.className = "btn btn-sm";
      fetchBtn.dataset.loc = "fetch";
      fetchBtn.textContent = "Fetch from device";
      fetchBtn.disabled = !consoleReady();
      if (fetchBtn.disabled) fetchBtn.title = "Connect the modem first";
      fetchBtn.addEventListener("click", () => {
        if (window.MeshConsole && window.MeshConsole.getLocation()) {
          setHint("Fetching the location from the repeater…");
        }
      });

      const pickBtn = document.createElement("button");
      pickBtn.type = "button";
      pickBtn.className = "btn btn-sm";
      pickBtn.textContent = "Pick on map";
      pickBtn.addEventListener("click", showPicker);

      actions.appendChild(fetchBtn);
      actions.appendChild(pickBtn);
      locBox.appendChild(actions);
    }
  }

  function showPicker() {
    mapBox.style.display = "block";
    if (!mapView && window.regionMapView) {
      const loc = (data && data.location) || {};
      mapView = window.regionMapView("config-map", {
        preview: loc.known ? { lat: loc.lat, lon: loc.lon } : undefined,
        onPick: (lat, lon) => saveLocation(lat, lon),
      });
    } else if (mapView && mapView.invalidateSize) {
      mapView.invalidateSize();
    }
  }

  async function saveLocation(lat, lon) {
    const loc = (data && data.location) || {};
    if (loc.known && distKm(loc.lat, loc.lon, lat, lon) > 1) {
      setHint(
        "Note: that differs from the repeater's current location (" +
          fmt(loc.lat) + ", " + fmt(loc.lon) + ").",
      );
    }
    try {
      const resp = await fetch(locationURL, {
        method: "POST",
        body: new URLSearchParams({ lat: String(lat), lon: String(lon) }),
      });
      if (!resp.ok && resp.status !== 204) throw new Error("HTTP " + resp.status);
    } catch (e) {
      setHint("Could not save the location.");
      return;
    }
    mapBox.style.display = "none";
    load(true); // reload so region commands reflect the new location
  }

  function renderCommands() {
    cmdBox.innerHTML = "";
    rows = [];
    const cmds = (data && data.commands) || [];
    if (!cmds.length) {
      cmdBox.innerHTML =
        '<p class="text-secondary mb-0">No recommended commands for this selection.</p>';
      updateRunState();
      return;
    }
    const list = document.createElement("div");
    list.className = "list-group";
    cmds.forEach((c) => {
      const row = document.createElement("div");
      row.className = "list-group-item d-flex align-items-center gap-2 py-2";

      const left = document.createElement("div");
      left.className = "flex-fill text-break";
      if (!c.line) {
        const note = document.createElement("span");
        note.className = "text-secondary fst-italic";
        note.textContent = c.comment || "";
        left.appendChild(note);
      } else {
        const code = document.createElement("code");
        code.textContent = c.line;
        left.appendChild(code);
      }
      row.appendChild(left);

      // Per-command status indicator (spinner while running, ✓/✗ on completion).
      const status = document.createElement("span");
      status.className = "cfg-status";
      status.style.minWidth = "1.25rem";
      status.style.textAlign = "center";
      row.appendChild(status);

      const idx = rows.length;
      if (c.line && c.runnable) {
        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "btn btn-sm";
        btn.textContent = "Run";
        btn.dataset.run = c.line;
        btn.addEventListener("click", () => runLines([idx]));
        row.appendChild(btn);
      } else if (c.line) {
        const badge = document.createElement("span");
        badge.className = "badge bg-secondary-lt";
        badge.textContent = c.reason === "note" ? "note" : "not permitted";
        if (c.reason && c.reason !== "note") badge.title = c.reason;
        row.appendChild(badge);
      }

      list.appendChild(row);
      rows.push({ line: c.line, runnable: !!(c.line && c.runnable), statusEl: status });
    });
    cmdBox.appendChild(list);
    updateRunState();
  }

  function setStatus(el, state) {
    el.className = "cfg-status";
    el.textContent = "";
    if (state === "running") {
      const sp = document.createElement("span");
      sp.className = "spinner-border spinner-border-sm text-secondary";
      sp.setAttribute("role", "status");
      el.appendChild(sp);
    } else if (state === "ok") {
      el.textContent = "✓";
      el.classList.add("text-success");
    } else if (state === "fail") {
      el.textContent = "✗";
      el.classList.add("text-danger");
    }
  }

  function runnableIndices() {
    const out = [];
    rows.forEach((r, i) => {
      if (r.runnable) out.push(i);
    });
    return out;
  }

  // Run the given command rows in order, waiting for each to complete and
  // updating its status icon. Serialized (running flag) so replies map to rows.
  async function runLines(indices) {
    if (running || !consoleReady()) return;
    running = true;
    updateRunState();
    indices.forEach((i) => rows[i] && setStatus(rows[i].statusEl, "idle"));
    let failed = false;
    for (const i of indices) {
      const row = rows[i];
      if (!row || !row.runnable) continue;
      setStatus(row.statusEl, "running");
      setHint("Running: " + row.line);
      const done = waitForResult();
      if (!window.MeshConsole.send(row.line)) {
        done.cancel();
        setStatus(row.statusEl, "fail");
        failed = true;
        break;
      }
      const ok = await done.promise;
      setStatus(row.statusEl, ok ? "ok" : "fail");
      if (!ok) {
        failed = true;
        break;
      }
    }
    running = false;
    updateRunState();
    setHint(failed ? "A command didn't succeed — see the console log." : "");
  }

  // Resolve true on the next successful reply, false on a failure/timeout.
  function waitForResult() {
    let timer;
    let onStatus;
    const promise = new Promise((resolve) => {
      const finish = (ok) => {
        clearTimeout(timer);
        document.removeEventListener("mesh:status", onStatus);
        resolve(ok);
      };
      timer = setTimeout(() => finish(false), 60000);
      onStatus = (ev) => {
        const st = ev.detail && ev.detail.state;
        if (st === "reply") finish(true);
        else if (st === "noreply" || st === "error" || st === "denied") finish(false);
      };
      document.addEventListener("mesh:status", onStatus);
    });
    return {
      promise,
      cancel() {
        clearTimeout(timer);
        document.removeEventListener("mesh:status", onStatus);
      },
    };
  }

  function updateConnectBtn() {
    if (!connectBtn) return;
    const supported = !!(window.MeshConsole && window.MeshConsole.supported);
    const ready = consoleReady();
    if (!supported) {
      connectBtn.disabled = true;
      connectBtn.title = "This browser doesn't support WebSerial (use Chrome or Edge).";
      if (connectLabel) connectLabel.textContent = "Modem unsupported";
    } else if (ready) {
      connectBtn.disabled = true;
      connectBtn.title = "";
      if (connectLabel) connectLabel.textContent = "Modem connected";
    } else {
      connectBtn.disabled = running;
      connectBtn.title = "";
      if (connectLabel) connectLabel.textContent = "Connect modem";
    }
  }

  function updateRunState() {
    updateConnectBtn();
    const ready = consoleReady();
    runAllBtn.disabled = running || !runnableIndices().length || !ready;
    cmdBox.querySelectorAll("button[data-run]").forEach((b) => {
      b.disabled = running || !ready;
    });
    const fetchBtn = locBox.querySelector('button[data-loc="fetch"]');
    if (fetchBtn) {
      fetchBtn.disabled = running || !ready;
      fetchBtn.title = ready ? "" : "Connect the modem first";
    }
  }

  // Show the connect prompt only when idle and disconnected; never clobber an
  // in-progress run's status message.
  function refreshHint() {
    if (running) return;
    setHint(consoleReady() ? "" : "Connect the modem to run commands.");
  }

  // helpers
  function fmt(n) {
    return typeof n === "number" ? n.toFixed(5) : "?";
  }
  function alertEl(kind, text) {
    const d = document.createElement("div");
    d.className = "alert alert-" + kind + " py-2 px-3 mb-2";
    d.textContent = text;
    return d;
  }
  function setHint(t) {
    hint.textContent = t;
  }
  function distKm(la1, lo1, la2, lo2) {
    const R = 6371;
    const rad = (d) => (d * Math.PI) / 180;
    const dLa = rad(la2 - la1);
    const dLo = rad(lo2 - lo1);
    const a =
      Math.sin(dLa / 2) ** 2 +
      Math.cos(rad(la1)) * Math.cos(rad(la2)) * Math.sin(dLo / 2) ** 2;
    return 2 * R * Math.asin(Math.sqrt(a));
  }

  // wiring
  orgSel.addEventListener("change", () => {
    profileSel.value = "";
    load(true);
  });
  profileSel.addEventListener("change", () => load(true));
  runAllBtn.addEventListener("click", () => runLines(runnableIndices()));
  if (connectBtn) {
    connectBtn.addEventListener("click", () => {
      if (window.MeshConsole) window.MeshConsole.connect();
      if (connectLabel) connectLabel.textContent = "Connecting…";
      connectBtn.disabled = true;
    });
  }
  // Load the config the first time the panel is expanded (not before — it's opt-in).
  panel.addEventListener("shown.bs.collapse", () => {
    if (!data) load(false);
    if (mapView && mapView.invalidateSize) mapView.invalidateSize();
  });
  document.addEventListener("mesh:ready", () => {
    updateConnectBtn();
    if (data) {
      updateRunState();
      refreshHint();
    }
  });
  document.addEventListener("mesh:closed", () => {
    updateConnectBtn();
    if (data) {
      updateRunState();
      refreshHint();
    }
  });
  document.addEventListener("mesh:status", (ev) => {
    const st = ev.detail && ev.detail.state;
    // The server confirms/updates location on connect or on a getloc request; when
    // it does, refresh so region commands and the location banner reflect it.
    if ((st === "location" || st === "confirmed") && data) load(true);
  });
})();
