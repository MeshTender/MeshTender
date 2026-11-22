package relay

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/ingest"
	"github.com/MeshTender/MeshTender/internal/observe"
)

// publishTimeout bounds the wait for a frame's PUBACK.
const publishTimeout = 10 * time.Second

// ErrClosed is returned by SendData once the connection has dropped or Close
// has been called.
var ErrClosed = errors.New("relay: connection closed")

// Config is what Dial needs: the broker the observer and MeshTender share, the
// topic prefix the observer uplinks under, and who signs and who transmits.
type Config struct {
	Broker      ingest.BrokerConfig
	TopicPrefix string
	Observer    meshcore.Identity
	Signer      meshcore.LocalIdentity
	Dialer      ingest.Dialer
	// Now is the clock stamped into frames; nil means time.Now. A field so
	// tests can fix it.
	Now func() time.Time
}

// Handlers receive what the observer sends back. Both are called from the MQTT
// client's goroutine, so they must not block.
type Handlers struct {
	// Packet gets every packet the observer heard: the raw bytes, as a KISS
	// modem's data handler would.
	Packet func(raw []byte)
	// Result gets the observer's answer to each frame.
	Result func(Result)
}

// Relay is one session's link to the mesh through an observer. It satisfies
// mesh.Sender, so an Exchanger can drive it in place of a KISS modem. Each
// session dials its own connection: the ingest supervisor's connections run
// only on the leader replica, and a session can be on any replica.
type Relay struct {
	client   mqtt.Client
	observer meshcore.Identity
	signer   meshcore.LocalIdentity
	now      func() time.Time
	txTopic  string
	nextID   atomic.Uint32

	lost      chan struct{}
	closeOnce sync.Once
}

// Dial connects to the broker and subscribes to the observer's packets and
// results. The connection doesn't reconnect: if it drops, Lost is closed and
// SendData fails, and the session ends.
func Dial(cfg Config, h Handlers) (*Relay, error) {
	r := &Relay{
		observer: cfg.Observer,
		signer:   cfg.Signer,
		now:      cfg.Now,
		txTopic:  TxTopic(cfg.Observer),
		lost:     make(chan struct{}),
	}
	if r.now == nil {
		r.now = time.Now
	}
	// Start request ids somewhere unpredictable, so results from an earlier
	// session on the same observer don't match this one's.
	r.nextID.Store(uint32(time.Now().UnixNano())) //nolint:gosec // G115: any 32 bits will do

	client, err := cfg.Dialer.Connect(cfg.Broker, func(error) { r.markLost() })
	if err != nil {
		return nil, err
	}
	r.client = client

	packets, results := PacketsTopic(cfg.TopicPrefix, cfg.Observer), ResultTopic(cfg.Observer)
	handler := func(_ mqtt.Client, msg mqtt.Message) {
		switch {
		case msg.Topic() == results:
			if res, err := ParseResult(msg.Payload()); err == nil && h.Result != nil {
				h.Result(res)
			}
		case strings.HasSuffix(msg.Topic(), "/packets"):
			if p, err := observe.ParsePacket(msg.Payload()); err == nil && h.Packet != nil {
				h.Packet(p.Raw)
			}
		}
	}
	if err := ingest.Subscribe(client, []string{packets, results}, handler); err != nil {
		client.Disconnect(0)
		return nil, err
	}
	return r, nil
}

// SendData wraps pkt in a signed frame and publishes it to the observer. It
// returns once the broker has acknowledged the frame; the observer's verdict
// arrives later through Handlers.Result.
func (r *Relay) SendData(pkt []byte) error {
	select {
	case <-r.lost:
		return ErrClosed
	default:
	}
	frame, err := EncodeTxFrame(r.observer, r.signer, r.nextID.Add(1), r.now(), pkt)
	if err != nil {
		return err
	}
	// QoS 1 so a frame isn't silently dropped between us and the broker, and
	// never retained: a retained frame would be transmitted again by an
	// observer that reconnects later (the firmware drops them anyway).
	tok := r.client.Publish(r.txTopic, 1, false, frame)
	if !tok.WaitTimeout(publishTimeout) {
		return fmt.Errorf("relay: publish: %w", context.DeadlineExceeded)
	}
	if err := tok.Error(); err != nil {
		return fmt.Errorf("relay: publish: %w", err)
	}
	return nil
}

// Lost is closed when the broker connection drops.
func (r *Relay) Lost() <-chan struct{} { return r.lost }

func (r *Relay) markLost() { r.closeOnce.Do(func() { close(r.lost) }) }

// Close disconnects from the broker.
func (r *Relay) Close() {
	r.markLost()
	r.client.Disconnect(250) // ms to let an in-flight frame finish
}
