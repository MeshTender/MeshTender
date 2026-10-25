package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// What net control's modem session does on the air (migration 0052).

// MaxNetSessionMessages bounds a session's channel log: anyone on the channel
// can send messages, so a busy or abusive channel mustn't grow it unbounded.
const MaxNetSessionMessages = 500

// MarkAired records members as acknowledged on the air in a session. Members
// already marked keep their first time.
func (s *Store) MarkAired(ctx context.Context, sessionID int64, userIDs []int64, at time.Time) error {
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO net_session_aired (session_id, user_id, aired_at)
		SELECT $1, unnest($2::bigint[]), $3
		ON CONFLICT DO NOTHING`, sessionID, userIDs, at); err != nil {
		return fmt.Errorf("mark aired: %w", err)
	}
	return nil
}

// NetSessionMessage is a logged channel message. Sender is whatever name the
// message claims: channel messages aren't signed.
type NetSessionMessage struct {
	ID      int64
	Sender  string
	Body    string
	HeardAt time.Time
}

// AddNetSessionMessage logs a channel message heard during a session. payload
// is the packet payload, which identifies the message across the copies
// repeaters send; a copy already logged, or a session at
// MaxNetSessionMessages, logs nothing. It reports whether the message was added.
func (s *Store) AddNetSessionMessage(ctx context.Context, sessionID int64, payload []byte, sender, body string, heardAt time.Time) (bool, error) {
	hash := sha256.Sum256(payload)
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO net_session_messages (session_id, msg_hash, sender, body, heard_at)
		SELECT $1, $2, $3, $4, $5
		WHERE (SELECT count(*) FROM net_session_messages WHERE session_id = $1) < $6
		ON CONFLICT (session_id, msg_hash) DO NOTHING`,
		sessionID, hash[:], sender, body, heardAt, MaxNetSessionMessages)
	if err != nil {
		return false, fmt.Errorf("add net session message: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ListNetSessionMessages returns a session's channel log in the order heard
// (at most MaxNetSessionMessages, so it's read whole).
func (s *Store) ListNetSessionMessages(ctx context.Context, sessionID int64) ([]NetSessionMessage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, sender, body, heard_at FROM net_session_messages
		WHERE session_id = $1 ORDER BY heard_at, id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list net session messages: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (NetSessionMessage, error) {
		var m NetSessionMessage
		return m, r.Scan(&m.ID, &m.Sender, &m.Body, &m.HeardAt)
	})
}
