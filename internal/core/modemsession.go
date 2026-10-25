package core

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/meshcore-go/meshcore-go/hardware"

	"github.com/MeshTender/MeshTender/internal/wsbridge"
)

// modemSession is a MeshCore KISS modem the user has plugged into their
// browser, reached over this request's WebSocket. The browser is a byte pipe
// (static/mesh-bridge.js): binary messages carry raw KISS serial bytes both
// ways, text messages carry small JSON control messages from the page and
// {state, message} statuses back to it. Everything else — framing, packets,
// what to transmit — happens here. The repeater console and the net control
// session are both built on it.
type modemSession struct {
	Ctx    context.Context
	Bridge *wsbridge.Conn
	Modem  *hardware.KissModem

	ws        *websocket.Conn
	cancel    context.CancelFunc
	idle      *time.Timer
	idleAfter time.Duration
	ready     chan struct{}
	msgs      chan modemClientMsg
	done      func()
}

// modemClientMsg is a control message from the page, e.g. {"type":"cmd","text":"get name"}.
type modemClientMsg struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// modemMsgQueue bounds how many page messages wait for the session to handle
// them; beyond it the page is told to wait.
const modemMsgQueue = 8

// openModemSession upgrades the request to the modem WebSocket. The session
// ends — and the socket closes — when the page goes away, the server shuts
// down, or nothing resets the idle timer for idleAfter. Callers must Close it.
// ok is false when the upgrade failed (the response has been written).
func (s *Handlers) openModemSession(w http.ResponseWriter, r *http.Request, idleAfter time.Duration, opts ...hardware.ModemOption) (*modemSession, bool) {
	// Track the socket so shutdown can drain it (http.Server.Shutdown doesn't
	// close hijacked/WebSocket conns). Add before Accept so a shutdown racing
	// the upgrade still waits for this handler.
	s.wsWG.Add(1)
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.wsWG.Done()
		return nil, false
	}
	// Derive from the server's WS context so shutdown cancels the session.
	ctx, cancel := context.WithCancel(s.wsCtx)
	bridge := wsbridge.New(ctx, ws)
	return &modemSession{
		Ctx:       ctx,
		Bridge:    bridge,
		Modem:     hardware.NewKissModem(bridge, opts...),
		ws:        ws,
		cancel:    cancel,
		idle:      time.AfterFunc(idleAfter, cancel),
		idleAfter: idleAfter,
		ready:     make(chan struct{}, 1),
		msgs:      make(chan modemClientMsg, modemMsgQueue),
		done:      s.wsWG.Done,
	}, true
}

// Start reads the socket: binary into the bridge (and so the modem), text into
// Messages, and "ready" into WaitReady. Call it once the modem is connected.
// When the page goes away it cancels the session.
func (m *modemSession) Start() {
	go func() {
		for {
			typ, data, err := m.ws.Read(m.Ctx)
			if err != nil {
				m.Bridge.MarkDead()
				m.cancel()
				return
			}
			switch typ {
			case websocket.MessageBinary:
				m.Bridge.Feed(data)
			case websocket.MessageText:
				var msg modemClientMsg
				if json.Unmarshal(data, &msg) != nil {
					continue
				}
				if msg.Type == "ready" {
					select {
					case m.ready <- struct{}{}:
					default:
					}
					continue
				}
				select {
				case m.msgs <- msg:
				default:
					_ = m.Bridge.Status("error", "busy — wait for the previous command")
				}
			}
		}
	}()
}

// WaitReady blocks until the page reports its serial port is open, or the
// session ends. It reports whether the page is ready.
func (m *modemSession) WaitReady() bool {
	select {
	case <-m.ready:
		return true
	case <-m.Ctx.Done():
		return false
	}
}

// Messages delivers the page's control messages in order.
func (m *modemSession) Messages() <-chan modemClientMsg { return m.msgs }

// ResetIdle restarts the idle timeout; call it on user activity.
func (m *modemSession) ResetIdle() { m.idle.Reset(m.idleAfter) }

// Close ends the session and releases the shutdown drain.
func (m *modemSession) Close() {
	m.idle.Stop()
	_ = m.Modem.Close()
	m.cancel()
	m.done()
}
