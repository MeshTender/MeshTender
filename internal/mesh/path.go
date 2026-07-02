package mesh

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// maxPathHops is the largest hop count a MeshCore path_len byte can describe: the
// count is the low 6 bits (0x3f).
const maxPathHops = 63

// ParsePath parses a user-entered repeater path into the raw path bytes and the
// MeshCore path_len descriptor byte. The input is comma-separated hex hops, each
// the same byte length (1, 2, or 3 bytes), e.g. "11, 22, 33" or "1111, 2222".
// Hops are listed in path order (from the modem outward to the repeater).
//
// Empty/whitespace input returns (nil, 0, nil) — "no path", not an error. The
// descriptor is (size-1)<<6 | count, matching floodPathLen and unwrapPathReturn.
func ParsePath(s string) (path []byte, pathLen byte, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, 0, nil
	}
	var hops [][]byte
	size := -1
	for _, raw := range strings.Split(s, ",") {
		h := strings.TrimSpace(raw)
		if h == "" {
			return nil, 0, errors.New("a path hop is empty — use hex hops separated by commas, e.g. 11, 22, 33")
		}
		b, decErr := hex.DecodeString(h)
		if decErr != nil {
			return nil, 0, fmt.Errorf("hop %q isn't valid hex", h)
		}
		if len(b) < 1 || len(b) > 3 {
			return nil, 0, fmt.Errorf("hop %q must be 1, 2, or 3 bytes (2, 4, or 6 hex digits)", h)
		}
		if size == -1 {
			size = len(b)
		} else if len(b) != size {
			return nil, 0, errors.New("all path hops must be the same length")
		}
		hops = append(hops, b)
	}
	if len(hops) > maxPathHops {
		return nil, 0, fmt.Errorf("too many hops (%d) — the maximum is %d", len(hops), maxPathHops)
	}
	path = make([]byte, 0, len(hops)*size)
	for _, b := range hops {
		path = append(path, b...)
	}
	pathLen = byte((size-1)<<6 | len(hops)) //nolint:gosec // G115: size≤3 and hops≤63, so the value is ≤191 and fits a byte
	return path, pathLen, nil
}
