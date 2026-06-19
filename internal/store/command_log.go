package store

import (
	"context"
	"fmt"
	"time"
)

// CommandLogEntry is one row of the command audit log, joined with the sender's
// name for display.
type CommandLogEntry struct {
	ID           int64
	RepeaterID   int64
	UserID       *int64
	SenderName   string // username (or display name); "(deleted)" if user gone
	CommandText  string
	SentAt       time.Time
	AckReceived  bool
	ResponseText *string
}

// LogCommand records a command send attempt within a session and returns its
// id. commandID may be 0 (stored NULL) when there's no catalog match.
func (s *Store) LogCommand(ctx context.Context, repeaterID, userID, sessionID, commandID int64, text string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO command_log (repeater_id, user_id, session_id, command_id, command_text)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		repeaterID, userID, sessionID, nullID(commandID), text).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("log command: %w", err)
	}
	return id, nil
}

func nullID(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

// MarkCommandReply records that a reply was received for a logged command.
func (s *Store) MarkCommandReply(ctx context.Context, logID int64, response string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE command_log SET ack_received = TRUE, response_text = $2 WHERE id = $1`,
		logID, response)
	if err != nil {
		return fmt.Errorf("mark command reply: %w", err)
	}
	return nil
}

// CommandSession groups command-log entries by the console session they were
// sent in.
type CommandSession struct {
	ID         int64
	SenderName string // who held the session
	StartedAt  time.Time
	EndedAt    *time.Time
	Entries    []*CommandLogEntry // chronological (oldest first)
}

// ListCommandLogSessions returns recent command-log entries for a repeater
// grouped into console sessions, newest session first. limit bounds the number
// of underlying log rows scanned.
func (s *Store) ListCommandLogSessions(ctx context.Context, repeaterID int64, limit int) ([]*CommandSession, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT l.id, l.command_text, l.sent_at, l.ack_received, l.response_text,
		       l.session_id, cs.started_at, cs.ended_at,
		       COALESCE(NULLIF(su.display_name, ''), su.username, '(deleted)')
		FROM command_log l
		JOIN console_sessions cs ON cs.id = l.session_id
		LEFT JOIN users su ON su.id = cs.user_id
		WHERE l.repeater_id = $1
		ORDER BY l.sent_at DESC
		LIMIT $2`, repeaterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list command sessions: %w", err)
	}
	defer rows.Close()

	var groups []*CommandSession
	byID := map[int64]*CommandSession{}
	for rows.Next() {
		var e CommandLogEntry
		var sessionID int64
		var startedAt time.Time
		var endedAt *time.Time
		var sender string
		if err := rows.Scan(&e.ID, &e.CommandText, &e.SentAt, &e.AckReceived, &e.ResponseText,
			&sessionID, &startedAt, &endedAt, &sender); err != nil {
			return nil, fmt.Errorf("scan session row: %w", err)
		}
		g := byID[sessionID]
		if g == nil {
			g = &CommandSession{ID: sessionID, SenderName: sender, StartedAt: startedAt, EndedAt: endedAt}
			byID[sessionID] = g
			groups = append(groups, g) // encounter order = newest session first
		}
		// Rows arrive newest-first; prepend to make each session chronological.
		g.Entries = append([]*CommandLogEntry{&e}, g.Entries...)
	}
	return groups, rows.Err()
}

// ListCommandLog returns recent log entries for a repeater, newest first.
func (s *Store) ListCommandLog(ctx context.Context, repeaterID int64, limit int) ([]*CommandLogEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT l.id, l.repeater_id, l.user_id,
		       COALESCE(NULLIF(u.display_name, ''), u.username, '(deleted)'),
		       l.command_text, l.sent_at, l.ack_received, l.response_text
		FROM command_log l
		LEFT JOIN users u ON u.id = l.user_id
		WHERE l.repeater_id = $1
		ORDER BY l.sent_at DESC
		LIMIT $2`, repeaterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list command log: %w", err)
	}
	defer rows.Close()
	var out []*CommandLogEntry
	for rows.Next() {
		var e CommandLogEntry
		if err := rows.Scan(&e.ID, &e.RepeaterID, &e.UserID, &e.SenderName,
			&e.CommandText, &e.SentAt, &e.AckReceived, &e.ResponseText); err != nil {
			return nil, fmt.Errorf("scan log: %w", err)
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}
