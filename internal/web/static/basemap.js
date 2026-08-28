// basemap.js — the CARTO basemaps shared by every map we render, and the small
// MapLibre helpers built on top of them.
//
// CARTO deprecated their raster tiles; the replacement is a MapLibre GL *vector*
// style, which Leaflet cannot render. So the basemap decides the engine: every map
// in the app is MapLibre GL, and this file is the one place that knows which styles
// we use, how the API key is attached, and how the dark/light choice is remembered.
//
// Load this after maplibre-gl.js and before meshmap.js / regionmap.js.

(function () {
  // The two CARTO GL styles, addressed on the tiles. host so that one CSP source
  // (https://*.basemaps.cartocdn.com) covers the style JSON as well as the tiles,
  // sprite and glyphs it points at — the bare basemaps.cartocdn.com would need a
  // second entry to match.
  var STYLES = {
    dark: "https://tiles.basemaps.cartocdn.com/gl/dark-matter-gl-style/style.json",
    light: "https://tiles.basemaps.cartocdn.com/gl/voyager-gl-style/style.json",
  };

  // Which basemap the operator last chose, remembered across maps and pages.
  var STORAGE_KEY = "mt_map_base";

  // Fontstack for any label we draw ourselves (cluster counts). Glyphs come from
  // the CARTO glyph host the style declares, so this has to be a face CARTO serves.
  var FONT = ["Open Sans Bold"];

  // MESHTENDER_MAPS is the registry of live maps, keyed by container element id.
  // MapLibre paints to a WebGL canvas, so — unlike the Leaflet DOM this replaced —
  // there are no per-shape nodes for a browser test to select. Registering the map
  // (and, on the area editor, its draw instance) gives the e2e suite a stable,
  // deliberate way to assert what is actually on the map.
  window.MESHTENDER_MAPS = window.MESHTENDER_MAPS || {};

  function cartoKey() {
    return document.documentElement.getAttribute("data-carto-key") || "";
  }

  // transformRequest attaches the CARTO API key to every request a style makes.
  //
  // It has to happen here rather than on the style URL: the style JSON names its
  // tiles, sprite and glyphs as absolute URLs that carry no key, so keying only the
  // style would leave the four requests that actually fetch map data unkeyed. With
  // no key configured the parameter is left off entirely rather than sent empty.
  function transformRequest(url) {
    var key = cartoKey();
    if (!key) return { url: url };
    if (url.indexOf("//") === -1) return { url: url };
    var host = url.split("/")[2] || "";
    if (host !== "cartocdn.com" && host.slice(-13) !== ".cartocdn.com") return { url: url };
    return { url: url + (url.indexOf("?") === -1 ? "?" : "&") + "key=" + encodeURIComponent(key) };
  }

  function storedBase() {
    try {
      return localStorage.getItem(STORAGE_KEY) === "light" ? "light" : "dark";
    } catch (e) {
      return "dark"; // storage unavailable (private mode); dark matches the UI
    }
  }

  function storeBase(name) {
    try {
      localStorage.setItem(STORAGE_KEY, name);
    } catch (e) {
      /* ignore */
    }
  }

  // The worker is a same-origin fingerprinted asset whose URL the server puts on
  // <html data-maplibre-worker>. This is why we vendor MapLibre's CSP build: the
  // default bundle spawns its worker from a blob:, which would force blob: into the
  // CSP's worker-src. Setting it is global and must happen before the first Map.
  var workerSet = false;
  function ensureWorker() {
    if (workerSet) return;
    var url = document.documentElement.getAttribute("data-maplibre-worker");
    if (url) maplibregl.setWorkerUrl(url);
    workerSet = true;
  }

  // BasemapControl is the dark/light switch. MapLibre ships no layer switcher, so
  // this replaces Leaflet's L.control.layers — same two choices, same remembered
  // preference. It is left expanded (two visible buttons) for the same reason the
  // Leaflet control was: a collapsed toggle needs an icon asset we don't bundle.
  function BasemapControl(onChange) {
    this._onChange = onChange;
  }

  BasemapControl.prototype.onAdd = function (map) {
    var self = this;
    this._map = map;
    var el = document.createElement("div");
    el.className = "maplibregl-ctrl maplibregl-ctrl-group mesh-basemap-ctrl";
    this._buttons = {};
    ["dark", "light"].forEach(function (name) {
      var b = document.createElement("button");
      b.type = "button";
      b.textContent = name === "dark" ? "Dark" : "Light";
      b.setAttribute("data-basemap", name);
      b.setAttribute("aria-pressed", String(storedBase() === name));
      b.addEventListener("click", function () {
        if (storedBase() === name) return;
        storeBase(name);
        self.sync();
        self._onChange(name);
      });
      el.appendChild(b);
      self._buttons[name] = b;
    });
    this._el = el;
    return el;
  };

  BasemapControl.prototype.sync = function () {
    var current = storedBase();
    for (var name in this._buttons) {
      if (Object.prototype.hasOwnProperty.call(this._buttons, name)) {
        this._buttons[name].setAttribute("aria-pressed", String(name === current));
      }
    }
  };

  BasemapControl.prototype.onRemove = function () {
    if (this._el && this._el.parentNode) this._el.parentNode.removeChild(this._el);
    this._map = null;
  };

  // meshCreateMap builds the MapLibre map every page in the app uses. opts:
  //   scrollZoom  — false to require a deliberate zoom gesture (page-embedded maps);
  //   interactive — false for a completely static map;
  //   overlays    — function(map) that adds this page's sources and layers.
  //
  // overlays is a callback rather than something the caller runs once because
  // switching basemap calls map.setStyle(), and **setStyle discards every source and
  // layer the page added**. Handing the work to meshCreateMap means it can be re-run
  // after each swap; a caller that added its layers inline would silently lose them
  // the first time someone pressed Light.
  window.meshCreateMap = function (elId, opts) {
    opts = opts || {};
    ensureWorker();

    var map = new maplibregl.Map({
      container: elId,
      style: STYLES[storedBase()],
      transformRequest: transformRequest,
      interactive: opts.interactive !== false,
      // The attribution comes from the style itself, which is how CARTO ship the
      // credit they require ("(c) CARTO, (c) OpenStreetMap contributors", with the
      // links). Kept non-compact so it is always visible rather than behind an
      // info button. Adding our own string on top would only duplicate it.
      attributionControl: { compact: false },
      // No rotation or pitch anywhere in the app: these are flat reference maps,
      // and a map that has quietly rotated is a map you can't read a bearing off.
      pitchWithRotate: false,
      rollEnabled: false,
      dragRotate: false,
      // Match the old Leaflet behavior of painting once, in the final position:
      // fitBounds/setCenter calls below all pass animate:false, and this stops the
      // basemap itself fading in over the top.
      fadeDuration: 0,
    });

    map.touchZoomRotate.disableRotation();
    map.keyboard.disableRotation();
    if (opts.scrollZoom === false) map.scrollZoom.disable();

    if (opts.interactive !== false) {
      map.addControl(new maplibregl.NavigationControl({ showCompass: false }), "top-left");
      var control = new BasemapControl(function (name) {
        map.setStyle(STYLES[name], { diff: false });
        // style.load, not styledata: styledata fires repeatedly through a style's
        // life (including before its sources are ready, where addLayer throws),
        // whereas style.load fires once the swapped-in style is actually loaded —
        // which is the moment the overlays can go back on.
        map.once("style.load", function () {
          if (opts.overlays) opts.overlays(map);
        });
      });
      map.addControl(control, "top-right");
    }

    if (opts.overlays) {
      map.on("load", function () {
        opts.overlays(map);
      });
    }

    window.MESHTENDER_MAPS[elId] = { map: map };
    return map;
  };

  // meshFont is the fontstack for labels we draw ourselves.
  window.meshFont = FONT;

  // meshFrame points the map at a set of [lon, lat] coordinates, once and without
  // animating, so the map paints in its final position with no fit/zoom flash.
  // opts:
  //   padding   — pixels of breathing room around the fitted box. Pixels rather
  //               than degrees on purpose: a geographic pad scales with the area,
  //               so a large region would zoom straight back out;
  //   pad       — an extra *geographic* nudge (as a fraction of the box) applied
  //               before fitting, for the one case that wants it — see
  //               PRIMARY_CONTEXT in regionmap.js;
  //   pointZoom — zoom for a degenerate box (a single point can't be fit).
  //
  // With no coordinates at all it falls back to the whole world, which is what both
  // callers did before.
  window.meshFrame = function (map, coords, opts) {
    opts = opts || {};
    if (!coords || !coords.length) {
      map.jumpTo({ center: [0, 20], zoom: 1 });
      return;
    }
    var w = coords[0][0], e = coords[0][0], s = coords[0][1], n = coords[0][1];
    coords.forEach(function (c) {
      if (c[0] < w) w = c[0];
      if (c[0] > e) e = c[0];
      if (c[1] < s) s = c[1];
      if (c[1] > n) n = c[1];
    });
    if (opts.pad) {
      var dx = (e - w) * opts.pad, dy = (n - s) * opts.pad;
      w -= dx; e += dx; s -= dy; n += dy;
    }
    if (w === e && s === n) {
      map.jumpTo({ center: [w, s], zoom: opts.pointZoom || 11 });
      return;
    }
    map.fitBounds([[w, s], [e, n]], { animate: false, padding: opts.padding || 24 });
  };

  // meshCoordsOf flattens any GeoJSON geometry to a flat array of [lon, lat] pairs,
  // which is what meshFrame frames on. Used for region polygons, whose nesting depth
  // differs between Polygon and MultiPolygon.
  window.meshCoordsOf = function (geometry) {
    var out = [];
    (function walk(node) {
      if (!node || !node.length) return;
      if (typeof node[0] === "number") {
        out.push([node[0], node[1]]);
        return;
      }
      node.forEach(walk);
    })(geometry && geometry.coordinates);
    return out;
  };
})();
