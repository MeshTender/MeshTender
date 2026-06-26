// Package geo provides GeoJSON polygon containment and overlap for org region
// geofences. It is a thin wrapper over the simplefeatures geometry library: it
// constrains input to Polygon/MultiPolygon, treats a nil *Shape as "matches
// everywhere", and exposes the handful of operations the region hierarchy needs
// (point containment, bounding box, and pairwise overlap area for parentage).
package geo

import (
	"fmt"
	"math"

	"github.com/peterstace/simplefeatures/geom"
)

// Shape is a parsed GeoJSON Polygon or MultiPolygon. A nil *Shape means "matches
// everywhere" — callers use it for an unbounded region — so all methods are safe
// on a nil receiver.
type Shape struct {
	g geom.Geometry
}

// Parse decodes a GeoJSON Polygon or MultiPolygon geometry. It returns (nil, nil)
// for empty/NULL input, which callers treat as "matches everywhere". The geometry
// is validated, so a self-intersecting or otherwise malformed polygon is rejected
// here rather than failing later during an overlap computation.
func Parse(data []byte) (*Shape, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}
	g, err := geom.UnmarshalGeoJSON(data)
	if err != nil {
		return nil, fmt.Errorf("geo: parse geometry: %w", err)
	}
	if !g.IsPolygon() && !g.IsMultiPolygon() {
		return nil, fmt.Errorf("geo: unsupported geometry type %v (expected Polygon or MultiPolygon)", g.Type())
	}
	return &Shape{g: g}, nil
}

// Contains reports whether (lat, lon) falls within the shape. A nil shape matches
// everywhere, so this is safe on nil.
func (s *Shape) Contains(lat, lon float64) bool {
	if s == nil {
		return true
	}
	pt := geom.XY{X: lon, Y: lat}.AsPoint().AsGeometry()
	return geom.Intersects(s.g, pt)
}

// Bounds returns the axis-aligned bounding box (in lat/lon) of the shape, and
// ok=false for a nil or empty shape.
func (s *Shape) Bounds() (minLat, minLon, maxLat, maxLon float64, ok bool) {
	if s == nil {
		return 0, 0, 0, 0, false
	}
	mn, mx, ok := s.g.Envelope().MinMaxXYs()
	if !ok {
		return 0, 0, 0, 0, false
	}
	return mn.Y, mn.X, mx.Y, mx.X, true
}

// OverlapArea returns the area of the intersection of s and other, in the
// coordinates' own planar units (degrees² here) — adequate for comparing which of
// several candidate regions a region overlaps most. A nil shape means
// "everywhere", so its overlap with another shape is that shape's full area.
func (s *Shape) OverlapArea(other *Shape) float64 {
	switch {
	case s == nil && other == nil:
		return math.Inf(1)
	case s == nil:
		return other.g.Area()
	case other == nil:
		return s.g.Area()
	}
	inter, err := geom.Intersection(s.g, other.g)
	if err != nil {
		return 0
	}
	return inter.Area()
}

// Rectangle builds a GeoJSON Polygon for an axis-aligned box, in GeoJSON [lon,
// lat] order with a closed ring. Handy for tests and any caller that wants a
// simple box without going through the map editor.
func Rectangle(minLat, minLon, maxLat, maxLon float64) []byte {
	if minLat > maxLat {
		minLat, maxLat = maxLat, minLat
	}
	if minLon > maxLon {
		minLon, maxLon = maxLon, minLon
	}
	return []byte(fmt.Sprintf(
		`{"type":"Polygon","coordinates":[[[%g,%g],[%g,%g],[%g,%g],[%g,%g],[%g,%g]]]}`,
		minLon, minLat, maxLon, minLat, maxLon, maxLat, minLon, maxLat, minLon, minLat))
}
