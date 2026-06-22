// Package geo provides minimal GeoJSON polygon parsing and point-in-polygon
// containment, with no external/GIS dependency. It is deliberately geometry-
// agnostic: a rectangle is just a 4-corner polygon, so callers that only draw
// rectangles today can store and resolve arbitrary polygons later without any
// change here.
package geo

import (
	"encoding/json"
	"fmt"
)

// Shape is a parsed GeoJSON Polygon or MultiPolygon. A point is contained when it
// lies inside any constituent polygon's outer ring and outside that polygon's
// holes.
type Shape struct {
	// polygons[i][0] is the outer ring; polygons[i][1:] are holes. Each ring is a
	// list of [lon, lat] coordinates, per GeoJSON axis order.
	polygons [][]ring
}

type ring [][2]float64

// geojson is the wire shape we accept. Coordinates is decoded per Type.
type geojson struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

// Parse decodes a GeoJSON Polygon or MultiPolygon geometry. It returns (nil, nil)
// for empty/NULL input, which callers treat as "matches everywhere".
func Parse(data []byte) (*Shape, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}
	var g geojson
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("geo: parse geometry: %w", err)
	}
	switch g.Type {
	case "Polygon":
		var rings []ring
		if err := json.Unmarshal(g.Coordinates, &rings); err != nil {
			return nil, fmt.Errorf("geo: parse polygon: %w", err)
		}
		return newShape([][]ring{rings})
	case "MultiPolygon":
		var polys [][]ring
		if err := json.Unmarshal(g.Coordinates, &polys); err != nil {
			return nil, fmt.Errorf("geo: parse multipolygon: %w", err)
		}
		return newShape(polys)
	default:
		return nil, fmt.Errorf("geo: unsupported geometry type %q", g.Type)
	}
}

func newShape(polys [][]ring) (*Shape, error) {
	for _, p := range polys {
		if len(p) == 0 || len(p[0]) < 3 {
			return nil, fmt.Errorf("geo: polygon needs an outer ring of at least 3 points")
		}
	}
	if len(polys) == 0 {
		return nil, fmt.Errorf("geo: geometry has no polygons")
	}
	return &Shape{polygons: polys}, nil
}

// Contains reports whether (lat, lon) falls within the shape. A nil shape matches
// everywhere (callers may use that for an unbounded zone), so this is safe on nil.
func (s *Shape) Contains(lat, lon float64) bool {
	if s == nil {
		return true
	}
	for _, poly := range s.polygons {
		if !poly[0].contains(lon, lat) {
			continue
		}
		inHole := false
		for _, h := range poly[1:] {
			if h.contains(lon, lat) {
				inHole = true
				break
			}
		}
		if !inHole {
			return true
		}
	}
	return false
}

// Bounds returns the axis-aligned bounding box (in lat/lon) of every point in the
// shape, and ok=false for a nil shape. For a rectangle the bounds are the
// rectangle itself, which is what the v1 rectangle-only editor needs to round-trip
// a stored zone back into its four corner inputs; for a richer polygon it is the
// enclosing box.
func (s *Shape) Bounds() (minLat, minLon, maxLat, maxLon float64, ok bool) {
	if s == nil {
		return 0, 0, 0, 0, false
	}
	first := true
	for _, poly := range s.polygons {
		for _, rg := range poly {
			for _, pt := range rg {
				lon, lat := pt[0], pt[1]
				if first {
					minLat, maxLat, minLon, maxLon = lat, lat, lon, lon
					first = false
					continue
				}
				minLat, maxLat = min(minLat, lat), max(maxLat, lat)
				minLon, maxLon = min(minLon, lon), max(maxLon, lon)
			}
		}
	}
	return minLat, minLon, maxLat, maxLon, !first
}

// contains runs the standard ray-casting test on a single ring. Points are
// [lon, lat]; we treat lon as x and lat as y. A point exactly on an edge is
// reported deterministically (good enough for zone selection; edges between
// adjacent zones are an authoring concern, not a correctness one).
func (r ring) contains(x, y float64) bool {
	in := false
	n := len(r)
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		xi, yi := r[i][0], r[i][1]
		xj, yj := r[j][0], r[j][1]
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			in = !in
		}
	}
	return in
}

// Rectangle builds a GeoJSON Polygon for an axis-aligned box. It is the v1
// editor's only geometry producer; the storage column and Parse accept any
// polygon, so richer shapes are a UI-only addition later. Coordinates are emitted
// in GeoJSON [lon, lat] order with a closed ring.
func Rectangle(minLat, minLon, maxLat, maxLon float64) []byte {
	if minLat > maxLat {
		minLat, maxLat = maxLat, minLat
	}
	if minLon > maxLon {
		minLon, maxLon = maxLon, minLon
	}
	g := geojson{Type: "Polygon"}
	coords := [][][2]float64{{
		{minLon, minLat}, {maxLon, minLat}, {maxLon, maxLat}, {minLon, maxLat}, {minLon, minLat},
	}}
	g.Coordinates, _ = json.Marshal(coords)
	out, _ := json.Marshal(g)
	return out
}
