package core

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/MeshTender/MeshTender/internal/netair"
	"github.com/MeshTender/MeshTender/internal/secretbox"
	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// A net's on-air settings: how net control's modem talks to it (store.NetAir).

const (
	// maxChannelName is a channel name's limit in the companion firmware
	// (ChannelDetails::name is 32 bytes with its terminator).
	maxChannelName = 31
	maxLogPrefix   = 16
)

// netAirView is the on-air card's data.
type netAirView struct {
	*store.NetAir
	Regions []store.Region // admins' scope choices
}

// UnjoinableHashtag reports a hashtag channel name with uppercase letters.
// The channel's key is derived from the exact name, and current companion apps
// only let members type lowercase names, so they couldn't join it.
func (v netAirView) UnjoinableHashtag() bool {
	return v.ChannelKind == store.ChannelHashtag && strings.ToLower(v.ChannelName) != v.ChannelName
}

// LowerChannelName is the channel name members could type.
func (v netAirView) LowerChannelName() string { return strings.ToLower(v.ChannelName) }

// ScopeID is the scope region's id, 0 when unscoped.
func (v netAirView) ScopeID() int64 {
	if v.ScopeRegionID == nil {
		return 0
	}
	return *v.ScopeRegionID
}

// parseChannelKey reads a private channel's 16-byte key as companion apps
// share it: 32 hex digits, or base64.
func parseChannelKey(s string) ([16]byte, bool) {
	var key [16]byte
	s = strings.TrimSpace(s)
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 16 {
		if b, err = base64.StdEncoding.DecodeString(s); err != nil || len(b) != 16 {
			return key, false
		}
	}
	copy(key[:], b)
	return key, true
}

// plainText reports whether s has no control characters.
func plainText(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) < 0
}

// parseNetAirForm validates the on-air form against the org's regions. The
// form has a field per channel kind — hashtag_name, and private_label with
// channel_key — and only the chosen kind's are read. key is the private channel
// key when one was entered. msg is a user-facing error.
func parseNetAirForm(r *http.Request, regions []store.Region) (a store.NetAir, key []byte, msg string) {
	a.ChannelKind = r.FormValue("channel_kind")
	switch a.ChannelKind {
	case store.ChannelPublic:
	case store.ChannelHashtag:
		// The key is SHA256 of the exact "#name", so keep it as typed.
		a.ChannelName = strings.TrimPrefix(strings.TrimSpace(r.FormValue("hashtag_name")), "#")
		if a.ChannelName == "" || len(a.ChannelName)+1 > maxChannelName || strings.ContainsFunc(a.ChannelName, unicode.IsSpace) ||
			!plainText(a.ChannelName) {
			return a, nil, "Enter the hashtag channel's name, without spaces (up to 30 characters)."
		}
	case store.ChannelPrivate:
		a.ChannelName = strings.TrimSpace(r.FormValue("private_label"))
		if len(a.ChannelName) > maxChannelName || !plainText(a.ChannelName) {
			return a, nil, "The channel name is too long."
		}
		if raw := r.FormValue("channel_key"); strings.TrimSpace(raw) != "" {
			k, ok := parseChannelKey(raw)
			if !ok {
				return a, nil, "The channel key must be 32 hex digits (or base64), as the companion app shows it."
			}
			key = k[:]
		}
	default:
		return a, nil, "Choose a channel."
	}
	if v := r.FormValue("scope_region"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		found := false
		for _, rg := range regions {
			found = found || (err == nil && rg.ID == id)
		}
		if !found {
			return a, nil, "Choose one of this organization's regions."
		}
		a.ScopeRegionID = &id
	}
	a.OnAirName = strings.TrimSpace(r.FormValue("on_air_name"))
	if len(a.OnAirName) > netair.MaxSender || strings.Contains(a.OnAirName, ":") || !plainText(a.OnAirName) {
		// Receivers split "Sender: text" at the first ": ".
		return a, nil, "The on-air name can be up to 32 characters, without a colon."
	}
	a.LogPrefix = strings.TrimSpace(r.FormValue("log_prefix"))
	if utf8.RuneCountInString(a.LogPrefix) > maxLogPrefix || !plainText(a.LogPrefix) {
		return a, nil, "The log prefix can be up to 16 characters."
	}
	return a, key, ""
}

// handleUpdateNetAir saves a net's on-air settings. A rejected form re-shows
// what was entered with the error (never the key).
func (s *Handlers) handleUpdateNetAir(w http.ResponseWriter, r *http.Request) {
	org, net, ok := s.settingsNet(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	regions, err := s.Store.ListRegions(ctx, org.ID)
	if err != nil {
		s.ServerError(w, r, "Couldn't load regions.", err)
		return
	}
	current, err := s.Store.GetNetAir(ctx, org.ID, net.ID)
	if err != nil {
		s.ServerError(w, r, "Couldn't load the net.", err)
		return
	}
	a, key, msg := parseNetAirForm(r, regions)
	if msg == "" && key == nil && a.ChannelKind == store.ChannelPrivate && (current.ChannelKind != store.ChannelPrivate || !current.HasKey()) {
		msg = "Enter the private channel's key."
	}
	if msg != "" {
		// Re-show the entries; a stored key stays stored, so say so.
		if current.ChannelKind == store.ChannelPrivate {
			a.PSKSealed = current.PSKSealed
		}
		s.renderNetSettings(w, r, org, net, map[string]any{"Air": netAirView{NetAir: &a, Regions: regions}, "AirError": msg})
		return
	}
	var sealed []byte
	if key != nil {
		if sealed, err = secretbox.Seal(s.Cfg.MasterKey, key); err != nil {
			s.ServerError(w, r, "Couldn't store the channel key.", err)
			return
		}
	}
	err = s.Store.UpdateNetAir(ctx, org.ID, net.ID, a, sealed)
	if errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't save the on-air settings.", err)
		return
	}
	web.RedirectOK(w, r, netSettingsURL(org.Slug, net.ID), "On-air settings saved.")
}
