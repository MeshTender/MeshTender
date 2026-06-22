package mesh

import (
	"encoding/binary"
	"testing"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
)

func TestBuildCommandPacketDecodableByRepeater(t *testing.T) {
	t.Parallel()
	server := mustIdentity(t)
	repeater := mustIdentity(t)
	now := time.Unix(1_700_001_000, 0)

	raw, err := BuildCommandPacket(server, repeater.Identity, "set tx 20", now)
	if err != nil {
		t.Fatalf("BuildCommandPacket: %v", err)
	}

	pkt, err := meshcore.PacketFromBytes(raw)
	if err != nil {
		t.Fatalf("PacketFromBytes: %v", err)
	}
	if pkt.PayloadType() != meshcore.PayloadTypeTxtMsg {
		t.Fatalf("payload type = %d, want TxtMsg", pkt.PayloadType())
	}
	tm, err := meshcore.TextMessageFromBytes(pkt.Payload)
	if err != nil {
		t.Fatalf("TextMessageFromBytes: %v", err)
	}
	if tm.Destination != repeater.Identity.Hash()[0] {
		t.Errorf("dest = 0x%02x, want 0x%02x", tm.Destination, repeater.Identity.Hash()[0])
	}
	shared, err := repeater.SharedSecret(server.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if !tm.VerifyMAC(shared) {
		t.Fatal("repeater could not verify command MAC")
	}
	plain := tm.Decrypt(shared)
	if plain == nil {
		t.Fatal("decrypt returned nil")
	}
	if got := binary.LittleEndian.Uint32(plain[:4]); got != uint32(now.Unix()) {
		t.Errorf("timestamp = %d, want %d", got, now.Unix())
	}
	if plain[4] != txtTypeCLIData<<2 {
		t.Errorf("flags = 0x%02x, want 0x%02x", plain[4], txtTypeCLIData<<2)
	}
	if got := string(plain[5:14]); got != "set tx 20" {
		t.Errorf("command = %q, want %q", got, "set tx 20")
	}
}

func TestDecodeCommandReply(t *testing.T) {
	t.Parallel()
	server := mustIdentity(t)
	repeater := mustIdentity(t)
	now := time.Unix(1_700_001_500, 0)

	// Simulate the repeater's CLI reply: TXT_MSG, plaintext [ts][flags][text].
	shared, err := repeater.SharedSecret(server.Identity)
	if err != nil {
		t.Fatal(err)
	}
	plaintext := meshcore.BuildTextPlaintext(now, txtTypeCLIData<<2, []byte("> tx power: 20 dBm"))
	tm, err := meshcore.NewTextMessage(repeater, server.Identity, plaintext, shared)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := tm.ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	pkt := &meshcore.Packet{
		Header:  meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeTxtMsg, 0),
		Payload: payload,
	}
	raw, err := pkt.ToBytes()
	if err != nil {
		t.Fatal(err)
	}

	got, err := DecodeCommandReply(server, repeater.Identity, raw)
	if err != nil {
		t.Fatalf("DecodeCommandReply: %v", err)
	}
	if got != "> tx power: 20 dBm" {
		t.Errorf("reply = %q, want %q", got, "> tx power: 20 dBm")
	}
}
