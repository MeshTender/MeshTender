package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrNotFound is returned when a queried row does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrDuplicate is returned when an insert violates a unique constraint.
var ErrDuplicate = errors.New("store: duplicate")

// User is a registered account. DisplayName is nil when unset (display falls
// back to Username). PasswordHash is nil when the user relies solely on a
// passkey.
type User struct {
	ID           int64
	Username     string
	DisplayName  *string
	PasswordHash *string
	// Instance-level capability flags.
	CapManageUsers   bool
	CapManageCatalog bool
}

// Name returns the display name if set, else the username.
func (u *User) Name() string {
	if u.DisplayName != nil && *u.DisplayName != "" {
		return *u.DisplayName
	}
	return u.Username
}

const userCols = `id, username, display_name, password_hash, cap_manage_users, cap_manage_catalog`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash,
		&u.CapManageUsers, &u.CapManageCatalog); err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateUser inserts a new user and returns it. displayName may be empty (then
// stored as NULL). Returns ErrDuplicate if the username is already taken.
func (s *Store) CreateUser(ctx context.Context, username, displayName string) (*User, error) {
	var dn *string
	if displayName != "" {
		dn = &displayName
	}
	u, err := scanUser(s.pool.QueryRow(ctx,
		`INSERT INTO users (username, display_name) VALUES ($1, $2) RETURNING `+userCols,
		username, dn))
	if isUniqueViolation(err) {
		return nil, ErrDuplicate
	}
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	// Bootstrap: the first account (when no one yet manages users) becomes the
	// instance superadmin, atomically.
	tag, err := s.pool.Exec(ctx, `
		UPDATE users SET cap_manage_users = TRUE, cap_manage_catalog = TRUE
		WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM users WHERE cap_manage_users AND id <> $1)`,
		u.ID)
	if err != nil {
		return nil, fmt.Errorf("bootstrap caps: %w", err)
	}
	if tag.RowsAffected() > 0 {
		u.CapManageUsers, u.CapManageCatalog = true, true
	}
	return u, nil
}

// SetCapabilities updates a user's instance capability flags.
func (s *Store) SetCapabilities(ctx context.Context, userID int64, manageUsers, manageCatalog bool) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET cap_manage_users = $2, cap_manage_catalog = $3 WHERE id = $1`,
		userID, manageUsers, manageCatalog)
	if err != nil {
		return fmt.Errorf("set capabilities: %w", err)
	}
	return nil
}

// CountManageUsers returns how many users hold the manage-users capability
// (used to prevent removing the last one).
func (s *Store) CountManageUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE cap_manage_users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count manage users: %w", err)
	}
	return n, nil
}

// ListUsers returns all users ordered by username (for the admin page).
func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY username`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// GetUserByUsername looks up a user by username, returning ErrNotFound if absent.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx,
		`SELECT `+userCols+` FROM users WHERE username = $1`, username))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user by username: %w", err)
	}
	return u, nil
}

// GetUserByID looks up a user by id, returning ErrNotFound if absent.
func (s *Store) GetUserByID(ctx context.Context, id int64) (*User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx,
		`SELECT `+userCols+` FROM users WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return u, nil
}

// SetPassword sets (or clears) a user's bcrypt password hash.
func (s *Store) SetPassword(ctx context.Context, userID int64, hash string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, hash, userID)
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	return nil
}

// AddCredential stores a marshaled WebAuthn credential for a user.
func (s *Store) AddCredential(ctx context.Context, userID int64, credentialID []byte, data []byte) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO webauthn_credentials (user_id, credential_id, data) VALUES ($1, $2, $3)`,
		userID, credentialID, data)
	if isUniqueViolation(err) {
		return ErrDuplicate
	}
	if err != nil {
		return fmt.Errorf("add credential: %w", err)
	}
	return nil
}

// GetCredentials returns the marshaled credential blobs for a user.
func (s *Store) GetCredentials(ctx context.Context, userID int64) ([][]byte, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT data FROM webauthn_credentials WHERE user_id = $1 ORDER BY id`, userID)
	if err != nil {
		return nil, fmt.Errorf("get credentials: %w", err)
	}
	defer rows.Close()

	var out [][]byte
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, fmt.Errorf("scan credential: %w", err)
		}
		out = append(out, data)
	}
	return out, rows.Err()
}

// UpdateCredential replaces the stored blob for a credential (used to persist
// the updated sign counter after a successful assertion).
func (s *Store) UpdateCredential(ctx context.Context, credentialID []byte, data []byte) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE webauthn_credentials SET data = $1 WHERE credential_id = $2`, data, credentialID)
	if err != nil {
		return fmt.Errorf("update credential: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
