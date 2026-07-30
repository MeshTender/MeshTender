package core

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/hardware"

	"github.com/jleight/meshtender/internal/auth"
	"github.com/jleight/meshtender/internal/identity"
	"github.com/jleight/meshtender/internal/store"
)

// TestConsoleLoginRetry drops the first login (simulating a lost packet) and
// verifies the console's login retries with a fresh timestamp and succeeds
// (confirming the repeater).
func TestConsoleLoginRetry(t *testing.T) {
	t.Parallel()
	st, ctx := coreStore(t)

	masterKey := testMasterKey
	idSvc, _ := identity.LoadOrCreate(ctx, st, masterKey)
	authSvc, _ := auth.New(st, st.Pool(), testAuthConfig())
	srv, _ := NewServer(st, authSvc, idSvc, testConfig())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	user := seedSession(t, ts, st, ctx, jar, "alice")

	repeater, _ := meshcore.GenerateLocalIdentity(rand.Reader)
	rep, err := st.CreateRepeater(ctx, &store.Repeater{
		OwnerID: user.ID, Name: "R", PublicKeyHex: repeater.String(),
		RadioFreqHz: 869525000, RadioBwHz: 250000, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/repeaters/" + rep.PublicID + "/console/ws"
	hdr := http.Header{}
	if cs := jar.Cookies(mustURL(t, ts.URL)); len(cs) > 0 {
		var parts []string
		for _, c := range cs {
			parts = append(parts, c.Name+"="+c.Value)
		}
		hdr.Set("Cookie", strings.Join(parts, "; "))
	}
	dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
	defer dcancel()
	ws, _, err := websocket.Dial(dctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	rw, rwcancel := context.WithTimeout(ctx, 15*time.Second)
	defer rwcancel()
	serverID := idSvc.Local().Identity
	shared, _ := repeater.SharedSecret(serverID)
	_ = ws.Write(rw, websocket.MessageText, []byte(`{"type":"ready"}`))

	var buf []byte
	loginsSeen := 0
	var lastTS uint32
	confirmed := false
	for !confirmed {
		typ, data, err := ws.Read(rw)
		if err != nil {
			t.Fatalf("ws read: %v", err)
		}
		if typ == websocket.MessageText {
			var m struct{ State, Message string }
			if json.Unmarshal(data, &m) == nil && m.State == "confirmed" {
				confirmed = true
			}
			continue
		}
		buf = append(buf, data...)
		frames, rest, _ := hardware.ExtractFrames(buf)
		buf = rest
		for _, f := range frames {
			if f.Command != hardware.KISS_CMD_DATA {
				continue
			}
			pkt, err := meshcore.PacketFromBytes(f.Data)
			if err != nil || pkt.PayloadType() != meshcore.PayloadTypeAnonReq {
				continue
			}
			anon, err := meshcore.AnonReqFromBytes(pkt.Payload)
			if err != nil {
				continue
			}
			// Each login must carry a fresh, strictly-increasing timestamp.
			plain := anon.Decrypt(shared)
			if plain == nil {
				t.Fatal("could not decrypt login")
			}
			ts := binary.LittleEndian.Uint32(plain[:4])
			if loginsSeen > 0 && ts <= lastTS {
				t.Fatalf("retry login timestamp %d not greater than previous %d", ts, lastTS)
			}
			lastTS = ts
			loginsSeen++

			// Drop the first login; reply to the second (PATH, admin).
			if loginsSeen < 2 {
				continue
			}
			respData := make([]byte, 13)
			binary.LittleEndian.PutUint32(respData[:4], ts)
			respData[6] = 1 // admin
			respData[7] = 3
			body := append([]byte{0x00, meshcore.PayloadTypeResponse}, respData...)
			enc, _ := meshcore.EncryptThenMAC(shared, body)
			p := &meshcore.Path{Destination: serverID.Hash()[0], Source: repeater.Hash()[0], MAC: [2]byte{enc[0], enc[1]}, EncryptedPayload: enc[2:]}
			payload, _ := p.ToBytes()
			lp := &meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypePath, 0), Payload: payload}
			raw, _ := lp.ToBytes()
			_ = ws.Write(rw, websocket.MessageBinary, hardware.EncodeDataFrame(raw))
		}
	}

	if loginsSeen < 2 {
		t.Fatalf("expected at least 2 login attempts, saw %d", loginsSeen)
	}
	got, err := st.GetRepeaterForUser(ctx, user.ID, rep.ID)
	if err != nil || !got.Confirmed {
		t.Fatalf("repeater not confirmed after retry (err %v)", err)
	}
}
