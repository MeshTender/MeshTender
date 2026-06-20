package web

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/hardware"

	"github.com/jleight/meshtender/internal/auth"
	"github.com/jleight/meshtender/internal/config"
	"github.com/jleight/meshtender/internal/identity"
	"github.com/jleight/meshtender/internal/store"
)

func TestParseLocationFloat(t *testing.T) {
	cases := map[string]struct {
		want float64
		ok   bool
	}{
		"> 37.7749":   {37.7749, true},
		"> -122.4194": {-122.4194, true},
		"0":           {0, true},
		"> n/a":       {0, false},
		"":            {0, false},
	}
	for in, exp := range cases {
		got, ok := parseLocationFloat(in)
		if ok != exp.ok || (ok && got != exp.want) {
			t.Errorf("parseLocationFloat(%q) = (%v,%v), want (%v,%v)", in, got, ok, exp.want, exp.ok)
		}
	}
}

// TestConfirmFetchesLocation drives the confirm flow with store_location set and
// verifies the repeater's lat/lon are fetched (get lat / get lon) and stored.
func TestConfirmFetchesLocation(t *testing.T) {
	dsn := os.Getenv("MESHTENDER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set MESHTENDER_TEST_DATABASE_URL to run this integration test")
	}
	if u, err := url.Parse(dsn); err != nil || !strings.HasSuffix(strings.TrimPrefix(u.Path, "/"), "_test") {
		t.Fatalf("refusing to run: test DB name must end in _test (got %q)", dsn)
	}
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := st.Pool().Exec(ctx, `TRUNCATE users, repeaters, organizations, server_identity, sessions RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	var masterKey [32]byte
	_, _ = rand.Read(masterKey[:])
	idSvc, _ := identity.LoadOrCreate(ctx, st, masterKey)
	authSvc, _ := auth.New(st, st.Pool(), auth.Config{RPID: "localhost", RPDisplayName: "t", RPOrigins: []string{"http://localhost"}})
	srv, _ := NewServer(st, authSvc, idSvc, &config.Config{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, _ := client.PostForm(ts.URL+"/signup/password", url.Values{"username": {"alice"}, "password": {"supersecret"}})
	resp.Body.Close()
	user, _ := st.GetUserByUsername(ctx, "alice")

	repeater, _ := meshcore.GenerateLocalIdentity(rand.Reader)
	rep, err := st.CreateRepeater(ctx, &store.Repeater{
		OwnerID: user.ID, Name: "Geo", PublicKeyHex: repeater.String(), StoreLocation: true,
		RadioFreqHz: 869525000, RadioBwHz: 250000, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/repeaters/" + rep.PublicID + "/ws"
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

	// Reply to login (PATH) then to get lat / get lon (TXT_MSG).
	replyText := func(text string) []byte {
		plain := meshcore.BuildTextPlaintext(time.Unix(1_700_003_000, 0), 1<<2, []byte(text))
		tm, _ := meshcore.NewTextMessage(repeater, serverID, plain, shared)
		payload, _ := tm.ToBytes()
		pkt := &meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeTxtMsg, 0), Payload: payload}
		raw, _ := pkt.ToBytes()
		return hardware.EncodeDataFrame(raw)
	}

	stored := func() bool {
		got, err := st.GetRepeaterForUser(ctx, user.ID, rep.ID)
		if err != nil || got.Latitude == nil || got.Longitude == nil {
			return false
		}
		if math.Abs(*got.Latitude-37.7749) > 1e-6 || math.Abs(*got.Longitude-(-122.4194)) > 1e-6 {
			t.Fatalf("stored location = %v,%v want 37.7749,-122.4194", *got.Latitude, *got.Longitude)
		}
		return true
	}

	var buf []byte
	loggedIn := false
	for {
		typ, data, err := ws.Read(rw)
		if err != nil {
			break // server finished and closed; check the DB below
		}
		if typ == websocket.MessageText {
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
			if err != nil {
				continue
			}
			switch pkt.PayloadType() {
			case meshcore.PayloadTypeAnonReq:
				if loggedIn {
					continue
				}
				loggedIn = true
				// PATH login reply granting admin.
				resp := make([]byte, 13)
				binary.LittleEndian.PutUint32(resp[:4], 1_700_003_000)
				resp[6] = 1 // admin
				resp[7] = 3
				plain := append([]byte{0x00, meshcore.PayloadTypeResponse}, resp...) // [path_len=0][type][response]
				enc, _ := meshcore.EncryptThenMAC(shared, plain)
				p := &meshcore.Path{Destination: serverID.Hash()[0], Source: repeater.Identity.Hash()[0], MAC: [2]byte{enc[0], enc[1]}, EncryptedPayload: enc[2:]}
				payload, _ := p.ToBytes()
				lp := &meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypePath, 0), Payload: payload}
				raw, _ := lp.ToBytes()
				_ = ws.Write(rw, websocket.MessageBinary, hardware.EncodeDataFrame(raw))
			case meshcore.PayloadTypeTxtMsg:
				tm, err := meshcore.TextMessageFromBytes(pkt.Payload)
				if err != nil {
					continue
				}
				cmd := strings.TrimRight(string(tm.Decrypt(shared)[5:]), "\x00")
				switch {
				case strings.HasPrefix(cmd, "get lat"):
					_ = ws.Write(rw, websocket.MessageBinary, replyText("> 37.7749"))
				case strings.HasPrefix(cmd, "get lon"):
					_ = ws.Write(rw, websocket.MessageBinary, replyText("> -122.4194"))
				}
			}
		}

		if stored() {
			return // success
		}
	}

	// Socket closed; poll briefly for the stored location.
	for i := 0; i < 40; i++ {
		if stored() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("location was not stored")
}
