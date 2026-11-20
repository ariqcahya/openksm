-- OpenKSM Tenant Database Migration 002 (M03: Customers, Service Points, Collections & Offline Sync)

CREATE TABLE IF NOT EXISTS customers (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    display_name text NOT NULL,
    phone_masked text,
    status       text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS service_points (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id     uuid NOT NULL REFERENCES customers(id),
    label           text NOT NULL,
    address_text    text NOT NULL,
    service_area_id uuid,
    status          text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_service_points_customer ON service_points(customer_id);

CREATE TABLE IF NOT EXISTS collection_jobs (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title             text NOT NULL,
    assigned_user_ref uuid NOT NULL,
    service_area_id   uuid,
    state             text NOT NULL CHECK (state IN ('scheduled', 'assigned', 'in_progress', 'completed', 'verified', 'discrepancy')),
    scheduled_at      timestamptz NOT NULL,
    version           integer NOT NULL DEFAULT 1,
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_collection_jobs_assignee ON collection_jobs(assigned_user_ref, scheduled_at);

CREATE TABLE IF NOT EXISTS collection_stops (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id           uuid NOT NULL REFERENCES collection_jobs(id),
    service_point_id uuid NOT NULL REFERENCES service_points(id),
    seq              integer NOT NULL CHECK (seq > 0),
    state            text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'completed', 'failed_pickup', 'rescheduled')),
    UNIQUE (job_id, seq)
);

CREATE TABLE IF NOT EXISTS pickup_events (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    stop_id         uuid NOT NULL REFERENCES collection_stops(id),
    event_type      text NOT NULL CHECK (event_type IN ('completed', 'failed_pickup', 'rescheduled')),
    idempotency_key text NOT NULL UNIQUE,
    occurred_at     timestamptz NOT NULL,
    reason          text,
    created_by      uuid NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);
