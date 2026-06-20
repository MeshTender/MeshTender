package mesh

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
)

// fakeModem stands in for a KISS modem + repeater: it drops the first dropN
// sends (simulating lost packets), then feeds a crafted reply back through the
// exchanger's HandleData on the next send.
type fakeModem struct {
	ex        *Exchanger
	repeater  meshcore.LocalIdentity
	server    meshcore.Identity
	replyText string
	dropN     int
	sends     int
}

func (f *fakeModem) SendData(_ []byte) error {
	f.sends++
	if f.sends <= f.dropN {
		return nil // lost packet — no reply
	}
	shared, _ := f.repeater.SharedSecret(f.server)
	plain := meshcore.BuildTextPlaintext(time.Unix(1_700_000_000, 0), 1<<2, []byte(f.replyText))
	tm, _ := meshcore.NewTextMessage(f.repeater, f.server, plain, shared)
	payload, _ := tm.ToBytes()
	pkt := &meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeTxtMsg, 0), Payload: payload}
	raw, _ := pkt.ToBytes()
	f.ex.HandleData(raw)
	return nil
}

func TestExchangerRetriesThenSucceeds(t *testing.T) {
	server := mustIdentity(t)
	repeater := mustIdentity(t)
	fm := &fakeModem{repeater: repeater, server: server.Identity, replyText: "> 42.0", dropN: 2}
	ex := NewExchanger(fm, server, repeater.Identity, 0, time.Millisecond, 4)
	fm.ex = ex

	var attempts int
	reply, err := ex.Command(context.Background(), "get lat", func(a, max int) { attempts = a })
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if reply != "> 42.0" {
		t.Errorf("reply = %q, want %q", reply, "> 42.0")
	}
	if fm.sends != 3 { // 2 dropped + 1 delivered
		t.Errorf("sends = %d, want 3", fm.sends)
	}
	if attempts != 3 {
		t.Errorf("last attempt = %d, want 3", attempts)
	}
}

func TestExchangerExhaustsRetries(t *testing.T) {
	server := mustIdentity(t)
	repeater := mustIdentity(t)
	fm := &fakeModem{repeater: repeater, server: server.Identity, replyText: "x", dropN: 100}
	ex := NewExchanger(fm, server, repeater.Identity, 0, time.Millisecond, 4)
	fm.ex = ex

	_, err := ex.Command(context.Background(), "get lat", nil)
	if !errors.Is(err, ErrNoReply) {
		t.Fatalf("err = %v, want ErrNoReply", err)
	}
	if fm.sends != 4 {
		t.Errorf("sends = %d, want 4 (maxSendTries)", fm.sends)
	}
}

// routeFakeModem replies to whatever it's asked to send via a callback, so a
// test can inspect the outbound packet (route type, path) and craft a reply.
type routeFakeModem struct{ reply func(raw []byte) }

func (f *routeFakeModem) SendData(raw []byte) error {
	if f.reply != nil {
		f.reply(raw)
	}
	return nil
}

// TestExchangerUsesDirectAfterLogin verifies that once Login learns a path, the
// next Command is sent via direct routing carrying that exact path.
func TestExchangerUsesDirectAfterLogin(t *testing.T) {
	server := mustIdentity(t)
	repeater := mustIdentity(t)
	shared, _ := repeater.SharedSecret(server.Identity)
	learnedPath := []byte{0xaa, 0xbb, 0xcc} // size 3, count 1

	var cmdRoute byte
	var cmdPath []byte
	fm := &routeFakeModem{}
	ex := NewExchanger(fm, server, repeater.Identity, 0, time.Millisecond, 4)
	fm.reply = func(raw []byte) {
		pkt, err := meshcore.PacketFromBytes(raw)
		if err != nil {
			return
		}
		switch pkt.PayloadType() {
		case meshcore.PayloadTypeAnonReq:
			// PATH login reply: [path_len][path][RESPONSE][reply_data], admin.
			resp := make([]byte, 13)
			resp[6] = 1 // admin
			resp[7] = 3
			pathLenByte := byte((3-1)<<6 | 1) // size 3, count 1
			body := append([]byte{pathLenByte}, learnedPath...)
			body = append(body, meshcore.PayloadTypeResponse)
			body = append(body, resp...)
			enc, _ := meshcore.EncryptThenMAC(shared, body)
			p := &meshcore.Path{Destination: server.Identity.Hash()[0], Source: repeater.Identity.Hash()[0], MAC: [2]byte{enc[0], enc[1]}, EncryptedPayload: enc[2:]}
			payload, _ := p.ToBytes()
			lp := &meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypePath, 0), Payload: payload}
			r, _ := lp.ToBytes()
			ex.HandleData(r)
		case meshcore.PayloadTypeTxtMsg:
			cmdRoute = pkt.RouteType()
			cmdPath = append([]byte(nil), pkt.Path...)
			plain := meshcore.BuildTextPlaintext(time.Unix(1, 0), 1<<2, []byte("> ok"))
			tm, _ := meshcore.NewTextMessage(repeater, server.Identity, plain, shared)
			payload, _ := tm.ToBytes()
			rp := &meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeTxtMsg, 0), Payload: payload}
			r, _ := rp.ToBytes()
			ex.HandleData(r)
		}
	}

	if _, err := ex.Login(context.Background(), "", nil); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := ex.Command(context.Background(), "get lat", nil); err != nil {
		t.Fatalf("Command: %v", err)
	}
	if cmdRoute != meshcore.RouteTypeDirect {
		t.Errorf("command route = %d, want direct (%d)", cmdRoute, meshcore.RouteTypeDirect)
	}
	if !bytes.Equal(cmdPath, learnedPath) {
		t.Errorf("command path = %x, want %x", cmdPath, learnedPath)
	}
}

func TestExchangerCancelled(t *testing.T) {
	server := mustIdentity(t)
	repeater := mustIdentity(t)
	fm := &fakeModem{repeater: repeater, server: server.Identity, dropN: 100}
	ex := NewExchanger(fm, server, repeater.Identity, 0, time.Hour, 4)
	fm.ex = ex

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ex.Command(ctx, "get lat", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
