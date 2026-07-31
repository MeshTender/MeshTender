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
	path := "/repeaters/" + rep.PublicID + "/transfer"

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

// TestTransferModalFragment: an htmx request gets the modal fragment — modal
// chrome, no page chrome — while the same URL visited directly (the delete page
// links to it, and a no-JS browser lands there) still gets the full page. Both
// must offer the same recipients, since they render one copy of the content.
func TestTransferModalFragment(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "modalowner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Modal Rep")
	path := "/repeaters/" + rep.PublicID + "/transfer"

	steward, err := st.CreateUser(ctx, "modalsteward", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddShare(ctx, rep.ID, steward.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetShareSteward(ctx, rep.ID, steward.ID, true); err != nil {
		t.Fatal(err)
	}

	frag := doHX(t, ts, h.app, path, sess)
	defer frag.Body.Close()
	if frag.StatusCode != http.StatusOK {
		t.Fatalf("HX GET transfer = %d, want 200", frag.StatusCode)
	}
	raw, _ := io.ReadAll(frag.Body)
	fragment := string(raw)
	if !strings.Contains(fragment, "modal-header") || !strings.Contains(fragment, `id="transfer-form"`) {
		t.Fatal("htmx request did not return the transfer modal fragment")
	}
	if strings.Contains(fragment, "<!doctype") || strings.Contains(fragment, "repeater-tabs") {
		t.Fatal("modal fragment carries full-page chrome")
	}
	if !strings.Contains(fragment, "@modalsteward") {
		t.Fatal("modal fragment does not offer the steward as a recipient")
	}

	// Same URL without htmx: the standalone page, same recipient.
	full := do(t, ts, h.app, path, sess)
	defer full.Body.Close()
	rawFull, _ := io.ReadAll(full.Body)
	page := string(rawFull)
	if full.StatusCode != http.StatusOK || !strings.Contains(page, "<!doctype") {
		t.Fatalf("direct GET transfer = %d, want a full 200 page", full.StatusCode)
	}
	if !strings.Contains(page, "@modalsteward") {
		t.Fatal("standalone transfer page does not offer the steward as a recipient")
	}
}

// TestTransferPageWithoutStewards explains the empty case rather than rendering
// a picker with nothing in it.
func TestTransferPageWithoutStewards(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "lonelyowner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Solo Rep")

	resp := do(t, ts, h.app, "/repeaters/"+rep.PublicID+"/transfer", sess)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET transfer = %d, want 200", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "No stewards yet") {
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

// TestSharePageOpensTransferModal: the sharing page is where an owner manages
// stewards, so it must be the way they find the handover — via the modal shell
// and the htmx trigger that fills it.
func TestSharePageOpensTransferModal(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "linkowner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Linked Rep")

	resp := do(t, ts, h.app, "/repeaters/"+rep.PublicID+"/share", sess)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	page := string(raw)
	if !strings.Contains(page, `hx-get="/repeaters/`+rep.PublicID+`/transfer"`) {
		t.Fatal("share page has no htmx trigger to load the transfer modal")
	}
	if !strings.Contains(page, `id="transfer-modal-content"`) {
		t.Fatal("share page has no modal shell for the transfer fragment to swap into")
	}
}
