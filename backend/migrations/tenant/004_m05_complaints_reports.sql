-- OpenKSM Tenant Database Migration 004 (M05: Complaints SLA/Timeline & Scoped Reporting)

CREATE TABLE IF NOT EXISTS complaints (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    service_point_id uuid REFERENCES service_points(id),
    title            text NOT NULL,
    description      text NOT NULL,
    state            text NOT NULL CHECK (state IN ('open', 'triaged', 'assigned', 'in_progress', 'resolved', 'closed', 'reopened', 'escalated')),
    assignee_ref     uuid,
    due_at           timestamptz NOT NULL,
    version          integer NOT NULL DEFAULT 1,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS complaint_events (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    complaint_id uuid NOT NULL REFERENCES complaints(id),
    actor_ref    uuid NOT NULL,
    event_type   text NOT NULL,
    note         text,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_complaint_events_cid ON complaint_events(complaint_id, created_at ASC);
