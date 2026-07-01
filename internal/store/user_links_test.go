package store

import (
	"strings"
	"testing"
)

func TestUserLinksReplaceAndList(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	u, err := st.CreateUser(ctx, "linkuser", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// A fresh user has no links.
	got, err := st.ListUserLinks(ctx, u.ID)
	if err != nil {
		t.Fatalf("list (empty): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("new user links = %d, want 0", len(got))
	}

	// Replace with three links; order is preserved and the second is primary.
	links := []UserLink{
		{Platform: "discord", URL: "https://discord.gg/abc"},
		{Platform: "website", Label: "Home", URL: "https://example.org", IsPrimary: true},
		{Platform: MeshCorePlatform, URL: strings.Repeat("a", 64)},
	}
	if err := st.ReplaceUserLinks(ctx, u.ID, links); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err = st.ListUserLinks(ctx, u.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("links = %d, want 3", len(got))
	}
	if got[0].Position != 0 || got[1].Position != 1 || got[2].Position != 2 {
		t.Errorf("positions = %d,%d,%d, want 0,1,2", got[0].Position, got[1].Position, got[2].Position)
	}
	if p := PrimaryUserLink(got); p == nil || p.URL != "https://example.org" {
		t.Errorf("primary = %+v, want the website link", p)
	}
	if !got[2].IsMeshCore() {
		t.Errorf("link[2] should be a MeshCore link")
	}
	if d := got[0].Display(); d != "Discord" {
		t.Errorf("link[0].Display() = %q, want %q", d, "Discord")
	}
}

func TestUserLinkHref(t *testing.T) {
	t.Parallel()
	cases := []struct {
		link UserLink
		want string
	}{
		{UserLink{Platform: EmailPlatform, URL: "a@b.com"}, "mailto:a@b.com"},
		{UserLink{Platform: SignalPlatform, URL: "alice.42"}, ""},
		{UserLink{Platform: MeshCorePlatform, URL: "abcd"}, ""},
		{UserLink{Platform: "website", URL: "https://example.org"}, "https://example.org"},
	}
	for _, c := range cases {
		if got := c.link.Href(); got != c.want {
			t.Errorf("%s.Href() = %q, want %q", c.link.Platform, got, c.want)
		}
	}
}

func TestReplaceUserLinksSinglePrimary(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	u, err := st.CreateUser(ctx, "multiprimary", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Two links flagged primary — only the first flagged should win.
	links := []UserLink{
		{Platform: "website", URL: "https://one.example", IsPrimary: true},
		{Platform: "website", URL: "https://two.example", IsPrimary: true},
	}
	if err := st.ReplaceUserLinks(ctx, u.ID, links); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err := st.ListUserLinks(ctx, u.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	primaries := 0
	for _, l := range got {
		if l.IsPrimary {
			primaries++
		}
	}
	if primaries != 1 {
		t.Fatalf("primary links = %d, want exactly 1", primaries)
	}
	if p := PrimaryUserLink(got); p == nil || p.URL != "https://one.example" {
		t.Errorf("primary = %+v, want the first flagged link", p)
	}

	// Replacing with an empty set clears every link.
	if err := st.ReplaceUserLinks(ctx, u.ID, nil); err != nil {
		t.Fatalf("replace (clear): %v", err)
	}
	got, err = st.ListUserLinks(ctx, u.ID)
	if err != nil {
		t.Fatalf("list (after clear): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("links after clear = %d, want 0", len(got))
	}
}

func TestUserHasPublicRole(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	// A user with no public role.
	nobody, err := st.CreateUser(ctx, "nobody", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if yes, err := st.UserHasPublicRole(ctx, nobody.ID); err != nil || yes {
		t.Fatalf("nobody has public role = %v (err %v), want false", yes, err)
	}

	// An org admin is public.
	admin, err := st.CreateUser(ctx, "orgadmin", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := st.CreateOrg(ctx, "Public Org", admin.ID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	if yes, err := st.UserHasPublicRole(ctx, admin.ID); err != nil || !yes {
		t.Fatalf("org admin has public role = %v (err %v), want true", yes, err)
	}

	// A repeater owner is public only once the public page is exposed.
	owner, err := st.CreateUser(ctx, "repowner", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	rep, err := st.CreateRepeater(ctx, &Repeater{
		OwnerID: owner.ID, Name: "R", PublicKeyHex: strings.Repeat("b", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}
	if yes, err := st.UserHasPublicRole(ctx, owner.ID); err != nil || yes {
		t.Fatalf("owner of private repeater has public role = %v, want false", yes)
	}
	if err := st.UpdateRepeater(ctx, owner.ID, rep.ID, "R", 1, 1, 11, 5, false, true); err != nil {
		t.Fatalf("expose public page: %v", err)
	}
	if yes, err := st.UserHasPublicRole(ctx, owner.ID); err != nil || !yes {
		t.Fatalf("owner of public repeater has public role = %v (err %v), want true", yes, err)
	}

	// A steward of that public repeater is also public.
	steward, err := st.CreateUser(ctx, "repsteward", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := st.AddShare(ctx, rep.ID, steward.ID); err != nil {
		t.Fatalf("add share: %v", err)
	}
	if yes, err := st.UserHasPublicRole(ctx, steward.ID); err != nil || yes {
		t.Fatalf("non-steward share has public role = %v, want false", yes)
	}
	if err := st.SetShareSteward(ctx, rep.ID, steward.ID, true); err != nil {
		t.Fatalf("set steward: %v", err)
	}
	if yes, err := st.UserHasPublicRole(ctx, steward.ID); err != nil || !yes {
		t.Fatalf("steward of public repeater has public role = %v (err %v), want true", yes, err)
	}
}
