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
	return Advert{Type: "CHAT", Name: name, Route: route, HashSize: hashSize, Path: path}.Packet(t, id, ts)
}

// Advert describes an advert packet to build. Lat/Lon are degrees (zero for
// none); Scope, when set, sends it TRANSPORT_FLOOD scoped to that region name
// the way the firmware's sendFloodScoped does (Route is then ignored).
type Advert struct {
	Type     string // meshcore.AdvertAppData type: "CHAT", "REPEATER", …
	Name     string
	Lat, Lon float64
	Route    byte
	HashSize int
	Path     [][]byte
	Scope    string
}

// Packet signs a as id at sender time ts and returns the raw packet.
func (a Advert) Packet(t testing.TB, id meshcore.LocalIdentity, ts uint32) []byte {
	t.Helper()
	app, err := (&meshcore.AdvertAppData{Type: a.Type, Name: a.Name,
		Lat: int32(a.Lat * 1e6), Lon: int32(a.Lon * 1e6)}).ToBytes()
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
	for _, h := range a.Path {
		flat = append(flat, h...)
	}
	hashSize := max(a.HashSize, 1)
	pkt := &meshcore.Packet{
		Header:     meshcore.MakeHeader(a.Route, meshcore.PayloadTypeAdvert, 0),
		PathLength: byte(hashSize-1)<<6 | byte(len(a.Path)), //nolint:gosec // test sizes are tiny
		Path:       flat,
		Payload:    payload,
	}
	if a.Scope != "" {
		pkt.Header = meshcore.MakeHeader(meshcore.RouteTypeTransportFlood, meshcore.PayloadTypeAdvert, 0)
		pkt.TransportCode1 = meshcore.NewRegionFromHashtag(a.Scope).CalcTransportCode(pkt)
	}
	raw, err := pkt.ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
