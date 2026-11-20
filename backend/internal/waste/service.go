package waste

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openksm/openksm/backend/internal/outbox"
)

var (
	ErrInvalidInput      = errors.New("invalid waste/inventory input")
	ErrInsufficientStock = errors.New("insufficient inventory stock")
	ErrNotFound          = errors.New("record not found")
)

type WeighingRecord struct {
	ID               string  `json:"id"`
	FacilityName     string  `json:"facility_name"`
	MaterialCategory string  `json:"material_category"`
	WeightKG         float64 `json:"weight_kg"`
	ScaleID          string  `json:"scale_id"`
	State            string  `json:"state"`
}

type InventoryBalance struct {
	MaterialCategory string  `json:"material_category"`
	AvailableKG      float64 `json:"available_kg"`
}

type SalesOrder struct {
	ID               string  `json:"id"`
	BuyerName        string  `json:"buyer_name"`
	MaterialCategory string  `json:"material_category"`
	QuantityKG       float64 `json:"quantity_kg"`
	TotalIDR         int64   `json:"total_idr"`
	State            string  `json:"state"`
}

type InterOrgTransfer struct {
	ID                string   `json:"id"`
	CounterpartyOrgID string   `json:"counterparty_org_id"`
	MaterialCategory  string   `json:"material_category"`
	ProposedKG        float64  `json:"proposed_kg"`
	ReceivedKG        *float64 `json:"received_kg,omitempty"`
	State             string   `json:"state"`
	Version           int      `json:"version"`
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// RecordIntake records facility weighing and credits inventory atomically (FR-019, FR-021).
func (s *Service) RecordIntake(ctx context.Context, actorUserID, facility, category, scaleID, idempotencyKey string, weightKG float64) (*WeighingRecord, error) {
	if facility == "" || category == "" || scaleID == "" || idempotencyKey == "" || weightKG <= 0 {
		return nil, fmt.Errorf("%w: facility, category, scale_id, idempotency_key, and weight_kg > 0 required", ErrInvalidInput)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var w WeighingRecord
	err = tx.QueryRow(ctx, `
		INSERT INTO weighing_records (facility_name, material_category, weight_kg, scale_id, state, created_by)
		VALUES ($1, $2, $3, $4, 'verified', $5::uuid)
		RETURNING id::text, facility_name, material_category, weight_kg::double precision, scale_id, state
	`, facility, category, weightKG, scaleID, actorUserID).Scan(&w.ID, &w.FacilityName, &w.MaterialCategory, &w.WeightKG, &w.ScaleID, &w.State)
	if err != nil {
		return nil, err
	}

	var moveID string
	err = tx.QueryRow(ctx, `
		INSERT INTO material_movements (material_category, delta_kg, source_type, source_id, idempotency_key)
		VALUES ($1, $2, 'weighing_intake', $3::uuid, $4)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id::text
	`, category, weightKG, w.ID, idempotencyKey).Scan(&moveID)
	if err == nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO inventory_balances (material_category, available_kg)
			VALUES ($1, $2)
			ON CONFLICT (material_category) DO UPDATE
			SET available_kg = inventory_balances.available_kg + EXCLUDED.available_kg, updated_at = now()
		`, category, weightKG); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &w, nil
}

func (s *Service) ListInventory(ctx context.Context) ([]InventoryBalance, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT material_category, available_kg::double precision
		FROM inventory_balances
		ORDER BY material_category ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InventoryBalance
	for rows.Next() {
		var b InventoryBalance
		if err := rows.Scan(&b.MaterialCategory, &b.AvailableKG); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// FulfillSale deducts inventory atomically; rejects if stock would go negative (FR-021, FR-022).
func (s *Service) FulfillSale(ctx context.Context, actorUserID, buyerName, category, idempotencyKey string, quantityKG float64, totalIDR int64) (*SalesOrder, error) {
	if buyerName == "" || category == "" || idempotencyKey == "" || quantityKG <= 0 || totalIDR < 0 {
		return nil, fmt.Errorf("%w: buyer_name, category, idempotency_key, quantity_kg > 0 required", ErrInvalidInput)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var avail float64
	err = tx.QueryRow(ctx, `
		SELECT available_kg::double precision FROM inventory_balances WHERE material_category = $1 FOR UPDATE
	`, category).Scan(&avail)
	if errors.Is(err, pgx.ErrNoRows) || avail < quantityKG {
		return nil, ErrInsufficientStock
	}
	if err != nil {
		return nil, err
	}

	var ord SalesOrder
	err = tx.QueryRow(ctx, `
		INSERT INTO sales_orders (buyer_name, material_category, quantity_kg, total_idr, state)
		VALUES ($1, $2, $3, $4, 'fulfilled')
		RETURNING id::text, buyer_name, material_category, quantity_kg::double precision, total_idr, state
	`, buyerName, category, quantityKG, totalIDR).Scan(&ord.ID, &ord.BuyerName, &ord.MaterialCategory, &ord.QuantityKG, &ord.TotalIDR, &ord.State)
	if err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO material_movements (material_category, delta_kg, source_type, source_id, idempotency_key)
		VALUES ($1, $2, 'sales_order', $3::uuid, $4)
	`, category, -quantityKG, ord.ID, idempotencyKey); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE inventory_balances SET available_kg = available_kg - $2, updated_at = now()
		WHERE material_category = $1
	`, category, quantityKG); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_ref, resource_type, resource_id, action, outcome, correlation_id)
		VALUES ($1::uuid, 'sales_order', $2::uuid, 'sales:fulfill', 'succeeded', $3::uuid)
	`, actorUserID, ord.ID, uuid.NewString()); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &ord, nil
}

// ProposeTransfer initiates an explicit inter-org transfer (FR-023 / WF-XFER).
func (s *Service) ProposeTransfer(ctx context.Context, actorUserID, counterpartyOrgID, category string, proposedKG float64) (*InterOrgTransfer, error) {
	if counterpartyOrgID == "" || category == "" || proposedKG <= 0 {
		return nil, fmt.Errorf("%w: counterparty_org_id, material_category, proposed_kg > 0 required", ErrInvalidInput)
	}
	var tr InterOrgTransfer
	err := s.pool.QueryRow(ctx, `
		INSERT INTO inter_org_transfers (counterparty_org_id, material_category, proposed_kg, state)
		VALUES ($1::uuid, $2, $3, 'proposed')
		RETURNING id::text, counterparty_org_id::text, material_category, proposed_kg::double precision, received_kg::double precision, state, version
	`, counterpartyOrgID, category, proposedKG).Scan(&tr.ID, &tr.CounterpartyOrgID, &tr.MaterialCategory, &tr.ProposedKG, &tr.ReceivedKG, &tr.State, &tr.Version)
	return &tr, err
}

// ReceiveTransfer reconciles bilateral received weight vs proposed weight (reconciled vs discrepancy).
func (s *Service) ReceiveTransfer(ctx context.Context, actorUserID, transferID string, receivedKG float64) (*InterOrgTransfer, error) {
	if receivedKG < 0 {
		return nil, fmt.Errorf("%w: received_kg must be >= 0", ErrInvalidInput)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var tr InterOrgTransfer
	err = tx.QueryRow(ctx, `
		SELECT id::text, counterparty_org_id::text, material_category, proposed_kg::double precision, received_kg::double precision, state, version
		FROM inter_org_transfers WHERE id = $1::uuid FOR UPDATE
	`, transferID).Scan(&tr.ID, &tr.CounterpartyOrgID, &tr.MaterialCategory, &tr.ProposedKG, &tr.ReceivedKG, &tr.State, &tr.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	nextState := "reconciled"
	if math.Abs(receivedKG-tr.ProposedKG) > 0.001 {
		nextState = "discrepancy"
	}

	err = tx.QueryRow(ctx, `
		UPDATE inter_org_transfers SET received_kg = $2, state = $3, version = version + 1
		WHERE id = $1::uuid
		RETURNING received_kg::double precision, state, version
	`, transferID, receivedKG, nextState).Scan(&tr.ReceivedKG, &tr.State, &tr.Version)
	if err != nil {
		return nil, err
	}

	if err := outbox.Enqueue(ctx, tx, "inter_org_transfer", tr.ID, "transfer."+nextState, map[string]any{
		"transfer_id": tr.ID,
		"proposed_kg": tr.ProposedKG,
		"received_kg": receivedKG,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &tr, nil
}
