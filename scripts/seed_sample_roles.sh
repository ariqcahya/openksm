#!/usr/bin/env bash
set -euo pipefail

# Seed 9 sample role users into openksm_registry for KSM Lestari Sleman (M07B verification).
ORG_ID="${1:-1980cab6-2aa9-463c-8965-658cc637beaf}"

ROLES=(
  "ketua@sleman.openksm.local:org_owner"
  "admin@sleman.openksm.local:org_admin"
  "bendahara@sleman.openksm.local:treasurer"
  "tagihan@sleman.openksm.local:billing_officer"
  "kasir@sleman.openksm.local:cashier"
  "operasi@sleman.openksm.local:ops_manager"
  "petugas@sleman.openksm.local:collection_officer"
  "cs@sleman.openksm.local:customer_service"
  "auditor@sleman.openksm.local:org_auditor"
)

for entry in "${ROLES[@]}"; do
  email="${entry%%:*}"
  role="${entry##*:}"
  docker exec openksm-postgres-1 psql -U openksm_registry_app -d openksm_registry -v ON_ERROR_STOP=1 -c "
    WITH u AS (
      INSERT INTO user_refs (issuer, subject, email)
      VALUES ('sample-role', '${email}', '${email}')
      ON CONFLICT (issuer, subject) DO UPDATE SET email = EXCLUDED.email
      RETURNING id
    ),
    m AS (
      INSERT INTO memberships (organization_id, user_ref_id, status)
      SELECT '${ORG_ID}'::uuid, u.id, 'active' FROM u
      ON CONFLICT (organization_id, user_ref_id) DO UPDATE SET status = 'active'
      RETURNING id
    )
    INSERT INTO role_assignments (membership_id, role_code, granted_by)
    SELECT m.id, '${role}', '58d2f3d2-9351-4c30-abf5-945d5df2155c'::uuid FROM m
    WHERE NOT EXISTS (
      SELECT 1 FROM role_assignments ra WHERE ra.membership_id = m.id AND ra.role_code = '${role}'
    );
  " >/dev/null
  echo "Seeded ${email} -> ${role}"
done
