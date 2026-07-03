package store

import "testing"

func platform(t *testing.T, key string) LinkPlatform {
	t.Helper()
	p, ok := UserLinkPlatform(key)
	if !ok {
		t.Fatalf("unknown platform %q", key)
	}
	return p
}

func TestCanonicalHandleURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		platform string
		in       string
		want     string // "" means expect ok == false
	}{
		// Bare handle, @-prefixed or not.
		{"github", "@octocat", "https://github.com/octocat"},
		{"github", "octocat", "https://github.com/octocat"},
		// A pasted profile URL on a known host is accepted and canonicalised.
		{"github", "https://github.com/octocat", "https://github.com/octocat"},
		{"github", "github.com/octocat/", "https://github.com/octocat"},
		// A URL on the wrong host is rejected (don't silently point elsewhere).
		{"github", "https://evil.com/octocat", ""},
		// X accepts its old twitter.com host and canonicalises to x.com.
		{"x", "https://twitter.com/jack", "https://x.com/jack"},
		{"x", "@jack", "https://x.com/jack"},
		// Reddit tolerates the "u/name" shorthand and the /user/ path.
		{"reddit", "u/spez", "https://reddit.com/user/spez"},
		{"reddit", "https://reddit.com/user/spez", "https://reddit.com/user/spez"},
		// YouTube keeps the @ in its canonical path.
		{"youtube", "@MrBeast", "https://youtube.com/@MrBeast"},
		{"youtube", "https://youtube.com/@MrBeast", "https://youtube.com/@MrBeast"},
		// Bluesky handles are domain-style (dots allowed).
		{"bluesky", "@alice.bsky.social", "https://bsky.app/profile/alice.bsky.social"},
		// LinkedIn uses /in/.
		{"linkedin", "ada", "https://linkedin.com/in/ada"},
		// Empty / whitespace is rejected.
		{"github", "   ", ""},
		// A space in the handle is rejected.
		{"github", "not a handle", ""},
	}
	for _, c := range cases {
		got, ok := platform(t, c.platform).CanonicalHandleURL(c.in)
		if c.want == "" {
			if ok {
				t.Errorf("%s.CanonicalHandleURL(%q) = %q, ok; want !ok", c.platform, c.in, got)
			}
			continue
		}
		if !ok || got != c.want {
			t.Errorf("%s.CanonicalHandleURL(%q) = %q, %v; want %q, true", c.platform, c.in, got, ok, c.want)
		}
	}
}

func TestCanonicalMastodon(t *testing.T) {
	t.Parallel()
	m := platform(t, "mastodon")
	cases := []struct {
		in   string
		want string
	}{
		{"@Gargron@mastodon.social", "https://mastodon.social/@Gargron"},
		{"Gargron@mastodon.social", "https://mastodon.social/@Gargron"},
		{"https://mastodon.social/@Gargron", "https://mastodon.social/@Gargron"},
		// No instance → not a valid Mastodon handle.
		{"@Gargron", ""},
		{"Gargron", ""},
	}
	for _, c := range cases {
		got, ok := m.CanonicalHandleURL(c.in)
		if c.want == "" {
			if ok {
				t.Errorf("mastodon CanonicalHandleURL(%q) = %q, ok; want !ok", c.in, got)
			}
			continue
		}
		if !ok || got != c.want {
			t.Errorf("mastodon CanonicalHandleURL(%q) = %q, %v; want %q, true", c.in, got, ok, c.want)
		}
	}
}

func TestHandleFromURLAndDisplay(t *testing.T) {
	t.Parallel()
	// HandleFromURL is what Display uses for handle platforms.
	if h := platform(t, "github").HandleFromURL("https://github.com/octocat"); h != "@octocat" {
		t.Errorf("github HandleFromURL = %q, want @octocat", h)
	}
	if h := platform(t, "mastodon").HandleFromURL("https://mastodon.social/@Gargron"); h != "@Gargron@mastodon.social" {
		t.Errorf("mastodon HandleFromURL = %q, want @Gargron@mastodon.social", h)
	}

	// Display/Href across kinds, via both link types.
	cases := []struct {
		link        UserLink
		wantDisplay string
		wantHref    string
	}{
		// Handle platform: display the @handle, link the stored URL.
		{UserLink{Platform: "github", URL: "https://github.com/octocat"}, "@octocat", "https://github.com/octocat"},
		// A custom label always wins over the derived handle.
		{UserLink{Platform: "github", Label: "My code", URL: "https://github.com/octocat"}, "My code", "https://github.com/octocat"},
		// Text platform: display the handle text, no link.
		{UserLink{Platform: "discord", URL: "cooluser"}, "cooluser", ""},
		{UserLink{Platform: SignalPlatform, URL: "alice.42"}, "alice.42", ""},
		// Email: mailto:, name as the default display.
		{UserLink{Platform: EmailPlatform, URL: "a@b.com"}, "Email", "mailto:a@b.com"},
		// URL platform with no label falls back to the platform name.
		{UserLink{Platform: "website", URL: "https://example.org"}, "Website", "https://example.org"},
		// MeshCore key: no href.
		{UserLink{Platform: MeshCorePlatform, URL: "abcd"}, "MeshCore", ""},
	}
	for _, c := range cases {
		if d := c.link.Display(); d != c.wantDisplay {
			t.Errorf("%+v Display() = %q, want %q", c.link, d, c.wantDisplay)
		}
		if h := c.link.Href(); h != c.wantHref {
			t.Errorf("%+v Href() = %q, want %q", c.link, h, c.wantHref)
		}
	}

	// OrgLink shares the same kind logic.
	if d := (OrgLink{Platform: "discord", URL: "guildmaster"}).Display(); d != "guildmaster" {
		t.Errorf("org discord Display() = %q, want guildmaster", d)
	}
	if h := (OrgLink{Platform: "discord", URL: "guildmaster"}).Href(); h != "" {
		t.Errorf("org discord Href() = %q, want empty", h)
	}
}
