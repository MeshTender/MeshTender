package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// HeardChannel carries packets the ingest leader heard that may be replies to
// MeshTender, or MeshTender's own packets being repeated (mesh.ClassifyHeard),
// to every replica: only the leader reads the brokers, but a console session
// can be on any replica.
const HeardChannel = "meshtender_heard"

// HeardPacket is one copy of a packet as an observer heard it.
type HeardPacket struct {
	Raw []byte `json:"raw"`
	// Observer is the hex public key of the observer that heard it.
	Observer string    `json:"observer"`
	At       time.Time `json:"at"`
}

// NotifyHeard passes a heard packet to every replica's ListenHeard. A packet
// is at most 255 bytes, so the payload stays well under NOTIFY's 8000-byte
// limit.
func (s *Store) NotifyHeard(ctx context.Context, h HeardPacket) error {
	payload, err := json.Marshal(h)
	if err != nil {
		return fmt.Errorf("encode heard packet: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `SELECT pg_notify($1, $2)`, HeardChannel, string(payload)); err != nil {
		return fmt.Errorf("notify heard packet: %w", err)
	}
	return nil
}

// ListenHeard calls handle with every packet NotifyHeard sends, until ctx ends
// or the connection fails; it returns the error either way, and the caller
// listens again. It holds one connection of its own, taken out of the pool and
// closed when it returns, so it never goes back to the pool still listening.
// handle runs on the listening goroutine and must not block.
func (s *Store) ListenHeard(ctx context.Context, handle func(HeardPacket)) error {
	pooled, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire heard listener: %w", err)
	}
	conn := pooled.Hijack()
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{HeardChannel}.Sanitize()); err != nil {
		return fmt.Errorf("listen %s: %w", HeardChannel, err)
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return fmt.Errorf("wait for heard packet: %w", err)
		}
		var h HeardPacket
		if json.Unmarshal([]byte(n.Payload), &h) != nil || len(h.Raw) == 0 {
			continue
		}
		handle(h)
	}
}
