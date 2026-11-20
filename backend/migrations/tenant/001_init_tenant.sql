-- OpenKSM Tenant Database Migration 001
-- Deployed to each organization's isolated IDNBase PostgreSQL database (p_<uuid>).

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS tasks (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    business_type text NOT NULL,
    business_id  uuid NOT NULL,
    assignee_ref uuid NOT NULL,
    state        text NOT NULL,
    priority     text NOT NULL DEFAULT 'normal',
    due_at       timestamptz,
    completed_at timestamptz,
    version      integer NOT NULL DEFAULT 1,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_tasks_assignee_state_due ON tasks(assignee_ref, state, due_at);

CREATE TABLE IF NOT EXISTS audit_events (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_ref      uuid,
    resource_type  text NOT NULL,
    resource_id    uuid NOT NULL,
    action         text NOT NULL,
    outcome        text NOT NULL CHECK (outcome IN ('allowed', 'denied', 'succeeded', 'failed')),
    correlation_id uuid NOT NULL,
    event_at       timestamptz NOT NULL DEFAULT now()
);

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
CREATE INDEX IF NOT EXISTS idx_tenant_outbox_pending ON outbox_events(created_at) WHERE processed_at IS NULL;

CREATE TABLE IF NOT EXISTS inbox_dedupe (
    event_id    uuid PRIMARY KEY,
    consumer    text NOT NULL,
    consumed_at timestamptz NOT NULL DEFAULT now()
);
