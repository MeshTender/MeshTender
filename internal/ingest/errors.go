package ingest

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"syscall"

	"github.com/eclipse/paho.mqtt.golang/packets"
)

// Error categories stored in org_mqtt_brokers.last_error and shown to the org.
// Deliberately coarse: a raw dial error ("connection refused" vs "i/o timeout"
// per port) would turn the broker form into a port scanner for whoever can
// edit it.
const (
	CategoryDNS       = "dns"
	CategoryBlocked   = "blocked"
	CategoryRefused   = "refused"
	CategoryTimeout   = "timeout"
	CategoryTLS       = "tls"
	CategoryAuth      = "auth"
	CategoryRejected  = "rejected"
	CategorySubscribe = "subscribe"
	CategoryConfig    = "config"
	CategoryConnect   = "connect"
)

// Category maps a connect/subscribe error to its display category.
func Category(err error) string {
	var dnsErr *DNSError
	var wsErr *HandshakeError
	var certErr *tls.CertificateVerificationError
	var unknownAuth x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var recErr tls.RecordHeaderError
	var netErr net.Error
	switch {
	case err == nil:
		return ""
	case errors.As(err, &dnsErr):
		return CategoryDNS
	case errors.Is(err, ErrBlockedAddress):
		return CategoryBlocked
	case errors.Is(err, packets.ErrorRefusedBadUsernameOrPassword), errors.Is(err, packets.ErrorRefusedNotAuthorised):
		return CategoryAuth
	case errors.Is(err, packets.ErrorRefusedBadProtocolVersion), errors.Is(err, packets.ErrorRefusedIDRejected),
		errors.Is(err, packets.ErrorRefusedServerUnavailable), errors.As(err, &wsErr):
		return CategoryRejected
	case errors.Is(err, ErrSubscribeRejected):
		return CategorySubscribe
	case errors.As(err, &certErr), errors.As(err, &unknownAuth), errors.As(err, &hostErr), errors.As(err, &recErr):
		return CategoryTLS
	case errors.Is(err, syscall.ECONNREFUSED):
		return CategoryRefused
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return CategoryTimeout
	default:
		return CategoryConnect
	}
}

// categoryMessages explain each category to the org admin who has to fix it.
var categoryMessages = map[string]string{
	CategoryDNS:       "Couldn't resolve the broker's hostname.",
	CategoryBlocked:   "That address isn't on the public internet. MeshTender only connects to public brokers.",
	CategoryRefused:   "The broker refused the connection. Check the port.",
	CategoryTimeout:   "Timed out connecting to the broker.",
	CategoryTLS:       "The TLS handshake failed. Check that the broker's certificate matches its hostname.",
	CategoryAuth:      "The broker rejected the username or password.",
	CategoryRejected:  "The broker rejected the connection.",
	CategorySubscribe: "Connected, but the broker refused the subscription. Check the account's read access to the topic prefix.",
	CategoryConfig:    "The stored password can't be read. Enter it again.",
	CategoryConnect:   "Couldn't connect to the broker.",
}

// CategoryMessage returns the user-facing explanation for a category, or ""
// for none.
func CategoryMessage(category string) string {
	if category == "" {
		return ""
	}
	if m, ok := categoryMessages[category]; ok {
		return m
	}
	return categoryMessages[CategoryConnect]
}
