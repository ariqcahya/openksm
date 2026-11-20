package org

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/openksm/openksm/backend/internal/iam"
)

// Unit represents an operational unit inside an organization.
type Unit struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	Name           string `json:"name"`
	UnitType       string `json:"unit_type"`
	Status         string `json:"status"`
}

// ServiceArea represents a region-agnostic geographic service area.
type ServiceArea struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	Name           string `json:"name"`
	RegionCode     string `json:"region_code"`
}

// EnsureUserRef resolves or creates a user_refs row for an external identity subject.
func (s *Service) EnsureUserRef(ctx context.Context, issuer, subject, email string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO user_refs (issuer, subject, email)
		VALUES ($1, $2, $3)
		ON CONFLICT (issuer, subject) DO UPDATE SET email = EXCLUDED.email
		RETURNING id::text
	`, issuer, subject, email).Scan(&id)
	return id, err
}

// GetMembershipContext loads a user's active membership and role grants for an organization.
func (s *Service) GetMembershipContext(ctx context.Context, orgID, userRefID string) (*iam.MembershipContext, error) {
	var membershipID, status string
	var expiresAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, status, expires_at
		FROM memberships
		WHERE organization_id = $1::uuid AND user_ref_id = $2::uuid
	`, orgID, userRefID).Scan(&membershipID, &status, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT role_code, COALESCE(service_area_id::text, ''), expires_at
		FROM role_assignments
		WHERE membership_id = $1::uuid
	`, membershipID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var grants []iam.RoleGrant
	for rows.Next() {
		var g iam.RoleGrant
		if err := rows.Scan(&g.RoleCode, &g.ServiceAreaID, &g.ExpiresAt); err != nil {
			return nil, err
		}
		grants = append(grants, g)
	}

	return &iam.MembershipContext{
		UserRefID:      userRefID,
		OrganizationID: orgID,
		Status:         status,
		ExpiresAt:      expiresAt,
		Roles:          grants,
	}, nil
}

// ListForUser returns organizations created by or having membership for userRefID.
func (s *Service) ListForUser(ctx context.Context, userRefID string) ([]Organization, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT o.id::text, o.name, o.slug, o.org_type, o.region_code, o.status, o.created_by::text, o.version
		FROM organizations o
		LEFT JOIN memberships m ON m.organization_id = o.id AND m.user_ref_id = $1::uuid AND m.status = 'active'
		WHERE o.created_by = $1::uuid OR m.id IS NOT NULL
		ORDER BY o.name ASC
	`, userRefID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Organization
	for rows.Next() {
		var o Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.OrgType, &o.RegionCode, &o.Status, &o.CreatedBy, &o.Version); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// CreateUnit creates an operational unit under an organization.
func (s *Service) CreateUnit(ctx context.Context, orgID, actorUserID, name, unitType string) (*Unit, error) {
	if name == "" || unitType == "" {
		return nil, fmt.Errorf("%w: name and unit_type are required", ErrInvalidInput)
	}
	var u Unit
	err := s.pool.QueryRow(ctx, `
		INSERT INTO operational_units (organization_id, name, unit_type)
		VALUES ($1::uuid, $2, $3)
		RETURNING id::text, organization_id::text, name, unit_type, status
	`, orgID, name, unitType).Scan(&u.ID, &u.OrganizationID, &u.Name, &u.UnitType, &u.Status)
	if err != nil {
		return nil, err
	}
	_, _ = s.pool.Exec(ctx, `
		INSERT INTO registry_audit_events (actor_ref, organization_id, resource_type, resource_id, action, outcome, correlation_id)
		VALUES ($1::uuid, $2::uuid, 'operational_unit', $3::uuid, 'unit:create', 'succeeded', $4::uuid)
	`, actorUserID, orgID, u.ID, uuid.NewString())
	return &u, nil
}

// CreateServiceArea creates a region-agnostic service area under an organization.
func (s *Service) CreateServiceArea(ctx context.Context, orgID, actorUserID, name, regionCode string) (*ServiceArea, error) {
	if name == "" || regionCode == "" {
		return nil, fmt.Errorf("%w: name and region_code are required", ErrInvalidInput)
	}
	var sa ServiceArea
	err := s.pool.QueryRow(ctx, `
		INSERT INTO service_areas (organization_id, name, region_code)
		VALUES ($1::uuid, $2, $3)
		RETURNING id::text, organization_id::text, name, region_code
	`, orgID, name, regionCode).Scan(&sa.ID, &sa.OrganizationID, &sa.Name, &sa.RegionCode)
	if err != nil {
		return nil, err
	}
	_, _ = s.pool.Exec(ctx, `
		INSERT INTO registry_audit_events (actor_ref, organization_id, resource_type, resource_id, action, outcome, correlation_id)
		VALUES ($1::uuid, $2::uuid, 'service_area', $3::uuid, 'service_area:create', 'succeeded', $4::uuid)
	`, actorUserID, orgID, sa.ID, uuid.NewString())
	return &sa, nil
}

// MembershipView represents a member and their role assignments within an organization.
type MembershipView struct {
	ID             string   `json:"id"`
	OrganizationID string   `json:"organization_id"`
	UserRefID      string   `json:"user_ref_id"`
	Email          string   `json:"email"`
	Status         string   `json:"status"`
	Roles          []string `json:"roles"`
}

// ListMemberships returns all memberships for an organization.
func (s *Service) ListMemberships(ctx context.Context, orgID string) ([]MembershipView, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT m.id::text, m.organization_id::text, m.user_ref_id::text, u.email, m.status,
		       COALESCE(array_agg(ra.role_code) FILTER (WHERE ra.role_code IS NOT NULL), '{}')
		FROM memberships m
		JOIN user_refs u ON u.id = m.user_ref_id
		LEFT JOIN role_assignments ra ON ra.membership_id = m.id
		WHERE m.organization_id = $1::uuid
		GROUP BY m.id, u.email
		ORDER BY m.created_at ASC
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MembershipView
	for rows.Next() {
		var mv MembershipView
		if err := rows.Scan(&mv.ID, &mv.OrganizationID, &mv.UserRefID, &mv.Email, &mv.Status, &mv.Roles); err != nil {
			return nil, err
		}
		out = append(out, mv)
	}
	return out, rows.Err()
}

// InviteMembership adds or updates an active membership and role assignment for an email.
func (s *Service) InviteMembership(ctx context.Context, orgID, actorUserID, email, roleCode, serviceAreaID string) (*MembershipView, error) {
	if email == "" || roleCode == "" {
		return nil, fmt.Errorf("%w: email and role_code are required", ErrInvalidInput)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var userRefID string
	err = tx.QueryRow(ctx, `
		INSERT INTO user_refs (issuer, subject, email)
		VALUES ('invited', $1, $1)
		ON CONFLICT (issuer, subject) DO UPDATE SET email = EXCLUDED.email
		RETURNING id::text
	`, email).Scan(&userRefID)
	if err != nil {
		return nil, err
	}

	var membershipID, status string
	err = tx.QueryRow(ctx, `
		INSERT INTO memberships (organization_id, user_ref_id, status)
		VALUES ($1::uuid, $2::uuid, 'active')
		ON CONFLICT (organization_id, user_ref_id) DO UPDATE SET status = 'active', updated_at = now()
		RETURNING id::text, status
	`, orgID, userRefID).Scan(&membershipID, &status)
	if err != nil {
		return nil, err
	}

	var saPtr *string
	if serviceAreaID != "" {
		saPtr = &serviceAreaID
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO role_assignments (membership_id, role_code, service_area_id, granted_by)
		VALUES ($1::uuid, $2, $3::uuid, $4::uuid)
	`, membershipID, roleCode, saPtr, actorUserID); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO registry_audit_events (actor_ref, organization_id, resource_type, resource_id, action, outcome, correlation_id)
		VALUES ($1::uuid, $2::uuid, 'membership', $3::uuid, 'membership:invite', 'succeeded', $4::uuid)
	`, actorUserID, orgID, membershipID, uuid.NewString()); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &MembershipView{
		ID:             membershipID,
		OrganizationID: orgID,
		UserRefID:      userRefID,
		Email:          email,
		Status:         status,
		Roles:          []string{roleCode},
	}, nil
}

// ServiceAreaExists verifies that serviceAreaID belongs to orgID (FR-007 enrollment guard).
func (s *Service) ServiceAreaExists(ctx context.Context, orgID, serviceAreaID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM service_areas WHERE id = $1::uuid AND organization_id = $2::uuid
		)
	`, serviceAreaID, orgID).Scan(&exists)
	return exists, err
}

// RevokeMembership revokes a membership inside an organization.
func (s *Service) RevokeMembership(ctx context.Context, orgID, actorUserID, membershipID string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE memberships SET status = 'revoked', updated_at = now()
		WHERE id = $1::uuid AND organization_id = $2::uuid
	`, membershipID, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, _ = s.pool.Exec(ctx, `
		INSERT INTO registry_audit_events (actor_ref, organization_id, resource_type, resource_id, action, outcome, correlation_id)
		VALUES ($1::uuid, $2::uuid, 'membership', $3::uuid, 'membership:revoke', 'succeeded', $4::uuid)
	`, actorUserID, orgID, membershipID, uuid.NewString())
	return nil
}
