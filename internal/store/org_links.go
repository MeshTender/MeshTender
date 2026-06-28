package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// MaxOrgLinks caps how many social/site links an org may list. Enough for the
// usual suspects (Discord, a few sites) without turning the page into a directory.
const MaxOrgLinks = 20

// LinkPlatform is a known destination an org link can point at. Key is the stable
// identifier persisted in org_links.platform and used to pick a brand icon; Name
// is the human label shown when a link has no custom label of its own.
type LinkPlatform struct {
	Key  string
	Name string
}

// linkPlatforms is the curated, ordered set of platforms an admin can choose from.
// "website" is the generic fallback for anything without a dedicated brand icon.
// To add a platform: append it here and define a matching "icon-brand-<key>" (or
// reuse an existing icon) in the link-icon partial in web/templates/icons.html.
var linkPlatforms = []LinkPlatform{
	{"website", "Website"},
	{"discord", "Discord"},
	{"facebook", "Facebook"},
	{"instagram", "Instagram"},
	{"x", "X (Twitter)"},
	{"youtube", "YouTube"},
	{"github", "GitHub"},
	{"telegram", "Telegram"},
	{"reddit", "Reddit"},
	{"linkedin", "LinkedIn"},
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

// linkPlatformName returns the display name for a platform key, or the key itself
// if it is unknown (defensive — stored rows should always be valid).
func linkPlatformName(key string) string {
	if p, ok := linkPlatformByKey[key]; ok {
		return p.Name
	}
	return key
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
// otherwise the platform's name (e.g. "Discord").
func (l OrgLink) Display() string {
	if l.Label != "" {
		return l.Label
	}
	return linkPlatformName(l.Platform)
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
