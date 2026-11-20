-- OpenKSM Registry Database Migration 001
-- Deployed to dedicated IDNBase-managed Registry PostgreSQL database.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS organizations (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name               text NOT NULL,
    slug               text NOT NULL UNIQUE,
    org_type           text NOT NULL CHECK (org_type IN ('ksm', 'tps3r', 'tpst', 'waste_bank', 'pemda', 'ngo', 'cooperative', 'private')),
    region_code        text NOT NULL,
    status             text NOT NULL CHECK (status IN ('draft', 'pending', 'approved', 'provisioning', 'active', 'revision_required', 'rejected', 'inactive', 'provisioning_failed')),
    idnbase_project_id uuid,
    created_by         uuid NOT NULL,
    approved_by        uuid,
    version            integer NOT NULL DEFAULT 1,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CHECK (approved_by IS NULL OR approved_by <> created_by)
);

CREATE TABLE IF NOT EXISTS operational_units (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id),
    name            text NOT NULL,
    unit_type       text NOT NULL CHECK (unit_type IN ('collection_team', 'tps3r', 'tpst', 'waste_bank', 'facility', 'office')),
    status          text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_operational_units_org ON operational_units(organization_id);

CREATE TABLE IF NOT EXISTS service_areas (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id),
    name            text NOT NULL,
    region_code     text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_service_areas_org ON service_areas(organization_id);

CREATE TABLE IF NOT EXISTS unit_service_areas (
    unit_id         uuid NOT NULL REFERENCES operational_units(id),
    service_area_id uuid NOT NULL REFERENCES service_areas(id),
    PRIMARY KEY (unit_id, service_area_id)
);

CREATE TABLE IF NOT EXISTS user_refs (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    issuer     text NOT NULL,
    subject    text NOT NULL,
    email      text NOT NULL,
    status     text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (issuer, subject)
);

CREATE TABLE IF NOT EXISTS memberships (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id),
    user_ref_id     uuid NOT NULL REFERENCES user_refs(id),
    status          text NOT NULL CHECK (status IN ('invited', 'active', 'suspended', 'revoked')),
    expires_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, user_ref_id)
);
CREATE INDEX IF NOT EXISTS idx_memberships_user ON memberships(user_ref_id, status);

CREATE TABLE IF NOT EXISTS role_assignments (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    membership_id   uuid NOT NULL REFERENCES memberships(id),
    role_code       text NOT NULL,
    service_area_id uuid REFERENCES service_areas(id),
    granted_by      uuid NOT NULL,
    expires_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_role_assignments_membership ON role_assignments(membership_id);

CREATE TABLE IF NOT EXISTS database_refs (
    organization_id       uuid PRIMARY KEY REFERENCES organizations(id),
    idnbase_project_id    uuid NOT NULL,
    connection_secret_ref text NOT NULL,
    schema_version        integer NOT NULL DEFAULT 0,
    updated_at            timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS idempotency_records (
    idempotency_key text PRIMARY KEY,
    actor_id        uuid NOT NULL,
    operation       text NOT NULL,
    status_code     integer NOT NULL,
    response_body   jsonb NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS registry_audit_events (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_ref      uuid,
    organization_id uuid,
    resource_type  text NOT NULL,
    resource_id    uuid NOT NULL,
    action         text NOT NULL,
    outcome        text NOT NULL CHECK (outcome IN ('allowed', 'denied', 'succeeded', 'failed')),
    correlation_id uuid NOT NULL,
    event_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_registry_audit_org_time ON registry_audit_events(organization_id, event_at DESC);

CREATE TABLE IF NOT EXISTS outbox_events (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type text NOT NULL,
    aggregate_id   uuid NOT NULL,
    event_type     text NOT NULL,
    payload        jsonb NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    processed_at   timestamptz,
    retries        integer NOT NULL DEFAULT 0,
    last_error     text
);
CREATE INDEX IF NOT EXISTS idx_registry_outbox_pending ON outbox_events(created_at) WHERE processed_at IS NULL;
