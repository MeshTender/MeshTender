package ingest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

// Org admins choose the broker address, and the server dials it. Without a
// guard that is a server-side request forgery primitive: point it at the
// cluster's internal services or loopback and MeshTender connects there. So
// every dial resolves the host itself, refuses any non-public address, and
// connects to the checked IP literal — never re-resolving the name, which would
// let a DNS answer change between the check and the dial (rebinding).

// ErrBlockedAddress is returned when the broker resolves to an address that
// isn't publicly routable.
var ErrBlockedAddress = errors.New("broker address is not public")

// Resolver is the subset of *net.Resolver the guard needs; tests fake it.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// AddrPolicy decides whether an address may be dialed.
type AddrPolicy func(netip.Addr) bool

// nonPublic lists ranges netip's predicates don't cover that must not be dialed:
// shared CGNAT space, the "this network" block, IETF protocol assignments,
// benchmarking, and the IPv6 transition prefixes that embed an IPv4 address
// (NAT64, 6to4, Teredo) — each could smuggle a private IPv4 target.
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2001::/32"),
}

// PublicAddr reports whether addr is publicly routable: not loopback, private,
// link-local, multicast, unspecified, or one of the special ranges above.
func PublicAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsLoopback() || addr.IsPrivate() || addr.IsUnspecified() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() ||
		addr.IsMulticast() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}

// Broker URL schemes. Observers usually reach a broker over MQTT-over-WebSocket
// behind an HTTPS endpoint (wss), which is also the only way through most
// reverse proxies; raw MQTT (tcp/tls) is for brokers exposed directly.
var defaultPorts = map[string]string{"tcp": "1883", "tls": "8883", "ws": "80", "wss": "443"}

// defaultWebSocketPath is where Mosquitto, VerneMQ and EMQX serve MQTT over
// WebSocket unless configured otherwise.
const defaultWebSocketPath = "/mqtt"

// ParseBrokerURL validates an org-entered broker URL: tcp://, tls://, ws:// or
// wss://, a host, an optional port, and for WebSocket an optional path.
// Credentials go in their own fields so the password can be sealed; queries and
// fragments have no meaning to MQTT.
func ParseBrokerURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("not a URL: %w", err)
	}
	if _, ok := defaultPorts[u.Scheme]; !ok {
		return nil, errors.New("must start with wss://, ws://, tls:// or tcp://")
	}
	if u.Hostname() == "" {
		return nil, errors.New("missing host")
	}
	websocket := u.Scheme == "ws" || u.Scheme == "wss"
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (!websocket && u.Path != "" && u.Path != "/") {
		return nil, errors.New("only scheme, host, port and (for WebSocket) a path are allowed")
	}
	return u, nil
}

// Target is a vetted dial target: the checked address to connect to, and the
// name the org entered, which TLS verifies the certificate against and the
// WebSocket handshake sends as Host.
type Target struct {
	Scheme     string
	Addr       string // checked IP and port
	ServerName string // hostname as entered
	Port       string
	Path       string // WebSocket path; empty for tcp/tls
}

// ResolveTarget resolves the broker URL's host and returns a Target for its
// first address, provided EVERY address the name resolves to passes allow. A
// name with a mix of public and private answers is refused rather than having
// one picked, so it can't be used to straddle the check.
func ResolveTarget(ctx context.Context, raw string, r Resolver, allow AddrPolicy) (Target, error) {
	u, err := ParseBrokerURL(raw)
	if err != nil {
		return Target{}, err
	}
	host := u.Hostname()
	var addrs []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{ip}
	} else {
		addrs, err = r.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return Target{}, &DNSError{Err: err}
		}
		if len(addrs) == 0 {
			return Target{}, &DNSError{Err: errors.New("no addresses")}
		}
	}
	for _, a := range addrs {
		if !allow(a) {
			return Target{}, ErrBlockedAddress
		}
	}
	port := u.Port()
	if port == "" {
		port = defaultPorts[u.Scheme]
	}
	t := Target{Scheme: u.Scheme, Addr: net.JoinHostPort(addrs[0].Unmap().String(), port), ServerName: host, Port: port}
	if u.Scheme == "ws" || u.Scheme == "wss" {
		t.Path = u.Path
		if t.Path == "" || t.Path == "/" {
			t.Path = defaultWebSocketPath
		}
	}
	return t, nil
}

// DNSError wraps a failed broker lookup so it can be categorized.
type DNSError struct{ Err error }

func (e *DNSError) Error() string { return "resolve broker: " + e.Err.Error() }
func (e *DNSError) Unwrap() error { return e.Err }
