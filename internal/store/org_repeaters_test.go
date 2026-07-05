package store

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestListPublicRepeaterPoints pins the map query's predicate: a repeater is
// plotted iff its owner is a member of the org, it opted into show_on_public_org,
// it has coordinates, and it isn't excluded from the org. This is the set the
// unauthenticated org map (and the repeaters_public_map_idx partial index) covers.
func TestListPublicRepeaterPoints(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	owner, err := st.CreateUser(ctx, "owner", "")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	org, err := st.CreateOrg(ctx, "Org", owner.ID) // creator becomes a member
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// mk creates a repeater owned by owner with the given public/location state and
	// returns its id. keyChar keeps the public key unique per repeater.
	mk := func(name string, keyChar byte, public bool, located bool) int64 {
		t.Helper()
		r, err := st.CreateRepeater(ctx, &Repeater{
			OwnerID: owner.ID, Name: name, PublicKeyHex: strings.Repeat(string(keyChar), 64),
			RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
			ShowOnPublicOrg: public,
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

	mk("shown", 'a', true, true)        // the only one that should appear
	mk("private", 'b', false, true)     // opted out of public
	mk("no-location", 'c', true, false) // public but unlocated
	excluded := mk("excluded", 'd', true, true)

	if err := st.SetRepeaterOrgExcluded(ctx, org.ID, excluded, true); err != nil {
		t.Fatalf("exclude: %v", err)
	}

	// A public, located repeater owned by a non-member must not appear.
	stranger, err := st.CreateUser(ctx, "stranger", "")
	if err != nil {
		t.Fatalf("create stranger: %v", err)
	}
	sr, err := st.CreateRepeater(ctx, &Repeater{
		OwnerID: stranger.ID, Name: "stranger-rep", PublicKeyHex: strings.Repeat("e", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5, ShowOnPublicOrg: true,
	})
	if err != nil {
		t.Fatalf("create stranger repeater: %v", err)
	}
	if err := st.SetRepeaterLocation(ctx, sr.ID, 41.0, -76.0); err != nil {
		t.Fatalf("set stranger location: %v", err)
	}

	points, err := st.ListPublicRepeaterPoints(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListPublicRepeaterPoints: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1: %+v", len(points), points)
	}
	if points[0].Name != "shown" || points[0].Lat != 40.0 || points[0].Lon != -75.0 {
		t.Fatalf("unexpected point %+v (want shown @ 40,-75)", points[0])
	}
}

// TestPublicRepeaterMapUsesPartialIndex proves migration 0035's partial index is
// the access path the map query takes: with a member owning many repeaters of
// which only a few are public+located, the planner must reach the eligible rows
// through repeaters_public_map_idx rather than scanning all of the owner's rows.
// This enforces the performance characteristic (CLAUDE.md: perf claims need proof),
// so a future query change that stops matching the index fails here.
func TestPublicRepeaterMapUsesPartialIndex(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	owner, err := st.CreateUser(ctx, "owner", "")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	org, err := st.CreateOrg(ctx, "Org", owner.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// Bulk-insert many private, unlocated repeaters (not in the partial index) plus
	// a handful of public+located ones (in it). Inserted directly for speed; hex key
	// is derived from n to stay unique and 64 chars.
	const total, eligible = 2000, 5
	for n := 0; n < total; n++ {
		public := n < eligible
		_, err := st.pool.Exec(ctx, `
			INSERT INTO repeaters (public_id, owner_id, name, public_key_hex,
			                       radio_freq_hz, radio_bw_hz, radio_sf, radio_cr,
			                       show_on_public_org, latitude, longitude)
			VALUES ($1, $2, $3, $4, 1, 1, 11, 5, $5, $6, $7)`,
			"pub"+itoa(n), owner.ID, "R"+itoa(n),
			leftPad(itoa(n), '0', 64), public,
			nullableFloat(public, 40.0), nullableFloat(public, -75.0))
		if err != nil {
			t.Fatalf("insert repeater %d: %v", n, err)
		}
	}
	if _, err := st.pool.Exec(ctx, `ANALYZE repeaters`); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	// EXPLAIN the exact predicate ListPublicRepeaterPoints uses (kept in sync with
	// that query) and require the partial index in the chosen plan. EXPLAIN returns
	// one row per plan line, so collect them all.
	rows, err := st.pool.Query(ctx, `
		EXPLAIN (FORMAT TEXT)
		SELECT r.name, r.latitude, r.longitude
		FROM repeaters r
		JOIN org_members om ON om.org_id = $1 AND om.user_id = r.owner_id
		WHERE r.show_on_public_org
		  AND r.latitude IS NOT NULL AND r.longitude IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM org_repeater_excludes e
		                  WHERE e.org_id = $1 AND e.repeater_id = r.id)`, org.ID)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	lines, err := collectRows(rows, func(r pgx.Row) (string, error) {
		var line string
		return line, r.Scan(&line)
	})
	if err != nil {
		t.Fatalf("scan plan: %v", err)
	}
	plan := strings.Join(lines, "\n")
	if !strings.Contains(plan, "repeaters_public_map_idx") {
		t.Fatalf("map query did not use repeaters_public_map_idx; plan:\n%s", plan)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func leftPad(s string, pad byte, width int) string {
	if len(s) >= width {
		return s[:width]
	}
	return strings.Repeat(string(pad), width-len(s)) + s
}

func nullableFloat(present bool, v float64) *float64 {
	if !present {
		return nil
	}
	return &v
}
