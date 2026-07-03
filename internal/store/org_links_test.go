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
		{Platform: "discord", URL: "guildmaster"},
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
	if got[0].Platform != "discord" || got[0].URL != "guildmaster" {
		t.Errorf("link[0] = %+v", got[0])
	}
	if got[0].Position != 0 || got[1].Position != 1 {
		t.Errorf("positions = %d,%d, want 0,1", got[0].Position, got[1].Position)
	}
	// Discord is a text platform: with no label, Display is the handle itself; a
	// custom label (the website's "Wiki") still wins when present.
	if d := got[0].Display(); d != "guildmaster" {
		t.Errorf("link[0].Display() = %q, want %q", d, "guildmaster")
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

func TestNormalizeLinkURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		// A bare domain gets https:// so people can type "example.com".
		{"example.com", "https://example.com"},
		{"  example.com  ", "https://example.com"},
		{"example.com/path?q=1", "https://example.com/path?q=1"},
		// Protocol-relative loses the leading // and gains a scheme.
		{"//example.com", "https://example.com"},
		// An explicit scheme is left alone.
		{"http://example.com", "http://example.com"},
		{"https://example.com", "https://example.com"},
		// A dangerous scheme without "://" is turned into an https host, so it
		// fails ValidLinkURL rather than being trusted.
		{"javascript:alert(1)", "https://javascript:alert(1)"},
		// Empty stays empty (an empty row is dropped upstream, not normalized).
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := NormalizeLinkURL(c.in); got != c.want {
			t.Errorf("NormalizeLinkURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// Normalized bare domains must then pass validation; the neutered
	// javascript: value must not.
	if !ValidLinkURL(NormalizeLinkURL("example.com")) {
		t.Error("normalized bare domain should be a valid link URL")
	}
	if ValidLinkURL(NormalizeLinkURL("javascript:alert(1)")) {
		t.Error("javascript: value must not be a valid link URL after normalization")
	}
}

func TestValidLinkURL(t *testing.T) {
	t.Parallel()
	valid := []string{"http://a.com", "https://a.com/x?y=1"}
	for _, s := range valid {
		if !ValidLinkURL(s) {
			t.Errorf("ValidLinkURL(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "example.com", "ftp://a.com", "javascript:alert(1)", "https://"}
	for _, s := range invalid {
		if ValidLinkURL(s) {
			t.Errorf("ValidLinkURL(%q) = true, want false", s)
		}
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
