package mesh

import (
	"fmt"
	"strings"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
)

// txtTypeCLIData is the firmware TXT_TYPE_CLI_DATA value (TxtDataHelpers.h). It
// is stored in the text-message flags byte shifted left by 2 (the low 2 bits
// are the attempt counter); the repeater reads it back as `flags = data[4]>>2`.
const txtTypeCLIData = 1

// BuildCommandPacket builds the raw LoRa packet bytes that send a CLI command
// to repeater (flood-routed) as the (admin) server identity. The repeater
// accepts CLI commands as encrypted TXT_MSG packets with plaintext
// [timestamp(4)][flags][command].
func BuildCommandPacket(server meshcore.LocalIdentity, repeater meshcore.Identity, command string, now time.Time) ([]byte, error) {
	return buildCommandPacket(server, repeater, command, now, meshcore.RouteTypeFlood, floodPathLen, nil)
}

// BuildCommandPacketDirect builds a direct-routed CLI command using a path
// learned from a prior login reply (path/pathLen as in LoginResponse.OutPath/
// OutPathLen). Direct packets traverse only the repeaters on the path rather
// than flooding the whole mesh.
func BuildCommandPacketDirect(server meshcore.LocalIdentity, repeater meshcore.Identity, command string, now time.Time, path []byte, pathLen byte) ([]byte, error) {
	return buildCommandPacket(server, repeater, command, now, meshcore.RouteTypeDirect, pathLen, path)
}

func buildCommandPacket(server meshcore.LocalIdentity, repeater meshcore.Identity, command string, now time.Time, routeType, pathLen byte, path []byte) ([]byte, error) {
	shared, err := server.SharedSecret(repeater)
	if err != nil {
		return nil, fmt.Errorf("derive shared secret: %w", err)
	}
	plaintext := meshcore.BuildTextPlaintext(now, txtTypeCLIData<<2, []byte(command))

	tm, err := meshcore.NewTextMessage(server, repeater, plaintext, shared)
	if err != nil {
		return nil, fmt.Errorf("build text message: %w", err)
	}
	payload, err := tm.ToBytes()
	if err != nil {
		return nil, fmt.Errorf("encode text message: %w", err)
	}
	pkt := &meshcore.Packet{
		Header:     meshcore.MakeHeader(routeType, meshcore.PayloadTypeTxtMsg, 0),
		PathLength: pathLen,
		Path:       path,
		Payload:    payload,
	}
	raw, err := pkt.ToBytes()
	if err != nil {
		return nil, fmt.Errorf("encode packet: %w", err)
	}
	return raw, nil
}

// DecodeCommandReply parses a received packet and, if it is a TXT_MSG reply
// addressed to the server whose MAC verifies under the shared secret with
// repeater, returns the command's reply text. The decrypted reply is
// [timestamp(4)][flags(1)][text]. Returns ErrNotForUs for anything that isn't a
// MAC-verified text reply to us, so the caller can keep listening.
func DecodeCommandReply(server meshcore.LocalIdentity, repeater meshcore.Identity, raw []byte) (string, error) {
	plain, payloadType, err := decodeAddressedReply(server, repeater, raw)
	if err != nil {
		return "", err
	}
	if payloadType != meshcore.PayloadTypeTxtMsg {
		return "", ErrNotForUs
	}
	if len(plain) < 5 {
		return "", nil // empty reply (some commands return no text)
	}
	return strings.TrimRight(string(plain[5:]), "\x00"), nil
}
