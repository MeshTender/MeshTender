package core

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/jleight/meshtender/internal/store"
)

// TestDeleteAccountPageShowsConsequences: the confirm page has to state what
// would actually be destroyed, and offer the handover for a repeater that has a
// steward — that link is the whole reason transfer exists.
func TestDeleteAccountPageShowsConsequences(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	authSSO(t, ts, h, "bootstrapadmin")
	sso := authSSO(t, ts, h, "leaving")
	u, err := st.GetUserByUsername(ctx, "leaving")
	if err != nil {
		t.Fatal(err)
	}
	rep := newOwnedRepeater(t, st, ctx, u.ID, "Hilltop")
	steward, err := st.CreateUser(ctx, "keeper", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddShare(ctx, rep.ID, steward.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetShareSteward(ctx, rep.ID, steward.ID, true); err != nil {
		t.Fatal(err)
	}

	resp := do(t, ts, h.auth, "/account/delete", sso)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /account/delete = %d, want 200", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	page := string(raw)
	if !strings.Contains(page, "Hilltop") {
		t.Fatal("confirm page doesn't name the repeater that would be destroyed")
	}
	if !strings.Contains(page, "/repeaters/"+rep.PublicID+"/transfer") {
		t.Fatal("confirm page doesn't offer to transfer a repeater that has a steward")
	}
	// The account page must lead here.
	acct := do(t, ts, h.auth, "/account", sso)
	defer acct.Body.Close()
	accRaw, _ := io.ReadAll(acct.Body)
	if !strings.Contains(string(accRaw), `href="/account/delete"`) {
		t.Fatal("account page has no link to delete the account")
	}
}

// TestDeleteAccountRequiresPassword is the re-auth gate: a live session alone
// must not be enough to destroy the account.
func TestDeleteAccountRequiresPassword(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	authSSO(t, ts, h, "bootstrapadmin")
	sso := authSSO(t, ts, h, "cautious")
	u, err := st.GetUserByUsername(ctx, "cautious")
	if err != nil {
		t.Fatal(err)
	}

	// No password at all.
	empty := post(t, ts, h.auth, "/account/delete", url.Values{}, sso)
	empty.Body.Close()
	assertRedirect(t, empty, "/account/delete", "delete with no proof")
	if loc, _ := url.Parse(empty.Header.Get("Location")); loc.Query().Get("error") == "" {
		t.Fatal("refusal carried no error message")
	}

	// Wrong password.
	wrong := post(t, ts, h.auth, "/account/delete", url.Values{"password": {"not-the-password"}}, sso)
	wrong.Body.Close()
	assertRedirect(t, wrong, "/account/delete", "delete with wrong password")

	if _, err := st.GetUserByID(ctx, u.ID); err != nil {
		t.Fatalf("account was deleted without a valid password: %v", err)
	}
}

// TestDeleteAccountSucceeds: with the right password the account and its data go,
// the session is torn down, and the freed username is held in reserve.
func TestDeleteAccountSucceeds(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	authSSO(t, ts, h, "bootstrapadmin")
	sso := authSSO(t, ts, h, "goodbye")
	u, err := st.GetUserByUsername(ctx, "goodbye")
	if err != nil {
		t.Fatal(err)
	}
	rep := newOwnedRepeater(t, st, ctx, u.ID, "Doomed")

	resp := post(t, ts, h.auth, "/account/delete", url.Values{"password": {testPassword}}, sso)
	resp.Body.Close()
	assertRedirect(t, resp, "/login", "delete account")
	if loc, _ := url.Parse(resp.Header.Get("Location")); loc.Query().Get("ok") == "" {
		t.Fatal("successful deletion carried no confirmation")
	}

	if _, err := st.GetUserByID(ctx, u.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetUserByID after delete = %v, want ErrNotFound", err)
	}
	if _, err := st.RepeaterIDByPublicID(ctx, rep.PublicID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("owned repeater survived the account: %v", err)
	}

	// The session is dead everywhere: the old cookie no longer authenticates on
	// the auth host (the logins row cascaded away with the account).
	after := do(t, ts, h.auth, "/account", sso)
	defer after.Body.Close()
	if after.StatusCode == http.StatusOK {
		t.Fatal("the deleted account's session still opens the account page")
	}
}

// TestDeleteAccountBlockedBySoleOrgAdmin: the page explains the blocker instead
// of offering a button that would fail, and the POST refuses too (a user who
// skips the page must not get further than one who reads it).
func TestDeleteAccountBlockedBySoleOrgAdmin(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	authSSO(t, ts, h, "bootstrapadmin")
	sso := authSSO(t, ts, h, "clubadmin")
	u, err := st.GetUserByUsername(ctx, "clubadmin")
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(ctx, "Radio Club", u.ID)
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.CreateUser(ctx, "clubmember", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}

	page := do(t, ts, h.auth, "/account/delete", sso)
	defer page.Body.Close()
	raw, _ := io.ReadAll(page.Body)
	body := string(raw)
	if !strings.Contains(body, "Radio Club") {
		t.Fatal("confirm page doesn't name the org blocking deletion")
	}
	if strings.Contains(body, `data-testid="confirm-delete"`) {
		t.Fatal("confirm page offers a delete button that could only fail")
	}

	resp := post(t, ts, h.auth, "/account/delete", url.Values{"password": {testPassword}}, sso)
	resp.Body.Close()
	assertRedirect(t, resp, "/account/delete", "blocked delete")
	if _, err := st.GetUserByID(ctx, u.ID); err != nil {
		t.Fatalf("account deleted despite being an org's only admin: %v", err)
	}
}

// TestReauthPasskeyBeginRequiresSession: the re-auth ceremony is session-scoped
// and takes no username, so it can't be used anonymously or as an account oracle.
func TestReauthPasskeyBeginRequiresSession(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)

	anon := post(t, ts, h.auth, "/account/reauth/passkey/begin", url.Values{})
	defer anon.Body.Close()
	// Unauthenticated requests are bounced by the session guard before reaching
	// the handler; either way they must not get a challenge.
	if anon.StatusCode == http.StatusOK {
		t.Fatalf("anonymous reauth/begin = 200, want a rejection")
	}
}

// TestReauthPasskeyBeginWithoutPasskey: an account with no passkey gets a clean
// 400 rather than an unusable challenge.
func TestReauthPasskeyBeginWithoutPasskey(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)
	authSSO(t, ts, h, "bootstrapadmin")
	sso := authSSO(t, ts, h, "nopasskey")

	resp := post(t, ts, h.auth, "/account/reauth/passkey/begin", url.Values{}, sso)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("reauth/begin with no passkey = %d, want 400", resp.StatusCode)
	}
}
