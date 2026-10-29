package observe

import meshcore "github.com/meshcore-go/meshcore-go"

// Scope is the flood scope a packet was sent with.
type Scope struct {
	// Scoped: sent TRANSPORT_FLOOD with a region code (firmware's
	// sendFloodScoped, after `region default <name>`); otherwise a plain flood.
	Scoped bool
	// Region is the candidate name whose code matched, "" when Scoped but none
	// did (a region we don't know, or a private `$` one).
	Region string
}

// MatchScope works out which of the candidate region names raw was scoped to.
// The transport code is HMAC(SHA256("#"+name)[:16], type ‖ payload) truncated
// to 16 bits, so the name can't be read back — only tested, one candidate at
// a time. A bare name gets the implicit '#' just as the firmware's
// RegionMap::getTransportKeysFor gives it. raw that doesn't parse is unscoped.
func MatchScope(raw []byte, candidates []string) Scope {
	pkt, err := meshcore.PacketFromBytes(raw)
	if err != nil || pkt.RouteType() != meshcore.RouteTypeTransportFlood || pkt.TransportCode1 == 0 {
		return Scope{}
	}
	for _, name := range candidates {
		if meshcore.NewRegionFromHashtag(name).MatchesPacket(pkt) {
			return Scope{Scoped: true, Region: name}
		}
	}
	return Scope{Scoped: true}
}
