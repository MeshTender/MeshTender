package core

import (
	"crypto/rand"
	"strings"
	"testing"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestOrgRepeatersPageBadgesStatus: the org's repeaters page flags nodes a member can
// actually do something about — one nobody has reached ("Unconfirmed"), and one where
// MeshTender holds only guest access ("guest only") and therefore cannot run any
// command at all.
//
// The same test pins the boundary: the PUBLIC org page must show neither. Which of an
// org's repeaters are unreachable or misconfigured is operational detail about other
// people's hardware, and the public list is built from a different query
// (ListPublicRepeaters) that deliberately leaves the flags unset.
func TestOrgRepeatersPageBadgesStatus(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "badgeowner")
	org, err := st.CreateOrg(ctx, "Badge Org", owner.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Three repeaters covering the states the badges distinguish. All are public-page
	// visible, so the public assertions below can't pass merely because nothing is
	// listed there.
	const adminRep, guestRep, newRep = "Admin Rep", "Guest Rep", "New Rep"
	for _, tc := range []struct {
		name      string
		confirmed bool
		admin     bool
		perms     int16
	}{
		{adminRep, true, true, 3},
		{guestRep, true, false, 0},
		{newRep, false, false, 0},
	} {
		id, err := meshcore.GenerateLocalIdentity(rand.Reader)
		if err != nil {
			t.Fatalf("generate identity: %v", err)
		}
		rep, err := st.CreateRepeater(ctx, &store.Repeater{
			OwnerID: owner.ID, Name: tc.name, PublicKeyHex: id.String(),
			RadioFreqHz: 869525000, RadioBwHz: 250000, RadioSF: 11, RadioCR: 5,
			ShowOnPublicOrg: true,
		})
		if err != nil {
			t.Fatalf("create %s: %v", tc.name, err)
		}
		if tc.confirmed {
			if err := st.SetRepeaterConfirmed(ctx, rep.ID, owner.ID, tc.admin, tc.perms); err != nil {
				t.Fatalf("confirm %s: %v", tc.name, err)
			}
		}
	}

	// Member view: both problems are flagged, and the healthy repeater carries neither.
	member := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/repeaters", sess))
	for _, want := range []string{adminRep, guestRep, newRep, "Unconfirmed", "guest only"} {
		if !strings.Contains(member, want) {
			t.Errorf("member repeaters page missing %q", want)
		}
	}
	if got := strings.Count(member, "Unconfirmed"); got != 1 {
		t.Errorf("Unconfirmed badge appears %d times, want 1 (only the unreached repeater)", got)
	}
	if got := strings.Count(member, "guest only"); got != 1 {
		t.Errorf("guest only badge appears %d times, want 1 (only the guest-access repeater)", got)
	}

	// Public view: the repeaters are listed, their access state is not.
	public := readBody(t, do(t, ts, h.root, "/orgs/"+org.Slug+"/repeaters"))
	if !strings.Contains(public, adminRep) {
		t.Fatal("public repeaters page lists no repeaters, so the assertions below prove nothing")
	}
	for _, leak := range []string{"Unconfirmed", "guest only"} {
		if strings.Contains(public, leak) {
			t.Errorf("public repeaters page exposes %q; a repeater's access state is not public", leak)
		}
	}
}
