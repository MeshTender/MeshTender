package core

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/ingest"
	"github.com/MeshTender/MeshTender/internal/mesh"
	"github.com/MeshTender/MeshTender/internal/relay"
	"github.com/MeshTender/MeshTender/internal/secretbox"
	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/wsbridge"
)

// senderFunc adapts a function to mesh.Sender, so an Exchanger can be built
// before the link it sends through exists.
type senderFunc func([]byte) error

func (f senderFunc) SendData(pkt []byte) error { return f(pkt) }

// errRelayNotAllowed means the user can't relay through the observer they
// asked for.
var errRelayNotAllowed = errors.New("relay not allowed")

// consoleRelay is a console session's link to the mesh through an observer.
type consoleRelay struct {
	*relay.Relay
	// answered is set once the observer has reported on any frame, which
	// proves it's subscribed to its tx topic on this broker.
	answered atomic.Bool
}

// dialConsoleRelay connects the session to the observer whose key is
// observerKey (lowercase hex), through the broker of an org the user
// administers that approved it. Every packet the observer hears goes to ex;
// the observer's verdict on each frame we send goes to the page when it's a
// refusal. With debug set, every packet heard is also shown in the log.
func (s *Handlers) dialConsoleRelay(ctx context.Context, uid int64, observerKey string, debug bool, ex *mesh.Exchanger, bridge *wsbridge.Conn) (*consoleRelay, error) {
	b, err := s.Store.RelayBroker(ctx, uid, observerKey)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errRelayNotAllowed
	}
	if err != nil {
		return nil, err
	}
	var password string
	if len(b.PasswordSealed) > 0 {
		pt, err := secretbox.Open(s.Cfg.MasterKey, b.PasswordSealed)
		if err != nil {
			return nil, fmt.Errorf("open broker password: %w", err)
		}
		password = string(pt)
	}
	observer, err := meshcore.NewIdentityFromHex(observerKey)
	if err != nil {
		return nil, err
	}
	dialer := ingest.NewDialer()
	if s.relayDialer != nil {
		dialer = *s.relayDialer
	}
	cr := &consoleRelay{}
	r, err := relay.Dial(relay.Config{
		Broker:      ingest.BrokerConfig{OrgID: b.OrgID, URL: b.URL, Username: b.Username, Password: password},
		TopicPrefix: b.TopicPrefix,
		Observer:    observer,
		Signer:      s.Identity.Local(),
		Dialer:      dialer,
	}, relay.Handlers{
		Packet: func(raw []byte) {
			if debug {
				_ = bridge.Status("debug", fmt.Sprintf("heard len=%d data=%x", len(raw), raw))
			}
			ex.HandleData(raw)
		},
		Result: func(res relay.Result) {
			cr.answered.Store(true)
			if !res.OK {
				_ = bridge.Status("warning", relayRefusal(res.Error))
			}
		},
	})
	if err != nil {
		return nil, err
	}
	cr.Relay = r
	return cr, nil
}

// relayRefusal explains an observer's refusal of a frame in plain words.
func relayRefusal(code string) string {
	switch code {
	case relay.ErrCodeNotAdmin:
		return "The observer refused to send the packet because MeshTender isn't an admin on it. Its owner may have removed MeshTender's access."
	case relay.ErrCodeClockUnsynced:
		return "The observer's clock isn't set yet, so it can't check the packet. Try again once it has synced over Wi-Fi."
	case relay.ErrCodeStale:
		return "The observer refused the packet because its clock and MeshTender's differ by more than a minute."
	case relay.ErrCodeBadSignature:
		return "The observer couldn't verify MeshTender's signature. Its firmware may be built for a different MeshTender server."
	case relay.ErrCodeUnsupportedVersion:
		return "The observer's firmware doesn't support this version of relaying. It needs an update."
	case relay.ErrCodeBusy:
		return "The observer was still sending the previous packet, so it skipped this one."
	default:
		return "The observer refused the packet (" + code + ")."
	}
}

// relayLostMessage ends a relay session whose broker connection dropped.
const relayLostMessage = "Lost the connection to the observer's MQTT broker. Connect again to continue."

// relayNoReplyHint is added when nothing came back through the observer at
// all: not a reply, and not even the observer's verdict on a frame.
const relayNoReplyHint = "The observer never answered, so it may not be listening for MeshTender on this broker. Check that relaying is turned on for this broker's slot on the observer (set mqttN.meshtender on) and that the broker lets it subscribe to its meshtender/v1/…/tx topic."
