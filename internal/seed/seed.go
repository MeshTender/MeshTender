// Package seed populates the database with realistic fake data (users with
// profiles and social links, orgs with public links, memberships, repeaters,
// shares, docs, locations) for local testing of things like directory
// pagination, maps, and the public pages. It is strictly additive — it never deletes or overwrites
// existing rows — so it is safe to run against a dev database more than once.
// Realistic values come from gofakeit (https://github.com/brianvoe/gofakeit),
// Go's analogue to .NET's Bogus.
package seed

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/brianvoe/gofakeit/v7"

	"github.com/jleight/meshtender/internal/store"
)

// How much to generate. numOrgs is deliberately above OrgsPageSize (50) so the
// public directory spans more than one page.
const (
	numUsers          = 60
	numOrgs           = 70
	maxRepeatersPerUs = 6
)

// radioPreset is a realistic MeshCore LoRa configuration (freq Hz, bw Hz, SF, CR).
type radioPreset struct {
	freq, bw int64
	sf, cr   int16
}

var radioPresets = []radioPreset{
	{906875000, 250000, 11, 5}, // US 915 MHz MeshCore default
	{869525000, 250000, 11, 5}, // EU 868 MHz
	{867500000, 250000, 10, 5}, // EU alternate
	{915000000, 250000, 12, 8}, // long-range / slow
}

var repeaterSuffixes = []string{"Repeater", "Hilltop", "Mesh Node", "Relay", "Ridge Node", "Tower", "Summit Relay"}
var orgKinds = []string{"Amateur Radio Club", "Mesh Network", "Repeater Group", "Radio Society", "ARES Group", "Mesh Collective", "Emergency Net"}

// Run generates the fake dataset. It logs progress and tolerates the occasional
// unique-name collision by skipping that single record.
func Run(ctx context.Context, st *store.Store, logger *slog.Logger) error {
	f := gofakeit.New(uint64(time.Now().UnixNano())) // fresh seed: repeated runs don't collide

	users, err := seedUsers(ctx, st, f, logger)
	if err != nil {
		return err
	}
	logger.Info("seed: users", "count", len(users))

	reps := seedRepeaters(ctx, st, f, users, logger)
	logger.Info("seed: repeaters", "count", len(reps))

	seedShares(ctx, st, f, users, reps)
	logger.Info("seed: shares done")

	orgs := seedOrgs(ctx, st, f, users, logger)
	logger.Info("seed: orgs", "count", orgs)

	return nil
}

func seedUsers(ctx context.Context, st *store.Store, f *gofakeit.Faker, logger *slog.Logger) ([]*store.User, error) {
	users := make([]*store.User, 0, numUsers)
	taken := map[string]bool{}
	for i := 0; i < numUsers; i++ {
		first, last := f.FirstName(), f.LastName()
		username := uniqueName(sanitizeUsername(first+"."+last), taken)
		u, err := st.CreateUser(ctx, username, first+" "+last)
		if err != nil {
			logger.Warn("seed: create user", "username", username, "err", err)
			continue
		}
		taken[username] = true
		seedUserProfile(ctx, st, f, u)
		users = append(users, u)
	}
	if len(users) == 0 {
		return nil, fmt.Errorf("seed: created no users")
	}
	return users, nil
}

// mastodonInstances are a few real-looking Mastodon instances to spread seeded
// Mastodon handles across.
var mastodonInstances = []string{"mastodon.social", "fosstodon.org", "hachyderm.io", "mstdn.social"}

// socialSeeds lists the handle-based platforms sprinkled onto seeded profiles,
// with the odds each user gets one. Keys must exist in store.UserLinkPlatforms.
var socialSeeds = []struct {
	key string
	pct int
}{
	{"github", 45}, {"x", 40}, {"instagram", 35}, {"mastodon", 25},
	{"bluesky", 25}, {"youtube", 20}, {"linkedin", 20}, {"telegram", 15},
	{"reddit", 15}, {"tiktok", 10}, {"twitch", 10}, {"facebook", 15},
}

// seedUserProfile fills a seeded user's public profile fields and a random spread
// of contact/social links. Best-effort: it logs nothing and ignores errors, like
// the other optional seed steps.
func seedUserProfile(ctx context.Context, st *store.Store, f *gofakeit.Faker, u *store.User) {
	var bio, location, callsign string
	if chance(f, 80) {
		bio = f.Sentence(f.Number(8, 18))
	}
	if chance(f, 70) {
		location = f.City() + ", " + f.StateAbr()
	}
	if chance(f, 40) {
		callsign = fakeCallsign(f)
	}
	if bio != "" || location != "" || callsign != "" {
		_ = st.SetProfile(ctx, u.ID, bio, location, callsign)
	}

	if links := seedUserLinks(f); len(links) > 0 {
		_ = st.ReplaceUserLinks(ctx, u.ID, links)
	}
}

// seedUserLinks builds a random, plausible set of profile links, all pre-stored
// in canonical form (handle platforms hold their canonical profile URL, matching
// what the editor produces). One non-MeshCore link is usually the primary contact.
func seedUserLinks(f *gofakeit.Faker) []store.UserLink {
	var links []store.UserLink
	if chance(f, 40) {
		links = append(links, store.UserLink{Platform: "website", URL: f.URL()})
	}
	if chance(f, 30) {
		links = append(links, store.UserLink{Platform: store.EmailPlatform, URL: f.Email()})
	}
	for _, s := range socialSeeds {
		if !chance(f, s.pct) {
			continue
		}
		if p, ok := store.UserLinkPlatform(s.key); ok {
			if v, ok := handleSeedURL(p, f); ok {
				links = append(links, store.UserLink{Platform: s.key, URL: v})
			}
		}
	}
	if chance(f, 25) {
		links = append(links, store.UserLink{Platform: store.SignalPlatform, URL: seedHandle(f)})
	}
	if chance(f, 25) {
		links = append(links, store.UserLink{Platform: "discord", URL: seedHandle(f)})
	}
	if chance(f, 50) {
		if pk, err := randomHex(32); err == nil {
			links = append(links, store.UserLink{Platform: store.MeshCorePlatform, Label: f.City() + " Node", URL: pk})
		}
	}
	// Promote the first reachable (non-MeshCore) link to primary contact.
	if chance(f, 75) {
		for i := range links {
			if links[i].Platform != store.MeshCorePlatform {
				links[i].IsPrimary = true
				break
			}
		}
	}
	return links
}

// handleSeedURL builds a canonical profile URL for a freshly generated handle on
// platform p (the social descriptors are shared by org and user links), via the
// platform's own canonicaliser. Mastodon handles get a random instance.
func handleSeedURL(p store.LinkPlatform, f *gofakeit.Faker) (string, bool) {
	h := seedHandle(f)
	if p.Key == "mastodon" {
		h += "@" + mastodonInstances[f.Number(0, len(mastodonInstances)-1)]
	}
	return p.CanonicalHandleURL(h)
}

// seedOrgLinks builds a random set of public links for a seeded org: a website, a
// community Discord, and a spread of social platforms, all in canonical form. Org
// links have no email/Signal/MeshCore or primary contact.
func seedOrgLinks(f *gofakeit.Faker) []store.OrgLink {
	var links []store.OrgLink
	if chance(f, 70) {
		links = append(links, store.OrgLink{Platform: "website", URL: f.URL()})
	}
	if chance(f, 45) {
		links = append(links, store.OrgLink{Platform: "discord", URL: seedHandle(f)})
	}
	for _, s := range socialSeeds {
		if !chance(f, s.pct) {
			continue
		}
		if p, ok := store.OrgLinkPlatform(s.key); ok {
			if v, ok := handleSeedURL(p, f); ok {
				links = append(links, store.OrgLink{Platform: s.key, URL: v})
			}
		}
	}
	return links
}

// seedHandle returns a valid social handle (letters/digits/._-).
func seedHandle(f *gofakeit.Faker) string { return sanitizeUsername(f.Username()) }

// fakeCallsign builds a plausible amateur-radio callsign, e.g. "W1AW" or "KD7ABC".
func fakeCallsign(f *gofakeit.Faker) string {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	prefix := []string{"K", "N", "W", "A"}[f.Number(0, 3)]
	if chance(f, 40) {
		prefix += string(letters[f.Number(0, 25)])
	}
	var suffix strings.Builder
	for i, n := 0, f.Number(1, 3); i < n; i++ {
		suffix.WriteByte(letters[f.Number(0, 25)])
	}
	return fmt.Sprintf("%s%d%s", prefix, f.Number(0, 9), suffix.String())
}

// repRef is the minimum we keep about a created repeater for the sharing pass.
type repRef struct{ id, ownerID int64 }

func seedRepeaters(ctx context.Context, st *store.Store, f *gofakeit.Faker, users []*store.User, logger *slog.Logger) []repRef {
	var out []repRef
	for _, u := range users {
		for j := 0; j < f.Number(0, maxRepeatersPerUs); j++ {
			preset := radioPresets[f.Number(0, len(radioPresets)-1)]
			pk, _ := randomHex(32)
			showOnOrg := chance(f, 60)
			rep, err := st.CreateRepeater(ctx, &store.Repeater{
				OwnerID: u.ID, Name: repeaterName(f), PublicKeyHex: pk,
				RadioFreqHz: preset.freq, RadioBwHz: preset.bw, RadioSF: preset.sf, RadioCR: preset.cr,
				ShowOnPublicOrg: showOnOrg,
			})
			if err != nil {
				logger.Warn("seed: create repeater", "err", err)
				continue
			}
			// ~70% have a known location (continental-US bounding box keeps maps sane).
			if chance(f, 70) {
				_ = st.SetRepeaterLocation(ctx, rep.ID, f.Float64Range(25, 49), f.Float64Range(-124, -67))
			}
			// ~40% also publish a standalone public page.
			if chance(f, 40) {
				_ = st.UpdateRepeater(ctx, u.ID, rep.ID, rep.Name, preset.freq, preset.bw, preset.sf, preset.cr, showOnOrg, true)
			}
			_ = st.UpdateRepeaterDocs(ctx, u.ID, rep.ID, f.Paragraph(1, 3, 14, " "), f.Paragraph(1, 2, 10, " "))
			// ~70% confirmed with admin access (perms 3 = admin, per the confirm flow).
			if chance(f, 70) {
				_ = st.SetRepeaterConfirmed(ctx, rep.ID, u.ID, true, 3)
			}
			backdate(ctx, st, "repeaters", rep.ID, f)
			out = append(out, repRef{rep.ID, u.ID})
		}
	}
	return out
}

func seedShares(ctx context.Context, st *store.Store, f *gofakeit.Faker, users []*store.User, reps []repRef) {
	for _, rp := range reps {
		if !chance(f, 35) { // ~35% of repeaters are shared with someone
			continue
		}
		for s := 0; s < f.Number(1, 3); s++ {
			other := users[f.Number(0, len(users)-1)]
			if other.ID == rp.ownerID {
				continue
			}
			if ok, _ := st.AddShare(ctx, rp.id, other.ID); ok && chance(f, 50) {
				_ = st.SetShareSteward(ctx, rp.id, other.ID, true)
			}
		}
	}
}

func seedOrgs(ctx context.Context, st *store.Store, f *gofakeit.Faker, users []*store.User, logger *slog.Logger) int {
	created := 0
	for i := 0; i < numOrgs; i++ {
		creator := users[f.Number(0, len(users)-1)]
		org, err := st.CreateOrg(ctx, orgName(f), creator.ID) // creator joins as admin
		if err != nil {
			logger.Warn("seed: create org", "err", err)
			continue
		}
		_ = st.UpdateOrg(ctx, org.ID, org.Slug, org.Name, f.Sentence(f.Number(8, 20)), f.State())
		if links := seedOrgLinks(f); len(links) > 0 {
			_ = st.ReplaceOrgLinks(ctx, org.ID, links)
		}
		// A random subset of users join, so member counts vary across orgs.
		joined := map[int64]bool{creator.ID: true}
		for s := 0; s < f.Number(0, len(users)-1); s++ {
			u := users[f.Number(0, len(users)-1)]
			if joined[u.ID] {
				continue
			}
			joined[u.ID] = true
			role := "member"
			if chance(f, 20) {
				role = "admin"
			}
			_ = st.AddOrgMember(ctx, org.ID, u.ID, role)
		}
		backdate(ctx, st, "organizations", org.ID, f)
		created++
	}
	return created
}

// --- helpers ---

// chance reports true pct% of the time.
func chance(f *gofakeit.Faker, pct int) bool { return f.Number(1, 100) <= pct }

func repeaterName(f *gofakeit.Faker) string {
	return f.City() + " " + repeaterSuffixes[f.Number(0, len(repeaterSuffixes)-1)]
}

func orgName(f *gofakeit.Faker) string {
	return f.City() + " " + orgKinds[f.Number(0, len(orgKinds)-1)]
}

// backdate spreads created_at across the last ~2 years so the "newest" sort and
// "Added" dates look realistic. table is a fixed internal constant — never user
// input — so the interpolation is safe.
func backdate(ctx context.Context, st *store.Store, table string, id int64, f *gofakeit.Faker) {
	when := time.Now().AddDate(0, 0, -f.Number(1, 730)).Add(-time.Duration(f.Number(0, 86400)) * time.Second)
	_, _ = st.Pool().Exec(ctx, fmt.Sprintf("UPDATE %s SET created_at = $1 WHERE id = $2", table), when, id)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// sanitizeUsername lowercases and keeps only [a-z0-9._-].
func sanitizeUsername(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		out = "user"
	}
	return out
}

// uniqueName returns base, or base with a numeric suffix if base is taken.
func uniqueName(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s%d", base, i)
		if !taken[cand] {
			return cand
		}
	}
}
