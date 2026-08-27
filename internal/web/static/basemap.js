// basemap.js — the CARTO basemap layers shared by every Leaflet map we render.
//
// meshmap.js and regionmap.js each used to carry their own copy of this, which meant
// the tile URL, the zoom ceiling and the localStorage key all had to be changed in
// two places to stay consistent. One definition here; both call it.
//
// Load this before meshmap.js / regionmap.js on any page with a map.

// cartoTileURL builds a CARTO raster tile URL for the given basemap style.
//
// CARTO watermark tiles served without an API key. The server puts the key on
// <html data-carto-key> (see web.Renderer); when it is absent the key parameter is
// left off entirely rather than sent empty.
function cartoTileURL(style) {
  var key = document.documentElement.getAttribute("data-carto-key") || "";
  var url = "https://{s}.basemaps.cartocdn.com/" + style + "/{z}/{x}/{y}{r}.png";
  return key ? url + "?key=" + encodeURIComponent(key) : url;
}

// meshBaseLayers adds the dark (default) and a light basemap to a map, with a
// layers control to toggle between them. The choice is remembered in localStorage
// so it carries across maps and pages. Dark matches the UI; the light layer (CARTO
// Voyager) reads better in bright conditions and for some eyes. The control is
// left expanded — Leaflet's collapsed toggle needs an icon asset we don't bundle.
function meshBaseLayers(map) {
  // Fresh options per layer: Leaflet copies them onto the layer, but a plugin that
  // reaches for the object we passed shouldn't be able to affect both layers.
  function opts() {
    return { maxZoom: 20, subdomains: "abcd", attribution: "&copy; OpenStreetMap &copy; CARTO" };
  }
  var dark = L.tileLayer(cartoTileURL("dark_all"), opts());
  var light = L.tileLayer(cartoTileURL("rastertiles/voyager"), opts());
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
