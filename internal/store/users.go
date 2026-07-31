package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	// Timezone is the user's preferred IANA zone name (e.g. "America/New_York")
	// for date/time display, or "" when unset (the browser auto-detects).
	Timezone string
	// Email is the account's optional email address, nil when unset. It is never
	// public — it exists for account recovery (and, later, security notices) and
	// must not be rendered on any page but the owner's own account settings.
	// EmailVerifiedAt is nil until the address has been confirmed; only a verified
	// address receives mail.
	Email           *string
	EmailVerifiedAt *time.Time
	// Instance-level capability flags.
	CapManageUsers   bool
	CapManageCatalog bool
	// LastLoginAt is the most recent successful sign-in, or nil if the account has
	// never logged in since the column was added.
	LastLoginAt *time.Time
	CreatedAt   time.Time
}

// Name returns the display name if set, else the username.
func (u *User) Name() string {
	if u.DisplayName != nil && *u.DisplayName != "" {
		return *u.DisplayName
	}
	return u.Username
}

const userCols = `id, username, display_name, password_hash, bio, location, callsign, timezone, email, email_verified_at, cap_manage_users, cap_manage_catalog, last_login_at, created_at`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash,
		&u.Bio, &u.Location, &u.Callsign, &u.Timezone, &u.Email, &u.EmailVerifiedAt,
		&u.CapManageUsers, &u.CapManageCatalog, &u.LastLoginAt, &u.CreatedAt); err != nil {
		return nil, err
	}
	return &u, nil
}

// EmailVerified reports whether the account has a confirmed address — the
// precondition for sending it anything.
func (u *User) EmailVerified() bool {
	return u.Email != nil && *u.Email != "" && u.EmailVerifiedAt != nil
}

// CanResetPassword reports whether this account can be recovered by email.
//
// Both halves are required, and the password half is the load-bearing one: reset
// only ever SETS a password on an account that already has one. A passkey-only
// account is deliberately not recoverable this way — allowing it would silently
// demote a phishing-resistant credential to "whoever controls the mailbox", which
// is the opposite of why someone chose a passkey. Such users are pointed at adding
// a second passkey instead.
func (u *User) CanResetPassword() bool {
	return u.PasswordHash != nil && u.EmailVerified()
}

// TouchLastLogin stamps the user's most recent sign-in time. Best-effort
// telemetry — callers should not fail a login if this errors.
func (s *Store) TouchLastLogin(ctx context.Context, userID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("touch last login: %w", err)
	}
	return nil
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

// UserSort names the orderings the admin user list can be browsed in.
type UserSort string

const (
	UserSortName      UserSort = "name"       // username A–Z (default)
	UserSortLastLogin UserSort = "last_login" // most recent sign-in first
	UserSortNewest    UserSort = "newest"     // most recently created first
)

// NormalizeUserSort coerces an untrusted sort key to a known value.
func NormalizeUserSort(s string) UserSort {
	switch UserSort(s) {
	case UserSortLastLogin, UserSortNewest:
		return UserSort(s)
	default:
		return UserSortName
	}
}

// UserCapFilter narrows the list to accounts with a given capability.
type UserCapFilter string

const (
	UserCapAny      UserCapFilter = ""         // no filter
	UserCapManagers UserCapFilter = "managers" // cap_manage_users
	UserCapCatalog  UserCapFilter = "catalog"  // cap_manage_catalog
	UserCapNone     UserCapFilter = "none"     // no capabilities
)

// NormalizeUserCapFilter coerces an untrusted capability filter to a known value.
func NormalizeUserCapFilter(s string) UserCapFilter {
	switch UserCapFilter(s) {
	case UserCapManagers, UserCapCatalog, UserCapNone:
		return UserCapFilter(s)
	default:
		return UserCapAny
	}
}

// UserListParams is one page request for the admin user list: an optional search
// over username/display name, a sort, a capability filter, and a keyset cursor.
type UserListParams struct {
	Query     string
	Sort      UserSort
	Cap       UserCapFilter
	HasCursor bool
	AfterName string    // username-sort cursor
	AfterID   int64     // tiebreaker
	AfterTime time.Time // last-login / newest cursor (for last-login, the coalesced value)
}

// nullLoginEpoch is the sort key for accounts that have never logged in, so the
// last-login ordering can keyset over a non-null column (NULLs sort last under
// DESC). It matches Postgres' 'epoch' timestamptz used in the query.
var nullLoginEpoch = time.Unix(0, 0).UTC()

// LastLoginKey returns the row's last-login sort key (its login time, or the
// epoch sentinel when it has never logged in) for building the next cursor.
func (u *User) LastLoginKey() time.Time {
	if u.LastLoginAt != nil {
		return *u.LastLoginAt
	}
	return nullLoginEpoch
}

// ListUsersPage returns one keyset page of the admin user list in the requested
// order, optionally searched and capability-filtered, seeking strictly past the
// cursor. It returns the page (capped at UsersPageSize) and whether more follow.
func (s *Store) ListUsersPage(ctx context.Context, p UserListParams) ([]*User, bool, error) {
	var args []any
	add := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }

	var where []string
	if q := strings.TrimSpace(p.Query); q != "" {
		ph := add("%" + escapeLikePattern(q) + "%")
		where = append(where, fmt.Sprintf("(username ILIKE %[1]s OR coalesce(display_name, '') ILIKE %[1]s)", ph))
	}
	switch p.Cap {
	case UserCapManagers:
		where = append(where, "cap_manage_users")
	case UserCapCatalog:
		where = append(where, "cap_manage_catalog")
	case UserCapNone:
		where = append(where, "NOT cap_manage_users AND NOT cap_manage_catalog")
	}

	// login_key coalesces last_login_at so the last-login sort can keyset over a
	// never-null expression (never-logged-in rows sort last under DESC).
	var order string
	switch p.Sort {
	case UserSortLastLogin:
		order = "coalesce(last_login_at, 'epoch') DESC, id DESC"
		if p.HasCursor {
			where = append(where, fmt.Sprintf("(coalesce(last_login_at, 'epoch'), id) < (%s, %s)", add(p.AfterTime), add(p.AfterID)))
		}
	case UserSortNewest:
		order = "created_at DESC, id DESC"
		if p.HasCursor {
			where = append(where, fmt.Sprintf("(created_at, id) < (%s, %s)", add(p.AfterTime), add(p.AfterID)))
		}
	default: // UserSortName
		order = "username ASC, id ASC"
		if p.HasCursor {
			where = append(where, fmt.Sprintf("(username, id) > (%s, %s)", add(p.AfterName), add(p.AfterID)))
		}
	}
	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	// Fetch one extra row to detect whether a further page exists.
	limit := add(UsersPageSize + 1)
	query := fmt.Sprintf(`SELECT %s FROM users %s ORDER BY %s LIMIT %s`, userCols, whereClause, order, limit)
	rows, err := s.pool.Query(ctx, query, args...)
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

// SetTimezone updates a user's preferred IANA time zone for date/time display.
// An empty string clears it (the browser auto-detects). Validating that tz is a
// real IANA name is the caller's responsibility.
func (s *Store) SetTimezone(ctx context.Context, userID int64, tz string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET timezone = $2 WHERE id = $1`, userID, tz)
	if err != nil {
		return fmt.Errorf("set timezone: %w", err)
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
