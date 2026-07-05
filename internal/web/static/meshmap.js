// meshBaseLayers adds the dark (default) and a light basemap to a map, with a
// layers control to toggle between them. The choice is remembered in localStorage
// so it carries across maps and pages. Dark matches the UI; the light layer (CARTO
// Voyager) reads better in bright conditions and for some eyes. The control is
// left expanded — Leaflet's collapsed toggle needs an icon asset we don't bundle.
function meshBaseLayers(map) {
  var attribution = "&copy; OpenStreetMap &copy; CARTO";
  var dark = L.tileLayer("https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png", {
    maxZoom: 19,
    subdomains: "abcd",
    attribution: attribution,
  });
  var light = L.tileLayer("https://{s}.basemaps.cartocdn.com/rastertiles/voyager/{z}/{x}/{y}{r}.png", {
    maxZoom: 19,
    subdomains: "abcd",
    attribution: attribution,
  });
  var pref = null;
  try {
    pref = localStorage.getItem("mt_map_base");
  } catch (e) {
    /* storage unavailable (private mode); fall back to dark */
  }
  (pref === "light" ? light : dark).addTo(map);
  L.control.layers({ Dark: dark, Light: light }, null, { position: "topright", collapsed: false }).addTo(map);
  map.on("baselayerchange", function (e) {
    try {
      localStorage.setItem("mt_map_base", e.name === "Light" ? "light" : "dark");
    } catch (e) {
      /* ignore */
    }
  });
}

// meshMap renders a dark-mode Leaflet map of points into the element with the
// given id. pts is an array of {name, lat, lon}. Does nothing if pts is empty.
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
  // Turn off every Leaflet animation so the map paints once, in its final
  // position, with no flash on load: zoomAnimation (zoom transitions),
  // fadeAnimation (tiles fading in), markerZoomAnimation (markers scaling).
  var map = L.map(elId, {
    scrollWheelZoom: false,
    zoomAnimation: false,
    fadeAnimation: false,
    markerZoomAnimation: false,
  });
  meshBaseLayers(map);
  var group = L.featureGroup(
    pts.map(function (p) {
      return L.circleMarker([p.lat, p.lon], {
        radius: 7,
        color: "#4dabf7",
        weight: 2,
        fillColor: "#4dabf7",
        fillOpacity: 0.6,
      }).bindPopup(p.name);
    })
  ).addTo(map);
  // Set the view exactly once, non-animated, so the map paints in its final
  // position with no fit/zoom flash on load. A lone repeater would otherwise fit
  // at max zoom, so give it a fixed neighborhood zoom for context instead.
  if (pts.length === 1) {
    map.setView([pts[0].lat, pts[0].lon], 15, { animate: false });
  } else {
    map.fitBounds(group.getBounds().pad(0.3), { animate: false });
  }
}
