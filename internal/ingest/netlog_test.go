package ingest

import (
	"context"
	"testing"
	"time"

	"github.com/MeshTender/MeshTender/internal/mesh"
	"github.com/MeshTender/MeshTender/internal/observe/observetest"
	"github.com/MeshTender/MeshTender/internal/secretbox"
	"github.com/MeshTender/MeshTender/internal/store"
)

// netLogFixture is fixture's org with a net in progress on a private channel
// that logs "news:" messages.
func netLogFixture(t *testing.T, st *store.Store, ctx context.Context) (*fixture, *store.Net, *store.NetSession, mesh.Channel) {
	t.Helper()
	f := newFixture(t, st, ctx)
	now := time.Now().UTC()
	net, err := st.CreateNet(ctx, f.org, store.NetInput{Name: "Log Net", Weekday: now.Weekday(),
		StartMinute: max(0, now.Hour()*60+now.Minute()-10), DurationMinutes: 120, Timezone: "UTC", Active: true}, f.owner, now)
	if err != nil {
		t.Fatal(err)
	}
	psk := [16]byte{9, 8, 7, 6, 5, 4, 3, 2, 1}
	sealed, err := secretbox.Seal(testMasterKey, psk[:])
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateNetAir(ctx, f.org, net.ID, store.NetAir{ChannelKind: store.ChannelPrivate, ChannelName: "Ops",
		OnAirName: "NC", LogPrefix: "news:"}, sealed); err != nil {
		t.Fatal(err)
	}
	sess, err := st.NextNetSession(ctx, net.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	return f, net, sess, mesh.PrivateChannel(psk)
}

// TestIngesterLogsNetChannelMessages: a running net's "news:" messages heard by
// the org's observers land in its channel log once, without the prefix — not
// other messages, other channels, unregistered observers' uplinks, or the
// net's own messages repeated back.
func TestIngesterLogsNetChannelMessages(t *testing.T) {
	t.Parallel()
	st, ctx := newStore(t)
	f, _, sess, ch := netLogFixture(t, st, ctx)
	in := &Ingester{Store: st, MasterKey: testMasterKey, Log: quietLog()}
	now := time.Now()
	text := func(c mesh.Channel, sender, body string) []byte {
		t.Helper()
		raw, err := mesh.BuildGroupText(c, sender, body, nil, now)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	news := text(ch, "Bob", "news: bridge out on Route 5")
	in.Handle(ctx, packetMsg(f.org, f.observer, news, now))
	in.Handle(ctx, packetMsg(f.org, f.observer, news, now)) // another copy
	for _, raw := range [][]byte{
		text(ch, "Bob", "anyone on?"),                  // no prefix
		text(ch, "NC", "news: our own, repeated back"), // the net's own sender
		text(mesh.PublicChannel(), "Bob", "news: wrong channel"),
	} {
		in.Handle(ctx, packetMsg(f.org, f.observer, raw, now))
	}
	stranger := observetest.NewIdentity(t) // not one of the org's observers
	in.Handle(ctx, packetMsg(f.org, stranger, text(ch, "Eve", "news: from an unregistered uplink"), now))

	msgs, err := st.ListNetSessionMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Sender != "Bob" || msgs[0].Body != "bridge out on Route 5" {
		t.Fatalf("channel log = %+v", msgs)
	}
}

// TestIngesterNetLogsReloadByMinute: the running nets are loaded once per
// minute, since sessions start and end on whole minutes.
func TestIngesterNetLogsReloadByMinute(t *testing.T) {
	t.Parallel()
	st, ctx := newStore(t)
	f, net, _, _ := netLogFixture(t, st, ctx)
	in := &Ingester{Store: st, MasterKey: testMasterKey, Log: quietLog()}
	minute := time.Now().Truncate(time.Minute)

	if logs := in.netLogs(ctx, minute.Add(5*time.Second))[f.org]; len(logs) != 1 || logs[0].Prefix != "news:" || logs[0].Sender != "NC" {
		t.Fatalf("logs = %+v", logs)
	}
	// Stop logging; within the same minute the loaded set stands.
	if err := st.UpdateNetAir(ctx, f.org, net.ID, store.NetAir{ChannelKind: store.ChannelPublic}, nil); err != nil {
		t.Fatal(err)
	}
	if logs := in.netLogs(ctx, minute.Add(50*time.Second))[f.org]; len(logs) != 1 {
		t.Fatalf("reloaded within the minute: %+v", logs)
	}
	if logs := in.netLogs(ctx, minute.Add(time.Minute+time.Second))[f.org]; len(logs) != 0 {
		t.Fatalf("next minute still logs: %+v", logs)
	}
}
