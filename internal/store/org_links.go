package store

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
)

// MaxOrgLinks caps how many social/site links an org may list. Enough for the
// usual suspects (Discord, a few sites) without turning the page into a directory.
const MaxOrgLinks = 20

// LinkKind classifies how a platform's value is entered, validated, stored, and
// rendered. It replaces the per-platform switch statements the link editors used
// to carry: behavior is a property of the platform, looked up once here.
type LinkKind string

const (
	// KindURL is a full http(s) URL the user pastes verbatim (e.g. Website).
	KindURL LinkKind = "url"
	// KindHandle is a username: the user types "@handle" or pastes a profile URL,
	// and we store the canonical profile URL (URLFmt applied to the handle). Href
	// is that URL; Display shows the "@handle".
	KindHandle LinkKind = "handle"
	// KindText is a handle shown as plain text with no hyperlink — the platform has
	// no public per-user URL (Discord, Signal).
	KindText LinkKind = "text"
	// KindEmail is an email address, rendered as a mailto: link.
	KindEmail LinkKind = "email"
	// KindKey is a MeshCore public key (hex), rendered as a QR code rather than a
	// link.
	KindKey LinkKind = "key"
)

// LinkPlatform is a known destination a link can point at, plus the rules for how
// its value is entered, stored, and rendered. Key is the stable identifier
// persisted in the platform column; Name is the human label shown when a link has
// no custom label of its own; Icon names the "icon-*" template used by link-icon.
//
// For KindHandle: URLFmt is the canonical profile URL with a single %s for the
// handle, and Hosts are the URL hosts we recognize when a user pastes a full
// profile URL instead of a bare handle. Placeholder is the editor input hint.
type LinkPlatform struct {
	Key         string
	Name        string
	Kind        LinkKind
	Icon        string
	URLFmt      string
	Hosts       []string
	Placeholder string
}

// mastodonKey is special-cased in handle canonicalisation because a Mastodon
// handle carries its own instance (@user@instance ↔ https://instance/@user), so
// there's no single URLFmt.
const mastodonKey = "mastodon"

// linkPlatforms is the curated, ordered set of platforms shared by the org- and
// user-link editors. To add one: append it here with its Kind/URLFmt/Hosts and
// define its Icon's "icon-*" template (add a branch to link-icon in
// web/templates/icons.html). "website" is the generic full-URL fallback.
var linkPlatforms = []LinkPlatform{
	{Key: "website", Name: "Website", Kind: KindURL, Icon: "icon-link", Placeholder: "https://example.com"},
	{Key: "github", Name: "GitHub", Kind: KindHandle, Icon: "icon-brand-github", URLFmt: "https://github.com/%s", Hosts: []string{"github.com", "www.github.com"}, Placeholder: "@username or profile URL"},
	{Key: "x", Name: "X (Twitter)", Kind: KindHandle, Icon: "icon-brand-x", URLFmt: "https://x.com/%s", Hosts: []string{"x.com", "www.x.com", "twitter.com", "www.twitter.com", "mobile.twitter.com"}, Placeholder: "@username or profile URL"},
	{Key: "instagram", Name: "Instagram", Kind: KindHandle, Icon: "icon-brand-instagram", URLFmt: "https://instagram.com/%s", Hosts: []string{"instagram.com", "www.instagram.com"}, Placeholder: "@username or profile URL"},
	{Key: "facebook", Name: "Facebook", Kind: KindHandle, Icon: "icon-brand-facebook", URLFmt: "https://facebook.com/%s", Hosts: []string{"facebook.com", "www.facebook.com", "m.facebook.com", "fb.com"}, Placeholder: "username or profile URL"},
	{Key: "youtube", Name: "YouTube", Kind: KindHandle, Icon: "icon-brand-youtube", URLFmt: "https://youtube.com/@%s", Hosts: []string{"youtube.com", "www.youtube.com", "m.youtube.com"}, Placeholder: "@handle or channel URL"},
	{Key: "tiktok", Name: "TikTok", Kind: KindHandle, Icon: "icon-brand-tiktok", URLFmt: "https://tiktok.com/@%s", Hosts: []string{"tiktok.com", "www.tiktok.com"}, Placeholder: "@username or profile URL"},
	{Key: "twitch", Name: "Twitch", Kind: KindHandle, Icon: "icon-brand-twitch", URLFmt: "https://twitch.tv/%s", Hosts: []string{"twitch.tv", "www.twitch.tv"}, Placeholder: "username or channel URL"},
	{Key: "linkedin", Name: "LinkedIn", Kind: KindHandle, Icon: "icon-brand-linkedin", URLFmt: "https://linkedin.com/in/%s", Hosts: []string{"linkedin.com", "www.linkedin.com"}, Placeholder: "username or profile URL"},
	{Key: "reddit", Name: "Reddit", Kind: KindHandle, Icon: "icon-brand-reddit", URLFmt: "https://reddit.com/user/%s", Hosts: []string{"reddit.com", "www.reddit.com", "old.reddit.com"}, Placeholder: "u/username or profile URL"},
	{Key: "telegram", Name: "Telegram", Kind: KindHandle, Icon: "icon-brand-telegram", URLFmt: "https://t.me/%s", Hosts: []string{"t.me", "telegram.me"}, Placeholder: "@username or profile URL"},
	{Key: mastodonKey, Name: "Mastodon", Kind: KindHandle, Icon: "icon-brand-mastodon", Placeholder: "@user@instance or profile URL"},
	{Key: "bluesky", Name: "Bluesky", Kind: KindHandle, Icon: "icon-brand-bluesky", URLFmt: "https://bsky.app/profile/%s", Hosts: []string{"bsky.app"}, Placeholder: "@handle or profile URL"},
	{Key: "discord", Name: "Discord", Kind: KindText, Icon: "icon-brand-discord", Placeholder: "username or invite link"},
}

var linkPlatformByKey = func() map[string]LinkPlatform {
	m := make(map[string]LinkPlatform, len(linkPlatforms))
	for _, p := range linkPlatforms {
		m[p.Key] = p
	}
	return m
}()

// LinkPlatforms returns the selectable platforms, in display order.
func LinkPlatforms() []LinkPlatform { return linkPlatforms }

// ValidLinkPlatform reports whether key is one of the known platforms.
func ValidLinkPlatform(key string) bool {
	_, ok := linkPlatformByKey[key]
	return ok
}

// OrgLinkPlatform returns the descriptor for an org-link platform key.
func OrgLinkPlatform(key string) (LinkPlatform, bool) {
	p, ok := linkPlatformByKey[key]
	return p, ok
}

// CanonicalHandleURL interprets a KindHandle value that is either a bare handle
// (optionally "@"-prefixed, or a "u/" Reddit prefix) or a full profile URL on one
// of the platform's Hosts, and returns the canonical profile URL to store. It
// reports false when the value can't be read as a handle or an on-platform URL.
func (p LinkPlatform) CanonicalHandleURL(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	if p.Key == mastodonKey {
		return canonicalMastodon(s)
	}
	// A pasted URL is accepted only on a known host; the handle is its last path
	// segment. Anything else is treated as a bare handle.
	if LooksLikeURL(s) {
		u, err := url.Parse(NormalizeLinkURL(s))
		if err != nil || !hostAllowed(u.Host, p.Hosts) {
			return "", false
		}
		s = lastPathSegment(u.Path)
	}
	h := strings.TrimPrefix(strings.TrimSpace(s), "@")
	h = strings.TrimPrefix(h, "u/") // Reddit's "u/name" shorthand
	h = strings.Trim(h, "/")
	if !validHandle(h) {
		return "", false
	}
	return fmt.Sprintf(p.URLFmt, h), true
}

// HandleFromURL derives the display handle for a stored KindHandle URL: "@handle"
// for most platforms, "@user@instance" for Mastodon. Returns "" if it can't parse
// (callers fall back to the platform name).
func (p LinkPlatform) HandleFromURL(stored string) string {
	u, err := url.Parse(stored)
	if err != nil || u.Host == "" {
		return ""
	}
	if p.Key == mastodonKey {
		user := strings.TrimPrefix(lastPathSegment(u.Path), "@")
		if user == "" {
			return ""
		}
		return "@" + user + "@" + strings.ToLower(u.Host)
	}
	seg := strings.TrimPrefix(lastPathSegment(u.Path), "@")
	if seg == "" {
		return ""
	}
	return "@" + seg
}

// canonicalMastodon accepts "@user@instance", "user@instance", or a profile URL
// (https://instance/@user) and returns the canonical https://instance/@user.
func canonicalMastodon(s string) (string, bool) {
	if LooksLikeURL(s) {
		u, err := url.Parse(NormalizeLinkURL(s))
		if err != nil || u.Host == "" {
			return "", false
		}
		user := strings.TrimPrefix(lastPathSegment(u.Path), "@")
		if !validHandle(user) {
			return "", false
		}
		return "https://" + strings.ToLower(u.Host) + "/@" + user, true
	}
	parts := strings.SplitN(strings.TrimPrefix(s, "@"), "@", 2)
	if len(parts) != 2 || !validHandle(parts[0]) || !validInstanceHost(parts[1]) {
		return "", false
	}
	return "https://" + strings.ToLower(parts[1]) + "/@" + parts[0], true
}

// hostAllowed reports whether host (case-insensitive, port stripped) is one of
// the platform's recognized hosts.
func hostAllowed(host string, hosts []string) bool {
	host = strings.ToLower(host)
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	for _, h := range hosts {
		if host == h {
			return true
		}
	}
	return false
}

// lastPathSegment returns the last non-empty "/"-separated segment of a URL path.
func lastPathSegment(path string) string {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i := len(segs) - 1; i >= 0; i-- {
		if segs[i] != "" {
			return segs[i]
		}
	}
	return ""
}

// validHandle reports whether h is a plausible platform handle: letters, digits,
// and a few separators (dots allow domain-style handles like Bluesky's), bounded
// in length. It's intentionally lenient — a wrong guess just yields a slightly
// off URL, and the user can always paste the full profile URL instead.
func validHandle(h string) bool {
	if h == "" || len(h) > 100 {
		return false
	}
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// validInstanceHost reports whether s is a plausible Mastodon instance hostname
// (a dotted domain of handle-safe labels).
func validInstanceHost(s string) bool {
	if !strings.Contains(s, ".") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || !validHandle(label) {
			return false
		}
	}
	return true
}

// NormalizeLinkURL prepares a user-entered link value for validation: it trims
// surrounding whitespace and, when the value carries no scheme (e.g. a bare
// "example.com" or a protocol-relative "//example.com"), assumes https:// — what
// people expect when they type a domain into a link box. It does not validate;
// pair it with ValidLinkURL, which still rejects anything that isn't http(s). By
// only prepending when "://" is absent, an explicit "javascript:..." becomes
// "https://javascript:..." and is then rejected, rather than being trusted.
func NormalizeLinkURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if !strings.Contains(s, "://") {
		s = "https://" + strings.TrimPrefix(s, "//")
	}
	return s
}

// ValidLinkURL reports whether s is an absolute http(s) URL with a host. Limiting
// the scheme keeps javascript:/data: URLs out of rendered hrefs. Shared by the
// org- and user-link editors.
func ValidLinkURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// OrgLink is a single social/third-party link on an org's public page.
type OrgLink struct {
	ID       int64
	OrgID    int64
	Platform string
	Label    string
	URL      string
	Position int
}

// Display is the text to show for the link: the admin's custom label if set,
// otherwise a per-kind default (an "@handle" for handle platforms, the stored
// handle for text platforms, else the platform name).
func (l OrgLink) Display() string {
	if l.Label != "" {
		return l.Label
	}
	return linkDisplay(linkPlatformByKey[l.Platform], l.Platform, l.URL)
}

// Href is the hyperlink target for this link, or "" when it isn't directly
// linkable (a text-only platform such as Discord). Handle/URL platforms store a
// ready-to-use URL; unknown platforms fall back to the stored value.
func (l OrgLink) Href() string {
	return linkHref(linkPlatformByKey[l.Platform], l.URL)
}

// PlatformName is the platform's display name (e.g. "Discord"), used as the
// icon's hover title. Falls back to the raw key for an unknown platform.
func (l OrgLink) PlatformName() string {
	if p, ok := linkPlatformByKey[l.Platform]; ok {
		return p.Name
	}
	return l.Platform
}

// linkDisplay computes the default display text for a link value given its
// platform descriptor (zero value if the key is unknown).
func linkDisplay(p LinkPlatform, key, value string) string {
	switch p.Kind {
	case KindHandle:
		if h := p.HandleFromURL(value); h != "" {
			return h
		}
		return p.Name
	case KindText:
		// A text handle (e.g. a Discord username) shows itself; but the field also
		// accepts an invite/profile URL, which reads better as the platform name.
		if isHTTPURL(value) {
			return p.Name
		}
		return value
	case KindEmail:
		// Show the email address itself rather than the platform name.
		return value
	case "":
		return key // unknown platform — show the raw key defensively
	default: // KindURL, KindKey — the platform name (or a custom label upstream)
		return p.Name
	}
}

// linkHref computes the hyperlink target for a link value given its platform
// descriptor. Key platforms aren't linkable; email becomes mailto:. A text handle
// isn't linkable either — except the field accepts a URL (e.g. a Discord invite),
// which we do link.
func linkHref(p LinkPlatform, value string) string {
	switch p.Kind {
	case KindKey:
		return ""
	case KindText:
		if isHTTPURL(value) {
			return value
		}
		return ""
	case KindEmail:
		return "mailto:" + value
	default: // url, handle, or unknown → the stored value
		return value
	}
}

// isHTTPURL reports whether value is already a stored http(s) URL. Stored values
// are normalized on save, so a scheme prefix is a reliable signal.
func isHTTPURL(value string) bool {
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}

// LooksLikeURL reports whether raw was entered as a web URL — an explicit scheme,
// a protocol-relative "//", or a dotted host followed by a path like
// "discord.gg/abc" — rather than a bare handle. A handle may contain dots (new
// Discord usernames do) but never a slash, so the path is the reliable
// discriminator.
func LooksLikeURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "//") {
		return true
	}
	if i := strings.IndexByte(raw, '/'); i > 0 {
		return strings.Contains(raw[:i], ".")
	}
	return false
}

// ListOrgLinks returns an org's links in display order.
func (s *Store) ListOrgLinks(ctx context.Context, orgID int64) ([]OrgLink, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, platform, label, url, position
		 FROM org_links WHERE org_id = $1 ORDER BY position, id`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list org links: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (OrgLink, error) {
		var l OrgLink
		err := r.Scan(&l.ID, &l.OrgID, &l.Platform, &l.Label, &l.URL, &l.Position)
		return l, err
	})
}

// ReplaceOrgLinks atomically replaces an org's entire link set with links, in the
// given order. An empty slice clears all links. Platform/URL validation is the
// caller's responsibility.
func (s *Store) ReplaceOrgLinks(ctx context.Context, orgID int64, links []OrgLink) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM org_links WHERE org_id = $1`, orgID); err != nil {
			return fmt.Errorf("clear org links: %w", err)
		}
		for i, l := range links {
			if _, err := tx.Exec(ctx,
				`INSERT INTO org_links (org_id, platform, label, url, position)
				 VALUES ($1, $2, $3, $4, $5)`,
				orgID, l.Platform, l.Label, l.URL, i); err != nil {
				return fmt.Errorf("insert org link: %w", err)
			}
		}
		return nil
	})
}
