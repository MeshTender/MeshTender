// meshmap.js — MapLibre maps of repeater points. The basemap, the map factory and
// the framing helper come from basemap.js, which the page must load first.

// meshMap renders a map of points into the element with the given id. pts is an
// array of {name, lat, lon}. Does nothing if pts is empty.
function meshMap(elId, pts) {
  if (!pts || !pts.length) return;
  renderMeshMap(elId, pts);
}

// meshMapFromURL loads the points from a same-origin JSON endpoint (an array of
// {name, lat, lon}) and renders them. Used by the public org pages so the point
// set is fetched (and cacheable) rather than inlined into the HTML. On a fetch
// error or empty set the map container is left blank.
function meshMapFromURL(elId, url) {
  fetch(url, { headers: { Accept: "application/json" } })
    .then(function (r) {
      return r.ok ? r.json() : [];
    })
    .then(function (pts) {
      if (Array.isArray(pts) && pts.length) renderMeshMap(elId, pts);
    })
    .catch(function () {
      /* leave the map container empty if points can't be loaded */
    });
}

// renderMeshMap draws the given points into the element; shared by meshMap (inline
// points) and meshMapFromURL (fetched points).
function renderMeshMap(elId, pts) {
  var ACCENT = "#4dabf7";

  var features = pts.map(function (p, i) {
    return {
      type: "Feature",
      // A stable feature id: clustering needs one to tell points apart, and it also
      // means a query against the source counts a point once rather than once per
      // tile it lands in.
      id: i,
      geometry: { type: "Point", coordinates: [p.lon, p.lat] },
      properties: { name: String(p.name == null ? "" : p.name) },
    };
  });

  // Clustering is MapLibre's own (the source does it), which is what retires the
  // markercluster plugin: co-located repeaters collapse into one counted circle
  // instead of stacking invisibly on top of each other.
  function overlays(map) {
    map.addSource("points", {
      type: "geojson",
      data: { type: "FeatureCollection", features: features },
      cluster: true,
      clusterRadius: 50,
      clusterMaxZoom: 16,
    });
    map.addLayer({
      id: "clusters",
      type: "circle",
      source: "points",
      filter: ["has", "point_count"],
      paint: {
        // Grow the disc with the count so a big cluster reads as big, but keep the
        // steps small — these sit on top of the basemap, not over it.
        "circle-radius": ["step", ["get", "point_count"], 14, 10, 18, 50, 22],
        "circle-color": ACCENT,
        "circle-opacity": 0.6,
        "circle-stroke-width": 2,
        "circle-stroke-color": ACCENT,
      },
    });
    map.addLayer({
      id: "cluster-count",
      type: "symbol",
      source: "points",
      filter: ["has", "point_count"],
      layout: { "text-field": ["get", "point_count_abbreviated"], "text-font": window.meshFont, "text-size": 12 },
      paint: { "text-color": "#ffffff" },
    });
    map.addLayer({
      id: "points",
      type: "circle",
      source: "points",
      filter: ["!", ["has", "point_count"]],
      paint: {
        "circle-radius": 7,
        "circle-color": ACCENT,
        "circle-opacity": 0.6,
        "circle-stroke-width": 2,
        "circle-stroke-color": ACCENT,
      },
    });
  }

  var map = window.meshCreateMap(elId, { scrollZoom: false, overlays: overlays });

  // Clicking a cluster zooms to the point where it splits up, which is the only way
  // to reach the repeaters inside one.
  map.on("click", "clusters", function (e) {
    var f = e.features && e.features[0];
    if (!f) return;
    map.getSource("points").getClusterExpansionZoom(f.properties.cluster_id).then(function (zoom) {
      map.easeTo({ center: f.geometry.coordinates, zoom: zoom });
    });
  });

  map.on("click", "points", function (e) {
    var f = e.features && e.features[0];
    if (!f) return;
    // setText, not setHTML: a repeater name is operator-supplied, and this is the
    // boundary where it would otherwise be parsed as markup.
    new maplibregl.Popup().setLngLat(f.geometry.coordinates).setText(f.properties.name).addTo(map);
  });

  ["clusters", "points"].forEach(function (layer) {
    map.on("mouseenter", layer, function () {
      map.getCanvas().style.cursor = "pointer";
    });
    map.on("mouseleave", layer, function () {
      map.getCanvas().style.cursor = "";
    });
  });

  // Frame once, without animating, so the map paints in its final position with no
  // fit/zoom flash on load. A lone repeater would otherwise fit at max zoom, so it
  // gets a fixed neighborhood zoom for context instead.
  window.meshFrame(
    map,
    pts.map(function (p) {
      return [p.lon, p.lat];
    }),
    { padding: 40, pointZoom: 15 },
  );
}
