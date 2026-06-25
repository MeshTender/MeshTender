// Package seed populates the database with realistic fake data (users, orgs,
// memberships, repeaters, shares, docs, locations) for local testing of things
// like directory pagination, maps, and the public pages. It is strictly
// additive — it never deletes or overwrites existing rows — so it is safe to run
// against a dev database more than once. Realistic values come from gofakeit
// (https://github.com/brianvoe/gofakeit), Go's analogue to .NET's Bogus.
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
		users = append(users, u)
	}
	if len(users) == 0 {
		return nil, fmt.Errorf("seed: created no users")
	}
	return users, nil
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
			// ~40% also publish a standalone public page (the NFC/QR target).
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
