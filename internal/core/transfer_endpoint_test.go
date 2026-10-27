package core

import (
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// TestTransferPageRendersStewards: the transfer page is owner-only and offers
// exactly the repeater's stewards as recipients — a plain shared user must not
// appear as a choice, since they can't receive it.
func TestTransferPageRendersStewards(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "xferowner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Ridge Rep")
	path := "/repeaters/" + rep.PublicID + "/settings/ownership/transfer"

	steward, err := st.CreateUser(ctx, "keeper", "")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := st.CreateUser(ctx, "bystander", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []int64{steward.ID, plain.ID} {
		if _, err := st.AddShare(ctx, rep.ID, u); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetShareSteward(ctx, rep.ID, steward.ID, true); err != nil {
		t.Fatal(err)
	}

	resp := do(t, ts, h.app, path, sess)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET transfer = %d, want 200", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	page := string(raw)
	if !strings.Contains(page, "@keeper") {
		t.Fatal("transfer page does not offer the steward as a recipient")
	}
	if strings.Contains(page, "@bystander") {
		t.Fatal("transfer page offers a non-steward as a recipient")
	}

	// Owner-only: the steward themselves gets a 404, not the transfer form.
	stewardSess := appSession(t, ts, st, ctx, h.app, steward)
	as := do(t, ts, h.app, path, stewardSess)
	defer as.Body.Close()
	if as.StatusCode != http.StatusNotFound {
		t.Fatalf("GET transfer as steward = %d, want 404", as.StatusCode)
	}
}

// TestTransferOldURLRedirects: the transfer page moved into Settings; its old
// address (once the modal fragment's) still finds it.
func TestTransferOldURLRedirects(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "oldxferowner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Old Rep")

	resp := do(t, ts, h.app, "/repeaters/"+rep.PublicID+"/transfer", sess)
	resp.Body.Close()
	want := "/repeaters/" + rep.PublicID + "/settings/ownership/transfer"
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != want {
		t.Fatalf("GET old transfer URL = %d → %q, want 301 → %s", resp.StatusCode, resp.Header.Get("Location"), want)
	}
}

// TestTransferPageWithoutStewards explains the empty case rather than rendering
// a picker with nothing in it.
func TestTransferPageWithoutStewards(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "lonelyowner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Solo Rep")

	resp := do(t, ts, h.app, "/repeaters/"+rep.PublicID+"/settings/ownership/transfer", sess)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET transfer = %d, want 200", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "You can only transfer a repeater to a steward") {
		t.Fatal("transfer page with no stewards does not explain how to designate one")
	}
	if strings.Contains(string(raw), `name="user_id"`) {
		t.Fatal("transfer page rendered a recipient picker with no stewards to pick")
	}
}

// TestTransferRepeaterPost is the endpoint happy path: ownership moves and the
// outgoing owner lands on the repeater page (which they can still see, as the
// handover leaves them a steward).
func TestTransferRepeaterPost(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "handerover")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Summit Rep")
	path := "/repeaters/" + rep.PublicID + "/transfer"

	steward, err := st.CreateUser(ctx, "successor", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddShare(ctx, rep.ID, steward.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetShareSteward(ctx, rep.ID, steward.ID, true); err != nil {
		t.Fatal(err)
	}

	resp := post(t, ts, h.app, path, url.Values{"user_id": {strconv.FormatInt(steward.ID, 10)}}, sess)
	resp.Body.Close()
	assertRedirect(t, resp, "/repeaters/"+rep.PublicID, "transfer ownership")
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("ok") == "" {
		t.Fatal("successful transfer carried no confirmation flash")
	}

	got, err := st.GetRepeaterOwned(ctx, steward.ID, rep.ID)
	if err != nil || got.OwnerID != steward.ID {
		t.Fatalf("ownership did not move: %v", err)
	}
	if isSteward, _ := st.IsSteward(ctx, rep.ID, owner.ID); !isSteward {
		t.Fatal("outgoing owner was not left a steward")
	}
}

// TestTransferRepeaterPostRejections covers the ways a submit can be wrong: a
// non-steward target (a stale form, since the picker only lists stewards), a
// missing choice, and a non-owner submitting at all. None may move the repeater.
func TestTransferRepeaterPostRejections(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "guardowner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Guarded Rep")
	path := "/repeaters/" + rep.PublicID + "/transfer"

	plain, err := st.CreateUser(ctx, "notasteward", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddShare(ctx, rep.ID, plain.ID); err != nil {
		t.Fatal(err)
	}

	// A shared user who isn't a steward: back to the transfer page with an error.
	bad := post(t, ts, h.app, path, url.Values{"user_id": {strconv.FormatInt(plain.ID, 10)}}, sess)
	bad.Body.Close()
	assertRedirect(t, bad, path, "transfer to non-steward")
	if loc, _ := url.Parse(bad.Header.Get("Location")); loc.Query().Get("error") == "" {
		t.Fatal("rejected transfer carried no error message")
	}

	// No choice at all.
	empty := post(t, ts, h.app, path, url.Values{}, sess)
	empty.Body.Close()
	assertRedirect(t, empty, path, "transfer with no recipient")

	// A non-owner submitting directly: 404 from the ownership gate, and no change.
	otherSess := appSession(t, ts, st, ctx, h.app, plain)
	sneaky := post(t, ts, h.app, path, url.Values{"user_id": {strconv.FormatInt(plain.ID, 10)}}, otherSess)
	defer sneaky.Body.Close()
	if sneaky.StatusCode != http.StatusNotFound {
		t.Fatalf("non-owner transfer = %d, want 404", sneaky.StatusCode)
	}

	if got, err := st.GetRepeaterOwned(ctx, owner.ID, rep.ID); err != nil || got.OwnerID != owner.ID {
		t.Fatalf("repeater changed hands despite every attempt being rejected (%v)", err)
	}
}

// TestOwnershipSettingsLinksTransfer: Settings › Transfer or delete is where an
// owner finds the handover, linking to its page.
func TestOwnershipSettingsLinksTransfer(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "linkowner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Linked Rep")

	page := readBody(t, do(t, ts, h.app, "/repeaters/"+rep.PublicID+"/settings/ownership", sess))
	if !strings.Contains(page, `href="/repeaters/`+rep.PublicID+`/settings/ownership/transfer"`) {
		t.Fatal("ownership settings don't link to the transfer page")
	}
}
