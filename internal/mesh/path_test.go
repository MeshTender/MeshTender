package mesh

import (
	"bytes"
	"context"
	"testing"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
)

func TestParsePath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      string
		path    []byte
		pathLen byte
		wantErr bool
	}{
		{"empty", "", nil, 0, false},
		{"whitespace", "   ", nil, 0, false},
		{"one-byte hops", "11, 22, 33", []byte{0x11, 0x22, 0x33}, 0x03, false}, // size 1, count 3
		{"no spaces", "aa,bb", []byte{0xaa, 0xbb}, 0x02, false},                // size 1, count 2
		{"two-byte hops", "1111, 2222", []byte{0x11, 0x11, 0x22, 0x22}, (2-1)<<6 | 2, false},
		{"three-byte hops", "111111, 222222", []byte{0x11, 0x11, 0x11, 0x22, 0x22, 0x22}, (3-1)<<6 | 2, false},
		{"single hop", "a1b2c3", []byte{0xa1, 0xb2, 0xc3}, (3-1)<<6 | 1, false},
		{"mixed lengths", "11, 2222", nil, 0, true},
		{"bad hex", "11, zz", nil, 0, true},
		{"too many bytes per hop", "11223344", nil, 0, true},
		{"empty hop", "11,,22", nil, 0, true},
		{"odd hex", "111", nil, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path, pathLen, err := ParsePath(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("ParsePath(%q) = (%x, %d, nil), want error", c.in, path, pathLen)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePath(%q): %v", c.in, err)
			}
			if !bytes.Equal(path, c.path) || pathLen != c.pathLen {
				t.Errorf("ParsePath(%q) = (%x, %#x), want (%x, %#x)", c.in, path, pathLen, c.path, c.pathLen)
			}
		})
	}
}

func TestParsePathTooManyHops(t *testing.T) {
	t.Parallel()
	var b []byte
	for i := 0; i < maxPathHops+1; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '0', '1')
	}
	if _, _, err := ParsePath(string(b)); err == nil {
		t.Fatalf("want error for %d hops", maxPathHops+1)
	}
}

// TestLoginUsesPresetPath verifies SetPath makes Login route directly with the
// supplied path, and that a direct RESPONSE reply (no path of its own) does not
// wipe the preset path — the subsequent command still routes direct via it.
func TestLoginUsesPresetPath(t *testing.T) {
	t.Parallel()
	server := mustIdentity(t)
	repeater := mustIdentity(t)
	shared, _ := repeater.SharedSecret(server.Identity)
	userPath := []byte{0x11, 0x22, 0x33} // 3 one-byte hops
	userPathLen := byte(0x03)            // size 1, count 3

	var loginRoute byte
	var loginPath []byte
	var cmdRoute byte
	var cmdPath []byte
	fm := &routeFakeModem{}
	ex := NewExchanger(fm, server, repeater.Identity, 0, time.Millisecond, 4)
	ex.SetPath(userPath, userPathLen)
	fm.reply = func(raw []byte) {
		pkt, err := meshcore.PacketFromBytes(raw)
		if err != nil {
			return
		}
		switch pkt.PayloadType() {
		case meshcore.PayloadTypeAnonReq:
			loginRoute = pkt.RouteType()
			loginPath = append([]byte(nil), pkt.Path...)
			// Reply as a direct RESPONSE (carries no return path of its own).
			resp := make([]byte, 13)
			resp[6] = 1 // admin
			resp[7] = 3
			enc, _ := meshcore.EncryptThenMAC(shared, resp)
			r := &meshcore.Response{Destination: server.Hash()[0], Source: repeater.Hash()[0], MAC: [2]byte{enc[0], enc[1]}, EncryptedPayload: enc[2:]}
			payload, _ := r.ToBytes()
			rp := &meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeDirect, meshcore.PayloadTypeResponse, 0), Payload: payload}
			b, _ := rp.ToBytes()
			ex.HandleData(b)
		case meshcore.PayloadTypeTxtMsg:
			cmdRoute = pkt.RouteType()
			cmdPath = append([]byte(nil), pkt.Path...)
			plain := meshcore.BuildTextPlaintext(time.Unix(1, 0), 1<<2, []byte("> ok"))
			tm, _ := meshcore.NewTextMessage(repeater, server.Identity, plain, shared)
			payload, _ := tm.ToBytes()
			rp := &meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeTxtMsg, 0), Payload: payload}
			b, _ := rp.ToBytes()
			ex.HandleData(b)
		}
	}

	lr, err := ex.Login(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !lr.IsAdmin {
		t.Fatalf("login not admin")
	}
	if loginRoute != meshcore.RouteTypeDirect {
		t.Errorf("login route = %d, want direct (%d)", loginRoute, meshcore.RouteTypeDirect)
	}
	if !bytes.Equal(loginPath, userPath) {
		t.Errorf("login path = %x, want %x", loginPath, userPath)
	}
	if _, err := ex.Command(context.Background(), "get lat", nil); err != nil {
		t.Fatalf("Command: %v", err)
	}
	if cmdRoute != meshcore.RouteTypeDirect || !bytes.Equal(cmdPath, userPath) {
		t.Errorf("command routed %d path %x, want direct with preset path %x (preset path was wiped)", cmdRoute, cmdPath, userPath)
	}
}
