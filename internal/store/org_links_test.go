package store

import "testing"

func TestOrgLinksReplaceAndList(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	owner, err := st.CreateUser(ctx, "linkowner", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	org, err := st.CreateOrg(ctx, "Linky Org", owner.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// A fresh org has no links.
	got, err := st.ListOrgLinks(ctx, org.ID)
	if err != nil {
		t.Fatalf("list (empty): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("new org links = %d, want 0", len(got))
	}

	// Replace with two links; order is preserved by insertion order.
	links := []OrgLink{
		{Platform: "discord", URL: "https://discord.gg/abc"},
		{Platform: "website", Label: "Wiki", URL: "https://wiki.example.org"},
	}
	if err := st.ReplaceOrgLinks(ctx, org.ID, links); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err = st.ListOrgLinks(ctx, org.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("links = %d, want 2", len(got))
	}
	if got[0].Platform != "discord" || got[0].URL != "https://discord.gg/abc" {
		t.Errorf("link[0] = %+v", got[0])
	}
	if got[0].Position != 0 || got[1].Position != 1 {
		t.Errorf("positions = %d,%d, want 0,1", got[0].Position, got[1].Position)
	}
	// Display falls back to the platform name when no label is set, and uses the
	// label when present.
	if d := got[0].Display(); d != "Discord" {
		t.Errorf("link[0].Display() = %q, want %q", d, "Discord")
	}
	if d := got[1].Display(); d != "Wiki" {
		t.Errorf("link[1].Display() = %q, want %q", d, "Wiki")
	}

	// Replacing with an empty set clears every link.
	if err := st.ReplaceOrgLinks(ctx, org.ID, nil); err != nil {
		t.Fatalf("replace (clear): %v", err)
	}
	got, err = st.ListOrgLinks(ctx, org.ID)
	if err != nil {
		t.Fatalf("list (after clear): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("links after clear = %d, want 0", len(got))
	}
}

func TestValidLinkPlatform(t *testing.T) {
	t.Parallel()
	if !ValidLinkPlatform("discord") {
		t.Error("discord should be valid")
	}
	if ValidLinkPlatform("myspace") {
		t.Error("myspace should be invalid")
	}
	if ValidLinkPlatform("") {
		t.Error("empty should be invalid")
	}
}
