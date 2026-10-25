package ingest

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/coder/websocket"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// dialTimeout bounds the network dial plus TLS and WebSocket handshakes, and
// separately the MQTT CONNECT/CONNACK exchange.
const dialTimeout = 10 * time.Second

// keepAlive is the MQTT keep-alive: Paho pings when the link is idle this long
// and treats a missing PINGRESP as a dead connection.
const keepAlive = 30 * time.Second

// Dialer opens broker connections. Every transport — raw MQTT over TCP or TLS,
// or MQTT over WebSocket (ws/wss) — goes through Open, which resolves the host
// itself, refuses a non-public address, and connects to the checked IP; Paho
// only ever runs MQTT over the connection Open returns. Paho's own WebSocket
// dialer would resolve the name again, which is the DNS-rebinding gap the
// guard exists to close.
type Dialer struct {
	Resolver Resolver
	Allow    AddrPolicy
	// RootCAs verifies TLS (tls and wss) certificates; nil means the system
	// roots. A field only so tests can trust their own CA.
	RootCAs *x509.CertPool
}

// NewDialer returns the production dialer: system DNS, public addresses only,
// system TLS roots.
func NewDialer() Dialer {
	return Dialer{Resolver: net.DefaultResolver, Allow: PublicAddr}
}

// Open connects to the broker URL's vetted address and completes the
// transport handshakes (TLS, WebSocket upgrade), returning a stream ready for
// MQTT.
func (d Dialer) Open(ctx context.Context, rawURL string) (net.Conn, error) {
	t, err := ResolveTarget(ctx, rawURL, d.Resolver, d.Allow)
	if err != nil {
		return nil, err
	}
	tlsConf := &tls.Config{ServerName: t.ServerName, RootCAs: d.RootCAs, MinVersion: tls.VersionTLS12}
	dialAddr := func(ctx context.Context) (net.Conn, error) {
		var nd net.Dialer
		return nd.DialContext(ctx, "tcp", t.Addr)
	}
	switch t.Scheme {
	case "tcp":
		return dialAddr(ctx)
	case "tls":
		conn, err := dialAddr(ctx)
		if err != nil {
			return nil, err
		}
		tc := tls.Client(conn, tlsConf)
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return tc, nil
	default: // ws, wss
		// The handshake URL keeps the entered hostname (Host header, SNI and
		// certificate check); the transport pins the TCP connection to the
		// vetted address and never consults a proxy that could route around it.
		u := url.URL{Scheme: t.Scheme, Host: net.JoinHostPort(t.ServerName, t.Port), Path: t.Path}
		client := &http.Client{Transport: &http.Transport{
			Proxy:           nil,
			DialContext:     func(ctx context.Context, _, _ string) (net.Conn, error) { return dialAddr(ctx) },
			TLSClientConfig: tlsConf,
		}}
		c, resp, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{
			HTTPClient:   client,
			Subprotocols: []string{"mqtt"},
		})
		if err != nil {
			if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
				return nil, &HandshakeError{Status: resp.StatusCode, Err: err}
			}
			return nil, err
		}
		// MQTT over WebSocket is carried in binary frames. The conn lives until
		// Paho closes it, so it isn't bound to the dial's context.
		return websocket.NetConn(context.Background(), c, websocket.MessageBinary), nil
	}
}

// Connect opens a connection with Open and completes the MQTT handshake. The
// client never reconnects on its own: the supervisor redials, so every
// reconnect goes back through the address guard. onLost (may be nil) is called
// once if an established connection drops.
func (d Dialer) Connect(b BrokerConfig, onLost func(error)) (mqtt.Client, error) {
	opts := mqtt.NewClientOptions().
		AddBroker(b.URL).
		SetClientID(clientID(b.OrgID)).
		SetUsername(b.Username).
		SetPassword(b.Password).
		SetProtocolVersion(4). // MQTT 3.1.1, explicit so Paho doesn't retry as 3.1
		SetCleanSession(true).
		SetKeepAlive(keepAlive).
		SetConnectTimeout(dialTimeout).
		SetAutoReconnect(false).
		SetConnectRetry(false).
		SetCustomOpenConnectionFn(func(*url.URL, mqtt.ClientOptions) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
			defer cancel()
			return d.Open(ctx, b.URL)
		})
	if onLost != nil {
		opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) { onLost(err) })
	}
	client := mqtt.NewClient(opts)
	tok := client.Connect()
	if !tok.WaitTimeout(2 * dialTimeout) {
		client.Disconnect(0)
		return nil, fmt.Errorf("connect: %w", context.DeadlineExceeded)
	}
	if err := tok.Error(); err != nil {
		return nil, err
	}
	return client, nil
}

// HandshakeError is a WebSocket upgrade the endpoint answered with something
// other than 101: a wrong path, or an HTTP server with no MQTT behind it.
type HandshakeError struct {
	Status int
	Err    error
}

func (e *HandshakeError) Error() string {
	return fmt.Sprintf("websocket handshake: HTTP %d: %v", e.Status, e.Err)
}
func (e *HandshakeError) Unwrap() error { return e.Err }

// ErrSubscribeRejected is returned when the broker refuses a subscription
// (SUBACK return code 0x80), typically an ACL on the topic prefix.
var ErrSubscribeRejected = errors.New("broker rejected the subscription")

// Subscribe subscribes to every filter at QoS 0 in one SUBSCRIBE and checks
// each grant: an MQTT 3.1.1 broker refuses a filter in the SUBACK rather than
// failing the request.
func Subscribe(client mqtt.Client, filters []string, handler mqtt.MessageHandler) error {
	want := make(map[string]byte, len(filters))
	for _, f := range filters {
		want[f] = 0
	}
	tok := client.SubscribeMultiple(want, handler)
	if !tok.WaitTimeout(dialTimeout) {
		return fmt.Errorf("subscribe: %w", context.DeadlineExceeded)
	}
	if err := tok.Error(); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	st, ok := tok.(*mqtt.SubscribeToken)
	if !ok {
		return nil
	}
	for f, code := range st.Result() {
		if code >= 0x80 {
			return fmt.Errorf("%w: %s", ErrSubscribeRejected, f)
		}
	}
	return nil
}

// BrokerConfig is what Connect needs: the stored broker settings with the
// password already unsealed.
type BrokerConfig struct {
	OrgID    int64
	URL      string
	Username string
	Password string
}

// clientID is unique per connection so MeshTender never takes over another
// client's session (MQTT disconnects the older holder of a client id).
func clientID(orgID int64) string {
	host, _ := os.Hostname()
	return fmt.Sprintf("meshtender-%d-%s-%08x", orgID, host, rand.Uint32()) //nolint:gosec // not a secret
}
