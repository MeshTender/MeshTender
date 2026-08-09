package core

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/store"
)

// mapPoint mirrors the JSON shape the public map endpoint serves (store.MapPoint).
type mapPoint struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

// TestOrgRepeatersJSON covers the public, cached map-points endpoint: it returns
// only located public repeaters (owner is a member, opted in, not excluded), sets
// caching headers, and honors If-None-Match with a 304.
func TestOrgRepeatersJSON(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	owner, err := st.CreateUser(ctx, "owner", "")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	org, err := st.CreateOrg(ctx, "Mesh Org", owner.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	mk := func(name string, keyChar byte, public, located bool) int64 {
		t.Helper()
		r, err := st.CreateRepeater(ctx, &store.Repeater{
			OwnerID: owner.ID, Name: name, PublicKeyHex: strings.Repeat(string(keyChar), 64),
			RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5, ShowOnPublicOrg: public,
		})
		if err != nil {
			t.Fatalf("create repeater %s: %v", name, err)
		}
		if located {
			if err := st.SetRepeaterLocation(ctx, r.ID, 40.0, -75.0); err != nil {
				t.Fatalf("set location %s: %v", name, err)
			}
		}
		return r.ID
	}

	mk("shown", 'a', true, true)
	mk("private", 'b', false, true)
	mk("unlocated", 'c', true, false)
	excluded := mk("excluded", 'd', true, true)
	if err := st.SetRepeaterOrgExcluded(ctx, org.ID, excluded, true); err != nil {
		t.Fatalf("exclude: %v", err)
	}

	path := "/orgs/" + org.Slug + "/repeaters.json"
	resp := do(t, ts, h.root, path)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=60" {
		t.Fatalf("Cache-Control = %q, want public, max-age=60", cc)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatalf("missing ETag")
	}

	var points []mapPoint
	if err := json.Unmarshal(body, &points); err != nil {
		t.Fatalf("unmarshal %s: %v", body, err)
	}
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1: %+v", len(points), points)
	}
	if points[0] != (mapPoint{Name: "shown", Lat: 40.0, Lon: -75.0}) {
		t.Fatalf("unexpected point %+v", points[0])
	}

	// A matching If-None-Match revalidates to 304 with no body.
	req2, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("conditional request: %v", err)
	}
	req2.Host = h.root
	req2.Header.Set("If-None-Match", etag)
	resp2, err := noRedirect().Do(req2)
	if err != nil {
		t.Fatalf("conditional do: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want 304", resp2.StatusCode)
	}
	if len(body2) != 0 {
		t.Fatalf("304 body = %q, want empty", body2)
	}
}
