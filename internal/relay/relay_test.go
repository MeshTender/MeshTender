package relay

import (
	"bytes"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MeshTender/MeshTender/internal/ingest"
	"github.com/MeshTender/MeshTender/internal/mqtttest"
)

// dialTest connects a Relay to a loopback test broker, with handlers that
// forward to channels.
func dialTest(t *testing.T, broker *mqtttest.Broker) (*Relay, chan []byte, chan Result) {
	t.Helper()
	packets, results := make(chan []byte, 4), make(chan Result, 4)
	r, err := Dial(Config{
		Broker:      ingest.BrokerConfig{OrgID: 1, URL: broker.URL(), Username: "meshtender", Password: "s3cret"},
		TopicPrefix: "meshcore",
		Observer:    seededIdentity("target").Identity,
		Signer:      seededIdentity("signer"),
		Dialer: ingest.Dialer{
			Resolver: net.DefaultResolver,
			Allow:    func(netip.Addr) bool { return true }, // the broker is on loopback
			RootCAs:  broker.RootCAs(),
		},
		Now: func() time.Time { return time.Unix(1_800_000_000, 0) },
	}, Handlers{
		Packet: func(raw []byte) { packets <- raw },
		Result: func(res Result) { results <- res },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r, packets, results
}

func TestDialSubscribesToTheObserversTopics(t *testing.T) {
	broker := mqtttest.New(t, "s3cret", "ws")
	dialTest(t, broker)
	key := strings.ToUpper(seededIdentity("target").String())
	filters, users, _ := broker.Snapshot()
	slices.Sort(filters)
	want := []string{"meshcore/+/" + key + "/packets", "meshtender/v1/" + key + "/txresult"}
	if !slices.Equal(filters, want) {
		t.Errorf("filters = %q, want %q", filters, want)
	}
	if !slices.Equal(users, []string{"meshtender"}) {
		t.Errorf("users = %q", users)
	}
}

func TestDialRefusedSubscription(t *testing.T) {
	broker := mqtttest.New(t, "s3cret", "ws")
	broker.RejectSubs = true
	_, err := Dial(Config{
		Broker:      ingest.BrokerConfig{URL: broker.URL(), Username: "meshtender", Password: "s3cret"},
		TopicPrefix: "meshcore",
		Observer:    seededIdentity("target").Identity,
		Signer:      seededIdentity("signer"),
		Dialer:      ingest.Dialer{Resolver: net.DefaultResolver, Allow: func(netip.Addr) bool { return true }},
	}, Handlers{})
	if !errors.Is(err, ingest.ErrSubscribeRejected) {
		t.Fatalf("err = %v, want ErrSubscribeRejected", err)
	}
}

func TestSendDataPublishesASignedFrame(t *testing.T) {
	broker := mqtttest.New(t, "s3cret", "wss")
	r, _, _ := dialTest(t, broker)
	target, signer := seededIdentity("target"), seededIdentity("signer")

	for _, pkt := range [][]byte{{0x01, 0x00, 0xAA}, {0x01, 0x00, 0xBB}} {
		if err := r.SendData(pkt); err != nil {
			t.Fatal(err)
		}
	}
	pubs := broker.Published()
	if len(pubs) != 2 {
		t.Fatalf("published %d frames, want 2", len(pubs))
	}
	var ids []uint32
	for i, m := range pubs {
		if m.Topic != TxTopic(target.Identity) || m.QoS != 1 || m.Retain {
			t.Errorf("frame %d: topic %q qos %d retain %v", i, m.Topic, m.QoS, m.Retain)
		}
		body, sig := m.Payload[:len(m.Payload)-sigLen], m.Payload[len(m.Payload)-sigLen:]
		if !signer.Verify(signedBytes(target.Identity, body), sig) {
			t.Errorf("frame %d: signature doesn't verify", i)
		}
		if got := body[headerLen:]; !bytes.Equal(got, [][]byte{{0x01, 0x00, 0xAA}, {0x01, 0x00, 0xBB}}[i]) {
			t.Errorf("frame %d: packet %x", i, got)
		}
		ids = append(ids, uint32(body[2])|uint32(body[3])<<8|uint32(body[4])<<16|uint32(body[5])<<24)
	}
	if ids[1] != ids[0]+1 {
		t.Errorf("request ids %d then %d, want consecutive", ids[0], ids[1])
	}
}

func TestPacketsAndResultsReachTheHandlers(t *testing.T) {
	broker := mqtttest.New(t, "s3cret", "ws")
	_, packets, results := dialTest(t, broker)
	key := strings.ToUpper(seededIdentity("target").String())

	broker.Publish("meshcore/BUF/"+key+"/packets", []byte(`{"raw":"0A02ABCD","SNR":"5.5"}`))
	select {
	case raw := <-packets:
		if !bytes.Equal(raw, []byte{0x0A, 0x02, 0xAB, 0xCD}) {
			t.Errorf("packet = %x", raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("packet never reached the handler")
	}

	broker.Publish("meshtender/v1/"+key+"/txresult", []byte(`{"v":1,"id":9,"ok":false,"error":"not_admin"}`))
	select {
	case res := <-results:
		if res.RequestID != 9 || res.OK || res.Error != ErrCodeNotAdmin {
			t.Errorf("result = %+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("result never reached the handler")
	}
}

func TestLostConnectionEndsTheRelay(t *testing.T) {
	broker := mqtttest.New(t, "s3cret", "ws")
	r, _, _ := dialTest(t, broker)
	broker.DropAll()
	select {
	case <-r.Lost():
	case <-time.After(5 * time.Second):
		t.Fatal("Lost never closed after the broker dropped the connection")
	}
	if err := r.SendData([]byte{1}); !errors.Is(err, ErrClosed) {
		t.Errorf("SendData after drop: %v, want ErrClosed", err)
	}
}
