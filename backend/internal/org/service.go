package org

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openksm/openksm/backend/internal/iam"
	"github.com/openksm/openksm/backend/internal/outbox"
	"github.com/openksm/openksm/backend/internal/platform/idnbase"
)

var (
	ErrInvalidInput      = errors.New("invalid organization input")
	ErrInvalidTransition = errors.New("invalid workflow state transition")
	ErrNotFound          = errors.New("organization not found")
	slugRegex            = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,48}[a-z0-9]$`)
)

// Organization mirrors a registry.organizations row.
type Organization struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Slug       string `json:"slug"`
	OrgType    string `json:"org_type"`
	RegionCode string `json:"region_code"`
	Status     string `json:"status"`
	CreatedBy  string `json:"created_by"`
	Version    int    `json:"version"`
}

// ApplyInput defines UC-ORG-01 application fields.
type ApplyInput struct {
	Name       string `json:"name"`
	Slug       string `json:"slug"`
	OrgType    string `json:"org_type"`
	RegionCode string `json:"region_code"`
}

// Service manages WF-ORG, units, and region-agnostic service areas.
type Service struct {
	pool *pgxpool.Pool
}

// NewService constructs an org service backed by the Registry DB pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// Apply creates a pending organization application with transactional audit + outbox.
func (s *Service) Apply(ctx context.Context, actorUserID string, in ApplyInput) (*Organization, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	in.RegionCode = strings.TrimSpace(in.RegionCode)
	if in.Name == "" || !slugRegex.MatchString(in.Slug) || in.RegionCode == "" {
		return nil, fmt.Errorf("%w: name, valid slug, and region_code are required", ErrInvalidInput)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var org Organization
	err = tx.QueryRow(ctx, `
		INSERT INTO organizations (name, slug, org_type, region_code, status, created_by)
		VALUES ($1, $2, $3, $4, 'pending', $5::uuid)
		RETURNING id::text, name, slug, org_type, region_code, status, created_by::text, version
	`, in.Name, in.Slug, in.OrgType, in.RegionCode, actorUserID).Scan(
		&org.ID, &org.Name, &org.Slug, &org.OrgType, &org.RegionCode, &org.Status, &org.CreatedBy, &org.Version,
	)
	if err != nil {
		return nil, err
	}

	corrID := uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO registry_audit_events (actor_ref, organization_id, resource_type, resource_id, action, outcome, correlation_id)
		VALUES ($1::uuid, $2::uuid, 'organization', $2::uuid, 'organization:apply', 'succeeded', $3::uuid)
	`, actorUserID, org.ID, corrID); err != nil {
		return nil, err
	}

	if err := outbox.Enqueue(ctx, tx, "organization", org.ID, "organization.applied", map[string]string{
		"organization_id": org.ID,
		"slug":            org.Slug,
		"region_code":     org.RegionCode,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &org, nil
}

// Decide enforces WF-ORG maker-checker transitions (pending -> approved | revision_required | rejected).
func (s *Service) Decide(ctx context.Context, orgID, approverUserID, decision string) (*Organization, error) {
	if decision != "approved" && decision != "revision_required" && decision != "rejected" {
		return nil, fmt.Errorf("%w: unsupported decision %q", ErrInvalidInput, decision)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var currentStatus, createdBy string
	var version int
	err = tx.QueryRow(ctx, `
		SELECT status, created_by::text, version
		FROM organizations
		WHERE id = $1::uuid
		FOR UPDATE
	`, orgID).Scan(&currentStatus, &createdBy, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	if currentStatus != "pending" {
		return nil, fmt.Errorf("%w: cannot decide from state %q", ErrInvalidTransition, currentStatus)
	}
	if err := iam.CheckMakerChecker(createdBy, approverUserID); err != nil {
		return nil, err
	}

	var org Organization
	err = tx.QueryRow(ctx, `
		UPDATE organizations
		SET status = $2, approved_by = $3::uuid, version = version + 1, updated_at = now()
		WHERE id = $1::uuid
		RETURNING id::text, name, slug, org_type, region_code, status, created_by::text, version
	`, orgID, decision, approverUserID).Scan(
		&org.ID, &org.Name, &org.Slug, &org.OrgType, &org.RegionCode, &org.Status, &org.CreatedBy, &org.Version,
	)
	if err != nil {
		return nil, err
	}

	corrID := uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO registry_audit_events (actor_ref, organization_id, resource_type, resource_id, action, outcome, correlation_id)
		VALUES ($1::uuid, $2::uuid, 'organization', $2::uuid, $3, 'succeeded', $4::uuid)
	`, approverUserID, org.ID, "organization:"+decision, corrID); err != nil {
		return nil, err
	}

	if err := outbox.Enqueue(ctx, tx, "organization", org.ID, "organization."+decision, map[string]string{
		"organization_id": org.ID,
		"decision":        decision,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &org, nil
}

// ProjectProvisioner abstracts IDNBase project provisioning for testability.
type ProjectProvisioner interface {
	ProvisionProject(ctx context.Context, sessionCookie, csrfToken, idempotencyKey string, payload idnbase.ProvisionProjectRequest) (*idnbase.ProvisionProjectResponse, error)
}

// Provision transitions an approved organization to provisioning -> active via IDNBase
// and records its database_refs + initial org_owner membership.
func (s *Service) Provision(ctx context.Context, orgID, actorUserID, idempotencyKey, sessionCookie, csrfToken string, prov ProjectProvisioner) (*Organization, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var org Organization
	err = tx.QueryRow(ctx, `
		SELECT id::text, name, slug, org_type, region_code, status, created_by::text, version
		FROM organizations
		WHERE id = $1::uuid
		FOR UPDATE
	`, orgID).Scan(&org.ID, &org.Name, &org.Slug, &org.OrgType, &org.RegionCode, &org.Status, &org.CreatedBy, &org.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if org.Status != "approved" && org.Status != "provisioning_failed" {
		return nil, fmt.Errorf("%w: cannot provision from state %q", ErrInvalidTransition, org.Status)
	}

	if _, err := tx.Exec(ctx, `UPDATE organizations SET status = 'provisioning', version = version + 1, updated_at = now() WHERE id = $1::uuid`, orgID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	res, perr := prov.ProvisionProject(ctx, sessionCookie, csrfToken, idempotencyKey, idnbase.ProvisionProjectRequest{
		Name:        org.Name,
		Slug:        org.Slug,
		Environment: "production",
	})
	if perr != nil {
		_, _ = s.pool.Exec(ctx, `UPDATE organizations SET status = 'provisioning_failed', version = version + 1, updated_at = now() WHERE id = $1::uuid`, orgID)
		return nil, perr
	}

	tx2, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx2.Rollback(ctx)

	err = tx2.QueryRow(ctx, `
		UPDATE organizations
		SET status = 'active', idnbase_project_id = $2::uuid, version = version + 1, updated_at = now()
		WHERE id = $1::uuid
		RETURNING id::text, name, slug, org_type, region_code, status, created_by::text, version
	`, orgID, res.ProjectID).Scan(&org.ID, &org.Name, &org.Slug, &org.OrgType, &org.RegionCode, &org.Status, &org.CreatedBy, &org.Version)
	if err != nil {
		return nil, err
	}

	secretRef := fmt.Sprintf("secret://idnbase/%s/api", res.ProjectID)
	if _, err := tx2.Exec(ctx, `
		INSERT INTO database_refs (organization_id, idnbase_project_id, connection_secret_ref, schema_version)
		VALUES ($1::uuid, $2::uuid, $3, 1)
		ON CONFLICT (organization_id) DO UPDATE SET idnbase_project_id = EXCLUDED.idnbase_project_id, connection_secret_ref = EXCLUDED.connection_secret_ref, updated_at = now()
	`, orgID, res.ProjectID, secretRef); err != nil {
		return nil, err
	}

	var membershipID string
	if err := tx2.QueryRow(ctx, `
		INSERT INTO memberships (organization_id, user_ref_id, status)
		VALUES ($1::uuid, $2::uuid, 'active')
		ON CONFLICT (organization_id, user_ref_id) DO UPDATE SET status = 'active', updated_at = now()
		RETURNING id::text
	`, orgID, org.CreatedBy).Scan(&membershipID); err != nil {
		return nil, err
	}
	if _, err := tx2.Exec(ctx, `
		INSERT INTO role_assignments (membership_id, role_code, granted_by)
		VALUES ($1::uuid, $2, $3::uuid)
	`, membershipID, iam.RoleOrgOwner, actorUserID); err != nil {
		return nil, err
	}

	if err := outbox.Enqueue(ctx, tx2, "organization", org.ID, "organization.provisioned", map[string]string{
		"organization_id":    org.ID,
		"idnbase_project_id": res.ProjectID,
	}); err != nil {
		return nil, err
	}
	if err := tx2.Commit(ctx); err != nil {
		return nil, err
	}
	return &org, nil
}
