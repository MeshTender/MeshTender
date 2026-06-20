package mesh

import (
	"sync"
	"time"
)

// SeqClock produces strictly increasing, second-granularity timestamps for the
// packets sent to a single repeater within a session.
//
// MeshCore repeaters use the sender's timestamp as a per-client sequence/replay
// guard: a login requires a timestamp strictly greater than the last one seen,
// and a CLI command with a timestamp equal to the previous one is treated as a
// retry — the repeater runs nothing and sends no reply. Because wall-clock
// time has only one-second resolution, two commands issued in the same second
// would collide. SeqClock advances by at least one second per call so every
// packet in a session is distinct and ordered.
type SeqClock struct {
	mu   sync.Mutex
	last int64
}

// Next returns the next timestamp: max(now, lastReturned+1) seconds.
func (c *SeqClock) Next() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().Unix()
	if now <= c.last {
		now = c.last + 1
	}
	c.last = now
	return time.Unix(now, 0)
}
