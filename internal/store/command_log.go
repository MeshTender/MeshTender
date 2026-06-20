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

// CommandLogPageSize is the number of console sessions shown per log page.
const CommandLogPageSize = 25

// CommandLogCursor is the keyset position in a repeater's session log: the
// (started_at, id) of the last session on the current page. The next page seeks
// strictly older than it.
type CommandLogCursor struct {
	StartedAt time.Time
	ID        int64
}

// ListCommandLogSessionsPage returns one keyset page of a repeater's command log
// grouped into console sessions, newest session first. before is the cursor from
// the previous page, or nil for the first page. It returns the sessions (each
// with its entries chronological) and whether older sessions remain.
//
// Sessions are the pagination unit (the page renders one card per session), so
// a session is never split across pages — and the per-page work is bounded by
// the page size, unlike the old fixed 500-row cap that silently hid older
// history. Both the session seek and the entry fetch ride existing indexes.
func (s *Store) ListCommandLogSessionsPage(ctx context.Context, repeaterID int64, before *CommandLogCursor) ([]*CommandSession, bool, error) {
	first := before == nil
	var curStarted time.Time
	var curID int64
	if !first {
		curStarted, curID = before.StartedAt, before.ID
	}
	// Fetch one extra session to detect whether an older page exists. Only
	// sessions that actually logged a command are shown (matching the inner-join
	// behavior of the previous query).
	headers, err := s.pool.Query(ctx, `
		SELECT cs.id, cs.started_at, cs.ended_at,
		       COALESCE(NULLIF(su.display_name, ''), su.username, '(deleted)')
		FROM console_sessions cs
		LEFT JOIN users su ON su.id = cs.user_id
		WHERE cs.repeater_id = $1
		  AND ($2 OR (cs.started_at, cs.id) < ($3, $4))
		  AND EXISTS (SELECT 1 FROM command_log l WHERE l.session_id = cs.id)
		ORDER BY cs.started_at DESC, cs.id DESC
		LIMIT $5`, repeaterID, first, curStarted, curID, CommandLogPageSize+1)
	if err != nil {
		return nil, false, fmt.Errorf("list command sessions: %w", err)
	}
	defer headers.Close()

	var groups []*CommandSession
	for headers.Next() {
		var g CommandSession
		if err := headers.Scan(&g.ID, &g.StartedAt, &g.EndedAt, &g.SenderName); err != nil {
			return nil, false, fmt.Errorf("scan session: %w", err)
		}
		groups = append(groups, &g)
	}
	if err := headers.Err(); err != nil {
		return nil, false, err
	}

	hasMore := len(groups) > CommandLogPageSize
	if hasMore {
		groups = groups[:CommandLogPageSize]
	}
	if len(groups) == 0 {
		return groups, false, nil
	}

	// Index the page's sessions so the entry fetch can attach rows by session.
	byID := make(map[int64]*CommandSession, len(groups))
	ids := make([]int64, 0, len(groups))
	for _, g := range groups {
		byID[g.ID] = g
		ids = append(ids, g.ID)
	}
	// Load all entries for the page's sessions at once, oldest first so each
	// session's slice ends up chronological.
	rows, err := s.pool.Query(ctx, `
		SELECT l.id, l.session_id, l.command_text, l.sent_at, l.ack_received, l.response_text
		FROM command_log l
		WHERE l.session_id = ANY($1)
		ORDER BY l.sent_at ASC, l.id ASC`, ids)
	if err != nil {
		return nil, false, fmt.Errorf("list command entries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e CommandLogEntry
		var sessionID int64
		if err := rows.Scan(&e.ID, &sessionID, &e.CommandText, &e.SentAt, &e.AckReceived, &e.ResponseText); err != nil {
			return nil, false, fmt.Errorf("scan entry: %w", err)
		}
		if g := byID[sessionID]; g != nil {
			g.Entries = append(g.Entries, &e)
		}
	}
	return groups, hasMore, rows.Err()
}

// OwnerCommandLogEntry is a recent command across all repeaters a user owns,
// joined with the repeater's name and the sender's name (for the dashboard).
type OwnerCommandLogEntry struct {
	RepeaterID       int64
	RepeaterPublicID string
	RepeaterName     string
	SenderName       string
	CommandText      string
	SentAt           time.Time
	AckReceived      bool
	ResponseText     *string
}

// ListRecentCommandsForOwner returns the most recent commands run on any
// repeater owned by ownerID, newest first, bounded by limit.
func (s *Store) ListRecentCommandsForOwner(ctx context.Context, ownerID int64, limit int) ([]OwnerCommandLogEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT l.repeater_id, r.public_id, r.name,
		       COALESCE(NULLIF(u.display_name, ''), u.username, '(deleted)'),
		       l.command_text, l.sent_at, l.ack_received, l.response_text
		FROM command_log l
		JOIN repeaters r ON r.id = l.repeater_id
		LEFT JOIN users u ON u.id = l.user_id
		WHERE r.owner_id = $1
		ORDER BY l.sent_at DESC
		LIMIT $2`, ownerID, limit)
	if err != nil {
		return nil, fmt.Errorf("list owner commands: %w", err)
	}
	defer rows.Close()
	var out []OwnerCommandLogEntry
	for rows.Next() {
		var e OwnerCommandLogEntry
		if err := rows.Scan(&e.RepeaterID, &e.RepeaterPublicID, &e.RepeaterName, &e.SenderName,
			&e.CommandText, &e.SentAt, &e.AckReceived, &e.ResponseText); err != nil {
			return nil, fmt.Errorf("scan owner command: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
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
