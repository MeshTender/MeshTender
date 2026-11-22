package mesh

import (
	"testing"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
)

func TestClassifyHeard(t *testing.T) {
	t.Parallel()
	server := mustIdentity(t)
	repeater := mustIdentity(t)
	other := mustIdentity(t)
	now := time.Unix(1_800_000_000, 0)

	login, err := BuildLoginPacket(server, repeater.Identity, "", now)
	if err != nil {
		t.Fatal(err)
	}
	command, err := BuildCommandPacket(server, repeater.Identity, "get name", now)
	if err != nil {
		t.Fatal(err)
	}
	otherLogin, err := BuildLoginPacket(other, repeater.Identity, "", now)
	if err != nil {
		t.Fatal(err)
	}
	// The repeater's reply to the server, and the same reply to someone else.
	reply := func(to meshcore.LocalIdentity) []byte {
		shared, _ := repeater.SharedSecret(to.Identity)
		plain := meshcore.BuildTextPlaintext(now, 1<<2, []byte("> ok"))
		tm, _ := meshcore.NewTextMessage(repeater, to.Identity, plain, shared)
		payload, _ := tm.ToBytes()
		raw, _ := (&meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeTxtMsg, 0), Payload: payload}).ToBytes()
		return raw
	}

	cases := []struct {
		name string
		raw  []byte
		want Heard
	}{
		{"our login", login, HeardOwn},
		{"our command", command, HeardOwn},
		{"someone else's login", otherLogin, 0},
		{"a reply to us", reply(server), HeardReply},
		{"garbage", []byte{0xff}, 0},
		{"empty", nil, 0},
	}
	for _, c := range cases {
		if got := ClassifyHeard(c.raw, server.Identity); got != c.want {
			t.Errorf("%s: %b, want %b", c.name, got, c.want)
		}
	}
	// A reply to another node is only ours if its one-byte hash collides.
	toOther := ClassifyHeard(reply(other), server.Identity)
	if collides := other.Hash()[0] == server.Hash()[0]; (toOther&HeardReply != 0) != collides {
		t.Errorf("reply to another node: %b (hashes collide: %v)", toOther, collides)
	}
}

func TestPacketKeyIgnoresThePath(t *testing.T) {
	t.Parallel()
	server := mustIdentity(t)
	repeater := mustIdentity(t)
	raw, err := BuildCommandPacket(server, repeater.Identity, "get name", time.Unix(1_800_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	pkt, err := meshcore.PacketFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	// The same flood after two more hops.
	pkt.PathLength, pkt.Path = floodPathLen+2, []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
	repeated, err := pkt.ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	a, okA := PacketKey(raw)
	b, okB := PacketKey(repeated)
	if !okA || !okB || a != b {
		t.Fatalf("keys %x (%v) and %x (%v) should match", a, okA, b, okB)
	}
	other, err := BuildCommandPacket(server, repeater.Identity, "get name", time.Unix(1_800_000_001, 0))
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := PacketKey(other); c == a {
		t.Fatal("a different command has the same key")
	}
	if _, ok := PacketKey([]byte{0xff}); ok {
		t.Fatal("garbage has a key")
	}
}
