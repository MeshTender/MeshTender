package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/hardware"

	"github.com/MeshTender/MeshTender/internal/auth"
	"github.com/MeshTender/MeshTender/internal/config"
	"github.com/MeshTender/MeshTender/internal/identity"
	"github.com/MeshTender/MeshTender/internal/mesh"
	"github.com/MeshTender/MeshTender/internal/observe/observetest"
	"github.com/MeshTender/MeshTender/internal/store"
)

func TestAckMessagesPackToTheCap(t *testing.T) {
	t.Parallel()
	sender := "Net Control"
	var entries []*store.RosterEntry
	for i := range 20 {
		entries = append(entries, &store.RosterEntry{MemberID: int64(i), CompanionAdvertName: fmt.Sprintf("Member %02d T-Deck", i)})
	}
	entries = append(entries, &store.RosterEntry{MemberID: 99, MemberName: "Hand Added"})
	msgs := ackMessages(sender, entries)
	seen := map[int64]bool{}
	for _, m := range msgs {
		if !strings.HasPrefix(m.text, "Copied: ") || len(m.text) > mesh.GroupTextRoom(sender) {
			t.Errorf("message %q (%d bytes) doesn't fit", m.text, len(m.text))
		}
		for _, id := range m.members {
			seen[id] = true
		}
	}
	if len(msgs) < 2 || len(seen) != len(entries) {
		t.Fatalf("%d messages covering %d of %d members", len(msgs), len(seen), len(entries))
	}
	if !strings.Contains(msgs[0].text, "@[Member 00 T-Deck], @[Member 01 T-Deck]") {
		t.Errorf("first message %q", msgs[0].text)
	}
	if last := msgs[len(msgs)-1].text; !strings.HasSuffix(last, "Hand Added") || strings.Contains(last, "@[Hand Added]") {
		t.Errorf("a member with no advert is named plainly: %q", last)
	}
}

func TestOnAirSender(t *testing.T) {
	t.Parallel()
	if got := onAirSender(&store.NetAir{OnAirName: "NC"}, "Sunday Net"); got != "NC" {
		t.Errorf("on-air name: %q", got)
	}
	if got := onAirSender(&store.NetAir{}, "Net: Sunday"); got != "Net Sunday" {
		t.Errorf("colon kept: %q", got)
	}
	long := strings.Repeat("é", 40)
	if got := onAirSender(&store.NetAir{}, long); len(got) > maxOnAirName || !strings.HasPrefix(long, got) {
		t.Errorf("long name: %q", got)
	}
}

// fakeModem plays the user's browser and KISS modem on the other end of the
// net modem socket: it collects statuses, decodes the frames MeshTender sends
// the modem, answers each transmission with TX_DONE, and can play packets as
// heard on the air.
type fakeModem struct {
	t        *testing.T
	ws       *websocket.Conn
	ctx      context.Context
	statuses chan struct{ State, Message string }
	data     chan []byte
	hw       chan *hardware.KissFrame
}

func dialNetModem(t *testing.T, ctx context.Context, url string, jar http.CookieJar, base string) (*fakeModem, *http.Response, error) {
	t.Helper()
	hdr := http.Header{}
	var parts []string
	for _, c := range jar.Cookies(mustURL(t, base)) {
		parts = append(parts, c.Name+"="+c.Value)
	}
	hdr.Set("Cookie", strings.Join(parts, "; "))
	ws, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		return nil, resp, err
	}
	m := &fakeModem{t: t, ws: ws, ctx: ctx, statuses: make(chan struct{ State, Message string }, 64),
		data: make(chan []byte, 64), hw: make(chan *hardware.KissFrame, 64)}
	go m.read()
	t.Cleanup(func() { ws.Close(websocket.StatusNormalClosure, "") })
	if err := ws.Write(ctx, websocket.MessageText, []byte(`{"type":"ready"}`)); err != nil {
		t.Fatal(err)
	}
	return m, resp, nil
}

func (m *fakeModem) read() {
	var buf []byte
	for {
		typ, data, err := m.ws.Read(m.ctx)
		if err != nil {
			close(m.statuses)
			return
		}
		if typ == websocket.MessageText {
			var s struct{ State, Message string }
			if json.Unmarshal(data, &s) == nil {
				m.statuses <- s
			}
			continue
		}
		buf = append(buf, data...)
		frames, rest, _ := hardware.ExtractFrames(buf)
		buf = rest
		for _, f := range frames {
			switch f.Command {
			case hardware.KISS_CMD_DATA:
				m.data <- f.Data
				// The modem reports the transmission done.
				_ = m.ws.Write(m.ctx, websocket.MessageBinary, hardware.EncodeHardwareFrame(0, hardware.HW_RESP_TX_DONE, []byte{0x01}))
			case hardware.KISS_CMD_SETHARDWARE:
				m.hw <- f
			}
		}
	}
}

// hear plays a packet as received on the air.
func (m *fakeModem) hear(raw []byte) {
	m.t.Helper()
	if err := m.ws.Write(m.ctx, websocket.MessageBinary, hardware.EncodeFrame(0, hardware.KISS_CMD_DATA, raw)); err != nil {
		m.t.Fatal(err)
	}
}

func (m *fakeModem) send(msg string) {
	m.t.Helper()
	if err := m.ws.Write(m.ctx, websocket.MessageText, []byte(msg)); err != nil {
		m.t.Fatal(err)
	}
}

// waitStatus waits for a status in state; an "error" status fails the test
// unless that's what's awaited.
func (m *fakeModem) waitStatus(state string) string {
	m.t.Helper()
	for {
		select {
		case s, ok := <-m.statuses:
			if !ok {
				m.t.Fatalf("socket closed waiting for %q", state)
			}
			if s.State == state {
				return s.Message
			}
			if s.State == "error" {
				m.t.Fatalf("error status waiting for %q: %s", state, s.Message)
			}
		case <-m.ctx.Done():
			m.t.Fatalf("timed out waiting for status %q", state)
		}
	}
}

// waitText waits for MeshTender to transmit a channel message on ch.
func (m *fakeModem) waitText(ch mesh.Channel) (mesh.GroupText, *meshcore.Packet) {
	m.t.Helper()
	for {
		select {
		case raw := <-m.data:
			gt, ok := mesh.DecodeGroupText(raw, ch)
			if !ok {
				m.t.Fatalf("transmitted something other than a message on the net's channel: %x", raw)
			}
			pkt, _ := meshcore.PacketFromBytes(raw)
			return gt, pkt
		case <-m.ctx.Done():
			m.t.Fatal("timed out waiting for a transmission")
		}
	}
}

// netModemFixture is an org with a hashtag-channel net scoped to a region,
// in progress now, a net control admin, and a member with a registered
// companion.
type netModemFixture struct {
	st             *store.Store
	ctx            context.Context
	ts             *httptest.Server
	jar, memberJar http.CookieJar
	org            *store.Org
	admin          *store.User
	member         *store.User
	sess           *store.NetSession
	companion      meshcore.LocalIdentity
	ch             mesh.Channel
	wsURL          string
	radio          config.RadioDefaults
}

func newNetModemFixture(t *testing.T, manualAck, withRadio bool) *netModemFixture {
	t.Helper()
	st, ctx := coreStore(t)
	idSvc, err := identity.LoadOrCreate(ctx, st, testMasterKey)
	if err != nil {
		t.Fatal(err)
	}
	authSvc, err := auth.New(st, st.Pool(), testAuthConfig())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(st, authSvc, idSvc, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	srv.app.netModem = netModemTiming{AckEvery: 200 * time.Millisecond, ManualAckEvery: 200 * time.Millisecond,
		MinGap: 10 * time.Millisecond, TxTimeout: 2 * time.Second, Grace: time.Minute}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	jar, _ := cookiejar.New(nil)
	admin := seedSession(t, ts, st, ctx, jar, "ncadmin")
	memberJar, _ := cookiejar.New(nil)
	member := seedSession(t, ts, st, ctx, memberJar, "ncmember")
	org, err := st.CreateOrg(ctx, "Modem Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	radio := config.RadioDefaults{FreqHz: 910_525_000, BwHz: 500_000, SF: 5, CR: 5} // fast, so spacing is short
	if withRadio {
		if err := st.SetOrgRadio(ctx, org.ID, &radio); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	net, err := st.CreateNet(ctx, org.ID, store.NetInput{Name: "Modem Net", Weekday: now.Weekday(),
		StartMinute: max(0, now.Hour()*60+now.Minute()-10), DurationMinutes: 120, Timezone: "UTC", Active: true, ManualAck: manualAck}, admin.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	region, err := st.CreateRegion(ctx, org.ID, store.RegionInput{Token: "us-ny", DisplayName: "New York", AllowFlood: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateNetAir(ctx, org.ID, net.ID, store.NetAir{ChannelKind: store.ChannelHashtag, ChannelName: "wnynet",
		ScopeRegionID: &region, OnAirName: "Net Control", LogPrefix: "news:"}, nil); err != nil {
		t.Fatal(err)
	}
	sess, err := st.NextNetSession(ctx, net.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	companion := observetest.NewIdentity(t)
	if _, err := st.CreateCompanion(ctx, member.ID, "My radio", companion.String()); err != nil {
		t.Fatal(err)
	}
	ch := mesh.HashtagChannel("wnynet")
	wsURL := fmt.Sprintf("ws%s/orgs/%s/nets/%d/sessions/%d/modem/ws", strings.TrimPrefix(ts.URL, "http"), org.Slug, net.ID, sess.ID)

	return &netModemFixture{st: st, ctx: ctx, ts: ts, jar: jar, memberJar: memberJar, org: org, admin: admin, member: member,
		sess: sess, companion: companion, ch: ch, wsURL: wsURL, radio: radio}
}

// TestNetModemSession runs net control's modem session end to end: tuning to
// the org's radio, counting a heard advert with MeshTender as the observer,
// acknowledging it on the air once (scoped, as a mention of the companion's
// advertised name), logging prefixed channel messages once each, the
// announcement buttons, and one modem per session.
func TestNetModemSession(t *testing.T) {
	t.Parallel()
	f := newNetModemFixture(t, false, true)
	st, ctx, ts, jar, memberJar, org, sess, companion, ch, wsURL, radio := f.st, f.ctx, f.ts, f.jar, f.memberJar, f.org, f.sess, f.companion, f.ch, f.wsURL, f.radio
	rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	// A member who isn't net control can't connect.
	if _, resp, err := dialNetModem(t, rctx, wsURL, memberJar, ts.URL); err == nil || resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("member dial: %v (%v)", err, resp)
	}

	m, _, err := dialNetModem(t, rctx, wsURL, jar, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case f := <-m.hw:
		sub, data, _ := hardware.DecodeHardwareFrame(f)
		want := (&hardware.RadioConfig{FreqHz: radio.FreqHz, BwHz: radio.BwHz, SF: radio.SF, CR: radio.CR}).ToBytes()
		if sub != hardware.HW_CMD_SET_RADIO || string(data) != string(want) {
			t.Fatalf("first hardware command %#x %x, want SET_RADIO %x", sub, data, want)
		}
	case <-rctx.Done():
		t.Fatal("radio never set")
	}
	m.waitStatus("ready")

	// The member's companion adverts; the modem hears it.
	at := time.Now()
	m.hear(observetest.AdvertPacket(t, companion, uint32(at.Unix()), "Xero T1", meshcore.RouteTypeFlood, 1, nil)) //nolint:gosec // test clock
	m.waitStatus("heard")

	// Acknowledged on the air, scoped, mentioning the advertised name.
	gt, pkt := m.waitText(ch)
	if gt.Sender != "Net Control" || gt.Text != "Copied: @[Xero T1]" {
		t.Fatalf("acknowledgement = %+v", gt)
	}
	if !meshcore.NewRegionFromHashtag("us-ny").MatchesPacket(pkt) {
		t.Error("acknowledgement isn't scoped to us-ny")
	}
	m.waitStatus("sent")
	roster, err := st.NetSessionRoster(ctx, org.ID, sess)
	if err != nil || len(roster) != 1 || !roster[0].Heard() || !roster[0].Aired() || roster[0].Observers != 1 {
		t.Fatalf("roster = %+v, %v", roster, err)
	}

	// Channel messages: a prefixed one is logged once however often it's
	// repeated; others aren't.
	news, err := mesh.BuildGroupText(ch, "Bob", "news: road closed on Main", nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	chatter, _ := mesh.BuildGroupText(ch, "Bob", "anyone around?", nil, time.Now())
	m.hear(news)
	m.hear(news)
	m.hear(chatter)
	if msg := m.waitStatus("message"); msg != "Bob: road closed on Main" {
		t.Errorf("message status %q", msg)
	}

	// Announcements.
	m.send(`{"type":"open"}`)
	if gt, _ := m.waitText(ch); gt.Text != "Modem Net is open. Send a flood advert to check in." {
		t.Errorf("open announcement %q", gt.Text)
	}
	m.send(`{"type":"say","text":"QSY in 5"}`)
	if gt, _ := m.waitText(ch); gt.Text != "QSY in 5" {
		t.Errorf("message %q", gt.Text)
	}

	// Nothing more goes out: the member was acknowledged once.
	select {
	case raw := <-m.data:
		gt, _ := mesh.DecodeGroupText(raw, ch)
		t.Fatalf("unexpected transmission %+v", gt)
	case <-time.After(time.Second):
	}
	if msgs, err := st.ListNetSessionMessages(ctx, sess.ID); err != nil || len(msgs) != 1 || msgs[0].Sender != "Bob" || msgs[0].Body != "road closed on Main" {
		t.Fatalf("channel log = %+v, %v", msgs, err)
	}

	// One modem per session.
	second, _, err := dialNetModem(t, rctx, wsURL, jar, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if msg := second.waitStatus("error"); !strings.Contains(msg, "already has a modem") {
		t.Errorf("second modem: %q", msg)
	}
}

// TestNetModemManualAck: on a manual net, a heard check-in isn't acknowledged
// on the air until net control clicks Acknowledge; a member net control added
// by hand is named plainly, having no advertised name to mention.
func TestNetModemManualAck(t *testing.T) {
	t.Parallel()
	f := newNetModemFixture(t, true, true)
	rctx, cancel := context.WithTimeout(f.ctx, 20*time.Second)
	defer cancel()
	m, _, err := dialNetModem(t, rctx, f.wsURL, f.jar, f.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	m.waitStatus("ready")
	m.hear(observetest.AdvertPacket(t, f.companion, uint32(time.Now().Unix()), "Xero T1", meshcore.RouteTypeFlood, 1, nil)) //nolint:gosec // test clock
	m.waitStatus("heard")
	select {
	case <-m.data:
		t.Fatal("acknowledged on the air before net control did")
	case <-time.After(700 * time.Millisecond):
	}

	if err := f.st.AcknowledgeMember(f.ctx, f.org.ID, f.sess.ID, f.member.ID, f.admin.ID, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if gt, _ := m.waitText(f.ch); gt.Text != "Copied: @[Xero T1]" {
		t.Fatalf("acknowledgement %q", gt.Text)
	}

	// Added by hand: another member, never heard.
	late := seedSession(t, f.ts, f.st, f.ctx, f.memberJar, "nclate")
	if err := f.st.AddOrgMember(f.ctx, f.org.ID, late.ID, "member"); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AcknowledgeMember(f.ctx, f.org.ID, f.sess.ID, late.ID, f.admin.ID, "by phone", time.Now()); err != nil {
		t.Fatal(err)
	}
	if gt, _ := m.waitText(f.ch); gt.Text != "Copied: nclate" {
		t.Fatalf("hand-added acknowledgement %q", gt.Text)
	}
}

// TestNetModemNeedsOrgRadio: without the org's radio there's nothing to tune
// the modem to, and net control is told why over the socket.
func TestNetModemNeedsOrgRadio(t *testing.T) {
	t.Parallel()
	f := newNetModemFixture(t, false, false)
	rctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	m, _, err := dialNetModem(t, rctx, f.wsURL, f.jar, f.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if msg := m.waitStatus("error"); !strings.Contains(msg, "radio settings aren't set") {
		t.Fatalf("status %q", msg)
	}
}
