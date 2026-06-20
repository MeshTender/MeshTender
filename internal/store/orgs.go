package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Org is an organization.
type Org struct {
	ID int64
	// Slug is the admin-chosen, non-enumerable identifier used in URLs.
	Slug        string
	Name        string
	Description string
	Region      string
	CreatedBy   *int64
	CreatedAt   time.Time
}

// OrgSummary is a public directory entry for an organization.
type OrgSummary struct {
	ID            int64
	Slug          string
	Name          string
	Description   string
	MemberCount   int
	RepeaterCount int
}

// OrgMembership pairs an org with the querying user's role in it.
type OrgMembership struct {
	Org  *Org
	Role string
}

// OrgMemberInfo describes a member for the org page.
type OrgMemberInfo struct {
	UserID      int64
	Username    string
	DisplayName *string
	Role        string
}

// Name returns the member's display name if set, else username.
func (m OrgMemberInfo) Name() string {
	if m.DisplayName != nil && *m.DisplayName != "" {
		return *m.DisplayName
	}
	return m.Username
}

// reservedSlugs are slugs that would collide with static /orgs routes or are
// otherwise not allowed as org identifiers.
var reservedSlugs = map[string]bool{"new": true}

var slugCharRE = regexp.MustCompile(`[^a-z0-9]+`)
var validSlugRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// slugify converts an arbitrary name into a slug candidate (lowercase, hyphen-
// separated alphanumerics). Returns "" if the name has no usable characters.
func slugify(name string) string {
	return strings.Trim(slugCharRE.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

// ValidOrgSlug reports whether s is an acceptable org slug: 3–40 chars, lowercase
// alphanumerics with single internal hyphens, and not reserved.
func ValidOrgSlug(s string) bool {
	if len(s) < 3 || len(s) > 40 || reservedSlugs[s] {
		return false
	}
	return validSlugRE.MatchString(s)
}

// rowQuerier is satisfied by both *pgxpool.Pool and pgx.Tx.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// uniqueOrgSlug returns base if free, else base-2, base-3, … finding the first
// unused slug. base is sanitized and falls back to "org" when empty.
func uniqueOrgSlug(ctx context.Context, q rowQuerier, base string) (string, error) {
	if base == "" {
		base = "org"
	}
	candidate := base
	for n := 2; ; n++ {
		var exists bool
		if err := q.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM organizations WHERE slug = $1)`, candidate).Scan(&exists); err != nil {
			return "", fmt.Errorf("check slug: %w", err)
		}
		if !exists {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, n)
	}
}

// CreateOrg creates an organization, makes the creator an admin, and seeds a v1
// permission policy from the catalog org defaults — all atomically.
func (s *Store) CreateOrg(ctx context.Context, name string, creatorID int64) (*Org, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	slug, err := uniqueOrgSlug(ctx, tx, slugify(name))
	if err != nil {
		return nil, err
	}

	var o Org
	if err := tx.QueryRow(ctx,
		`INSERT INTO organizations (slug, name, created_by) VALUES ($1, $2, $3)
		 RETURNING id, slug, name, description, region, created_by, created_at`,
		slug, name, creatorID).Scan(&o.ID, &o.Slug, &o.Name, &o.Description, &o.Region, &o.CreatedBy, &o.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert org: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'admin')`,
		o.ID, creatorID); err != nil {
		return nil, fmt.Errorf("add creator: %w", err)
	}

	// Seed version 1 from the catalog default sets.
	var versionID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO org_permission_versions (org_id, version, note, created_by)
		 VALUES ($1, 1, 'Initial policy', $2) RETURNING id`,
		o.ID, creatorID).Scan(&versionID); err != nil {
		return nil, fmt.Errorf("seed version: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO org_permission_commands (version_id, command_id, tier)
		 SELECT $1, id, 'admin' FROM command_catalog WHERE in_org_admin_default`, versionID); err != nil {
		return nil, fmt.Errorf("seed admin commands: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO org_permission_commands (version_id, command_id, tier)
		 SELECT $1, id, 'member' FROM command_catalog WHERE in_org_member_default`, versionID); err != nil {
		return nil, fmt.Errorf("seed member commands: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &o, nil
}

// GetOrg returns an org by id.
func (s *Store) GetOrg(ctx context.Context, id int64) (*Org, error) {
	var o Org
	err := s.pool.QueryRow(ctx,
		`SELECT id, slug, name, description, region, created_by, created_at FROM organizations WHERE id = $1`, id).
		Scan(&o.ID, &o.Slug, &o.Name, &o.Description, &o.Region, &o.CreatedBy, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get org: %w", err)
	}
	return &o, nil
}

// OrgIDBySlug resolves a URL slug to the internal int64 primary key, or
// ErrNotFound. Membership/role checks are enforced separately.
func (s *Store) OrgIDBySlug(ctx context.Context, slug string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT id FROM organizations WHERE slug = $1`, slug).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("org by slug: %w", err)
	}
	return id, nil
}

// UpdateOrg updates an org's slug, name, description, and region. Returns
// ErrDuplicate if the slug is already taken by another org.
func (s *Store) UpdateOrg(ctx context.Context, orgID int64, slug, name, description, region string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE organizations SET slug = $2, name = $3, description = $4, region = $5 WHERE id = $1`,
		orgID, slug, name, description, region)
	if isUniqueViolation(err) {
		return ErrDuplicate
	}
	if err != nil {
		return fmt.Errorf("update org: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListPublicOrgs returns every organization with member/repeater counts, for the
// public directory. Orgs are publicly listed by default.
func (s *Store) ListPublicOrgs(ctx context.Context) ([]OrgSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.slug, o.name, o.description,
		       (SELECT count(*) FROM org_members m WHERE m.org_id = o.id),
		       (SELECT count(*) FROM org_repeaters orp WHERE orp.org_id = o.id)
		FROM organizations o
		ORDER BY o.name`)
	if err != nil {
		return nil, fmt.Errorf("list public orgs: %w", err)
	}
	defer rows.Close()
	var out []OrgSummary
	for rows.Next() {
		var s OrgSummary
		if err := rows.Scan(&s.ID, &s.Slug, &s.Name, &s.Description, &s.MemberCount, &s.RepeaterCount); err != nil {
			return nil, fmt.Errorf("scan org summary: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// OrgCounts returns the member and contributed-repeater counts for an org.
func (s *Store) OrgCounts(ctx context.Context, orgID int64) (members, repeaters int, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM org_members WHERE org_id = $1),
		       (SELECT count(*) FROM org_repeaters WHERE org_id = $1)`, orgID).
		Scan(&members, &repeaters)
	if err != nil {
		return 0, 0, fmt.Errorf("org counts: %w", err)
	}
	return members, repeaters, nil
}

// ListOrgsForUser returns the orgs a user belongs to with their role.
func (s *Store) ListOrgsForUser(ctx context.Context, userID int64) ([]OrgMembership, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.slug, o.name, o.description, o.created_by, o.created_at, m.role
		FROM org_members m JOIN organizations o ON o.id = m.org_id
		WHERE m.user_id = $1 ORDER BY o.name`, userID)
	if err != nil {
		return nil, fmt.Errorf("list orgs: %w", err)
	}
	defer rows.Close()
	var out []OrgMembership
	for rows.Next() {
		var o Org
		var role string
		if err := rows.Scan(&o.ID, &o.Slug, &o.Name, &o.Description, &o.CreatedBy, &o.CreatedAt, &role); err != nil {
			return nil, fmt.Errorf("scan org: %w", err)
		}
		out = append(out, OrgMembership{Org: &o, Role: role})
	}
	return out, rows.Err()
}

// OrgRole returns the user's role in an org and whether they're a member.
func (s *Store) OrgRole(ctx context.Context, orgID, userID int64) (string, bool, error) {
	var role string
	err := s.pool.QueryRow(ctx,
		`SELECT role FROM org_members WHERE org_id = $1 AND user_id = $2`, orgID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("org role: %w", err)
	}
	return role, true, nil
}

// IsOrgAdmin reports whether the user is an admin of the org.
func (s *Store) IsOrgAdmin(ctx context.Context, orgID, userID int64) (bool, error) {
	role, ok, err := s.OrgRole(ctx, orgID, userID)
	return ok && role == "admin", err
}

// AddOrgMember adds a user to an org (idempotent — keeps an existing role).
func (s *Store) AddOrgMember(ctx context.Context, orgID, userID int64, role string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, $3)
		 ON CONFLICT (org_id, user_id) DO NOTHING`, orgID, userID, role)
	if err != nil {
		return fmt.Errorf("add org member: %w", err)
	}
	return nil
}

// ListOrgMembers returns an org's members ordered admins-first then by name.
func (s *Store) ListOrgMembers(ctx context.Context, orgID int64) ([]OrgMemberInfo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.id, u.username, u.display_name, m.role
		FROM org_members m JOIN users u ON u.id = m.user_id
		WHERE m.org_id = $1
		ORDER BY (m.role = 'admin') DESC, u.username`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()
	var out []OrgMemberInfo
	for rows.Next() {
		var m OrgMemberInfo
		if err := rows.Scan(&m.UserID, &m.Username, &m.DisplayName, &m.Role); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// countOrgAdmins returns the number of admins in an org.
func (s *Store) countOrgAdmins(ctx context.Context, orgID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM org_members WHERE org_id = $1 AND role = 'admin'`, orgID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count admins: %w", err)
	}
	return n, nil
}

// ErrLastAdmin is returned when an action would leave an org with no admins.
var ErrLastAdmin = errors.New("store: cannot remove the last org admin")

// SetOrgMemberRole changes a member's role, refusing to demote the last admin.
func (s *Store) SetOrgMemberRole(ctx context.Context, orgID, userID int64, role string) error {
	if role != "admin" {
		if err := s.guardLastAdmin(ctx, orgID, userID); err != nil {
			return err
		}
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE org_members SET role = $3 WHERE org_id = $1 AND user_id = $2`, orgID, userID, role)
	if err != nil {
		return fmt.Errorf("set role: %w", err)
	}
	return nil
}

// RemoveOrgMember removes a member, refusing to remove the last admin.
func (s *Store) RemoveOrgMember(ctx context.Context, orgID, userID int64) error {
	if err := s.guardLastAdmin(ctx, orgID, userID); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx,
		`DELETE FROM org_members WHERE org_id = $1 AND user_id = $2`, orgID, userID)
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	return nil
}

// guardLastAdmin returns ErrLastAdmin if userID is the org's only admin.
func (s *Store) guardLastAdmin(ctx context.Context, orgID, userID int64) error {
	role, ok, err := s.OrgRole(ctx, orgID, userID)
	if err != nil {
		return err
	}
	if !ok || role != "admin" {
		return nil // not an admin; removing/demoting is fine
	}
	n, err := s.countOrgAdmins(ctx, orgID)
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastAdmin
	}
	return nil
}

// --- invites (multi-use, member-only) ---

// OrgInvite is a multi-use join link.
type OrgInvite struct {
	ID          int64
	Token       string
	Description string
	CreatedAt   time.Time
}

// CreateOrgInvite mints a new multi-use member join link.
func (s *Store) CreateOrgInvite(ctx context.Context, orgID int64, description string) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO org_invites (org_id, token, description) VALUES ($1, $2, $3)`,
		orgID, token, description)
	if err != nil {
		return "", fmt.Errorf("create org invite: %w", err)
	}
	return token, nil
}

// ListOrgInvites returns an org's active join links.
func (s *Store) ListOrgInvites(ctx context.Context, orgID int64) ([]OrgInvite, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, token, description, created_at FROM org_invites WHERE org_id = $1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list org invites: %w", err)
	}
	defer rows.Close()
	var out []OrgInvite
	for rows.Next() {
		var inv OrgInvite
		if err := rows.Scan(&inv.ID, &inv.Token, &inv.Description, &inv.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan invite: %w", err)
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// DeleteOrgInvite revokes a join link (scoped to its org).
func (s *Store) DeleteOrgInvite(ctx context.Context, orgID, inviteID int64) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM org_invites WHERE id = $1 AND org_id = $2`, inviteID, orgID)
	if err != nil {
		return fmt.Errorf("delete org invite: %w", err)
	}
	return nil
}

// OrgByInviteToken resolves a join-link token to its org, or ErrNotFound.
func (s *Store) OrgByInviteToken(ctx context.Context, token string) (*Org, error) {
	var o Org
	err := s.pool.QueryRow(ctx, `
		SELECT o.id, o.slug, o.name, o.description, o.created_by, o.created_at
		FROM org_invites i JOIN organizations o ON o.id = i.org_id
		WHERE i.token = $1`, token).Scan(&o.ID, &o.Slug, &o.Name, &o.Description, &o.CreatedBy, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("org by invite: %w", err)
	}
	return &o, nil
}
