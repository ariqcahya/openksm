-- name: GetOrganizationByID :one
SELECT id, name, slug, org_type, region_code, status, idnbase_project_id, created_by, version
FROM organizations
WHERE id = $1;

-- name: ListOrganizationsForUser :many
SELECT DISTINCT o.id, o.name, o.slug, o.org_type, o.region_code, o.status, o.created_by, o.version
FROM organizations o
LEFT JOIN memberships m ON m.organization_id = o.id AND m.user_ref_id = $1 AND m.status = 'active'
WHERE o.created_by = $1 OR m.id IS NOT NULL
ORDER BY o.name ASC;
