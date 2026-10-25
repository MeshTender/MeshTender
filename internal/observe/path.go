package observe

import (
	"encoding/hex"
	"strings"
)

// RepeaterKey is a known repeater a path hash can resolve to.
type RepeaterKey struct {
	PublicID     string
	Name         string
	PublicKeyHex string
}

// Hop is one path entry resolved against known repeaters. Candidates is empty
// for an unknown repeater and has more than one entry when the hash is
// ambiguous — likely with 1-byte hashes (1 in 256 collide), rare with 3.
type Hop struct {
	Hash       string // lowercase hex
	Candidates []RepeaterKey
}

// PathResolver maps path hashes to repeaters by public-key prefix. Build it once
// per page from the org's repeaters, then resolve every path on the page.
type PathResolver struct {
	byPrefix map[string][]RepeaterKey
}

// maxPathHashSize is the largest path entry firmware encodes (path_len's top two
// bits give 1–3; 4 is invalid).
const maxPathHashSize = 3

// NewPathResolver indexes repeaters under each prefix length a path entry can
// have, so a lookup is a single map hit.
func NewPathResolver(repeaters []RepeaterKey) *PathResolver {
	r := &PathResolver{byPrefix: make(map[string][]RepeaterKey, len(repeaters)*maxPathHashSize)}
	for _, rep := range repeaters {
		key := strings.ToLower(rep.PublicKeyHex)
		for size := 1; size <= maxPathHashSize && 2*size <= len(key); size++ {
			p := key[:2*size]
			r.byPrefix[p] = append(r.byPrefix[p], rep)
		}
	}
	return r
}

// Resolve maps each path entry to its candidate repeaters.
func (r *PathResolver) Resolve(path [][]byte) []Hop {
	hops := make([]Hop, len(path))
	for i, h := range path {
		hx := hex.EncodeToString(h)
		hops[i] = Hop{Hash: hx, Candidates: r.byPrefix[hx]}
	}
	return hops
}
