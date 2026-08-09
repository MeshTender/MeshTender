package auth

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// passkeyView is display metadata for one registered passkey.
type passkeyView struct {
	ID      int64
	ShortID string
	Name    string
	Added   time.Time
}

// accountRedirect bounces back to the account page with a flash message under
// the given key ("ok" or "error"), shown in the page-level banner.
func accountRedirect(w http.ResponseWriter, r *http.Request, key, msg string) {
	web.RedirectFlash(w, r, "/account", key, msg)
}

// passkeyRedirect bounces back to the account page with a flash shown inside the
// "Sign-in & security" card ("pk" for success, "pkerr" for failure), so passkey
// messages stay next to the passkey controls rather than in the top banner.
func passkeyRedirect(w http.ResponseWriter, r *http.Request, key, msg string) {
	web.RedirectFlash(w, r, "/account", key, msg)
}

// pageAccount renders the current user's account-settings page.
func (s *Handlers) pageAccount(w http.ResponseWriter, r *http.Request) {
	s.renderAccount(w, r, s.Auth.CurrentUserID(r.Context()), nil)
}

// renderAccount assembles the account-page data and renders it, applying
// overrides last. Overrides let a failed POST re-render the page with the user's
// just-submitted values and an inline error instead of redirecting to a fresh
// page (which would show the unchanged stored data and lose their work).
func (s *Handlers) renderAccount(w http.ResponseWriter, r *http.Request, uid int64, overrides map[string]any) {
	u, err := s.Store.GetUserByID(r.Context(), uid)
	if err != nil {
		http.Error(w, "could not load account", http.StatusInternalServerError)
		return
	}
	creds, err := s.Store.ListCredentials(r.Context(), uid)
	if err != nil {
		http.Error(w, "could not load passkeys", http.StatusInternalServerError)
		return
	}
	views := make([]passkeyView, 0, len(creds))
	for _, c := range creds {
		short := hex.EncodeToString(c.CredentialID)
		if len(short) > 12 {
			short = short[:12]
		}
		views = append(views, passkeyView{ID: c.ID, ShortID: short, Name: c.Name, Added: c.CreatedAt})
	}
	// When set, the user changed their username recently and must wait until
	// this time before changing it again.
	nextRename, err := s.Store.NextRenameAllowed(r.Context(), uid)
	if err != nil {
		http.Error(w, "could not load account", http.StatusInternalServerError)
		return
	}
	links, err := s.Store.ListUserLinks(r.Context(), uid)
	if err != nil {
		http.Error(w, "could not load profile links", http.StatusInternalServerError)
		return
	}
	data := map[string]any{
		"User":        u,
		"DisplayName": u.DisplayName, // nil when unset
		"Bio":         u.Bio,
		"Location":    u.Location,
		"Callsign":    u.Callsign,
		"Timezone":    u.Timezone, // "" when unset (browser auto-detects)
		"Links":       links,
		"Platforms":   store.UserLinkPlatforms(),
		"PlatformsJS": web.LinkPlatformsJS(store.UserLinkPlatforms()),
		"HasPassword": u.PasswordHash != nil,
		// Email state, rendered honestly rather than as a single "has email" flag:
		// what an address does for this account depends on whether it's confirmed AND
		// whether there's a password to reset (see store.User.CanResetPassword).
		"Email":          u.Email, // nil when unset
		"EmailVerified":  u.EmailVerified(),
		"CanResetByMail": u.CanResetPassword(),
		// False when no mail provider is configured, which hides the whole card:
		// offering recovery we can't deliver is worse than not offering it.
		"MailEnabled": s.Auth.MailEnabled(),
		// Stated in the change-password form and used as minlength, so the client
		// hint can't drift from the server's floor.
		"MinPasswordLen": MinPasswordLen,
		"Passkeys":       views,
		"NextRename":     nextRename, // nil when a rename is allowed now
		"Error":          r.URL.Query().Get("error"),
		"OK":             r.URL.Query().Get("ok"),
		"PKMsg":          r.URL.Query().Get("pk"),
		"PKErr":          r.URL.Query().Get("pkerr"),
		"EmMsg":          r.URL.Query().Get(flashEmail),
		"EmErr":          r.URL.Query().Get(flashEmailErr),
	}
	for k, v := range overrides {
		data[k] = v
	}
	s.Render(w, r, "account.html", data)
}

// handleChangeUsername renames the current user, enforcing validation, the
// per-user rename interval, and the release cooldown on names others hold.
func (s *Handlers) handleChangeUsername(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.Auth.CurrentUserID(ctx)
	newName := NormalizeUsername(r.FormValue("username"))
	if !ValidUsername(newName) {
		accountRedirect(w, r, "error", "Choose a username 3–32 characters long, using only letters, digits, and _ . -")
		return
	}
	meta := store.UsernameChangeContext{ChangedBy: uid, IP: web.ClientIP(r), UserAgent: r.UserAgent()}
	err := s.Store.SetUsername(ctx, uid, newName, meta, true)
	switch {
	case errors.Is(err, store.ErrDuplicate), errors.Is(err, store.ErrUsernameReserved):
		// Collapse "taken" and "reserved" into one message so we don't reveal
		// that a name was previously in use by someone else.
		accountRedirect(w, r, "error", "That username isn't available. Please choose another.")
	case errors.Is(err, store.ErrRenameTooSoon):
		accountRedirect(w, r, "error", "You can only change your username once every 30 days.")
	case err != nil:
		accountRedirect(w, r, "error", "Could not change your username.")
	default:
		accountRedirect(w, r, "ok", "Username changed to @"+newName+".")
	}
}

// handleSaveProfile saves the user's whole public profile from the single form
// on the account page: display name, the text fields shown on their public page,
// and the link set. One form, one endpoint, one transaction — nothing here is
// meaningful on its own, and saving them separately let a user leave the page
// having stored half of what they'd written.
func (s *Handlers) handleSaveProfile(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	if err := r.ParseForm(); err != nil {
		accountRedirect(w, r, "error", "Could not save your profile.")
		return
	}
	p := store.UserProfile{
		DisplayName: NormalizeDisplayName(r.FormValue("display_name")),
		Bio:         boundedText(r.FormValue("bio"), 500),
		Location:    boundedText(r.FormValue("location"), 120),
		Callsign:    boundedText(r.FormValue("callsign"), 32),
	}
	links, errMsg := parseUserLinks(r.Form)
	if errMsg != "" {
		// Re-render the form with everything the user typed and the error, so their
		// work survives the round-trip. 200, not a redirect: there's nothing new to
		// bookmark and we need the POST body to rebuild the fields. Every field goes
		// back, not just the links — they're one form now, so a bad link row must not
		// cost someone the bio they wrote next to it.
		s.renderAccount(w, r, uid, map[string]any{
			"DisplayName": p.DisplayName,
			"Bio":         p.Bio,
			"Location":    p.Location,
			"Callsign":    p.Callsign,
			"Links":       submittedUserLinks(r.Form),
			"Error":       errMsg,
		})
		return
	}
	if err := s.Store.SaveUserProfile(r.Context(), uid, p, links); err != nil {
		accountRedirect(w, r, "error", "Could not save your profile.")
		return
	}
	accountRedirect(w, r, "ok", "Profile updated.")
}

// handleSetTimezone saves the user's preferred IANA time zone for date/time
// display. An empty value clears it (the browser auto-detects). The submitted
// value is validated against the tz database before it's stored.
func (s *Handlers) handleSetTimezone(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	tz := strings.TrimSpace(r.FormValue("timezone"))
	if !web.ValidTimeZone(tz) {
		accountRedirect(w, r, "error", "That isn't a recognized time zone.")
		return
	}
	if err := s.Store.SetTimezone(r.Context(), uid, tz); err != nil {
		accountRedirect(w, r, "error", "Could not save your time zone.")
		return
	}
	if tz == "" {
		accountRedirect(w, r, "ok", "Time zone set to auto-detect.")
		return
	}
	accountRedirect(w, r, "ok", "Time zone updated.")
}

// parseUserLinks validates the repeatable link rows posted by the editor into
// the set we'd persist. Rows with a blank value are dropped. Most rows carry an
// http(s) URL; a "meshcore" row carries a MeshCore public key (validated as hex)
// and renders as a QR code. The optional primary-contact radio flags one
// non-MeshCore link as the preferred way to reach the user. It returns the first
// validation problem as a message; an empty message means every row was fine.
func parseUserLinks(form url.Values) ([]store.UserLink, string) {
	// Index-aligned parallel arrays, one entry per row, in row order.
	platforms := form["link_platform"]
	labels := form["link_label"]
	urls := form["link_url"]
	primaryIdx := primaryLinkIndex(form)
	// Validate every non-empty row into the set we'd persist. On the first
	// problem we remember the message but keep parsing, so a failed save can
	// re-render every row the user entered (see errMsg handling below) rather
	// than silently dropping the ones after the bad one.
	var links []store.UserLink
	errMsg := ""
	for i, raw := range urls {
		val := strings.TrimSpace(raw)
		if val == "" {
			continue // empty row — skip it
		}
		platform := ""
		if i < len(platforms) {
			platform = platforms[i]
		}
		p, ok := store.UserLinkPlatform(platform)
		if !ok {
			setIfEmpty(&errMsg, "Choose a type for each link.")
			continue
		}
		// Validate and canonicalise the value according to the platform's kind. Each
		// case leaves `val` as the string we'd persist.
		switch p.Kind {
		case store.KindKey:
			val = strings.ToLower(val)
			if !validMeshCoreKey(val) {
				setIfEmpty(&errMsg, "Enter a valid MeshCore public key (64-character hex).")
				continue
			}
		case store.KindEmail:
			addr, err := mail.ParseAddress(val)
			if err != nil || addr.Name != "" {
				setIfEmpty(&errMsg, "Enter a valid email address.")
				continue
			}
			val = addr.Address
		case store.KindText:
			v, msg := normalizeTextHandle(platform, val)
			if msg != "" {
				setIfEmpty(&errMsg, msg)
				continue
			}
			val = v
		case store.KindHandle:
			// Accept a bare "@handle" or a pasted profile URL; store the canonical URL.
			canon, ok := p.CanonicalHandleURL(val)
			if !ok {
				setIfEmpty(&errMsg, "Enter a valid "+p.Name+" username or profile URL.")
				continue
			}
			val = canon
		default: // KindURL
			// Accept a bare domain ("example.com") by assuming https://; only then
			// require it to be a real http(s) URL.
			val = store.NormalizeLinkURL(val)
			if !store.ValidLinkURL(val) {
				setIfEmpty(&errMsg, "Each link must be a valid http:// or https:// URL.")
				continue
			}
		}
		label := ""
		if i < len(labels) {
			label = strings.TrimSpace(labels[i])
		}
		val = web.Clip(val, 300)
		label = web.Clip(label, 60)
		// A MeshCore key is an identity, not a way to reach someone, so it can't be
		// the primary contact (mirrors excluding callsign/node info).
		primary := i == primaryIdx && p.Kind != store.KindKey
		links = append(links, store.UserLink{Platform: platform, Label: label, URL: val, IsPrimary: primary})
		if len(links) >= store.MaxUserLinks {
			break
		}
	}
	return links, errMsg
}

// primaryLinkIndex reads the primary-contact radio, whose value is the row index
// (renumbered to DOM order on submit). It returns -1 when no primary is chosen.
func primaryLinkIndex(form url.Values) int {
	n, err := strconv.Atoi(form.Get("link_primary"))
	if err != nil {
		return -1
	}
	return n
}

// setIfEmpty stores msg in *dst only if it's still empty, so we surface the first
// validation problem while continuing to parse the remaining rows.
func setIfEmpty(dst *string, msg string) {
	if *dst == "" {
		*dst = msg
	}
}

// submittedUserLinks reconstructs the editor rows a user just posted, keeping
// their raw (untrimmed-of-meaning) values, so a failed save can re-render exactly
// what they typed. Empty rows are dropped to match the save path; the primary
// radio is preserved by index.
func submittedUserLinks(form url.Values) []store.UserLink {
	platforms := form["link_platform"]
	labels := form["link_label"]
	urls := form["link_url"]
	primaryIdx := primaryLinkIndex(form)
	var rows []store.UserLink
	for i, raw := range urls {
		val := strings.TrimSpace(raw)
		if val == "" {
			continue
		}
		platform := ""
		if i < len(platforms) {
			platform = platforms[i]
		}
		label := ""
		if i < len(labels) {
			label = strings.TrimSpace(labels[i])
		}
		rows = append(rows, store.UserLink{
			Platform:  platform,
			Label:     label,
			URL:       val,
			IsPrimary: i == primaryIdx,
		})
	}
	return rows
}

// boundedText trims s and caps it at max bytes (empty means "unset").
func boundedText(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		s = s[:max]
	}
	return s
}

// validMeshCoreKey reports whether s is a valid MeshCore public key (a 32-byte
// Ed25519 key, hex-encoded), using the MeshCore library's own parser.
func validMeshCoreKey(s string) bool {
	_, err := meshcore.NewIdentityFromHex(s)
	return err == nil
}

// normalizeTextHandle validates and trims a KindText handle (a value shown as
// plain text, no link) and returns (stored value, "") or ("", errorMessage).
// Signal enforces its username grammar; other text platforms (Discord) just need
// a non-empty, space-free handle.
func normalizeTextHandle(platform, val string) (string, string) {
	val = strings.TrimSpace(val)
	if platform == store.SignalPlatform {
		v := strings.TrimPrefix(val, "@")
		if !validSignalUsername(v) {
			return "", "Enter a valid Signal username (3–32 characters: letters, digits, . and _)."
		}
		return v, ""
	}
	// A text handle (Discord) also accepts an invite/profile URL, stored and
	// rendered as a clickable link; a bare username stays plain text.
	if store.LooksLikeURL(val) {
		u := store.NormalizeLinkURL(val)
		if !store.ValidLinkURL(u) {
			return "", "Enter a valid username or invite link."
		}
		return u, ""
	}
	v := strings.TrimPrefix(val, "@")
	if v == "" || len(v) > 64 || strings.ContainsAny(v, " \t\n\r") {
		return "", "Enter a valid username (no spaces) or an invite link."
	}
	return v, ""
}

// validSignalUsername reports whether s is a plausible Signal username: 3–32
// characters of letters, digits, dot, or underscore (Signal usernames carry a
// dotted numeric discriminator, e.g. alice.42).
func validSignalUsername(s string) bool {
	if len(s) < 3 || len(s) > 32 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_':
		default:
			return false
		}
	}
	return true
}

// handleChangePassword sets, changes, or removes the user's password.
func (s *Handlers) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.Auth.CurrentUserID(ctx)
	u, err := s.Store.GetUserByID(ctx, uid)
	if err != nil {
		accountRedirect(w, r, "error", "Could not load account.")
		return
	}

	if r.FormValue("remove") == "1" {
		if u.PasswordHash == nil {
			accountRedirect(w, r, "error", "No password to remove.")
			return
		}
		n, err := s.Store.CountCredentials(ctx, uid)
		if err != nil {
			accountRedirect(w, r, "error", "Could not load passkeys.")
			return
		}
		if n < 1 {
			accountRedirect(w, r, "error", "Add a passkey before removing your password — it's your only way to sign in.")
			return
		}
		// Removing a password permanently narrows how this account can be recovered,
		// so it takes a fresh assertion — the same bar as deleting the account. A live
		// session only says somebody signed in here at some point; the passkey says
		// the account holder is at the keyboard now. The UI runs the ceremony before
		// submitting, so reaching this message means the proof expired or the form was
		// posted directly.
		if !s.Auth.ReauthFresh(ctx) {
			accountRedirect(w, r, "error", "Verify with your passkey to remove your password.")
			return
		}
		if err := s.Store.ClearPassword(ctx, uid); err != nil {
			accountRedirect(w, r, "error", "Could not remove password.")
			return
		}
		accountRedirect(w, r, "ok", "Password removed. You'll sign in with a passkey.")
		return
	}

	newPassword := r.FormValue("new_password")
	if !ValidPassword(newPassword) {
		accountRedirect(w, r, "error", fmt.Sprintf(
			"New password must be at least %d characters.", MinPasswordLen))
		return
	}
	// The confirmation field catches a typo in a value the user can't see. Checked
	// server-side as well as by the form, since the form is only a convenience.
	if r.FormValue("confirm_password") != newPassword {
		accountRedirect(w, r, "error", "The new passwords don't match.")
		return
	}
	// When a password already exists, require the current one to change it.
	if u.PasswordHash != nil && !s.Auth.PasswordMatches(u, r.FormValue("current_password")) {
		accountRedirect(w, r, "error", "Current password is incorrect.")
		return
	}
	if err := s.Auth.SetPassword(ctx, uid, newPassword); err != nil {
		accountRedirect(w, r, "error", "Could not update password.")
		return
	}
	accountRedirect(w, r, "ok", "Password updated.")
}

// handleRenamePasskey sets or clears the human-friendly label on one of the
// user's passkeys.
func (s *Handlers) handleRenamePasskey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.Auth.CurrentUserID(ctx)
	credID, err := strconv.ParseInt(r.FormValue("credential_id"), 10, 64)
	if err != nil {
		passkeyRedirect(w, r, "pkerr", "Invalid passkey.")
		return
	}
	name := NormalizePasskeyName(r.FormValue("name"))
	if err := s.Store.SetCredentialName(ctx, uid, credID, name); err != nil {
		passkeyRedirect(w, r, "pkerr", "Could not rename that passkey.")
		return
	}
	passkeyRedirect(w, r, "pk", "Passkey name saved.")
}

// handleDeletePasskey removes one of the user's passkeys, refusing to remove
// the last sign-in method.
func (s *Handlers) handleDeletePasskey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := s.Auth.CurrentUserID(ctx)
	credID, err := strconv.ParseInt(r.FormValue("credential_id"), 10, 64)
	if err != nil {
		passkeyRedirect(w, r, "pkerr", "Invalid passkey.")
		return
	}
	u, err := s.Store.GetUserByID(ctx, uid)
	if err != nil {
		passkeyRedirect(w, r, "pkerr", "Could not load account.")
		return
	}
	n, err := s.Store.CountCredentials(ctx, uid)
	if err != nil {
		passkeyRedirect(w, r, "pkerr", "Could not load passkeys.")
		return
	}
	// After removal the user must retain at least one way to sign in.
	if n-1 < 1 && u.PasswordHash == nil {
		passkeyRedirect(w, r, "pkerr", "You can't remove your only sign-in method. Add another passkey or set a password first.")
		return
	}
	if err := s.Store.DeleteCredential(ctx, uid, credID); err != nil {
		passkeyRedirect(w, r, "pkerr", "Could not remove that passkey.")
		return
	}
	passkeyRedirect(w, r, "pk", "Passkey removed.")
}
