-- OpenKSM Tenant Database Migration 006 (NFR-SEC-06 / FR-028: Scoped Private Attachments)

CREATE TABLE IF NOT EXISTS object_attachments (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_type text NOT NULL CHECK (resource_type IN ('pickup_event', 'complaint', 'payment')),
    resource_id   uuid NOT NULL,
    bucket_name   text NOT NULL,
    object_key    text NOT NULL,
    content_type  text NOT NULL,
    size_bytes    bigint NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 5242880),
    uploaded_by   uuid NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_attachments_resource ON object_attachments(resource_type, resource_id);
