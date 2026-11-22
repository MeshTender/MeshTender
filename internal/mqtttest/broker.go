// Package mqtttest is a hand-written MQTT 3.1.1 broker for tests. It speaks
// just enough of the protocol to drive MeshTender's MQTT clients: CONNECT
// (checking the password), SUBSCRIBE, PUBLISH in both directions (QoS 0 and 1),
// PINGREQ and DISCONNECT, over raw TCP or WebSocket (ws, or wss with a test
// CA). A real broker would be a new dependency or a container, so the handful
// of frames are written by hand here.
package mqtttest

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

// Broker records what its clients ask for and can push PUBLISH frames to them.
type Broker struct {
	password string
	// RejectSubs answers every SUBSCRIBE with the 3.1.1 failure code 0x80.
	RejectSubs bool

	rawURL  string
	rootCAs *x509.CertPool // for wss: trusts the test server's certificate

	mu        sync.Mutex
	conns     []net.Conn // live, authenticated connections
	filters   []string
	users     []string
	paths     []string // WebSocket request paths
	published []Message
	onPublish func(Message)
}

// Message is a PUBLISH a client sent to the broker.
type Message struct {
	Topic   string
	Payload []byte
	QoS     byte
	Retain  bool
}

// New starts a broker on loopback that accepts password. transport is "tcp",
// "ws" or "wss". It shuts down when the test ends.
func New(t *testing.T, password, transport string) *Broker {
	t.Helper()
	b := &Broker{password: password}
	if transport == "tcp" {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		b.rawURL = "tcp://" + ln.Addr().String()
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go b.serve(c)
			}
		}()
		t.Cleanup(func() { _ = ln.Close() })
	} else {
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"mqtt"}})
			if err != nil {
				return
			}
			if c.Subprotocol() != "mqtt" {
				_ = c.Close(websocket.StatusPolicyViolation, "mqtt subprotocol required")
				return
			}
			b.mu.Lock()
			b.paths = append(b.paths, r.URL.Path)
			b.mu.Unlock()
			b.serve(websocket.NetConn(context.Background(), c, websocket.MessageBinary))
		}))
		if transport == "wss" {
			srv.StartTLS()
			b.rootCAs = x509.NewCertPool()
			b.rootCAs.AddCert(srv.Certificate())
		} else {
			srv.Start()
		}
		// No path: the client should default to /mqtt.
		b.rawURL = transport + "://" + srv.Listener.Addr().String()
		t.Cleanup(srv.Close)
	}
	t.Cleanup(func() {
		b.mu.Lock()
		for _, c := range b.conns {
			_ = c.Close()
		}
		b.mu.Unlock()
	})
	return b
}

// URL is the broker's address, e.g. "ws://127.0.0.1:1234".
func (b *Broker) URL() string { return b.rawURL }

// RootCAs trusts a wss broker's certificate; nil for the other transports.
func (b *Broker) RootCAs() *x509.CertPool { return b.rootCAs }

// OnPublish registers a callback for every PUBLISH a client sends, called from
// that client's connection goroutine after the message is recorded.
func (b *Broker) OnPublish(f func(Message)) {
	b.mu.Lock()
	b.onPublish = f
	b.mu.Unlock()
}

// readFrame reads one MQTT control packet: the type byte and its body.
func readFrame(r *bufio.Reader) (byte, []byte, error) {
	typ, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	var n, shift int
	for {
		d, err := r.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		n |= int(d&0x7f) << shift
		if d&0x80 == 0 {
			break
		}
		shift += 7
	}
	body := make([]byte, n)
	_, err = io.ReadFull(r, body)
	return typ, body, err
}

func writeFrame(w io.Writer, typ byte, body []byte) error {
	hdr := []byte{typ}
	n := len(body)
	for {
		d := byte(n & 0x7f)
		n >>= 7
		if n > 0 {
			d |= 0x80
		}
		hdr = append(hdr, d)
		if n == 0 {
			break
		}
	}
	_, err := w.Write(append(hdr, body...))
	return err
}

// str reads an MQTT length-prefixed string from p, returning the rest.
func str(p []byte) (string, []byte) {
	if len(p) < 2 {
		return "", nil
	}
	n := int(binary.BigEndian.Uint16(p))
	if len(p) < 2+n {
		return "", nil
	}
	return string(p[2 : 2+n]), p[2+n:]
}

func (b *Broker) serve(c net.Conn) {
	defer func() {
		_ = c.Close()
		b.mu.Lock()
		for i, x := range b.conns {
			if x == c {
				b.conns = append(b.conns[:i], b.conns[i+1:]...)
				break
			}
		}
		b.mu.Unlock()
	}()
	r := bufio.NewReader(c)
	typ, body, err := readFrame(r)
	if err != nil || typ>>4 != 1 {
		return
	}
	// CONNECT: protocol name, level, flags, keep-alive, client id, then the
	// username and password when flagged (no will: the client sends none).
	_, rest := str(body)
	if len(rest) < 4 {
		return
	}
	flags := rest[1]
	rest = rest[4:]
	_, rest = str(rest) // client id
	var user, pass string
	if flags&0x80 != 0 {
		user, rest = str(rest)
	}
	if flags&0x40 != 0 {
		pass, _ = str(rest)
	}
	if pass != b.password {
		_ = writeFrame(c, 0x20, []byte{0x00, 0x05}) // not authorized
		return
	}
	if err := writeFrame(c, 0x20, []byte{0x00, 0x00}); err != nil {
		return
	}
	b.mu.Lock()
	b.conns = append(b.conns, c)
	b.users = append(b.users, user)
	b.mu.Unlock()

	for {
		typ, body, err := readFrame(r)
		if err != nil {
			return
		}
		switch typ >> 4 {
		case 3: // PUBLISH: topic, packet id (QoS > 0), payload
			if !b.received(c, typ, body) {
				return
			}
		case 8: // SUBSCRIBE: packet id, then (filter, qos) pairs
			id, rest := body[:2], body[2:]
			var granted []byte
			for len(rest) > 0 {
				var f string
				f, rest = str(rest)
				if len(rest) == 0 {
					return
				}
				rest = rest[1:]
				b.mu.Lock()
				b.filters = append(b.filters, f)
				b.mu.Unlock()
				if b.RejectSubs {
					granted = append(granted, 0x80)
				} else {
					granted = append(granted, 0x00)
				}
			}
			if err := writeFrame(c, 0x90, append(append([]byte{}, id...), granted...)); err != nil {
				return
			}
		case 12: // PINGREQ
			_ = writeFrame(c, 0xD0, nil)
		case 14: // DISCONNECT
			return
		}
	}
}

// received records a client's PUBLISH and acknowledges it at QoS 1. It
// reports false if the frame is malformed or the ack can't be written.
func (b *Broker) received(c net.Conn, typ byte, body []byte) bool {
	qos := (typ >> 1) & 0x03
	topic, rest := str(body)
	if rest == nil {
		return false
	}
	var id []byte
	if qos > 0 {
		if len(rest) < 2 {
			return false
		}
		id, rest = rest[:2], rest[2:]
	}
	m := Message{Topic: topic, Payload: append([]byte(nil), rest...), QoS: qos, Retain: typ&0x01 != 0}
	b.mu.Lock()
	b.published = append(b.published, m)
	cb := b.onPublish
	b.mu.Unlock()
	if qos == 1 && writeFrame(c, 0x40, id) != nil { // PUBACK
		return false
	}
	if cb != nil {
		cb(m)
	}
	return true
}

// Publish sends a QoS 0 PUBLISH to every connected client.
func (b *Broker) Publish(topic string, payload []byte) {
	body := binary.BigEndian.AppendUint16(nil, uint16(len(topic))) //nolint:gosec // test topics are short
	body = append(append(body, topic...), payload...)
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.conns {
		_ = writeFrame(c, 0x30, body)
	}
}

// DropAll closes every client connection, as a broker restart would.
func (b *Broker) DropAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.conns {
		_ = c.Close()
	}
}

// Live is the number of currently connected clients.
func (b *Broker) Live() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.conns)
}

// Snapshot returns the subscribed filters, the usernames that connected and
// the WebSocket request paths, in order.
func (b *Broker) Snapshot() (filters, users, paths []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.filters...), append([]string(nil), b.users...), append([]string(nil), b.paths...)
}

// Published returns every PUBLISH clients have sent, in order.
func (b *Broker) Published() []Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Message(nil), b.published...)
}
