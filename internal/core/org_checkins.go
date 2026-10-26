package core

import (
	"context"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/MeshTender/MeshTender/internal/ingest"
	"github.com/MeshTender/MeshTender/internal/secretbox"
	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// The org's check-in plumbing: its MQTT broker and registered observers
// (/orgs/{id}/observers) and its members' companion activity
// (/orgs/{id}/activity). Members can see both; only admins change them.

// topicPrefixRE is one MQTT topic level with no wildcards: the ingester
// subscribes to <prefix>/+/+/packets and parses the prefix as one segment.
var topicPrefixRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// orgMemberPage loads the org and the viewer's role for a members-only tab,
// writing a 404 for non-members. ok is false when a response was written.
func (s *Handlers) orgMemberPage(w http.ResponseWriter, r *http.Request) (org *store.Org, isAdmin bool, ok bool) {
	id, found := s.orgID(r)
	if !found {
		s.NotFound(w, r)
		return nil, false, false
	}
	org, err := s.Store.GetOrg(r.Context(), id)
	if err != nil {
		s.NotFound(w, r)
		return nil, false, false
	}
	role, isMember, err := s.Store.OrgRole(r.Context(), id, s.Auth.CurrentUserID(r.Context()))
	if err != nil {
		s.ServerError(w, r, "Couldn't load your membership.", err)
		return nil, false, false
	}
	if !isMember {
		s.NotFound(w, r)
		return nil, false, false
	}
	return org, role == "admin", true
}

func (s *Handlers) orgNav(ctx context.Context, org *store.Org, active string, isAdmin bool) map[string]any {
	return s.OrgNavFor(ctx, web.OrgNavArgs{OrgID: org.ID, Name: org.Name, Slug: org.Slug, Active: active,
		IsMember: true, IsAdmin: isAdmin, Manage: true})
}

// mqttURL is Settings › MQTT, where the broker and observers are managed.
func mqttURL(r *http.Request) string { return settingsURL(orgParam(r), "mqtt") }

func (s *Handlers) handleSaveOrgBroker(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	fail := func(msg string) { web.RedirectErr(w, r, mqttURL(r), msg) }

	rawURL := strings.TrimSpace(r.FormValue("url"))
	if _, err := ingest.ParseBrokerURL(rawURL); err != nil {
		fail("Broker URL: " + err.Error() + ".")
		return
	}
	// Check the address now so a private one is refused at the form, not
	// discovered later in the status line. The ingester checks again on every
	// dial, since DNS can change.
	lookupCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	_, err := ingest.ResolveTarget(lookupCtx, rawURL, net.DefaultResolver, ingest.PublicAddr)
	cancel()
	if err != nil {
		fail(ingest.CategoryMessage(ingest.Category(err)))
		return
	}
	prefix := strings.TrimSpace(r.FormValue("topic_prefix"))
	if prefix == "" {
		prefix = "meshcore"
	}
	if !topicPrefixRE.MatchString(prefix) {
		fail("The topic prefix must be a single topic level: letters, digits, '.', '_' or '-'.")
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	if utf8.RuneCountInString(username) > 256 {
		fail("The username is too long.")
		return
	}
	var sealed []byte
	if pw := r.FormValue("password"); pw != "" {
		if sealed, err = secretbox.Seal(s.Cfg.MasterKey, []byte(pw)); err != nil {
			s.ServerError(w, r, "Couldn't store the password.", err)
			return
		}
	}
	b := store.OrgBroker{OrgID: id, URL: rawURL, Username: username, TopicPrefix: prefix,
		Enabled: r.FormValue("enabled") == "1"}
	if err := s.Store.UpsertOrgBroker(r.Context(), b, sealed, r.FormValue("clear_password") == "1",
		s.Auth.CurrentUserID(r.Context())); err != nil {
		s.ServerError(w, r, "Couldn't save the broker.", err)
		return
	}
	web.RedirectOK(w, r, mqttURL(r), "Broker saved. MeshTender connects within a minute.")
}

func (s *Handlers) handleDeleteOrgBroker(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteOrgBroker(r.Context(), id); err != nil {
		s.ServerError(w, r, "Couldn't remove the broker.", err)
		return
	}
	web.RedirectOK(w, r, mqttURL(r), "Broker removed.")
}

// handleTestOrgBroker connects to the saved broker once (address guard and all)
// and reports the result, so an admin doesn't have to wait for the ingester's
// next attempt to find a typo.
func (s *Handlers) handleTestOrgBroker(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	b, err := s.Store.GetOrgBroker(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		web.RedirectErr(w, r, mqttURL(r), "Save the broker settings first.")
		return
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't load the broker.", err)
		return
	}
	var password string
	if b.HasPassword() {
		pt, err := secretbox.Open(s.Cfg.MasterKey, b.PasswordSealed)
		if err != nil {
			web.RedirectErr(w, r, mqttURL(r), ingest.CategoryMessage(ingest.CategoryConfig))
			return
		}
		password = string(pt)
	}
	// Connect bounds itself (dial, handshakes and CONNACK each time out).
	client, err := ingest.NewDialer().Connect(ingest.BrokerConfig{OrgID: id, URL: b.URL, Username: b.Username, Password: password}, nil)
	if err != nil {
		web.RedirectErr(w, r, mqttURL(r), ingest.CategoryMessage(ingest.Category(err)))
		return
	}
	client.Disconnect(250)
	web.RedirectOK(w, r, mqttURL(r), "Connected to the broker.")
}

func (s *Handlers) handleAddOrgObserver(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	fail := func(msg string) { web.RedirectErr(w, r, mqttURL(r)+"/observers/new", msg) }
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || utf8.RuneCountInString(name) > maxCompanionName {
		fail("Give the observer a name (up to 64 characters).")
		return
	}
	key, ok := normalizeCompanionKey(r.FormValue("public_key"))
	if !ok {
		fail("That isn't a MeshCore public key (64 hex characters).")
		return
	}
	err := s.Store.CreateOrgObserver(r.Context(), id, name, key, s.Auth.CurrentUserID(r.Context()))
	if errors.Is(err, store.ErrDuplicate) {
		fail("That observer is already approved.")
		return
	}
	if err != nil {
		s.ServerError(w, r, "Couldn't add the observer.", err)
		return
	}
	http.Redirect(w, r, mqttURL(r), http.StatusSeeOther) //nolint:gosec // G710: local path
}

// observerListURL is the MQTT settings page showing the observer list the
// admin acted from (the form's "from"), so approving one after another keeps
// them on the Unapproved list.
func observerListURL(r *http.Request) string {
	return mqttURL(r) + "?observers=" + string(observerFilter(r.FormValue("from"))) + "#observers"
}

// orgObserverAction resolves the org (admins only) and the observer id for a
// POST on one of its observers. ok is false when a response was written.
func (s *Handlers) orgObserverAction(w http.ResponseWriter, r *http.Request) (orgID, observerID int64, ok bool) {
	orgID, ok = s.requireOrgAdmin(w, r)
	if !ok {
		return 0, 0, false
	}
	observerID, err := strconv.ParseInt(chi.URLParam(r, "oid"), 10, 64)
	if err != nil {
		s.NotFound(w, r)
		return 0, 0, false
	}
	return orgID, observerID, true
}

// finishObserverAction answers an observer action's store result: 404 for an
// observer the org doesn't have, else back to the list.
func (s *Handlers) finishObserverAction(w http.ResponseWriter, r *http.Request, err error, failMsg string) {
	if errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return
	}
	if err != nil {
		s.ServerError(w, r, failMsg, err)
		return
	}
	http.Redirect(w, r, observerListURL(r), http.StatusSeeOther) //nolint:gosec // G710: local path
}

// handleApproveOrgObserver approves an observer under the given name, or
// renames an approved one.
func (s *Handlers) handleApproveOrgObserver(w http.ResponseWriter, r *http.Request) {
	id, oid, ok := s.orgObserverAction(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || utf8.RuneCountInString(name) > maxCompanionName {
		page := mqttURL(r) + "/observers/" + strconv.FormatInt(oid, 10) + "?from=" + string(observerFilter(r.FormValue("from")))
		web.RedirectErr(w, r, page, "Give the observer a name (up to 64 characters).")
		return
	}
	err := s.Store.ApproveOrgObserver(r.Context(), id, oid, name, s.Auth.CurrentUserID(r.Context()))
	s.finishObserverAction(w, r, err, "Couldn't approve the observer.")
}

func (s *Handlers) handleUnapproveOrgObserver(w http.ResponseWriter, r *http.Request) {
	id, oid, ok := s.orgObserverAction(w, r)
	if ok {
		s.finishObserverAction(w, r, s.Store.UnapproveOrgObserver(r.Context(), id, oid), "Couldn't unapprove the observer.")
	}
}

func (s *Handlers) handleDeleteOrgObserver(w http.ResponseWriter, r *http.Request) {
	id, oid, ok := s.orgObserverAction(w, r)
	if ok {
		s.finishObserverAction(w, r, s.Store.DeleteOrgObserver(r.Context(), id, oid), "Couldn't remove the observer.")
	}
}

func (s *Handlers) pageOrgActivity(w http.ResponseWriter, r *http.Request) {
	org, isAdmin, ok := s.orgMemberPage(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	after, _ := web.DecodeCursor[store.ActivityCursor](r.URL.Query().Get("after"))
	var cursor *store.ActivityCursor
	if after.ID != 0 {
		cursor = &after
	}
	rows, hasMore, err := s.Store.ListOrgCompanionActivityPage(ctx, org.ID, cursor)
	if err != nil {
		s.ServerError(w, r, "Couldn't load activity.", err)
		return
	}
	data := map[string]any{
		"Org":      org,
		"Nav":      s.orgNav(ctx, org, "activity", isAdmin),
		"IsAdmin":  isAdmin,
		"Activity": rows,
		"First":    cursor == nil,
	}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		data["NextAfter"] = web.EncodeCursor(store.ActivityCursor{Name: last.Name, ID: last.ID})
	}
	s.Render(w, r, "org_activity.html", data)
}
