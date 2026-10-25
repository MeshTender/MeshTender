// Package observe decodes what MeshCore observers publish to an MQTT broker:
// the topic an uplink posts to, the JSON envelope around each received packet
// or status report, and the advert inside a packet. It does no I/O; the
// ingester feeds it bytes and decides what to store.
//
// Observers publish to <prefix>/<IATA>/<observer pubkey hex>/{packets,status}.
// Uplink programs disagree on most envelope fields, so only the raw packet hex
// and the radio's SNR/RSSI are trusted from the payload; everything else
// (route, packet type, timestamps) is re-derived from the raw bytes and the
// receiver's own clock.
package observe

import (
	"errors"
	"fmt"
	"strings"

	meshcore "github.com/meshcore-go/meshcore-go"
)

// Kind is the last topic segment: what the message carries.
type Kind string

const (
	KindPackets Kind = "packets"
	KindStatus  Kind = "status"
)

// Topic is a parsed observer topic.
type Topic struct {
	IATA string
	// Observer is the uplinking node. On a broker that authenticates publishers
	// by key (ours checks a JWT signed by this key and only lets it post under
	// its own pubkey), this is the authenticated sender; never trust the
	// payload's origin_id instead.
	Observer meshcore.Identity
	Kind     Kind
}

// ErrForeignTopic is returned for a topic that isn't an observer packets or
// status topic under prefix. Brokers carry other traffic; the caller skips it.
var ErrForeignTopic = errors.New("observe: not an observer topic")

// ParseTopic parses <prefix>/<IATA>/<pubkey>/<kind>.
func ParseTopic(prefix, topic string) (Topic, error) {
	parts := strings.Split(topic, "/")
	if len(parts) != 4 || parts[0] != prefix {
		return Topic{}, ErrForeignTopic
	}
	kind := Kind(parts[3])
	if kind != KindPackets && kind != KindStatus {
		return Topic{}, ErrForeignTopic
	}
	if parts[1] == "" {
		return Topic{}, fmt.Errorf("observe: empty IATA in %q", topic)
	}
	id, err := meshcore.NewIdentityFromHex(parts[2])
	if err != nil {
		return Topic{}, fmt.Errorf("observe: observer key in %q: %w", topic, err)
	}
	return Topic{IATA: parts[1], Observer: id, Kind: kind}, nil
}
