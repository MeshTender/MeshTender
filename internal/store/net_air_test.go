package store

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestNetAir(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	admin := mustUser(t, st, ctx, "airadmin")
	org := mustOrg(t, st, ctx, "AirOrg", admin)
	other := mustOrg(t, st, ctx, "AirOther", admin)
	net, err := st.CreateNet(ctx, org, testNetInput(), admin, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	region, err := st.CreateRegion(ctx, org, RegionInput{Token: "us-ny", DisplayName: "New York", AllowFlood: true})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := st.CreateRegion(ctx, other, RegionInput{Token: "us-ca", DisplayName: "California", AllowFlood: true})
	if err != nil {
		t.Fatal(err)
	}

	a, err := st.GetNetAir(ctx, org, net.ID)
	if err != nil || a.ChannelKind != ChannelPublic || a.LogPrefix != "news:" || a.ScopeRegionID != nil || a.OnAirName != "" {
		t.Fatalf("defaults = %+v, %v", a, err)
	}
	if _, err := st.GetNetAir(ctx, other, net.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another org's net = %v", err)
	}

	hashtag := NetAir{ChannelKind: ChannelHashtag, ChannelName: "wnynet", ScopeRegionID: &region, OnAirName: "NC", LogPrefix: ""}
	if err := st.UpdateNetAir(ctx, org, net.ID, hashtag, nil); err != nil {
		t.Fatal(err)
	}
	a, _ = st.GetNetAir(ctx, org, net.ID)
	if a.ChannelName != "wnynet" || a.ScopeToken != "us-ny" || a.OnAirName != "NC" || a.LogPrefix != "" {
		t.Fatalf("after hashtag = %+v", a)
	}

	// Scoping to another org's region is refused.
	bad := hashtag
	bad.ScopeRegionID = &foreign
	if err := st.UpdateNetAir(ctx, org, net.ID, bad, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign region = %v", err)
	}
	// A private channel needs a key, and keeps it when none is given.
	private := NetAir{ChannelKind: ChannelPrivate, ChannelName: "Ops"}
	if err := st.UpdateNetAir(ctx, org, net.ID, private, nil); err == nil {
		t.Fatal("a private channel without a key was saved")
	}
	if err := st.UpdateNetAir(ctx, org, net.ID, private, []byte("sealed-key")); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateNetAir(ctx, org, net.ID, private, nil); err != nil {
		t.Fatal(err)
	}
	if a, _ = st.GetNetAir(ctx, org, net.ID); !bytes.Equal(a.PSKSealed, []byte("sealed-key")) {
		t.Fatalf("key after resave = %q", a.PSKSealed)
	}
	// Leaving the private channel drops its key.
	if err := st.UpdateNetAir(ctx, org, net.ID, NetAir{ChannelKind: ChannelPublic, ScopeRegionID: &region}, nil); err != nil {
		t.Fatal(err)
	}
	if a, _ = st.GetNetAir(ctx, org, net.ID); a.HasKey() {
		t.Fatal("public channel kept the private key")
	}
	// Deleting the region leaves the net unscoped.
	if err := st.DeleteRegion(ctx, org, region); err != nil {
		t.Fatal(err)
	}
	if a, _ = st.GetNetAir(ctx, org, net.ID); a.ScopeRegionID != nil || a.ScopeToken != "" {
		t.Fatalf("after region delete = %+v", a)
	}
}

func TestRecordAdvertHeardKeepsNewestName(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	u := mustUser(t, st, ctx, "namer")
	c := mustCompanion(t, st, ctx, u, "My radio", 1)
	at := c.CreatedAt.Add(time.Minute)
	advert := func(ts uint32, name string) {
		t.Helper()
		heard(t, st, ctx, AdvertHeard{CompanionID: c.ID, AdvertTS: ts, ReceivedAt: at, ObserverPublicKeyHex: testKey(9), Name: name})
	}
	name := func() string {
		t.Helper()
		got, err := st.GetCompanionForViewer(ctx, c.PublicID, u)
		if err != nil {
			t.Fatal(err)
		}
		return got.AdvertName
	}
	advert(1000, "Xero T1")
	if got := name(); got != "Xero T1" {
		t.Fatalf("advert name = %q", got)
	}
	advert(900, "Old Name") // a straggling copy of an older advert
	if got := name(); got != "Xero T1" {
		t.Fatalf("an older advert renamed it to %q", got)
	}
	advert(1100, "Xero T1 Pro")
	if got := name(); got != "Xero T1 Pro" {
		t.Fatalf("a newer advert didn't rename it: %q", got)
	}
	advert(1200, "") // no name in the advert
	if got := name(); got != "Xero T1 Pro" {
		t.Fatalf("a nameless advert cleared it: %q", got)
	}
}

func TestNetSessionMessagesDedupeAndCap(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	admin := mustUser(t, st, ctx, "logadmin")
	org := mustOrg(t, st, ctx, "LogOrg", admin)
	net, err := st.CreateNet(ctx, org, testNetInput(), admin, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.NextNetSession(ctx, net.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	add := func(payload, body string) bool {
		t.Helper()
		ok, err := st.AddNetSessionMessage(ctx, sess.ID, []byte(payload), "Bob", body, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !add("p1", "news: one") || add("p1", "news: one") {
		t.Fatal("a repeated copy should log once")
	}
	if _, err := st.pool.Exec(ctx, `
		INSERT INTO net_session_messages (session_id, msg_hash, sender, body, heard_at)
		SELECT $1, int8send(g), 'x', 'news', now() FROM generate_series(1, $2) g`, sess.ID, MaxNetSessionMessages-1); err != nil {
		t.Fatal(err)
	}
	if add("p2", "news: over the cap") {
		t.Fatal("logged past the cap")
	}
	if msgs, err := st.ListNetSessionMessages(ctx, sess.ID); err != nil || len(msgs) != MaxNetSessionMessages || msgs[0].Body != "news: one" {
		t.Fatalf("log has %d messages, first %+v, %v", len(msgs), msgs[0], err)
	}
}
