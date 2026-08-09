package core

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestSharePageShowsInviteExpiry checks how the share page presents link expiry to
// the owner:
//   - the intro copy states the shelf life, so an owner knows a link is time-boxed
//     before they hand it out
//   - a live link shows its expiry alongside a copy control
//   - an expired link is badged Expired and its copy control is GONE — offering to
//     copy a token that can no longer be redeemed just produces a confused recipient
//
// Companion to the store-level tests for audit finding S3.
func TestSharePageShowsInviteExpiry(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	owner := seedSession(t, ts, st, ctx, jar, "shareexpiry")
	rep, err := st.CreateRepeater(ctx, &store.Repeater{
		OwnerID: owner.ID, Name: "Rep", PublicKeyHex: strings.Repeat("b", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}
	cookies := jar.Cookies(mustURL(t, ts.URL))

	sharePage := func() string {
		t.Helper()
		resp := do(t, ts, h.app, "/repeaters/"+rep.PublicID+"/share", cookies...)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("share page = %d, want 200", resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		return string(body)
	}

	// A live link.
	liveToken, err := st.CreateInvite(ctx, rep.ID, "live link", nil)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	page := sharePage()
	if !strings.Contains(page, "expire 7 days") {
		t.Error("share page doesn't state how long links last")
	}
	if !strings.Contains(page, liveToken) {
		t.Error("live link's token isn't shown for copying")
	}
	if !strings.Contains(page, "Expires") {
		t.Error("live link doesn't show its expiry date")
	}
	if strings.Contains(page, `data-testid="invite-expired"`) {
		t.Error("a live link is badged Expired")
	}

	// Now age it out.
	if _, err := st.Pool().Exec(ctx,
		`UPDATE repeater_invites SET expires_at = now() - interval '1 minute' WHERE token = $1`,
		liveToken); err != nil {
		t.Fatalf("expire invite: %v", err)
	}

	page = sharePage()
	if !strings.Contains(page, `data-testid="invite-expired"`) {
		t.Error("expired link isn't badged Expired")
	}
	if strings.Contains(page, liveToken) {
		t.Error("expired link still offers its token to copy — it can't be redeemed")
	}
	if !strings.Contains(page, "can no longer be accepted") {
		t.Error("expired link doesn't explain that it's dead")
	}
	// The row must still be listed so the owner can tidy it up.
	if !strings.Contains(page, `data-testid="invite-row"`) {
		t.Error("expired link vanished from the list entirely")
	}
}

// TestInvitePageForDeadLink covers what the *recipient* sees. Every "invalid" path
// renders the same page, and that page names InviteTTLDays — a render payload
// missing the key would put a literal "<no value>" in the sentence, which is why
// they all go through renderInviteInvalid.
//
// Also pins that the message stays vague about *why* the link is dead: someone
// holding a token shouldn't learn whether it never existed, was already claimed,
// was revoked, or simply lapsed.
func TestInvitePageForDeadLink(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	owner := seedSession(t, ts, st, ctx, jar, "deadlink")
	rep, err := st.CreateRepeater(ctx, &store.Repeater{
		// Distinctive so the leak check below can't match navbar chrome ("Repeaters").
		OwnerID: owner.ID, Name: "Zarquon Ridge Relay", PublicKeyHex: strings.Repeat("d", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}
	expired, err := st.CreateInvite(ctx, rep.ID, "gone", nil)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if _, err := st.Pool().Exec(ctx,
		`UPDATE repeater_invites SET expires_at = now() - interval '1 minute' WHERE token = $1`,
		expired); err != nil {
		t.Fatalf("expire invite: %v", err)
	}

	// Both a never-existed token and a lapsed one must render the same way.
	for _, tc := range []struct{ name, token string }{
		{"expired token", expired},
		{"unknown token", "totally-made-up-token"},
	} {
		resp := do(t, ts, h.app, "/invite/"+tc.token)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("%s: read body: %v", tc.name, err)
		}
		page := string(body)
		if !strings.Contains(page, "Invalid link") {
			t.Errorf("%s: page doesn't show the invalid-link state", tc.name)
		}
		if strings.Contains(page, "<no value>") {
			t.Errorf("%s: render payload is missing a key the template names", tc.name)
		}
		if !strings.Contains(page, "expire after 7 days") {
			t.Errorf("%s: message doesn't mention the expiry window", tc.name)
		}
		if strings.Contains(page, rep.Name) {
			t.Errorf("%s: leaks the repeater name for a link that can't be redeemed", tc.name)
		}
	}
}
