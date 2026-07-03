package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// MaxUserLinks caps how many contact/social links a user may list on their
// public profile. Same generous bound as orgs.
const MaxUserLinks = 20

// Platform keys whose values aren't plain http(s) URLs and so need their own
// validation and rendering: a MeshCore public key (hex, shown as a QR), an email
// address (a mailto: link), and a Signal username (shown as text — Signal has no
// stable link-from-username).
const (
	MeshCorePlatform = "meshcore"
	EmailPlatform    = "email"
	SignalPlatform   = "signal"
)

// userLinkPlatforms is the curated set a user can choose from: direct contact
// channels first (email, Signal, MeshCore), then the shared social platforms.
var userLinkPlatforms = append([]LinkPlatform{
	{Key: EmailPlatform, Name: "Email", Kind: KindEmail, Icon: "icon-mail", Placeholder: "you@example.com"},
	{Key: SignalPlatform, Name: "Signal", Kind: KindText, Icon: "icon-brand-signal", Placeholder: "@username"},
	{Key: MeshCorePlatform, Name: "MeshCore", Kind: KindKey, Icon: "icon-key", Placeholder: "64-character public key"},
}, linkPlatforms...)

var userLinkPlatformByKey = func() map[string]LinkPlatform {
	m := make(map[string]LinkPlatform, len(userLinkPlatforms))
	for _, p := range userLinkPlatforms {
		m[p.Key] = p
	}
	return m
}()

// UserLinkPlatforms returns the platforms a user may pick for a profile link.
func UserLinkPlatforms() []LinkPlatform { return userLinkPlatforms }

// UserLinkPlatform returns the descriptor for a user-link platform key.
func UserLinkPlatform(key string) (LinkPlatform, bool) {
	p, ok := userLinkPlatformByKey[key]
	return p, ok
}

// UserLink is a single contact/social link on a user's public profile.
type UserLink struct {
	ID       int64
	UserID   int64
	Platform string
	Label    string
	// URL holds an http(s) URL for most platforms, or a MeshCore public key (hex)
	// when Platform == MeshCorePlatform.
	URL       string
	Position  int
	IsPrimary bool
}

// Display is the text to show for the link: the custom label if set, otherwise a
// per-kind default (an "@handle" for handle platforms, the stored handle for text
// platforms such as Signal, else the platform name).
func (l UserLink) Display() string {
	if l.Label != "" {
		return l.Label
	}
	return linkDisplay(userLinkPlatformByKey[l.Platform], l.Platform, l.URL)
}

// IsMeshCore reports whether this link carries a MeshCore public key (rendered as
// a QR code) rather than an ordinary URL.
func (l UserLink) IsMeshCore() bool { return l.Platform == MeshCorePlatform }

// Href is the hyperlink target for this link, or "" when it isn't directly
// linkable (a MeshCore key renders as a QR; Signal/Discord handles as plain
// text). An email becomes a mailto: link; handle/URL platforms use the stored
// URL as-is.
func (l UserLink) Href() string {
	return linkHref(userLinkPlatformByKey[l.Platform], l.URL)
}

// PrimaryUserLink returns the link flagged as the primary contact, or nil if
// none is. Used to decide whether a publicly-listed user has a way to be reached.
func PrimaryUserLink(links []UserLink) *UserLink {
	for i := range links {
		if links[i].IsPrimary {
			return &links[i]
		}
	}
	return nil
}

// ListUserLinks returns a user's profile links in display order.
func (s *Store) ListUserLinks(ctx context.Context, userID int64) ([]UserLink, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, platform, label, url, position, is_primary
		 FROM user_links WHERE user_id = $1 ORDER BY position, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list user links: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (UserLink, error) {
		var l UserLink
		err := r.Scan(&l.ID, &l.UserID, &l.Platform, &l.Label, &l.URL, &l.Position, &l.IsPrimary)
		return l, err
	})
}

// ReplaceUserLinks atomically replaces a user's entire link set with links, in
// the given order. An empty slice clears all links. At most one link is stored
// as primary (the first flagged wins). Platform/URL validation is the caller's
// responsibility.
func (s *Store) ReplaceUserLinks(ctx context.Context, userID int64, links []UserLink) error {
	primarySeen := false
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM user_links WHERE user_id = $1`, userID); err != nil {
			return fmt.Errorf("clear user links: %w", err)
		}
		for i, l := range links {
			primary := l.IsPrimary && !primarySeen
			if primary {
				primarySeen = true
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO user_links (user_id, platform, label, url, position, is_primary)
				 VALUES ($1, $2, $3, $4, $5, $6)`,
				userID, l.Platform, l.Label, l.URL, i, primary); err != nil {
				return fmt.Errorf("insert user link: %w", err)
			}
		}
		return nil
	})
}
