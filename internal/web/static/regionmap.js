// regionmap.js — MapLibre + Terra Draw maps for org config regions.
//
// A region's geofence is a GeoJSON Polygon/MultiPolygon geometry; no geometry means
// the region is a draft that applies nowhere until an area is drawn. Two entry
// points:
//
//   initRegionArea(mapId, opts)  — the admin area workspace: draw/edit exactly one
//       region's polygon, with the org's other regions outlined as read-only context.
//   regionMapView(mapId, opts) — read-only: a location picker (with an optional
//       previewed location marker).
//
// Load after maplibre-gl.js, terra-draw.js, terra-draw-maplibre-gl-adapter.js and
// basemap.js.
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
  var FIT_PADDING = 24;
  var PRIMARY_CONTEXT = 0.08;

  // Region fills stack: nested regions overlap, so N of them at opacity a cover
  // 1-(1-a)^N of the basemap. Orgs run deep hierarchies (3 levels is common, 5-6 is
  // planned), and at a=0.25 six levels reach 82% — the map underneath disappears.
  // These are chosen so six levels stay readable (~43% matched, ~22% context) while
  // three land at ~25%, the weight a single region used to carry. Raise them and
  // deep hierarchies turn to mud; the stroke, not the fill, is what identifies a
  // region.
  var FILL_MATCHED = 0.09;
  var FILL_CONTEXT = 0.04;

  // The one shape the area editor can modify is drawn prominently, so it always
  // reads which polygon the tools will act on.
  var EDIT_FILL_OPACITY = 0.25;
  var EDIT_OUTLINE_WIDTH = 3;

  // The picked-location marker, on both the preview and the click-to-pick paths.
  var PICK_COLOR = "#ffffff";

  // How near, in screen pixels, a click counts as "the same point" while drawing.
  //
  // This is the floor on how finely an outline can be drawn, because Terra Draw
  // measures it against the previous vertex as well as the first one: at its default
  // of 40 the next click has to land more than 40px away or it is read as closing the
  // ring rather than extending it, which makes tracing anything detailed — a
  // shoreline, a county line — impossible without zooming way in. It is also the grab
  // radius for a vertex in select mode, where 40 means dense vertices fight over the
  // pointer.
  //
  // 12 leaves the closing point a 24px target, which is still a comfortable click and
  // the usual floor for a touch target, so finishing by clicking the first vertex
  // stays workable. Going much below this starts trading away that target.
  var POINTER_DISTANCE = 12;

  function parseGeometry(str) {
    if (!str) return null;
    try {
      var g = JSON.parse(str);
      return g && g.coordinates && g.coordinates.length ? g : null;
    } catch (e) {
      return null;
    }
  }

  // polygonsOf splits any stored geometry into a list of Polygon coordinate arrays.
  // Terra Draw edits Polygons only, so a stored MultiPolygon is loaded as one
  // editable shape per part and reassembled on the way out (see serialize below) —
  // that way existing multi-part regions stay editable rather than read-only.
  function polygonsOf(geometry) {
    if (!geometry) return [];
    if (geometry.type === "Polygon") return [geometry.coordinates];
    if (geometry.type === "MultiPolygon") return geometry.coordinates;
    return [];
  }

  // regionFeatures turns the server's region list into one FeatureCollection. The
  // matched/context distinction rides on each feature's properties and is resolved
  // by a paint expression, so N nested regions still cost two layers, not 2N.
  function regionFeatures(regions) {
    var features = [];
    (regions || []).forEach(function (r) {
      if (!r) return;
      var geom = parseGeometry(r.geojson);
      if (!geom) return;
      features.push({
        type: "Feature",
        // A stable feature id, so a polygon spanning several tiles is still one
        // feature to anything querying the source rather than one hit per tile.
        id: features.length,
        geometry: geom,
        properties: {
          color: r.color || FALLBACK,
          matched: !!r.matched,
          primary: !!r.primary,
        },
      });
    });
    return features;
  }

  // addRegionLayers draws a set of regions as outlines: dimmed by default, with any
  // region that applies at the previewed location brought forward — which is what
  // explains why its token shows up in the assembled config.
  //
  // The outlines carry no name labels. A symbol layer over a polygon source labels
  // each tile the polygon covers, so a nationwide region picks up one label per tile
  // and reads as several regions rather than one. What names a region is the legend
  // beside the map, whose swatch comes from the same server palette as the stroke.
  function addRegionLayers(map, id, features) {
    map.addSource(id, { type: "geojson", data: { type: "FeatureCollection", features: features } });
    map.addLayer({
      id: id + "-fill",
      type: "fill",
      source: id,
      paint: {
        "fill-color": ["get", "color"],
        "fill-opacity": ["case", ["get", "matched"], FILL_MATCHED, FILL_CONTEXT],
      },
    });
    map.addLayer({
      id: id + "-line",
      type: "line",
      source: id,
      paint: {
        "line-color": ["get", "color"],
        "line-width": ["case", ["get", "matched"], 3, 1.5],
        "line-opacity": ["case", ["get", "matched"], 1, 0.55],
      },
    });
  }

  // markerFeature is the picked/previewed location, drawn as a small white dot.
  function addMarkerLayer(map, id, lngLat) {
    map.addSource(id, {
      type: "geojson",
      data: {
        type: "FeatureCollection",
        features: lngLat ? [{ type: "Feature", geometry: { type: "Point", coordinates: lngLat }, properties: {} }] : [],
      },
    });
    map.addLayer({
      id: id + "-point",
      type: "circle",
      source: id,
      paint: {
        "circle-radius": 7,
        "circle-color": PICK_COLOR,
        "circle-opacity": 0.9,
        "circle-stroke-width": 2,
        "circle-stroke-color": PICK_COLOR,
      },
    });
  }

  // notSelfIntersecting builds the geofence validity rule: a polygon with a crossed
  // edge has no well-defined inside, which is the constraint Geoman enforced as
  // allowSelfIntersection: false.
  //
  // Which updates it applies to is the whole subtlety. Terra Draw runs validation on
  // provisional updates too — every pointer move, and every click while the ring is
  // still open — and a polygon under construction is auto-closed back to its first
  // point. So an outline that runs south, then east, and then wants to head farther
  // south crosses that closing edge *in the intermediate state*, even though the
  // finished shape never does. Enforcing there rejects the click with no feedback at
  // all: the cursor simply stops adding points, and concave areas — a state whose
  // coastline doubles back, say — cannot be drawn. Judge the finished shape instead.
  function notSelfIntersecting(updateTypes, onInvalid) {
    return function (feature, context) {
      if (updateTypes.indexOf(context.updateType) === -1) return { valid: true };
      var result = terraDraw.ValidateNotSelfIntersecting(feature);
      // Terra Draw refuses the update and says nothing, so an operator who has just
      // crossed their own outline sees the tool stop responding with no reason
      // given. Report it.
      if (!result.valid && onInvalid) onInvalid();
      return result;
    };
  }

  // ---- Area editor (one region) -------------------------------------------

  // DrawControl is the editor's toolbar. Terra Draw is headless — it renders shapes
  // and handles the pointer work but ships no UI at all — so unlike Leaflet-Geoman,
  // which brought its own toolbar, the buttons are ours. They are built here rather
  // than in the template so the modes and the buttons that switch them stay in one
  // file, and they carry data-testid hooks for the browser tests.
  function DrawControl(actions) {
    this._actions = actions;
  }

  DrawControl.prototype.onAdd = function () {
    var self = this;
    var el = document.createElement("div");
    el.className = "maplibregl-ctrl maplibregl-ctrl-group mesh-draw-ctrl";
    this._buttons = {};
    [
      { key: "polygon", label: "Draw" },
      { key: "rectangle", label: "Rectangle" },
      { key: "select", label: "Edit" },
    ].forEach(function (spec) {
      var b = document.createElement("button");
      b.type = "button";
      b.textContent = spec.label;
      b.setAttribute("data-testid", "region-draw-" + spec.key);
      b.addEventListener("click", function () {
        self._actions.setMode(spec.key);
      });
      el.appendChild(b);
      self._buttons[spec.key] = b;
    });
    this._el = el;
    return el;
  };

  // sync reflects the editor's state on the toolbar: with a shape present the draw
  // tools are off (one region, one area, so a second draw would be ambiguous) and
  // editing is on; with none, the reverse.
  DrawControl.prototype.sync = function (mode, hasShape) {
    if (!this._buttons) return;
    this._buttons.polygon.disabled = hasShape;
    this._buttons.rectangle.disabled = hasShape;
    this._buttons.select.disabled = !hasShape;
    for (var key in this._buttons) {
      if (Object.prototype.hasOwnProperty.call(this._buttons, key)) {
        this._buttons[key].setAttribute("aria-pressed", String(key === mode));
      }
    }
  };

  DrawControl.prototype.onRemove = function () {
    if (this._el && this._el.parentNode) this._el.parentNode.removeChild(this._el);
  };

  // initRegionArea drives the per-region area workspace. It edits exactly one
  // shape — the region whose page this is — and renders the org's other regions as
  // read-only outlines for spatial reference. opts:
  //   input    — id of the hidden field carrying the GeoJSON geometry (required);
  //   status   — id of an element to show "Custom area" / "No area yet" in;
  //   clear    — id of a button that removes the shape;
  //   name     — this region's label, used on the drawn shape;
  //   color    — this region's palette color (see web.RegionPalette);
  //   siblings — [{name, geojson, color}] drawn read-only.
  //
  // Everything the form submits lives in the hidden input, so the server sees the
  // same shape whether it was drawn, edited, or cleared — and, because the input is
  // the single source of truth, the editor can be rebuilt from it after a basemap
  // swap without losing work.
  window.initRegionArea = function (mapId, opts) {
    opts = opts || {};
    var input = document.getElementById(opts.input);
    if (!input) return;
    var statusEl = opts.status ? document.getElementById(opts.status) : null;
    var color = opts.color || FALLBACK;
    var siblings = regionFeatures(opts.siblings);

    var draw = null;
    var control = new DrawControl({
      setMode: function (mode) {
        if (!draw) return;
        draw.setMode(mode);
        sync();
      },
    });

    // shapes are the polygons the operator is editing. Terra Draw's store also holds
    // the select mode's own furniture — selection points and midpoints — so filter
    // to polygons rather than taking the snapshot whole.
    function shapes() {
      if (!draw) return [];
      return draw.getSnapshot().filter(function (f) {
        return f.geometry && f.geometry.type === "Polygon";
      });
    }

    function serialize() {
      var polys = shapes();
      if (!polys.length) {
        input.value = "";
      } else if (polys.length === 1) {
        input.value = JSON.stringify(polys[0].geometry);
      } else {
        // Reassemble the parts of a multi-part region exactly as they arrived.
        input.value = JSON.stringify({
          type: "MultiPolygon",
          coordinates: polys.map(function (f) {
            return f.geometry.coordinates;
          }),
        });
      }
      if (statusEl) statusEl.textContent = polys.length ? "Custom area" : "No area yet";
      sync();
    }

    function sync() {
      control.sync(draw ? draw.getMode() : "select", shapes().length > 0);
    }

    // rejectCrossed explains a refused edit in the status line. serialize() overwrites
    // it as soon as there is a valid shape again.
    function rejectCrossed() {
      if (statusEl) statusEl.textContent = "Edges cross — an area can't overlap itself";
    }

    function buildDraw(map) {
      var styles = {
        fillColor: color,
        fillOpacity: EDIT_FILL_OPACITY,
        outlineColor: color,
        outlineWidth: EDIT_OUTLINE_WIDTH,
      };
      var flags = {
        feature: {
          // An edit lands as a commit, so that is where a dragged vertex is judged.
          validation: notSelfIntersecting(["commit", "finish"], rejectCrossed),
          draggable: true,
          coordinates: { midpoints: true, draggable: true, deletable: true },
        },
      };
      return new terraDraw.TerraDraw({
        adapter: new terraDrawMaplibreGlAdapter.TerraDrawMapLibreGLAdapter({ map: map }),
        modes: [
          // Drawing is judged only when the operator finishes; see notSelfIntersecting.
          new terraDraw.TerraDrawPolygonMode({
            styles: styles,
            validation: notSelfIntersecting(["finish"], rejectCrossed),
            editable: true,
            pointerDistance: POINTER_DISTANCE,
          }),
          new terraDraw.TerraDrawRectangleMode({ styles: styles }),
          new terraDraw.TerraDrawSelectMode({
            flags: { polygon: flags, rectangle: flags },
            pointerDistance: POINTER_DISTANCE,
          }),
        ],
      });
    }

    function loadShapes(geometry) {
      var polys = polygonsOf(geometry);
      if (!polys.length) return;
      draw.addFeatures(
        polys.map(function (coords) {
          return {
            type: "Feature",
            geometry: { type: "Polygon", coordinates: coords },
            properties: { mode: "polygon" },
          };
        }),
      );
    }

    // overlays runs on first load and again after every basemap swap, because
    // setStyle discards the sibling layers *and* the layers Terra Draw's adapter
    // renders into. Rebuilding the editor from the hidden input is lossless — the
    // input is always current — and is simpler than trying to re-register an adapter
    // against a style that no longer has its sources.
    function overlays(map) {
      addRegionLayers(map, "siblings", siblings);
      var geometry = parseGeometry(input.value);
      if (draw) {
        try {
          draw.stop();
        } catch (e) {
          /* already torn down with the old style */
        }
      }
      draw = buildDraw(map);
      draw.start();
      // Only a finished shape reaches the field. Terra Draw keeps the ring currently
      // being drawn in the same store as finished ones, so serializing on every
      // change would put a half-drawn outline into the form — and would leave a
      // self-intersecting one there after its finish was refused, letting Save
      // persist a geofence with no inside. A change in select mode, by contrast, is
      // a real edit of a real shape.
      draw.on("change", function () {
        if (draw.getMode() === "select") serialize();
        else sync();
      });
      draw.on("finish", function () {
        // Drawing is done: hand the operator the tool that edits what they drew.
        draw.setMode("select");
        serialize();
      });
      loadShapes(geometry);
      window.MESHTENDER_MAPS[mapId].draw = draw;
      draw.setMode(geometry ? "select" : "polygon");
      serialize();
    }

    var map = window.meshCreateMap(mapId, { overlays: overlays });
    map.addControl(control, "top-right");

    var clearBtn = opts.clear ? document.getElementById(opts.clear) : null;
    if (clearBtn) {
      clearBtn.addEventListener("click", function () {
        if (!draw) return;
        draw.clear();
        draw.setMode("polygon");
        serialize();
      });
    }

    // Frame the edited shape when there is one, otherwise the siblings — a brand-new
    // region should open looking at where its neighbors are.
    var own = window.meshCoordsOf(parseGeometry(input.value));
    var context = [];
    siblings.forEach(function (f) {
      context = context.concat(window.meshCoordsOf(f.geometry));
    });
    window.meshFrame(map, own.length ? own : context, { padding: FIT_PADDING, pointZoom: 13 });
    return map;
  };

  // ---- Read-only viewer ---------------------------------------------------

  // regionMapView renders a read-only region map. opts:
  //   regions — [{name, geojson, matched, color, primary}] drawn as outlines, the
  //             ones matching the previewed location brought forward. Omit for a
  //             plain location picker (the console and serial-setup callers do);
  //   pickURL — clicking the map navigates here with lat/lon appended so the
  //             server resolves the region def for that point (must end "?"/"&");
  //   preview — {lat, lon} of an already-picked point, shown as a marker;
  //   bounds  — [[minLat,minLon],[maxLat,maxLon]] to frame the org's regions.
  window.regionMapView = function (mapId, opts) {
    opts = opts || {};
    var features = regionFeatures(opts.regions);
    var preview = opts.preview ? [opts.preview.lon, opts.preview.lat] : null;
    var picked = preview;

    function overlays(map) {
      addRegionLayers(map, "regions", features);
      // The marker source goes on after the regions so the picked location always
      // sits on top of them.
      addMarkerLayer(map, "picked", picked);
    }

    var map = window.meshCreateMap(mapId, { scrollZoom: false, overlays: overlays });

    if (opts.pickURL) {
      map.on("click", function (e) {
        window.location = opts.pickURL + "lat=" + e.lngLat.lat.toFixed(6) + "&lon=" + e.lngLat.lng.toFixed(6);
      });
    }
    // onPick drops/moves a marker and reports the point instead of navigating —
    // used by in-page pickers (e.g. the serial setup form) that keep the value
    // client-side rather than round-tripping to the server.
    if (opts.onPick) {
      map.on("click", function (e) {
        picked = [e.lngLat.lng, e.lngLat.lat];
        var src = map.getSource("picked");
        if (src) {
          src.setData({
            type: "FeatureCollection",
            features: [{ type: "Feature", geometry: { type: "Point", coordinates: picked }, properties: {} }],
          });
        }
        opts.onPick(e.lngLat.lat, e.lngLat.lng);
      });
    }

    // Framing. An explicit bounds wins; otherwise open framed on the primary region —
    // where the org actually operates — with room around it for context, rather than
    // zooming out to contain a nationwide parent. Without a primary, frame everything
    // drawn. The previewed point is always kept in view.
    var coords = [];
    var pad = 0;
    if (opts.bounds) {
      coords.push([opts.bounds[0][1], opts.bounds[0][0]], [opts.bounds[1][1], opts.bounds[1][0]]);
    } else if (features.length) {
      // Read primary off the features, not the input list: regionFeatures drops
      // regions with no drawn area, so the two are not index-aligned.
      var primary = null;
      features.forEach(function (f) {
        if (f.properties.primary) primary = f;
      });
      (primary ? [primary] : features).forEach(function (f) {
        coords = coords.concat(window.meshCoordsOf(f.geometry));
      });
      if (primary) pad = PRIMARY_CONTEXT;
    }
    if (preview) coords.push(preview);
    window.meshFrame(map, coords, { padding: FIT_PADDING, pad: pad, pointZoom: 11 });

    // Return the map so callers that show it inside an initially-hidden container
    // (e.g. a modal) can resize() once it becomes visible.
    return map;
  };
})();
