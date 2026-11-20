-- OpenKSM Tenant Database Migration 005 (M06: Waste Intake, Material Balance, Inventory, Sales & Inter-Org Transfers)

CREATE TABLE IF NOT EXISTS weighing_records (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    facility_name     text NOT NULL,
    material_category text NOT NULL,
    weight_kg         numeric(12,3) NOT NULL CHECK (weight_kg > 0),
    scale_id          text NOT NULL,
    state             text NOT NULL CHECK (state IN ('intake_recorded', 'verified', 'rejected_intake')),
    created_by        uuid NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS processing_batches (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    weighing_id uuid REFERENCES weighing_records(id),
    input_kg    numeric(12,3) NOT NULL CHECK (input_kg > 0),
    output_kg   numeric(12,3) NOT NULL CHECK (output_kg >= 0),
    residue_kg  numeric(12,3) NOT NULL CHECK (residue_kg >= 0),
    created_at  timestamptz NOT NULL DEFAULT now(),
    CHECK (output_kg + residue_kg <= input_kg * 1.05)
);

CREATE TABLE IF NOT EXISTS inventory_balances (
    material_category text PRIMARY KEY,
    available_kg      numeric(12,3) NOT NULL CHECK (available_kg >= 0),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS material_movements (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    material_category text NOT NULL,
    delta_kg          numeric(12,3) NOT NULL,
    source_type       text NOT NULL,
    source_id         uuid NOT NULL,
    idempotency_key   text NOT NULL UNIQUE,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sales_orders (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    buyer_name        text NOT NULL,
    material_category text NOT NULL,
    quantity_kg       numeric(12,3) NOT NULL CHECK (quantity_kg > 0),
    total_idr         bigint NOT NULL CHECK (total_idr >= 0),
    state             text NOT NULL CHECK (state IN ('ordered', 'fulfilled', 'cancelled')),
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS inter_org_transfers (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    counterparty_org_id uuid NOT NULL,
    material_category   text NOT NULL,
    proposed_kg         numeric(12,3) NOT NULL CHECK (proposed_kg > 0),
    received_kg         numeric(12,3),
    state               text NOT NULL CHECK (state IN ('proposed', 'accepted', 'dispatched', 'received', 'reconciled', 'discrepancy', 'rejected')),
    version             integer NOT NULL DEFAULT 1,
    created_at          timestamptz NOT NULL DEFAULT now()
);
