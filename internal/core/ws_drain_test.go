package core

import (
	"context"
	"crypto/rand"
	"errors"
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

// TestDrainWebSockets: an active console WebSocket is closed and its handler
// returns when the server drains on shutdown — http.Server.Shutdown alone leaves
// hijacked sockets running, which is what this covers.
func TestDrainWebSockets(t *testing.T) {
	t.Parallel()
	st, ctx := coreStore(t)

	var masterKey [32]byte
	_, _ = rand.Read(masterKey[:])
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
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	user := seedSession(t, ts, st, ctx, jar, "wsuser")
	repeater, err := meshcore.GenerateLocalIdentity(rand.Reader)
	if err != nil {
		t.Fatalf("repeater identity: %v", err)
	}
	rep, err := st.CreateRepeater(ctx, &store.Repeater{
		OwnerID: user.ID, Name: "Test", PublicKeyHex: repeater.String(),
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
	defer ws.Close(websocket.StatusInternalError, "")

	// Send ready and read one status frame so the handler is definitely past the
	// upgrade/connect and running its session loop before we drain.
	rw, rwcancel := context.WithTimeout(ctx, 10*time.Second)
	defer rwcancel()
	if err := ws.Write(rw, websocket.MessageText, []byte(`{"type":"ready"}`)); err != nil {
		t.Fatalf("write ready: %v", err)
	}
	if _, _, err := ws.Read(rw); err != nil {
		t.Fatalf("read first frame: %v", err)
	}

	// Drain: cancels the WS context and waits for the handler. It must finish well
	// within the deadline (the handler is context-aware).
	drainCtx, dcancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer dcancel2()
	if !srv.DrainWebSockets(drainCtx) {
		t.Fatal("DrainWebSockets timed out — a WebSocket handler did not exit on shutdown")
	}

	// The server should have closed the socket. Read past any buffered status frames
	// until a read fails; a close (clean status or EOF) is expected — a read
	// deadline would mean the socket was left open.
	readCtx, rcancel := context.WithTimeout(ctx, 3*time.Second)
	defer rcancel()
	for {
		_, _, err := ws.Read(readCtx)
		if err == nil {
			continue // a buffered status frame; keep reading toward the close
		}
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("socket still open after drain (read deadline hit)")
		}
		break // socket closed
	}
}
