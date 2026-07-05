// Package wsbridge adapts a browser WebSocket — which relays raw KISS serial
// bytes to/from a WebSerial-connected MeshCore KISS modem — to the
// meshcore-go hardware.Transport interface. This lets the server own all
// MeshCore crypto while the radio is physically attached to the user's machine.
//
// Wire protocol on the socket:
//   - Binary messages  : raw KISS serial bytes (both directions).
//   - Text messages    : JSON control/status frames (browser→server "ready",
//     server→browser status updates).
package wsbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/coder/websocket"
	"github.com/meshcore-go/meshcore-go/hardware"
)

// maxKissBuffer bounds the inbound reassembly buffer. A single KISS frame carries
// at most one MeshCore packet — MAX_TRANS_UNIT is 255 bytes over the air (firmware
// src/MeshCore.h) and meshcore-go caps a KISS packet at KISS_MAX_PACKET_SIZE=255 —
// so even with worst-case KISS byte-stuffing (every byte escaped ×2) plus FEND
// framing a frame is ~520 bytes on the wire. This buffer only ever holds one
// not-yet-FEND-terminated frame, so 4 KiB is generous headroom; if this much
// accumulates with no frame delimiter the peer is sending garbage, so we drop it
// instead of letting it grow without bound.
const maxKissBuffer = 4096

// ErrKissBufferOverflow is reported to the error handler when the reassembly
// buffer is dropped for exceeding maxKissBuffer with no frame boundary.
var ErrKissBufferOverflow = errors.New("wsbridge: inbound KISS buffer overflow; discarding")

// Conn implements hardware.Transport over a WebSocket.
type Conn struct {
	ws  *websocket.Conn
	ctx context.Context

	writeMu sync.Mutex // serializes all socket writes (coder/websocket needs one writer)

	mu       sync.Mutex
	frameH   func(*hardware.KissFrame)
	errH     func(error)
	observer func(*hardware.KissFrame) // optional debug tap, sees every inbound frame
	buf      []byte                    // leftover inbound bytes between messages

	dead     chan struct{}
	deadOnce sync.Once
}

// New wraps an accepted WebSocket. ctx should outlive the connection (not the
// per-request context, which is unsafe to use after Accept).
func New(ctx context.Context, ws *websocket.Conn) *Conn {
	return &Conn{ws: ws, ctx: ctx, dead: make(chan struct{})}
}

// --- hardware.Transport ---

// Connect is a no-op; the socket is already established when New is called.
func (c *Conn) Connect(context.Context) error { return nil }

// Close marks the bridge dead and closes the socket.
func (c *Conn) Close() error {
	c.markDead()
	return c.ws.Close(websocket.StatusNormalClosure, "closed")
}

// Send writes already-KISS-framed bytes to the browser as a binary message.
func (c *Conn) Send(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.Write(c.ctx, websocket.MessageBinary, data)
}

// SetFrameHandler registers the modem's inbound-frame callback.
func (c *Conn) SetFrameHandler(h func(*hardware.KissFrame)) {
	c.mu.Lock()
	c.frameH = h
	c.mu.Unlock()
}

// SetErrorHandler registers the modem's error callback.
func (c *Conn) SetErrorHandler(h func(error)) {
	c.mu.Lock()
	c.errH = h
	c.mu.Unlock()
}

// Dead returns a channel closed when the socket read loop exits.
func (c *Conn) Dead() <-chan struct{} { return c.dead }

// SetObserver registers an optional callback invoked for every inbound KISS
// frame (in addition to the modem's handler). Used for debug logging.
func (c *Conn) SetObserver(h func(*hardware.KissFrame)) {
	c.mu.Lock()
	c.observer = h
	c.mu.Unlock()
}

// --- driven by the owning read loop ---

// Feed pushes inbound serial bytes (from the browser) into the KISS frame
// parser, dispatching any complete frames to the modem's handler.
//
// It reassembles KISS frames itself rather than via hardware.ExtractFrames,
// which loses frame sync when a read chunk ends exactly on a frame's trailing
// FEND (it drops the delimiter, so the next frame's body is discarded as
// pre-FEND junk). Here we keep everything after the *last* FEND as the
// incomplete remainder and decode each non-empty segment between FENDs; since
// KISS escapes any 0xC0 inside data, splitting on literal FEND is safe.
func (c *Conn) Feed(data []byte) {
	c.mu.Lock()
	c.buf = append(c.buf, data...)
	last := bytes.LastIndexByte(c.buf, hardware.KISS_FEND)
	if last < 0 {
		// No complete frame boundary yet. Cap the buffer so a peer that never sends
		// a FEND can't grow it without bound; drop it and report the overflow.
		var eh func(error)
		if len(c.buf) > maxKissBuffer {
			c.buf = nil
			eh = c.errH
		}
		c.mu.Unlock()
		if eh != nil {
			eh(ErrKissBufferOverflow)
		}
		return
	}
	processable := c.buf[:last+1]
	c.buf = append([]byte(nil), c.buf[last+1:]...) // bytes after the last FEND
	h, eh, obs := c.frameH, c.errH, c.observer
	c.mu.Unlock()

	for _, seg := range bytes.Split(processable, []byte{hardware.KISS_FEND}) {
		if len(seg) == 0 {
			continue // inter-frame fill / empty segment
		}
		frame, err := hardware.DecodeFrame(seg)
		if err != nil {
			if eh != nil {
				eh(err)
			}
			continue
		}
		if obs != nil {
			obs(frame)
		}
		if h != nil {
			h(frame)
		}
	}
}

// Status sends a JSON control/status frame to the browser as a text message.
func (c *Conn) Status(state, message string) error {
	payload, err := json.Marshal(map[string]string{"state": state, "message": message})
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.Write(c.ctx, websocket.MessageText, payload)
}

// MarkDead closes the Dead channel (call when the socket read loop exits).
func (c *Conn) MarkDead() { c.markDead() }

func (c *Conn) markDead() {
	c.deadOnce.Do(func() { close(c.dead) })
}
