-- OpenKSM Tenant Database Migration 003 (M04: Tariffs, Subscriptions, Unique Invoices, Payments & Balanced Ledger)

CREATE TABLE IF NOT EXISTS tariffs (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    label          text NOT NULL,
    amount_idr     bigint NOT NULL CHECK (amount_idr >= 0),
    effective_from date NOT NULL,
    effective_to   date,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CHECK (effective_to IS NULL OR effective_to > effective_from)
);

CREATE TABLE IF NOT EXISTS subscriptions (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    service_point_id uuid NOT NULL REFERENCES service_points(id),
    tariff_id        uuid NOT NULL REFERENCES tariffs(id),
    started_at       date NOT NULL,
    ended_at         date,
    status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'ended')),
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS invoices (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subscription_id uuid NOT NULL REFERENCES subscriptions(id),
    billing_period  date NOT NULL,
    amount_idr      bigint NOT NULL CHECK (amount_idr >= 0),
    status          text NOT NULL CHECK (status IN ('draft', 'issued', 'partially_paid', 'paid', 'voided', 'disputed')),
    version         integer NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (subscription_id, billing_period)
);

CREATE TABLE IF NOT EXISTS payments (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id      uuid NOT NULL REFERENCES invoices(id),
    idempotency_key text NOT NULL UNIQUE,
    amount_idr      bigint NOT NULL CHECK (amount_idr > 0),
    method          text NOT NULL DEFAULT 'cash' CHECK (method IN ('cash', 'transfer')),
    status          text NOT NULL CHECK (status IN ('submitted', 'under_review', 'accepted', 'posted', 'rejected', 'reversed')),
    created_by      uuid NOT NULL,
    verified_by     uuid,
    version         integer NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (verified_by IS NULL OR verified_by <> created_by)
);

CREATE TABLE IF NOT EXISTS ledger_entries (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_id       uuid NOT NULL REFERENCES payments(id),
    amount_delta_idr bigint NOT NULL,
    account_code     text NOT NULL,
    event_type       text NOT NULL,
    reversal_of      uuid REFERENCES ledger_entries(id),
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_ledger_payment ON ledger_entries(payment_id);

CREATE TABLE IF NOT EXISTS adjustment_requests (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id       uuid NOT NULL REFERENCES invoices(id),
    amount_delta_idr bigint NOT NULL CHECK (amount_delta_idr <> 0),
    reason           text NOT NULL,
    status           text NOT NULL CHECK (status IN ('requested', 'approved', 'rejected')),
    requested_by     uuid NOT NULL,
    reviewed_by      uuid,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CHECK (reviewed_by IS NULL OR reviewed_by <> requested_by)
);
