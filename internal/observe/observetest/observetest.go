// Package observetest builds synthetic MeshCore advert packets for tests, so
// no real member's key or location ends up in a fixture.
package observetest

import (
	"crypto/rand"
	"testing"

	meshcore "github.com/meshcore-go/meshcore-go"
)

// NewIdentity generates a fresh MeshCore identity.
func NewIdentity(t testing.TB) meshcore.LocalIdentity {
	t.Helper()
	id, err := meshcore.GenerateLocalIdentity(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// AdvertPacket returns the raw bytes of a companion advert as firmware puts it
// on air: signed by id at sender time ts, routed as route with path (hashSize
// bytes per entry).
func AdvertPacket(t testing.TB, id meshcore.LocalIdentity, ts uint32, name string, route byte, hashSize int, path [][]byte) []byte {
	t.Helper()
	app, err := (&meshcore.AdvertAppData{Type: "CHAT", Name: name}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	adv := &meshcore.Advert{PublicKey: id.Identity, Timestamp: ts, RawAppData: app}
	adv.SignWith(id)
	payload, err := adv.ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	var flat []byte
	for _, h := range path {
		flat = append(flat, h...)
	}
	pkt := &meshcore.Packet{
		Header:     meshcore.MakeHeader(route, meshcore.PayloadTypeAdvert, 0),
		PathLength: byte(hashSize-1)<<6 | byte(len(path)), //nolint:gosec // test sizes are tiny
		Path:       flat,
		Payload:    payload,
	}
	raw, err := pkt.ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
