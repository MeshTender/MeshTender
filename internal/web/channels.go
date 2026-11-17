package web

import (
	"context"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/url"
	"strings"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/secretbox"
	"github.com/MeshTender/MeshTender/internal/store"
)

// The Guide's channels and regions: what a member adds to their companion to
// take part — each channel's key and suggested scope, with a QR code the
// MeshCore app scans — and the region tokens they can scope to.

// ChannelView is one channel as the Guide shows it.
type ChannelView struct {
	Name   string
	KeyHex string // the 16-byte key as the companion app shows it: 32 hex digits
	// Scope is the suggested scope in words ("lake", "your default scope",
	// "unscoped"); ScopeToken is the region token the QR code carries, "" for none.
	Scope      string
	ScopeToken string
	Note       string
	QR         template.URL // a data: URI; "" if encoding failed
	// Unjoinable flags a hashtag name with uppercase letters: the key comes from
	// the exact name, and companion apps only let members type lowercase, so
	// they couldn't join it by hand (the QR code still works).
	Unjoinable bool
}

// RegionRow is one of the org's regions in the Guide's list, indented by depth.
type RegionRow struct {
	Token, DisplayName string
	Depth              int
	DefaultScope       bool
}

// OrgChannels is the Guide's channels and regions. HiddenPrivate counts the
// private channels a viewer who isn't a member doesn't see.
type OrgChannels struct {
	Channels      []ChannelView
	HiddenPrivate int
	Regions       []RegionRow
}

// BuildOrgChannels loads an org's channels and regions for its Guide. A private
// channel's key is opened with masterKey only when showPrivate — for members,
// on the app host; anyone else gets a count of them instead.
func BuildOrgChannels(ctx context.Context, st *store.Store, orgID int64, masterKey [32]byte, showPrivate bool) (OrgChannels, error) {
	channels, err := st.ListOrgChannels(ctx, orgID)
	if err != nil {
		return OrgChannels{}, err
	}
	regions, err := st.ListRegions(ctx, orgID)
	if err != nil {
		return OrgChannels{}, err
	}
	var out OrgChannels
	for _, c := range channels {
		key, ok, err := channelKey(c, masterKey, showPrivate)
		if err != nil {
			return OrgChannels{}, err
		}
		if !ok {
			out.HiddenPrivate++
			continue
		}
		v := ChannelView{Name: c.Name, KeyHex: hex.EncodeToString(key[:]), Note: c.Note,
			Unjoinable: c.Hashtag() && UppercaseHashtag(c.Name)}
		v.Scope, v.ScopeToken = ChannelScope(c, regions)
		v.QR, _ = QRDataURI(MeshCoreChannelURI(v.Name, v.KeyHex, v.ScopeToken))
		out.Channels = append(out.Channels, v)
	}

	minLayer := 0
	for i, z := range regions {
		if i == 0 || z.Layer < minLayer {
			minLayer = z.Layer
		}
	}
	for _, z := range regions { // ListRegions orders them (layer, token): root to leaf
		out.Regions = append(out.Regions, RegionRow{Token: z.Token, DisplayName: z.DisplayName,
			Depth: z.Layer - minLayer, DefaultScope: z.DefaultScope})
	}
	return out, nil
}

// ChannelScope is a channel's suggested scope in words ("lake", "your default
// scope", "unscoped") and the region token a QR code carries for it: its region,
// or for the default scope the org's default-scope region, "" for none.
// regions is the org's (bounded; a linear scan).
func ChannelScope(c store.OrgChannel, regions []store.Region) (label, token string) {
	if c.Unscoped {
		return "unscoped", ""
	}
	for _, z := range regions {
		if c.ScopeRegionID != nil && z.ID == *c.ScopeRegionID {
			return z.Token, z.Token
		}
		if c.ScopeRegionID == nil && z.DefaultScope {
			token = z.Token
		}
	}
	return "your default scope", token
}

// channelKey is a channel's 16-byte key: a hashtag channel's derived from its
// name, a private channel's opened from its sealed copy. ok is false for a
// private channel the viewer may not see.
func channelKey(c store.OrgChannel, masterKey [32]byte, showPrivate bool) (key [16]byte, ok bool, err error) {
	if c.Hashtag() {
		return meshcore.DeriveHashtagPSK(c.Name), true, nil
	}
	if !showPrivate {
		return key, false, nil
	}
	plain, err := secretbox.Open(masterKey, c.SecretSealed)
	if err != nil || len(plain) != len(key) {
		return key, false, fmt.Errorf("open channel %d key: %w", c.ID, err)
	}
	copy(key[:], plain)
	return key, true, nil
}

// UppercaseHashtag reports a hashtag channel name with uppercase letters, which
// companion apps won't let members type.
func UppercaseHashtag(name string) bool { return strings.ToLower(name) != name }

// MeshCoreChannelURI builds the meshcore:// deep link that adds a channel in the
// MeshCore app (docs/qr_codes.md): its name, 16-byte key as 32 hex digits and,
// when given, the region token to scope it to — bare, as the app shows it.
func MeshCoreChannelURI(name, keyHex, regionScope string) string {
	q := url.Values{}
	q.Set("name", name)
	q.Set("secret", keyHex)
	if regionScope != "" {
		q.Set("region_scope", regionScope)
	}
	return "meshcore://channel/add?" + q.Encode()
}
