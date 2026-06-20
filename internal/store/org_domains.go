package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// OrgDomain is a custom hostname an org has pointed at the app. It serves the
// org's public page only once verified_at is set (proven via a DNS TXT record).
type OrgDomain struct {
	ID                int64
	Hostname          string
	VerificationToken string
	VerifiedAt        *time.Time
	CreatedAt         time.Time
}

// Verified reports whether the domain's ownership has been confirmed.
func (d OrgDomain) Verified() bool { return d.VerifiedAt != nil }

// CreateOrgDomain registers a custom hostname for an org with a fresh
// verification token. Returns ErrDuplicate if the hostname is already claimed.
func (s *Store) CreateOrgDomain(ctx context.Context, orgID int64, hostname string) (*OrgDomain, error) {
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	var d OrgDomain
	err = s.pool.QueryRow(ctx,
		`INSERT INTO org_domains (org_id, hostname, verification_token) VALUES ($1, $2, $3)
		 RETURNING id, hostname, verification_token, verified_at, created_at`,
		orgID, hostname, token).
		Scan(&d.ID, &d.Hostname, &d.VerificationToken, &d.VerifiedAt, &d.CreatedAt)
	if isUniqueViolation(err) {
		return nil, ErrDuplicate
	}
	if err != nil {
		return nil, fmt.Errorf("create org domain: %w", err)
	}
	return &d, nil
}

// ListOrgDomains returns an org's custom domains, newest first.
func (s *Store) ListOrgDomains(ctx context.Context, orgID int64) ([]OrgDomain, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, hostname, verification_token, verified_at, created_at
		 FROM org_domains WHERE org_id = $1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list org domains: %w", err)
	}
	defer rows.Close()
	var out []OrgDomain
	for rows.Next() {
		var d OrgDomain
		if err := rows.Scan(&d.ID, &d.Hostname, &d.VerificationToken, &d.VerifiedAt, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan org domain: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetOrgDomain returns a single domain scoped to its org, or ErrNotFound.
func (s *Store) GetOrgDomain(ctx context.Context, orgID, domainID int64) (*OrgDomain, error) {
	var d OrgDomain
	err := s.pool.QueryRow(ctx,
		`SELECT id, hostname, verification_token, verified_at, created_at
		 FROM org_domains WHERE id = $1 AND org_id = $2`, domainID, orgID).
		Scan(&d.ID, &d.Hostname, &d.VerificationToken, &d.VerifiedAt, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get org domain: %w", err)
	}
	return &d, nil
}

// MarkOrgDomainVerified records that a domain's ownership has been confirmed.
func (s *Store) MarkOrgDomainVerified(ctx context.Context, orgID, domainID int64) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE org_domains SET verified_at = now() WHERE id = $1 AND org_id = $2`, domainID, orgID)
	if err != nil {
		return fmt.Errorf("verify org domain: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteOrgDomain removes a custom domain (scoped to its org).
func (s *Store) DeleteOrgDomain(ctx context.Context, orgID, domainID int64) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM org_domains WHERE id = $1 AND org_id = $2`, domainID, orgID)
	if err != nil {
		return fmt.Errorf("delete org domain: %w", err)
	}
	return nil
}

// OrgByVerifiedDomain resolves a request hostname to its org, only if the domain
// has been verified. Returns (nil, false, nil) when no verified domain matches.
func (s *Store) OrgByVerifiedDomain(ctx context.Context, hostname string) (*Org, bool, error) {
	var o Org
	err := s.pool.QueryRow(ctx, `
		SELECT o.id, o.slug, o.name, o.description, o.region, o.created_by, o.created_at
		FROM org_domains d JOIN organizations o ON o.id = d.org_id
		WHERE d.hostname = $1 AND d.verified_at IS NOT NULL`, hostname).
		Scan(&o.ID, &o.Slug, &o.Name, &o.Description, &o.Region, &o.CreatedBy, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("org by domain: %w", err)
	}
	return &o, true, nil
}
