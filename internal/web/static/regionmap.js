// regionmap.js — Leaflet + Geoman map for org config regions.
//
// A region's geofence is a GeoJSON Polygon/MultiPolygon geometry; an empty
// geometry means "applies everywhere" (no shape on the map). Two entry points:
//
//   initRegionEditor(mapId, listId)  — the admin editor: draw/edit one polygon
//       per region block, with every region shown at once so overlaps are visible.
//   regionMapView(mapId, regions, preview) — read-only: render every region's
//       polygon (and an optional previewed location marker).
(function () {
  // Distinct translucent fills so overlapping regions read as layered colors.
  var PALETTE = ["#4dabf7", "#f783ac", "#69db7c", "#ffa94d", "#9775fa", "#ffd43b", "#3bc9db", "#ff8787"];

  function darkTiles(map) {
    // CARTO "dark matter" basemap, matching the read-only maps (see meshmap.js).
    L.tileLayer("https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png", {
      maxZoom: 19,
      subdomains: "abcd",
      attribution: "&copy; OpenStreetMap &copy; CARTO",
    }).addTo(map);
  }

  function styleFor(i, active) {
    var c = PALETTE[i % PALETTE.length];
    return {
      color: c,
      weight: active ? 3 : 1.5,
      opacity: active ? 1 : 0.6,
      fillColor: c,
      fillOpacity: active ? 0.25 : 0.1,
    };
  }

  // layerFromGeoJSON turns a stored geometry string into a single Leaflet layer,
  // or null for an empty/invalid shape. geometryToLayer handles the [lon,lat] ->
  // [lat,lng] axis flip for us, so we never touch coordinate order by hand.
  function layerFromGeoJSON(str) {
    if (!str) return null;
    try {
      return L.GeoJSON.geometryToLayer(JSON.parse(str));
    } catch (e) {
      return null;
    }
  }

  // geomString serializes a layer back to a GeoJSON geometry string. toGeoJSON
  // emits [lon,lat] per spec — the same axis order the Go geo package expects.
  function geomString(layer) {
    return JSON.stringify(layer.toGeoJSON().geometry);
  }

  function fitToLayers(map, layers, fallback) {
    var group = L.featureGroup(layers);
    if (layers.length) {
      map.fitBounds(group.getBounds().pad(0.3), { animate: false });
    } else {
      map.setView(fallback || [20, 0], fallback ? 13 : 2, { animate: false });
    }
  }

  // ---- Editor -------------------------------------------------------------

  window.initRegionEditor = function (mapId, listId) {
    var listEl = document.getElementById(listId);
    if (!listEl) return;
    var map = L.map(mapId);
    darkTiles(map);

    // Geoman toolbar: only the tools that make sense for area geofences.
    map.pm.addControls({
      position: "topleft",
      drawMarker: false,
      drawCircleMarker: false,
      drawCircle: false,
      drawPolyline: false,
      drawText: false,
      drawPolygon: true,
      drawRectangle: true,
      editMode: true,
      dragMode: true,
      removalMode: true,
      cutPolygon: false,
      rotateMode: false,
    });
    map.pm.setGlobalOptions({ allowSelfIntersection: false });

    var blocks = []; // { el, hidden, status, layer }
    var active = null;
    var banner = document.getElementById(mapId + "-active");

    // While a polygon is being drawn, clicks place vertices — they must not be
    // treated as region selections. drawTarget locks onto the region that was
    // selected when drawing began, captured here so a vertex landing on top of
    // another region's polygon can't hijack the selection mid-draw.
    var drawing = false;
    var drawTarget = null;
    map.on("pm:drawstart", function () { drawing = true; drawTarget = active; });
    map.on("pm:drawend", function () { drawing = false; });

    function setStatus(b) {
      if (b.status) b.status.textContent = b.layer ? "Custom area" : "No area yet";
    }
    function serialize(b) {
      b.hidden.value = b.layer ? geomString(b.layer) : "";
      setStatus(b);
    }
    function restyle() {
      blocks.forEach(function (b, i) {
        if (b.layer && b.layer.setStyle) b.layer.setStyle(styleFor(i, b === active));
      });
    }
    function setActive(b) {
      active = b;
      blocks.forEach(function (x) {
        var on = x === b;
        x.el.classList.toggle("region-active", on);
        // The Edit button is the visible selection affordance: it lights up and
        // reads "Editing" for the active region, "Edit" otherwise.
        if (x.edit) {
          x.edit.classList.toggle("btn-primary", on);
          x.edit.textContent = on ? "Editing" : "Edit";
        }
      });
      if (banner) banner.textContent = b ? regionName(b) : "—";
      restyle();
    }
    function regionName(b) {
      var display = b.el.querySelector('input[name="region_display"]');
      var token = b.el.querySelector('input[name="region_token"]');
      return (display && display.value.trim()) || (token && token.value.trim()) || "this region";
    }

    function bindLayer(b, layer) {
      layer.addTo(map);
      // Re-serialize after any vertex edit or whole-shape drag.
      layer.on("pm:edit pm:update pm:dragend pm:markerdragend", function () { serialize(b); });
      layer.on("click", function () { if (!drawing) setActive(b); });
      b.layer = layer;
    }
    function attach(b, layer) {
      if (b.layer) map.removeLayer(b.layer); // one shape per region
      bindLayer(b, layer);
      serialize(b);
      restyle();
    }
    function clearShape(b) {
      if (b.layer) {
        map.removeLayer(b.layer);
        b.layer = null;
      }
      serialize(b);
    }

    // reconcile() syncs our block list with the DOM, which the page mutates as
    // the admin adds/removes region blocks (addBlock/removeBlock in the template).
    function reconcile() {
      var els = Array.prototype.slice.call(listEl.querySelectorAll(".region-block"));
      // Drop blocks whose DOM node is gone.
      blocks = blocks.filter(function (b) {
        if (els.indexOf(b.el) === -1) {
          if (b.layer) map.removeLayer(b.layer);
          if (active === b) active = null;
          return false;
        }
        return true;
      });
      // Register newly added blocks.
      var added = [];
      els.forEach(function (el) {
        if (blocks.some(function (b) { return b.el === el; })) return;
        var b = {
          el: el,
          hidden: el.querySelector('input[name="region_geojson"]'),
          status: el.querySelector(".region-shape-status"),
          edit: el.querySelector(".region-edit-btn"),
          layer: null,
        };
        var layer = layerFromGeoJSON(b.hidden && b.hidden.value);
        if (layer) bindLayer(b, layer);
        setStatus(b);
        // Selection is driven by the explicit Edit button (and clicking the shape on
        // the map) — the card itself isn't clickable, which read as unintuitive.
        if (b.edit) b.edit.addEventListener("click", function () { if (!drawing) setActive(b); });
        var clearBtn = el.querySelector(".region-clear");
        if (clearBtn) clearBtn.addEventListener("click", function (e) {
          e.stopPropagation();
          setActive(b);
          clearShape(b);
        });
        blocks.push(b);
        added.push(b);
      });
      // Reorder to match DOM order (keeps palette colors stable by position).
      blocks.sort(function (a, b) {
        return els.indexOf(a.el) - els.indexOf(b.el);
      });
      restyle();
      // A newly added block becomes the active one (so clicking "Add region"
      // selects it); otherwise keep the current selection, defaulting to the last.
      if (added.length) setActive(added[added.length - 1]);
      else if (!active && blocks.length) setActive(blocks[blocks.length - 1]);
    }

    // A freshly drawn shape fills the region that was selected when drawing began
    // (drawTarget) — but only if that region has no shape yet. If it already has
    // one, or nothing was selected, the shape goes into a brand-new region, so
    // drawing never overwrites an existing area (e.g. drawing the US inside an
    // existing North America region adds the US rather than replacing NA).
    map.on("pm:create", function (e) {
      var target = drawTarget;
      if (!target || target.layer) {
        if (typeof addBlock === "function") {
          addBlock("region");
          reconcile(); // registers + selects the new block synchronously
        }
        target = active || (blocks.length ? blocks[blocks.length - 1] : null);
      }
      if (!target) { map.removeLayer(e.layer); return; } // nothing to attach to
      setActive(target);
      attach(target, e.layer);
      if (target.el.scrollIntoView) target.el.scrollIntoView({ block: "nearest" });
    });
    // Toolbar removal: clear whichever region owned the removed layer.
    map.on("pm:remove", function (e) {
      var b = blocks.filter(function (x) { return x.layer === e.layer; })[0];
      if (b) { b.layer = null; serialize(b); }
    });

    new MutationObserver(reconcile).observe(listEl, { childList: true });
    reconcile();
    fitToLayers(map, blocks.map(function (b) { return b.layer; }).filter(Boolean));
  };

  // ---- Read-only viewer ---------------------------------------------------

  // regionMapView renders a location-picker map (no region outlines — those are
  // just noise here). opts:
  //   pickURL — clicking the map navigates here with lat/lon appended so the
  //             server resolves the region def for that point (must end "?"/"&");
  //   preview — {lat, lon} of an already-picked point, shown as a marker;
  //   bounds  — [[minLat,minLon],[maxLat,maxLon]] to frame the org's regions.
  window.regionMapView = function (mapId, opts) {
    opts = opts || {};
    var map = L.map(mapId, { scrollWheelZoom: false });
    darkTiles(map);
    if (opts.pickURL) {
      map.on("click", function (e) {
        window.location = opts.pickURL + "lat=" + e.latlng.lat.toFixed(6) + "&lon=" + e.latlng.lng.toFixed(6);
      });
    }
    // onPick drops/moves a marker and reports the point instead of navigating —
    // used by in-page pickers (e.g. the serial setup form) that keep the value
    // client-side rather than round-tripping to the server.
    if (opts.onPick) {
      var picked = null;
      map.on("click", function (e) {
        if (picked) picked.setLatLng(e.latlng);
        else picked = L.circleMarker(e.latlng, {
          radius: 7, color: "#fff", weight: 2, fillColor: "#fff", fillOpacity: 0.9,
        }).addTo(map);
        opts.onPick(e.latlng.lat, e.latlng.lng);
      });
    }
    var fit = [];
    if (opts.bounds) fit.push(opts.bounds[0], opts.bounds[1]);
    if (opts.preview) {
      L.circleMarker([opts.preview.lat, opts.preview.lon], {
        radius: 7, color: "#fff", weight: 2, fillColor: "#fff", fillOpacity: 0.9,
      }).addTo(map);
      fit.push([opts.preview.lat, opts.preview.lon]);
    }
    if (fit.length) {
      var b = L.latLngBounds(fit);
      // A zero-size box (single point — one repeater, or a preview with no
      // region box) can't be fit; center on it at a neighborhood zoom instead.
      if (b.getNorthEast().equals(b.getSouthWest())) map.setView(b.getCenter(), 11, { animate: false });
      else map.fitBounds(b.pad(0.2), { animate: false });
    } else {
      map.setView([20, 0], 2, { animate: false });
    }
  };
})();
