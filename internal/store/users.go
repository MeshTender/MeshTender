package store

import (
	"context"
	"errors"
	"fmt"
	"time"

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
	// Public profile fields, shown on the user's public page (/u/{username}).
	// Empty when unset; a blank field simply doesn't render.
	Bio      string
	Location string
	Callsign string
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

const userCols = `id, username, display_name, password_hash, bio, location, callsign, cap_manage_users, cap_manage_catalog`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash,
		&u.Bio, &u.Location, &u.Callsign, &u.CapManageUsers, &u.CapManageCatalog); err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateUser inserts a new user with a database-assigned id and returns it.
// displayName may be empty (then stored as NULL). Returns ErrDuplicate if the
// username is already taken, or ErrUsernameReserved if it was recently released
// by someone else and is still within its cooldown.
func (s *Store) CreateUser(ctx context.Context, username, displayName string) (*User, error) {
	return s.createUser(ctx, 0, username, displayName)
}

// ReserveUserID consumes and returns the next users.id from the identity
// sequence without inserting a row. The deferred passkey signup uses it to fix
// the account's id — and therefore its WebAuthn user handle, which is the id
// encoded as 8 bytes — at BeginRegistration, then writes the row with
// CreateUserWithID only once a credential is verified at finish. An abandoned
// ceremony merely leaves a gap in the sequence; no orphan account is created.
func (s *Store) ReserveUserID(ctx context.Context) (int64, error) {
	var id int64
	if err := s.pool.QueryRow(ctx,
		`SELECT nextval(pg_get_serial_sequence('users', 'id'))`).Scan(&id); err != nil {
		return 0, fmt.Errorf("reserve user id: %w", err)
	}
	return id, nil
}

// CreateUserWithID inserts a new user with a caller-reserved id (from
// ReserveUserID). See CreateUser for the error contract.
func (s *Store) CreateUserWithID(ctx context.Context, id int64, username, displayName string) (*User, error) {
	return s.createUser(ctx, id, username, displayName)
}

// createUser is the shared insert. id == 0 lets the identity column assign one;
// a nonzero id is forced via OVERRIDING SYSTEM VALUE (the column is GENERATED
// ALWAYS).
func (s *Store) createUser(ctx context.Context, id int64, username, displayName string) (*User, error) {
	var dn *string
	if displayName != "" {
		dn = &displayName
	}
	// A brand-new account has no incumbent identity, so any prior owner's recent
	// release reserves the name (exceptUserID 0 matches no one).
	reserved, err := nameReservedByOther(ctx, s.pool, username, 0)
	if err != nil {
		return nil, err
	}
	if reserved {
		return nil, ErrUsernameReserved
	}
	query := `INSERT INTO users (username, display_name) VALUES ($1, $2) RETURNING ` + userCols
	args := []any{username, dn}
	if id != 0 {
		query = `INSERT INTO users (id, username, display_name) OVERRIDING SYSTEM VALUE VALUES ($1, $2, $3) RETURNING ` + userCols
		args = []any{id, username, dn}
	}
	u, err := scanUser(s.pool.QueryRow(ctx, query, args...))
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

// UsersPageSize is the number of users returned per admin-page request.
const UsersPageSize = 50

// ListUsersPage returns one keyset page of users for the admin page, ordered by
// username, starting strictly after afterUsername (empty starts at the
// beginning). It returns the page (capped at UsersPageSize) and whether more
// rows follow. username is UNIQUE, so it's a stable single-column cursor and
// the seek rides the existing unique index.
func (s *Store) ListUsersPage(ctx context.Context, afterUsername string) ([]*User, bool, error) {
	// Fetch one extra row to detect whether a further page exists.
	rows, err := s.pool.Query(ctx,
		`SELECT `+userCols+` FROM users WHERE username > $1 ORDER BY username LIMIT $2`,
		afterUsername, UsersPageSize+1)
	if err != nil {
		return nil, false, fmt.Errorf("list users: %w", err)
	}
	out, err := collectRows(rows, scanUser)
	if err != nil {
		return nil, false, fmt.Errorf("scan user: %w", err)
	}
	hasMore := len(out) > UsersPageSize
	if hasMore {
		out = out[:UsersPageSize]
	}
	return out, hasMore, nil
}

// GetUserByUsername looks up a user by username, returning ErrNotFound if absent.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx,
		`SELECT `+userCols+` FROM users WHERE username = $1`, username))
	if err != nil {
		return nil, notFoundOr(err, "get user by username")
	}
	return u, nil
}

// GetUserByID looks up a user by id, returning ErrNotFound if absent.
func (s *Store) GetUserByID(ctx context.Context, id int64) (*User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx,
		`SELECT `+userCols+` FROM users WHERE id = $1`, id))
	if err != nil {
		return nil, notFoundOr(err, "get user by id")
	}
	return u, nil
}

// SetDisplayName updates a user's display name. An empty name clears it (stored
// as NULL), so Name() falls back to the username.
func (s *Store) SetDisplayName(ctx context.Context, userID int64, displayName string) error {
	var dn *string
	if displayName != "" {
		dn = &displayName
	}
	_, err := s.pool.Exec(ctx, `UPDATE users SET display_name = $1 WHERE id = $2`, dn, userID)
	if err != nil {
		return fmt.Errorf("set display name: %w", err)
	}
	return nil
}

// SetProfile updates a user's public profile fields (bio, location, callsign).
// Empty strings clear a field. Length bounding is the caller's responsibility.
func (s *Store) SetProfile(ctx context.Context, userID int64, bio, location, callsign string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET bio = $2, location = $3, callsign = $4 WHERE id = $1`,
		userID, bio, location, callsign)
	if err != nil {
		return fmt.Errorf("set profile: %w", err)
	}
	return nil
}

// UserHasPublicRole reports whether the user is listed on any public page: as an
// org admin, or as the owner or steward of a repeater with a published public
// page. Such users are nudged to add a way to be reached.
func (s *Store) UserHasPublicRole(ctx context.Context, userID int64) (bool, error) {
	var yes bool
	err := s.pool.QueryRow(ctx, `SELECT
		    EXISTS(SELECT 1 FROM org_members WHERE user_id = $1 AND role = 'admin')
		 OR EXISTS(SELECT 1 FROM repeaters WHERE owner_id = $1 AND expose_public_page)
		 OR EXISTS(SELECT 1 FROM repeater_shares rs JOIN repeaters r ON r.id = rs.repeater_id
		            WHERE rs.user_id = $1 AND rs.steward AND r.expose_public_page)`,
		userID).Scan(&yes)
	if err != nil {
		return false, fmt.Errorf("user has public role: %w", err)
	}
	return yes, nil
}

// SetPassword sets a user's bcrypt password hash.
func (s *Store) SetPassword(ctx context.Context, userID int64, hash string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, hash, userID)
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	return nil
}

// ClearPassword removes a user's password, leaving passkeys as the only way in.
func (s *Store) ClearPassword(ctx context.Context, userID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET password_hash = NULL WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("clear password: %w", err)
	}
	return nil
}

// AddCredential stores a marshaled WebAuthn credential for a user, with an
// optional human-friendly name (empty means unnamed).
func (s *Store) AddCredential(ctx context.Context, userID int64, credentialID []byte, data []byte, name string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO webauthn_credentials (user_id, credential_id, data, name) VALUES ($1, $2, $3, $4)`,
		userID, credentialID, data, name)
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
	return collectRows(rows, func(r pgx.Row) ([]byte, error) {
		var data []byte
		err := r.Scan(&data)
		return data, err
	})
}

// CredentialInfo is display metadata for a registered passkey.
type CredentialInfo struct {
	ID           int64
	CredentialID []byte
	Name         string
	CreatedAt    time.Time
}

// ListCredentials returns metadata for a user's passkeys, newest first.
func (s *Store) ListCredentials(ctx context.Context, userID int64) ([]CredentialInfo, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, credential_id, name, created_at FROM webauthn_credentials WHERE user_id = $1 ORDER BY created_at DESC, id DESC`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (CredentialInfo, error) {
		var c CredentialInfo
		err := r.Scan(&c.ID, &c.CredentialID, &c.Name, &c.CreatedAt)
		return c, err
	})
}

// SetCredentialName updates the human-friendly label on one of the user's
// passkeys. It scopes the update to the owner and returns ErrNotFound if no
// such credential exists.
func (s *Store) SetCredentialName(ctx context.Context, userID, credentialRowID int64, name string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE webauthn_credentials SET name = $1 WHERE id = $2 AND user_id = $3`, name, credentialRowID, userID)
	if err != nil {
		return fmt.Errorf("set credential name: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountCredentials returns how many passkeys a user has registered.
func (s *Store) CountCredentials(ctx context.Context, userID int64) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM webauthn_credentials WHERE user_id = $1`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count credentials: %w", err)
	}
	return n, nil
}

// DeleteCredential removes one of the user's passkeys by row id. It scopes the
// delete to the owner and returns ErrNotFound if no such credential exists.
func (s *Store) DeleteCredential(ctx context.Context, userID, credentialRowID int64) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM webauthn_credentials WHERE id = $1 AND user_id = $2`, credentialRowID, userID)
	if err != nil {
		return fmt.Errorf("delete credential: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
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
