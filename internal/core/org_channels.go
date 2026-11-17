package core

import (
	"crypto/rand"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/MeshTender/MeshTender/internal/secretbox"
	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// Settings › Channels: the channels an org recommends its members join, shown in
// its Guide. A name starting with "#" is a hashtag channel, keyed by its name;
// any other name is private, with a key we store sealed (internal/secretbox)
// and show only to members.

// maxChannelNote bounds a channel's note (the column's CHECK says the same).
const maxChannelNote = 200

// channelForm is the channel form's values: a channel's own, or what was
// submitted when it's re-shown with an error. Scope is "default", "unscoped" or
// a region id. Key is only ever what was typed — a stored key isn't put back in
// the form.
type channelForm struct {
	ID                     int64
	Name, Key, Scope, Note string
}

func channelFormFrom(c store.OrgChannel) channelForm {
	f := channelForm{ID: c.ID, Name: c.Name, Note: c.Note, Scope: "default"}
	switch {
	case c.Unscoped:
		f.Scope = "unscoped"
	case c.ScopeRegionID != nil:
		f.Scope = strconv.FormatInt(*c.ScopeRegionID, 10)
	}
	return f
}

// parseChannelForm validates the form against the org's regions. key is the
// private key typed in (nil when none was). msg is a user-facing error.
func parseChannelForm(r *http.Request, regions []store.Region) (f channelForm, c store.OrgChannel, key []byte, msg string) {
	f = channelForm{Name: strings.TrimSpace(r.FormValue("name")), Key: strings.TrimSpace(r.FormValue("key")),
		Scope: r.FormValue("scope"), Note: strings.TrimSpace(r.FormValue("note"))}
	c = store.OrgChannel{Name: f.Name, Note: f.Note}
	switch {
	case f.Name == "" || f.Name == "#":
		return f, c, nil, "Give the channel a name."
	case len(f.Name) > maxChannelName || !plainText(f.Name):
		return f, c, nil, "The channel name can be up to 31 characters."
	case store.IsHashtagChannel(f.Name) && strings.ContainsFunc(f.Name, unicode.IsSpace):
		return f, c, nil, "A hashtag channel's name can't have spaces."
	}
	if !store.IsHashtagChannel(f.Name) && f.Key != "" {
		k, ok := parseChannelKey(f.Key)
		if !ok {
			return f, c, nil, "The channel key must be 32 hex digits (or base64), as the companion app shows it."
		}
		key = k[:]
	}
	switch f.Scope {
	case "default", "":
		f.Scope = "default"
	case "unscoped":
		c.Unscoped = true
	default:
		id, err := strconv.ParseInt(f.Scope, 10, 64)
		found := false
		for _, z := range regions {
			found = found || (err == nil && z.ID == id)
		}
		if !found {
			return f, c, nil, "Choose one of this organization's regions."
		}
		c.ScopeRegionID = &id
	}
	if utf8.RuneCountInString(f.Note) > maxChannelNote || !plainText(f.Note) {
		return f, c, nil, "The note can be up to 200 characters."
	}
	return f, c, key, ""
}

// channelRow is a channel in the Settings list, with its suggested scope in words.
type channelRow struct {
	store.OrgChannel
	Scope string
}

// channelSettingsURL is Settings › Channels, or one channel's page there.
func channelSettingsURL(slug string, id int64) string {
	if id == 0 {
		return settingsURL(slug, "channels")
	}
	return settingsURL(slug, "channels") + "/" + strconv.FormatInt(id, 10)
}

// pageNewChannel is the list's New page.
func (s *Handlers) pageNewChannel(w http.ResponseWriter, r *http.Request) {
	if org, ok := s.requireSettings(w, r); ok {
		s.renderSettings(w, r, org, "channels", map[string]any{"New": true, "ChannelForm": channelForm{Scope: "default"}})
	}
}

// pageChannel edits one channel, showing its key — the admin is a member.
func (s *Handlers) pageChannel(w http.ResponseWriter, r *http.Request) {
	org, ok := s.requireSettings(w, r)
	if !ok {
		return
	}
	c, ok := s.settingsChannel(w, r, org)
	if !ok {
		return
	}
	s.renderChannelEdit(w, r, org, c, channelFormFrom(*c), "")
}

// settingsChannel loads the {cid} channel of org, writing a 404 if it isn't one.
func (s *Handlers) settingsChannel(w http.ResponseWriter, r *http.Request, org *store.Org) (*store.OrgChannel, bool) {
	cid, err := strconv.ParseInt(chi.URLParam(r, "cid"), 10, 64)
	if err != nil {
		s.NotFound(w, r)
		return nil, false
	}
	c, err := s.Store.GetOrgChannel(r.Context(), org.ID, cid)
	if errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return nil, false
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't load the channel.", err)
		return nil, false
	}
	return c, true
}

// renderChannelEdit renders a channel's page with its current key.
func (s *Handlers) renderChannelEdit(w http.ResponseWriter, r *http.Request, org *store.Org, c *store.OrgChannel, f channelForm, msg string) {
	views, err := web.BuildOrgChannels(r.Context(), s.Store, org.ID, s.Cfg.MasterKey, true)
	if err != nil {
		s.ServerError(w, r, "Couldn't load the channel.", err)
		return
	}
	var keyHex string
	for _, v := range views.Channels {
		if v.Name == c.Name {
			keyHex = v.KeyHex
		}
	}
	w.Header().Set("Cache-Control", "private, no-store") // it can carry a private key
	s.renderSettings(w, r, org, "channels", map[string]any{"ChannelEdit": c, "ChannelForm": f, "KeyHex": keyHex, "ChannelError": msg,
		"ChannelUppercase": c.Hashtag() && web.UppercaseHashtag(c.Name)})
}

// handleCreateChannel adds a channel. A private channel without a key typed in
// gets a random one.
func (s *Handlers) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	org, regions, ok := s.channelAdmin(w, r)
	if !ok {
		return
	}
	f, c, key, msg := parseChannelForm(r, regions)
	if msg == "" && !store.IsHashtagChannel(c.Name) {
		if c.SecretSealed, ok = s.sealChannelKey(w, r, key); !ok {
			return
		}
	}
	if msg == "" {
		_, err := s.Store.CreateOrgChannel(r.Context(), org.ID, c)
		switch {
		case errors.Is(err, store.ErrDuplicate):
			msg = "This organization already has a channel by that name."
		case errors.Is(err, store.ErrChannelLimit):
			msg = "This organization has the most channels it can list."
		case err != nil:
			s.ServerError(w, r, "Couldn't save the channel.", err)
			return
		}
	}
	if msg != "" {
		s.renderSettings(w, r, org, "channels", map[string]any{"New": true, "ChannelForm": f, "ChannelError": msg})
		return
	}
	web.RedirectOK(w, r, channelSettingsURL(org.Slug, 0), "Channel added.")
}

// handleUpdateChannel edits a channel. A private channel keeps its key unless a
// new one is typed in; one renamed from a hashtag without a key gets a random one.
func (s *Handlers) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	org, regions, ok := s.channelAdmin(w, r)
	if !ok {
		return
	}
	current, ok := s.settingsChannel(w, r, org)
	if !ok {
		return
	}
	f, c, key, msg := parseChannelForm(r, regions)
	f.ID = current.ID
	if msg == "" && !store.IsHashtagChannel(c.Name) && (key != nil || current.Hashtag()) {
		if c.SecretSealed, ok = s.sealChannelKey(w, r, key); !ok {
			return
		}
	}
	if msg == "" {
		err := s.Store.UpdateOrgChannel(r.Context(), org.ID, current.ID, c)
		switch {
		case errors.Is(err, store.ErrDuplicate):
			msg = "This organization already has a channel by that name."
		case errors.Is(err, store.ErrNotFound):
			s.NotFound(w, r)
			return
		case err != nil:
			s.ServerError(w, r, "Couldn't save the channel.", err)
			return
		}
	}
	if msg != "" {
		s.renderChannelEdit(w, r, org, current, f, msg)
		return
	}
	web.RedirectOK(w, r, channelSettingsURL(org.Slug, 0), "Channel saved.")
}

// handleDeleteChannel removes a channel.
func (s *Handlers) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	org, _, ok := s.channelAdmin(w, r)
	if !ok {
		return
	}
	cid, err := strconv.ParseInt(chi.URLParam(r, "cid"), 10, 64)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	if err := s.Store.DeleteOrgChannel(r.Context(), org.ID, cid); errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return
	} else if err != nil {
		s.ServerError(w, r, "Couldn't delete the channel.", err)
		return
	}
	web.RedirectOK(w, r, channelSettingsURL(org.Slug, 0), "Channel deleted.")
}

// channelAdmin resolves the org for a channel write (admins only) and its
// regions, the scope choices.
func (s *Handlers) channelAdmin(w http.ResponseWriter, r *http.Request) (*store.Org, []store.Region, bool) {
	orgID, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return nil, nil, false
	}
	org, err := s.Store.GetOrg(r.Context(), orgID)
	if err != nil {
		s.NotFound(w, r)
		return nil, nil, false
	}
	regions, err := s.Store.ListRegions(r.Context(), orgID)
	if err != nil {
		s.ServerError(w, r, "Couldn't load regions.", err)
		return nil, nil, false
	}
	return org, regions, true
}

// sealChannelKey seals a private channel's key for storage, generating a random
// one when key is nil. ok is false once it has responded with a server error.
func (s *Handlers) sealChannelKey(w http.ResponseWriter, r *http.Request, key []byte) (sealed []byte, ok bool) {
	if key == nil {
		key = make([]byte, 16)
		if _, err := rand.Read(key); err != nil {
			s.ServerError(w, r, "Couldn't make a channel key.", err)
			return nil, false
		}
	}
	sealed, err := secretbox.Seal(s.Cfg.MasterKey, key)
	if err != nil {
		s.ServerError(w, r, "Couldn't store the channel key.", err)
		return nil, false
	}
	return sealed, true
}
