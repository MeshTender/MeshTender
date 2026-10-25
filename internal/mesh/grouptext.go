package mesh

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	meshcore "github.com/meshcore-go/meshcore-go"
)

// Channel messages (GRP_TXT) are what a MeshCore companion shows in a channel:
// [channel hash][2-byte MAC][AES-128-ECB(timestamp LE | flags | "Sender: text")],
// flood-routed, never acknowledged. Anyone with the channel key can send one
// under any sender name — the name is unauthenticated text.
// (firmware: BaseChatMesh::sendGroupMessage, Mesh::createGroupDatagram.)

// MaxGroupTextLen is the most a channel message can carry, counting the
// "Sender: " prefix: the firmware's MAX_TEXT_LEN (BaseChatMesh.h), which it
// truncates to on send.
const MaxGroupTextLen = 160

// publicChannelPSK is the well-known key of the Public channel every companion
// ships with.
const publicChannelPSK = "izOH6cXN6mrJ5e26oRXNcg=="

// Channel is a group channel's key and the 1-byte hash a packet names it by.
type Channel struct {
	PSK  [16]byte
	Hash byte
}

// PublicChannel is the default Public channel.
func PublicChannel() Channel {
	psk, err := base64.StdEncoding.DecodeString(publicChannelPSK)
	if err != nil {
		panic(err) // a constant
	}
	var k [16]byte
	copy(k[:], psk)
	return PrivateChannel(k)
}

// HashtagChannel is a public "#name" channel, keyed by its name.
func HashtagChannel(name string) Channel {
	e := meshcore.NewChannelFromHashtag(name)
	return Channel{PSK: e.PSK, Hash: e.Hash}
}

// PrivateChannel is a channel with a shared secret key.
func PrivateChannel(psk [16]byte) Channel {
	return Channel{PSK: psk, Hash: meshcore.DeriveChannelHash(psk)}
}

// ErrPrivateRegion is returned when asked to scope to a "$" region: those
// have secret keys a repeater loads separately, which we don't hold.
var ErrPrivateRegion = errors.New("mesh: can't scope to a private ($) region")

// RegionScope is the transport key for a region token as used in `region def`.
// The firmware treats a bare token as the public hashtag region "#token"
// (RegionMap::getTransportKeysFor) and keys it SHA256("#token")[:16].
func RegionScope(token string) (meshcore.RegionKey, error) {
	if strings.HasPrefix(token, "$") {
		return meshcore.RegionKey{}, ErrPrivateRegion
	}
	return meshcore.NewRegionFromHashtag(token).Key, nil
}

// GroupTextRoom is how many bytes of text fit in one message from sender.
func GroupTextRoom(sender string) int {
	return MaxGroupTextLen - len(sender) - len(": ")
}

// FitGroupText trims text to fit one message from sender, cutting on a
// character boundary and marking the cut with an ellipsis.
func FitGroupText(sender, text string) string {
	room := GroupTextRoom(sender)
	if len(text) <= room {
		return text
	}
	const more = "…"
	return truncateUTF8(text, room-len(more)) + more
}

// truncateUTF8 returns the longest prefix of s of at most n bytes that doesn't
// split a character.
func truncateUTF8(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// BuildGroupText encodes a channel message from sender, as a companion sends
// it: flags 0 (plain text), text trimmed to fit (FitGroupText), flooded with
// our usual 3-byte path hashes. A non-nil scope sends it as a transport flood
// scoped to that region — transport code 0 is the region's code for this
// packet, code 1 is 0, exactly as a companion's sendFloodScoped does — so
// repeaters that deny unscoped floods still repeat it.
func BuildGroupText(ch Channel, sender, text string, scope *meshcore.RegionKey, now time.Time) ([]byte, error) {
	msg := meshcore.GroupTextPayload{
		Timestamp: uint32(now.Unix()), //nolint:gosec // unix seconds fit until 2106, as the firmware's
		Sender:    sender,
		Text:      FitGroupText(sender, text),
	}
	gt, err := msg.Encrypt(ch.Hash, ch.PSK[:])
	if err != nil {
		return nil, fmt.Errorf("encrypt group text: %w", err)
	}
	payload, err := gt.ToBytes()
	if err != nil {
		return nil, fmt.Errorf("encode group text: %w", err)
	}
	pkt := &meshcore.Packet{
		Header:     meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeGrpTxt, 0),
		PathLength: floodPathLen,
		Payload:    payload,
	}
	if scope != nil {
		pkt.Header = meshcore.MakeHeader(meshcore.RouteTypeTransportFlood, meshcore.PayloadTypeGrpTxt, 0)
		pkt.TransportCode1 = scope.CalcTransportCode(meshcore.PayloadTypeGrpTxt, payload)
	}
	raw, err := pkt.ToBytes()
	if err != nil {
		return nil, fmt.Errorf("encode packet: %w", err)
	}
	return raw, nil
}

// GroupText is a channel message as heard. Sender is whatever name the sender
// typed — unauthenticated, anyone with the key can claim any name.
type GroupText struct {
	Sender    string
	Text      string
	Timestamp uint32
}

// DecodeGroupText reads a received packet as a plain-text message on ch. ok is
// false for anything else: another packet type or channel, a MAC that doesn't
// verify under ch's key, or a non-text flags type (the firmware drops
// flags>>2 != 0, BaseChatMesh::onGroupDataRecv). The text is capped at
// MaxGroupTextLen like the firmware's own buffer.
func DecodeGroupText(raw []byte, ch Channel) (GroupText, bool) {
	pkt, err := meshcore.PacketFromBytes(raw)
	if err != nil || pkt.PayloadType() != meshcore.PayloadTypeGrpTxt {
		return GroupText{}, false
	}
	gt, err := meshcore.GroupTextFromBytes(pkt.Payload)
	if err != nil || gt.ChannelHash != ch.Hash {
		return GroupText{}, false
	}
	p, err := gt.DecryptStruct(ch.PSK[:])
	if err != nil || p.Flags>>2 != 0 {
		return GroupText{}, false
	}
	return GroupText{
		Sender:    p.Sender,
		Text:      truncateUTF8(p.Text, MaxGroupTextLen),
		Timestamp: p.Timestamp,
	}, true
}
