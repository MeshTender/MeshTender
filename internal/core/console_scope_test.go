package core

import (
	"net/url"
	"strings"
	"sync"
	"testing"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/mqtttest"
	"github.com/MeshTender/MeshTender/internal/relay"
	"github.com/MeshTender/MeshTender/internal/store"
)

// scopeRecorder answers each frame as the observer and records the route of
// every login the console sends; with answerLogin it also replies to the login
// as the repeater.
type scopeRecorder struct {
	mu     sync.Mutex
	logins []*meshcore.Packet
}

func (s *scopeRecorder) record(h *relayHarness, answerLogin bool) {
	h.broker.OnPublish(func(m mqtttest.Message) {
		if m.Topic != relay.TxTopic(h.observer.Identity) {
			return
		}
		h.answer(m.Payload, "")
		pkt := h.framePacket(m.Payload)
		if pkt.PayloadType() != meshcore.PayloadTypeAnonReq {
			return
		}
		s.mu.Lock()
		s.logins = append(s.logins, pkt)
		s.mu.Unlock()
		if answerLogin {
			h.uplink(buildResponsePacket(h.t, h.repeater, h.server))
		}
	})
}

func (s *scopeRecorder) firstLogin(t *testing.T) *meshcore.Packet {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.logins) == 0 {
		t.Fatal("no login was sent")
	}
	return s.logins[0]
}

func TestConsoleFloodsInTheChosenRegion(t *testing.T) {
	t.Parallel()
	h := newRelayHarness(t, true)
	rec := &scopeRecorder{}
	rec.record(h, true)
	ws := h.dial("scope=" + url.QueryEscape("#wny"))
	if _, msg := readUntil(t, ws, "info"); msg != "Flooding in the region #wny." {
		t.Errorf("first status = %q", msg)
	}
	readUntil(t, ws, "ready")
	login := rec.firstLogin(t)
	if login.RouteType() != meshcore.RouteTypeTransportFlood || !meshcore.NewRegionFromHashtag("wny").MatchesPacket(login) {
		t.Fatalf("login route %d, want a transport flood scoped to #wny", login.RouteType())
	}
}

func TestConsoleRefusesAPathAndAScope(t *testing.T) {
	t.Parallel()
	h := newRelayHarness(t, true)
	ws := h.dial("path=11", "scope=wny")
	if _, msg := readUntil(t, ws, "error"); !strings.Contains(msg, "not both") {
		t.Fatalf("error = %q", msg)
	}
	if n := len(h.broker.Published()); n != 0 {
		t.Errorf("%d frames published, want none", n)
	}
}

func TestConsoleIgnoresAPrivateScope(t *testing.T) {
	t.Parallel()
	h := newRelayHarness(t, true)
	rec := &scopeRecorder{}
	rec.record(h, true)
	ws := h.dial("scope=" + url.QueryEscape("$secret"))
	if _, msg := readUntil(t, ws, "warning"); !strings.Contains(msg, "private") {
		t.Fatalf("warning = %q", msg)
	}
	readUntil(t, ws, "ready")
	if route := rec.firstLogin(t).RouteType(); route != meshcore.RouteTypeFlood {
		t.Fatalf("login route %d, want a plain flood", route)
	}
}

func TestConfigurePreselectsTheDefaultScope(t *testing.T) {
	t.Parallel()
	h := newRelayHarness(t, true)
	if page := h.page(""); !strings.Contains(page, `data-default-route="flood"`) || strings.Contains(page, `id="scope-region"`) {
		t.Fatal("without regions, the route should default to a plain flood, with only a text field for a region")
	}
	for _, z := range []store.RegionInput{
		{Token: "wny", DisplayName: "Western NY", DefaultScope: true},
		{Token: "buf", DisplayName: "Buffalo"},
		{Token: "$club", DisplayName: "Club"},
	} {
		z.Layer, z.AllowFlood = 1, true
		if _, err := h.st.CreateRegion(h.ctx, h.orgID, z); err != nil {
			t.Fatal(err)
		}
	}
	page := h.page("")
	for _, want := range []string{
		`data-default-route="scope"`,
		`<option value="buf">Buffalo (#buf)</option>`,
		`<option value="wny" selected>Western NY (#wny)</option>`,
		`<option value="">Another region…</option>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing %s", want)
		}
	}
	if strings.Contains(page, `value="$club"`) {
		t.Error("a private region is offered as a scope")
	}
}
