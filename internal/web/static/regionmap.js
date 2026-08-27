// regionmap.js — Leaflet + Geoman map for org config regions.
//
// A region's geofence is a GeoJSON Polygon/MultiPolygon geometry; no geometry means
// the region is a draft that applies nowhere until an area is drawn. Two entry
// points:
//
//   initRegionArea(mapId, opts)  — the admin area workspace: draw/edit exactly one
//       region's polygon, with the org's other regions outlined as read-only context.
//   regionMapView(mapId, opts) — read-only: a location picker (with an optional
//       previewed location marker).
(function () {
  // Colors come from the server (web.RegionPalette) so a legend swatch and the
  // polygon it labels can never drift apart. FALLBACK covers a caller that passes
  // none.
  var FALLBACK = "#4dabf7";

  // Breathing room when framing a map. FIT_PADDING is in screen pixels, so it stays
  // a consistent visual margin no matter how large the area is — a geographic pad
  // would scale with the region and zoom a big one right back out. PRIMARY_CONTEXT
  // is the small geographic nudge that shows a little of what surrounds the primary
  // region; keep it low, because fitBounds then rounds *down* to an integer zoom and
  // any slack here gets magnified by that rounding.
  var FIT_PADDING = [24, 24];
  var PRIMARY_CONTEXT = 0.08;

  // editableStyle is the one shape the current page can modify: drawn prominently so
  // it always reads which polygon the tools will act on.
  function editableStyle(color) {
    var c = color || FALLBACK;
    return { color: c, weight: 3, opacity: 1, fillColor: c, fillOpacity: 0.25 };
  }

  // Region fills stack: nested regions overlap, so N of them at opacity a cover
  // 1-(1-a)^N of the basemap. Orgs run deep hierarchies (3 levels is common, 5-6 is
  // planned), and at a=0.25 six levels reach 82% — the map underneath disappears.
  // These are chosen so six levels stay readable (~43% matched, ~22% context) while
  // three land at ~25%, the weight a single region used to carry. Raise them and
  // deep hierarchies turn to mud; the stroke, not the fill, is what identifies a
  // region.
  var FILL_MATCHED = 0.09;
  var FILL_CONTEXT = 0.04;

  // outlineStyle is a region drawn for reference — a neighbor on the area editor, or
  // any region on the read-only config map. Dimmed by default; a region that applies
  // at the previewed location is brought forward, which is what explains why its
  // token shows up in the assembled config.
  function outlineStyle(color, matched) {
    var c = color || FALLBACK;
    return {
      color: c,
      weight: matched ? 3 : 1.5,
      opacity: matched ? 1 : 0.55,
      fillColor: c,
      fillOpacity: matched ? FILL_MATCHED : FILL_CONTEXT,
      interactive: false,
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

  // ---- Area editor (one region) -------------------------------------------

  // initRegionArea drives the per-region area workspace. It edits exactly one
  // shape — the region whose page this is — and renders the org's other regions as
  // non-interactive outlines for spatial reference. opts:
  //   input    — id of the hidden field carrying the GeoJSON geometry (required);
  //   status   — id of an element to show "Custom area" / "No area yet" in;
  //   clear    — id of a button that removes the shape;
  //   name     — this region's label, used in the shape's tooltip;
  //   color    — this region's palette color (see web.RegionPalette);
  //   siblings — [{name, geojson, color}] drawn read-only.
  //
  // Everything the form submits lives in the hidden input, so the server sees the
  // same shape whether it was drawn, edited, or cleared.
  window.initRegionArea = function (mapId, opts) {
    opts = opts || {};
    var input = document.getElementById(opts.input);
    if (!input) return;
    var statusEl = opts.status ? document.getElementById(opts.status) : null;
    var map = L.map(mapId);
    meshBaseLayers(map);

    // Geoman toolbar: only the tools that make sense for an area geofence. Drawing
    // is enabled only while there's no shape — one region, one polygon, so a second
    // draw would be ambiguous.
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

    var layer = null;

    function serialize() {
      input.value = layer ? geomString(layer) : "";
      if (statusEl) statusEl.textContent = layer ? "Custom area" : "No area yet";
      // With a shape present, drawing is off (edit/drag/remove stay on); once it's
      // cleared, the draw tools come back.
      map.pm.Toolbar.setButtonDisabled("drawPolygon", !!layer);
      map.pm.Toolbar.setButtonDisabled("drawRectangle", !!layer);
    }

    function bind(l) {
      layer = l;
      l.setStyle(editableStyle(opts.color));
      if (opts.name) l.bindTooltip(String(opts.name), { sticky: true });
      l.addTo(map);
      // Re-serialize after any vertex edit or whole-shape drag.
      l.on("pm:edit pm:update pm:dragend pm:markerdragend", serialize);
      serialize();
    }

    function clear() {
      if (layer) {
        map.removeLayer(layer);
        layer = null;
      }
      serialize();
    }

    map.on("pm:create", function (e) {
      // Belt and braces: the draw buttons are disabled while a shape exists, but if
      // one is somehow drawn anyway, replace rather than orphan a second polygon.
      if (layer) map.removeLayer(layer);
      bind(e.layer);
    });
    map.on("pm:remove", function (e) {
      if (e.layer === layer) clear();
    });

    // Siblings first, so the edited shape sits on top of them.
    var context = [];
    (opts.siblings || []).forEach(function (sib) {
      if (!sib) return;
      var l = layerFromGeoJSON(sib.geojson);
      if (!l) return;
      l.setStyle(outlineStyle(sib.color));
      if (sib.name) l.bindTooltip(String(sib.name));
      l.addTo(map);
      context.push(l);
    });

    var existing = layerFromGeoJSON(input.value);
    if (existing) bind(existing);
    else serialize();

    var clearBtn = opts.clear ? document.getElementById(opts.clear) : null;
    if (clearBtn) clearBtn.addEventListener("click", clear);

    // Frame the edited shape when there is one, otherwise the siblings — a brand-new
    // region should open looking at where its neighbors are.
    fitToLayers(map, layer ? [layer] : context);
  };

  // ---- Read-only viewer ---------------------------------------------------

  // regionMapView renders a read-only region map. opts:
  //   regions — [{name, geojson, matched, color, primary}] drawn as non-interactive
  //             outlines, the ones matching the previewed location brought forward.
  //             Omit for a plain location picker (the console and serial-setup
  //             callers do);
  //   pickURL — clicking the map navigates here with lat/lon appended so the
  //             server resolves the region def for that point (must end "?"/"&");
  //   preview — {lat, lon} of an already-picked point, shown as a marker;
  //   bounds  — [[minLat,minLon],[maxLat,maxLon]] to frame the org's regions.
  window.regionMapView = function (mapId, opts) {
    opts = opts || {};
    var map = L.map(mapId, { scrollWheelZoom: false });
    meshBaseLayers(map);

    // Region outlines go on first so the picked-location marker stays on top. They
    // are non-interactive (see outlineStyle), so a click still reaches the map and
    // drops a pin rather than being swallowed by a polygon.
    var drawn = [];
    var primary = null;
    (opts.regions || []).forEach(function (r) {
      if (!r) return;
      var l = layerFromGeoJSON(r.geojson);
      if (!l) return;
      l.setStyle(outlineStyle(r.color, r.matched));
      if (r.name) l.bindTooltip(String(r.name));
      l.addTo(map);
      drawn.push(l);
      if (r.primary) primary = l;
    });
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
    // With no explicit bounds, open framed on the primary region — where the org
    // actually operates — with room around it for context, rather than zooming out
    // to contain a nationwide parent. Without a primary, frame everything drawn.
    if (!opts.bounds && drawn.length) {
      var rb = L.featureGroup(primary ? [primary] : drawn).getBounds();
      if (primary) rb = rb.pad(PRIMARY_CONTEXT);
      fit.push(rb.getSouthWest(), rb.getNorthEast());
    }
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
      else map.fitBounds(b, { animate: false, padding: FIT_PADDING });
    } else {
      map.setView([20, 0], 2, { animate: false });
    }
    // Return the map so callers that show it inside an initially-hidden container
    // (e.g. a modal) can invalidateSize() once it becomes visible.
    return map;
  };
})();
