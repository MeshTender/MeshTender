// meshMap renders a dark-mode Leaflet map of points into the element with the
// given id. pts is an array of {name, lat, lon}. Does nothing if pts is empty.
function meshMap(elId, pts) {
  if (!pts || !pts.length) return;
  var map = L.map(elId, { scrollWheelZoom: false });
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
  map.fitBounds(group.getBounds().pad(0.3), { animate: false });
  // A lone repeater otherwise fills the frame at max zoom; pull back a few
  // levels so the surrounding area is visible for context.
  if (pts.length === 1) {
    map.setZoom(map.getZoom() - 4, { animate: false });
  }
}
