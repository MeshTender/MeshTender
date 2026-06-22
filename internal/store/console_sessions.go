package store

import (
	"context"
	"fmt"
)

// StartConsoleSession records the start of a console session and returns its id.
func (s *Store) StartConsoleSession(ctx context.Context, repeaterID, userID int64) (int64, error) {
	var id int64
	// Snapshot the username so the log stays a point-in-time record after renames.
	err := s.pool.QueryRow(ctx,
		`INSERT INTO console_sessions (repeater_id, user_id, sender_username)
		 VALUES ($1, $2, (SELECT username FROM users WHERE id = $2)) RETURNING id`,
		repeaterID, userID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("start console session: %w", err)
	}
	return id, nil
}

// EndConsoleSession stamps a session's end time. Best-effort.
func (s *Store) EndConsoleSession(ctx context.Context, sessionID int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE console_sessions SET ended_at = now() WHERE id = $1 AND ended_at IS NULL`, sessionID)
	if err != nil {
		return fmt.Errorf("end console session: %w", err)
	}
	return nil
}
