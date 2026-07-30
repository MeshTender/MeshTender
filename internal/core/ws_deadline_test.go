package core

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/jleight/meshtender/internal/auth"
	"github.com/jleight/meshtender/internal/identity"
	"github.com/jleight/meshtender/internal/store"
)

// TestConsoleWebSocketOutlivesServerReadTimeout pins the one thing audit O1's server
// timeouts could plausibly have broken: a long-lived console session.
//
// http.Server's ReadTimeout/WriteTimeout/IdleTimeout become deadlines on the
// underlying connection, which looks like it should sever an upgraded WebSocket the
// moment one elapses. It doesn't — net/http clears the deadline when a handler hijacks
// the connection (hijackLocked calls rwc.SetDeadline(time.Time{})), so the socket
// inherits nothing and is bounded only by consoleIdleTimeout and the shutdown drain.
//
// That behaviour is load-bearing but belongs to the standard library, not to this
// codebase, so it's worth an explicit test: it would catch a future WebSocket library
// that re-arms deadlines after hijacking, or a change in Go's behaviour, either of
// which would silently start cutting console sessions mid-command.
//
// The server here uses a deliberately tiny 250ms ReadTimeout rather than the
// production 30s so the assertion takes a second instead of half a minute.
func TestConsoleWebSocketOutlivesServerReadTimeout(t *testing.T) {
	t.Parallel()
	st, ctx := coreStore(t)

	masterKey := testMasterKey
	idSvc, err := identity.LoadOrCreate(ctx, st, masterKey)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	authSvc, err := auth.New(st, st.Pool(), testAuthConfig())
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	srv, err := NewServer(st, authSvc, idSvc, testConfig())
	if err != nil {
		t.Fatalf("server: %v", err)
	}

	// The production timeouts live in main.go; mirror their SHAPE here with a tiny
	// read deadline so the hazard reproduces in milliseconds instead of 30 seconds.
	const readTimeout = 250 * time.Millisecond
	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.Config.ReadHeaderTimeout = readTimeout
	ts.Config.ReadTimeout = readTimeout
	ts.Config.WriteTimeout = readTimeout
	ts.Config.IdleTimeout = readTimeout
	ts.Start()
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	user := seedSession(t, ts, st, ctx, jar, "wsdeadline")

	repeater, err := meshcore.GenerateLocalIdentity(rand.Reader)
	if err != nil {
		t.Fatalf("repeater identity: %v", err)
	}
	rep, err := st.CreateRepeater(ctx, &store.Repeater{
		OwnerID: user.ID, Name: "Deadline", PublicKeyHex: repeater.String(),
		RadioFreqHz: 869525000, RadioBwHz: 250000, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/repeaters/" + rep.PublicID + "/console/ws"
	hdr := http.Header{}
	var parts []string
	for _, c := range jar.Cookies(mustURL(t, ts.URL)) {
		parts = append(parts, c.Name+"="+c.Value)
	}
	if len(parts) > 0 {
		hdr.Set("Cookie", strings.Join(parts, "; "))
	}

	dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
	defer dcancel()
	ws, _, err := websocket.Dial(dctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	// Idle well past every deadline above, then use the socket. A surviving write and
	// read prove the connection wasn't torn down by an inherited request deadline.
	time.Sleep(4 * readTimeout)

	wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
	defer wcancel()
	if err := ws.Write(wctx, websocket.MessageText, []byte(`{"type":"ready"}`)); err != nil {
		t.Fatalf("write after %v idle failed — the socket inherited the server's read/write "+
			"deadline instead of clearing it: %v", 4*readTimeout, err)
	}
	// The handler answers a "ready" with its tuning/status frames, so a successful read
	// confirms the session is still live end to end, not just that the write buffered.
	if _, _, err := ws.Read(wctx); err != nil {
		t.Fatalf("read after %v idle failed — the session did not survive the server's "+
			"request deadlines: %v", 4*readTimeout, err)
	}
}
