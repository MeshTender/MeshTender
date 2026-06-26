package geo

import "testing"

func mustParse(t *testing.T, data []byte) *Shape {
	t.Helper()
	s, err := Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return s
}

func TestRectangleContains(t *testing.T) {
	t.Parallel()
	// A box covering lat [10,20], lon [30,40].
	s := mustParse(t, Rectangle(10, 30, 20, 40))
	cases := []struct {
		name     string
		lat, lon float64
		want     bool
	}{
		{"center", 15, 35, true},
		{"south of box", 5, 35, false},
		{"east of box", 15, 45, false},
		{"corner-ish inside", 10.1, 30.1, true},
	}
	for _, c := range cases {
		if got := s.Contains(c.lat, c.lon); got != c.want {
			t.Errorf("%s: Contains(%v,%v) = %v, want %v", c.name, c.lat, c.lon, got, c.want)
		}
	}
}

func TestConcavePolygon(t *testing.T) {
	t.Parallel()
	// An L-shaped (concave) polygon — proves the model isn't rectangle-bound even
	// though the v1 UI only draws rectangles. Coordinates are [lon, lat].
	//   (0,0)→(4,0)→(4,2)→(2,2)→(2,4)→(0,4)→close
	s := mustParse(t, []byte(`{"type":"Polygon","coordinates":[[[0,0],[4,0],[4,2],[2,2],[2,4],[0,4],[0,0]]]}`))
	// (3,3) lies in the notch that was cut out of the square → outside.
	if s.Contains(3, 3) {
		t.Errorf("point in the concave notch should be outside")
	}
	// (1,1) is in the solid part → inside.
	if !s.Contains(1, 1) {
		t.Errorf("point in the solid arm should be inside")
	}
}

func TestPolygonWithHole(t *testing.T) {
	t.Parallel()
	// Outer 0..10 square with an inner 4..6 hole. [lon, lat] order.
	s := mustParse(t, []byte(`{"type":"Polygon","coordinates":[`+
		`[[0,0],[10,0],[10,10],[0,10],[0,0]],`+
		`[[4,4],[6,4],[6,6],[4,6],[4,4]]]}`))
	if !s.Contains(2, 2) {
		t.Errorf("point in the ring (outside the hole) should be inside")
	}
	if s.Contains(5, 5) {
		t.Errorf("point in the hole should be outside")
	}
}

func TestMultiPolygon(t *testing.T) {
	t.Parallel()
	s := mustParse(t, []byte(`{"type":"MultiPolygon","coordinates":[`+
		`[[[0,0],[2,0],[2,2],[0,2],[0,0]]],`+
		`[[[10,10],[12,10],[12,12],[10,12],[10,10]]]]}`))
	if !s.Contains(1, 1) {
		t.Errorf("point in first polygon should be inside")
	}
	if !s.Contains(11, 11) {
		t.Errorf("point in second polygon should be inside")
	}
	if s.Contains(5, 5) {
		t.Errorf("point between polygons should be outside")
	}
}

func TestOverlapArea(t *testing.T) {
	t.Parallel()
	// Two 10×10 boxes overlapping in a 5×5 corner: area 25.
	a := mustParse(t, Rectangle(0, 0, 10, 10))
	b := mustParse(t, Rectangle(5, 5, 15, 15))
	if got := a.OverlapArea(b); got != 25 {
		t.Errorf("overlap area = %v, want 25", got)
	}
	// Disjoint boxes overlap by zero.
	c := mustParse(t, Rectangle(20, 20, 30, 30))
	if got := a.OverlapArea(c); got != 0 {
		t.Errorf("disjoint overlap = %v, want 0", got)
	}
	// A nil (match-all) shape overlaps another by that other's full area.
	var everywhere *Shape
	if got := everywhere.OverlapArea(a); got != 100 {
		t.Errorf("match-all overlap = %v, want 100 (a's area)", got)
	}
}

func TestNilAndEmptyMatchEverywhere(t *testing.T) {
	t.Parallel()
	if s, err := Parse(nil); err != nil || s != nil {
		t.Fatalf("Parse(nil) = (%v,%v), want (nil,nil)", s, err)
	}
	var s *Shape
	if !s.Contains(123, 456) {
		t.Errorf("nil shape should contain every point (match-all)")
	}
}

func TestBadGeometry(t *testing.T) {
	t.Parallel()
	if _, err := Parse([]byte(`{"type":"Point","coordinates":[0,0]}`)); err == nil {
		t.Errorf("Point geometry should be rejected")
	}
	if _, err := Parse([]byte(`{"type":"Polygon","coordinates":[[[0,0],[1,1]]]}`)); err == nil {
		t.Errorf("a 2-point ring should be rejected")
	}
}
