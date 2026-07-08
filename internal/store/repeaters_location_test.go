package store

import (
	"strings"
	"testing"
)

// TestSetRepeaterCoordIndependent pins that SetRepeaterLatitude and
// SetRepeaterLongitude each write only their own column. The console reads the
// two coordinates independently ("get lat" / "get lon"), so writing one must
// never clobber the other — otherwise a single "get lat" would blank the stored
// longitude.
func TestSetRepeaterCoordIndependent(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	owner, err := st.CreateUser(ctx, "owner", "")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	rep, err := st.CreateRepeater(ctx, &Repeater{
		OwnerID: owner.ID, Name: "rep", PublicKeyHex: strings.Repeat("a", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}

	read := func() (lat, lon *float64) {
		t.Helper()
		r, err := st.GetRepeaterOwned(ctx, owner.ID, rep.ID)
		if err != nil {
			t.Fatalf("get repeater: %v", err)
		}
		return r.Latitude, r.Longitude
	}

	// A fresh repeater has neither coordinate.
	if lat, lon := read(); lat != nil || lon != nil {
		t.Fatalf("new repeater should be unlocated, got lat=%v lon=%v", lat, lon)
	}

	// Setting latitude alone leaves longitude NULL.
	if err := st.SetRepeaterLatitude(ctx, rep.ID, 37.7749); err != nil {
		t.Fatalf("set latitude: %v", err)
	}
	if lat, lon := read(); lat == nil || *lat != 37.7749 || lon != nil {
		t.Fatalf("after set latitude: got lat=%v lon=%v, want lat=37.7749 lon=nil", lat, lon)
	}

	// Setting longitude alone must not disturb the stored latitude.
	if err := st.SetRepeaterLongitude(ctx, rep.ID, -122.4194); err != nil {
		t.Fatalf("set longitude: %v", err)
	}
	if lat, lon := read(); lat == nil || *lat != 37.7749 || lon == nil || *lon != -122.4194 {
		t.Fatalf("after set longitude: got lat=%v lon=%v, want lat=37.7749 lon=-122.4194", lat, lon)
	}

	// Overwriting latitude keeps longitude intact.
	if err := st.SetRepeaterLatitude(ctx, rep.ID, 40.0); err != nil {
		t.Fatalf("overwrite latitude: %v", err)
	}
	if lat, lon := read(); lat == nil || *lat != 40.0 || lon == nil || *lon != -122.4194 {
		t.Fatalf("after overwrite latitude: got lat=%v lon=%v, want lat=40.0 lon=-122.4194", lat, lon)
	}
}
