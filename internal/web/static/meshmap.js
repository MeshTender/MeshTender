// meshMap renders a dark-mode Leaflet map of points into the element with the
// given id. pts is an array of {name, lat, lon}. Does nothing if pts is empty.
function meshMap(elId, pts) {
  if (!pts || !pts.length) return;
  // Turn off every Leaflet animation so the map paints once, in its final
  // position, with no flash on load: zoomAnimation (zoom transitions),
  // fadeAnimation (tiles fading in), markerZoomAnimation (markers scaling).
  var map = L.map(elId, {
    scrollWheelZoom: false,
    zoomAnimation: false,
    fadeAnimation: false,
    markerZoomAnimation: false,
  });
  // CARTO "dark matter" basemap (OpenStreetMap data) for a dark UI.
  L.tileLayer("https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png", {
    maxZoom: 19,
    subdomains: "abcd",
    attribution: "&copy; OpenStreetMap &copy; CARTO",
  }).addTo(map);
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
