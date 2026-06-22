package store

import (
	"errors"
	"strings"
	"testing"
)

func TestRepeaterPublicID(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner, _ := st.CreateUser(ctx, "pidowner", "")

	mk := func(key string) *Repeater {
		rep, err := st.CreateRepeater(ctx, &Repeater{
			OwnerID: owner.ID, Name: "R", PublicKeyHex: key,
			RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
		})
		if err != nil {
			t.Fatalf("create repeater: %v", err)
		}
		return rep
	}
	a := mk(strings.Repeat("a", 64))
	b := mk(strings.Repeat("b", 64))

	if a.PublicID == "" || b.PublicID == "" {
		t.Fatalf("public ids must be populated: %q %q", a.PublicID, b.PublicID)
	}
	if a.PublicID == b.PublicID {
		t.Fatalf("public ids must be distinct: %q", a.PublicID)
	}
	got, err := st.RepeaterIDByPublicID(ctx, a.PublicID)
	if err != nil || got != a.ID {
		t.Fatalf("resolve %q = (%d, %v), want %d", a.PublicID, got, err, a.ID)
	}
	if _, err := st.RepeaterIDByPublicID(ctx, "does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown public id = %v, want ErrNotFound", err)
	}
}

func TestOrgSlugGeneration(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	creator, _ := st.CreateUser(ctx, "slugcreator", "")

	o1, err := st.CreateOrg(ctx, "Buffalo Mesh!", creator.ID)
	if err != nil {
		t.Fatal(err)
	}
	if o1.Slug != "buffalo-mesh" {
		t.Fatalf("slug = %q, want buffalo-mesh", o1.Slug)
	}
	o2, err := st.CreateOrg(ctx, "Buffalo Mesh", creator.ID)
	if err != nil {
		t.Fatal(err)
	}
	if o2.Slug != "buffalo-mesh-2" {
		t.Fatalf("collision slug = %q, want buffalo-mesh-2", o2.Slug)
	}

	got, err := st.OrgIDBySlug(ctx, "buffalo-mesh")
	if err != nil || got != o1.ID {
		t.Fatalf("resolve slug = (%d, %v), want %d", got, err, o1.ID)
	}
	if _, err := st.OrgIDBySlug(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown slug = %v, want ErrNotFound", err)
	}

	// Renaming o2's slug to o1's is rejected as a duplicate.
	if err := st.UpdateOrg(ctx, o2.ID, "buffalo-mesh", "X", "", ""); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("update to taken slug = %v, want ErrDuplicate", err)
	}
	// A free slug is accepted.
	if err := st.UpdateOrg(ctx, o2.ID, "buffalo-backup", "X", "", ""); err != nil {
		t.Fatalf("update to free slug: %v", err)
	}
}

func TestValidOrgSlug(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"buffalo-mesh": true,
		"abc":          true,
		"a1-b2-c3":     true,
		"new":          false, // reserved (collides with /orgs/new)
		"ab":           false, // too short
		"-leading":     false,
		"trailing-":    false,
		"double--hyph": false,
		"UpperCase":    false,
		"has space":    false,
		"":             false,
	}
	for s, want := range cases {
		if got := ValidOrgSlug(s); got != want {
			t.Errorf("ValidOrgSlug(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestOrgDomains(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	creator, _ := st.CreateUser(ctx, "domowner", "")
	org, err := st.CreateOrg(ctx, "Domain Org", creator.ID)
	if err != nil {
		t.Fatal(err)
	}

	d, err := st.CreateOrgDomain(ctx, org.ID, "mesh.example.org")
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}
	if d.VerificationToken == "" || d.Verified() {
		t.Fatalf("new domain should be unverified with a token, got %+v", d)
	}

	// Unverified domains do not resolve.
	if _, ok, err := st.OrgByVerifiedDomain(ctx, "mesh.example.org"); err != nil || ok {
		t.Fatalf("unverified resolve = (ok=%v, err=%v), want ok=false", ok, err)
	}

	// Claiming the same hostname again is a duplicate.
	if _, err := st.CreateOrgDomain(ctx, org.ID, "mesh.example.org"); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate hostname = %v, want ErrDuplicate", err)
	}

	if err := st.MarkOrgDomainVerified(ctx, org.ID, d.ID); err != nil {
		t.Fatalf("verify: %v", err)
	}
	got, ok, err := st.OrgByVerifiedDomain(ctx, "mesh.example.org")
	if err != nil || !ok || got.ID != org.ID {
		t.Fatalf("verified resolve = (%v, ok=%v, err=%v), want org %d", got, ok, err, org.ID)
	}

	if err := st.DeleteOrgDomain(ctx, org.ID, d.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := st.OrgByVerifiedDomain(ctx, "mesh.example.org"); ok {
		t.Fatalf("deleted domain still resolves")
	}
}
