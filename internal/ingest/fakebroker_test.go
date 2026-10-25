package ingest

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
)

// fakeBroker is just enough of an MQTT 3.1.1 broker to drive the supervisor:
// it answers CONNECT (checking the password), SUBSCRIBE and PINGREQ, records
// what the client asked for, and can push PUBLISH frames to it. go-mqtt only
// ships client-side encoders, and a real broker would be a new dependency or a
// container, so the handful of server frames are written by hand here.
type fakeBroker struct {
	t        *testing.T
	ln       net.Listener
	password string

	mu      sync.Mutex
	conns   []net.Conn // live, authenticated connections
	filters []string
	users   []string
}

func newFakeBroker(t *testing.T, password string) *fakeBroker {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &fakeBroker{t: t, ln: ln, password: password}
	go b.accept()
	t.Cleanup(func() {
		_ = ln.Close()
		b.mu.Lock()
		for _, c := range b.conns {
			_ = c.Close()
		}
		b.mu.Unlock()
	})
	return b
}

func (b *fakeBroker) url() string { return "tcp://" + b.ln.Addr().String() }

func (b *fakeBroker) accept() {
	for {
		c, err := b.ln.Accept()
		if err != nil {
			return
		}
		go b.serve(c)
	}
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

func (b *fakeBroker) serve(c net.Conn) {
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
				granted = append(granted, 0x00)
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

// publish sends a QoS 0 PUBLISH to every connected client.
func (b *fakeBroker) publish(topic string, payload []byte) {
	body := binary.BigEndian.AppendUint16(nil, uint16(len(topic))) //nolint:gosec // test topics are short
	body = append(append(body, topic...), payload...)
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.conns {
		_ = writeFrame(c, 0x30, body)
	}
}

// live is the number of currently connected clients.
func (b *fakeBroker) live() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.conns)
}

func (b *fakeBroker) snapshot() (filters, users []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.filters...), append([]string(nil), b.users...)
}
