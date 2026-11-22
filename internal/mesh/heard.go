package mesh

import (
	"bytes"

	meshcore "github.com/meshcore-go/meshcore-go"
)

// Heard is what a packet some observer heard is to MeshTender. Observers
// upload everything they hear, so a reply to MeshTender, or MeshTender's own
// request being repeated, can be heard by any of them, not only by the device
// that sent the request.
type Heard uint8

const (
	// HeardReply: a packet that may be addressed to MeshTender — a RESPONSE,
	// TXT_MSG or PATH whose destination hash is the server's. Only the
	// exchanger that can decrypt it knows whether it really is one: the hash
	// is a single byte, so about 1 in 256 of other nodes' packets match too.
	HeardReply Heard = 1 << iota
	// HeardOwn: a packet that may be one MeshTender sent — an ANON_REQ
	// carrying the server's public key (a login), or a REQ or TXT_MSG whose
	// source hash is the server's. A session confirms it by its PacketHash.
	HeardOwn
)

// ClassifyHeard reports what raw might be to server; 0 when it's neither, or
// doesn't parse. The destination and source hashes are a payload's first two
// bytes (PATH_HASH_SIZE is 1); an ANON_REQ has the sender's key after its
// destination hash instead.
func ClassifyHeard(raw []byte, server meshcore.Identity) Heard {
	pkt, err := meshcore.PacketFromBytes(raw)
	if err != nil {
		return 0
	}
	p := pkt.Payload
	hash := server.Hash()[0]
	var h Heard
	switch pkt.PayloadType() {
	case meshcore.PayloadTypeResponse, meshcore.PayloadTypePath:
		if len(p) >= 2 && p[0] == hash {
			h |= HeardReply
		}
	case meshcore.PayloadTypeTxtMsg:
		if len(p) >= 2 && p[0] == hash {
			h |= HeardReply
		}
		if len(p) >= 2 && p[1] == hash {
			h |= HeardOwn
		}
	case meshcore.PayloadTypeReq:
		if len(p) >= 2 && p[1] == hash {
			h |= HeardOwn
		}
	case meshcore.PayloadTypeAnonReq:
		if len(p) >= 1+meshcore.PubKeySize && bytes.Equal(p[1:1+meshcore.PubKeySize], server.PublicKeyBytes()) {
			h |= HeardOwn
		}
	}
	return h
}

// PacketKey is a packet's identity across every copy of it heard: the
// firmware's dedup fingerprint, which covers the payload type and payload but
// not the path, so a flood heard after more hops, or by another observer,
// keys the same. ok is false if raw doesn't parse.
func PacketKey(raw []byte) (key [meshcore.PacketHashSize]byte, ok bool) {
	pkt, err := meshcore.PacketFromBytes(raw)
	if err != nil {
		return key, false
	}
	return pkt.PacketHash(), true
}
