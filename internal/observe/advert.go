package observe

import (
	"errors"
	"fmt"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"
)

// ErrNotAdvert is returned by DecodeAdvert for any other packet type — most
// traffic. The caller skips it.
var ErrNotAdvert = errors.New("observe: not an advert")

// Advert is an advert packet as one observer heard it. It is parsed but NOT yet
// verified: the ingester first checks the key is one it cares about, then calls
// Verify, so the ed25519 check only runs for registered companions.
type Advert struct {
	PublicKey meshcore.Identity
	// Timestamp is the sender's clock when it emitted the advert (unix secs).
	Timestamp uint32
	// NodeType is the advert's ADV_TYPE_* (meshcore.AdvertTypeChat for a companion).
	NodeType byte
	Name     string

	// RouteType is the packet's meshcore.RouteType* as received.
	RouteType byte
	// PathHashSize is the bytes per path entry (1–3); Path holds one entry per
	// repeater that forwarded the flood, oldest first.
	PathHashSize uint8
	Path         [][]byte

	advert *meshcore.Advert
}

// DecodeAdvert parses raw as a MeshCore packet carrying an advert.
func DecodeAdvert(raw []byte) (*Advert, error) {
	pkt, err := meshcore.PacketFromBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("observe: packet: %w", err)
	}
	if pkt.PayloadType() != meshcore.PayloadTypeAdvert {
		return nil, ErrNotAdvert
	}
	adv, err := meshcore.AdvertFromBytes(pkt.Payload)
	if err != nil {
		return nil, fmt.Errorf("observe: advert: %w", err)
	}
	app := adv.AppData()
	return &Advert{
		PublicKey:    adv.PublicKey,
		Timestamp:    adv.Timestamp,
		NodeType:     adv.Type(),
		Name:         app.Name,
		RouteType:    pkt.RouteType(),
		PathHashSize: pkt.PathHashSize(),
		Path:         pkt.PathHashes(),
		advert:       adv,
	}, nil
}

// Verify checks the advert's ed25519 signature over pubkey ‖ timestamp ‖ app
// data — the same message the firmware signs in Mesh::createAdvert. A valid
// signature proves the holder of PublicKey emitted this advert at Timestamp;
// it says nothing about WHEN it was heard (see Skew).
func (a *Advert) Verify() bool {
	return a.advert.Verify()
}

// ZeroHop reports whether the advert was sent zero-hop: firmware sends those
// as a direct packet with an empty path, so repeaters never forward them.
func (a *Advert) ZeroHop() bool {
	return (a.RouteType == meshcore.RouteTypeDirect || a.RouteType == meshcore.RouteTypeTransportDirect) &&
		len(a.Path) == 0
}

// Skew is how far the advert's own timestamp is from when it was heard:
// positive when the sender's clock is behind. A captured advert replayed later
// still verifies, so the ingester bounds this to reject replays.
func (a *Advert) Skew(heardAt time.Time) time.Duration {
	return heardAt.Sub(time.Unix(int64(a.Timestamp), 0))
}
