package finance

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openksm/openksm/backend/internal/iam"
	"github.com/openksm/openksm/backend/internal/outbox"
)

var (
	ErrInvalidInput      = errors.New("invalid finance input")
	ErrDuplicateInvoice  = errors.New("invoice already exists for subscription and billing period")
	ErrInvalidTransition = errors.New("invalid payment transition")
	ErrNotFound          = errors.New("finance record not found")
)

type Tariff struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	AmountIDR     int64  `json:"amount_idr"`
	EffectiveFrom string `json:"effective_from"`
}

type Subscription struct {
	ID             string `json:"id"`
	ServicePointID string `json:"service_point_id"`
	TariffID       string `json:"tariff_id"`
	StartedAt      string `json:"started_at"`
	Status         string `json:"status"`
}

type Invoice struct {
	ID             string `json:"id"`
	SubscriptionID string `json:"subscription_id"`
	BillingPeriod  string `json:"billing_period"`
	AmountIDR      int64  `json:"amount_idr"`
	Status         string `json:"status"`
	Version        int    `json:"version"`
}

type Payment struct {
	ID             string  `json:"id"`
	InvoiceID      string  `json:"invoice_id"`
	IdempotencyKey string  `json:"idempotency_key"`
	AmountIDR      int64   `json:"amount_idr"`
	Method         string  `json:"method"`
	Status         string  `json:"status"`
	CreatedBy      string  `json:"created_by"`
	VerifiedBy     *string `json:"verified_by,omitempty"`
	Version        int     `json:"version"`
}

type LedgerSummary struct {
	NetBalanceIDR int64 `json:"net_balance_idr"`
	CashIDR       int64 `json:"cash_idr"`
	ReceivableIDR int64 `json:"receivable_idr"`
	EntryCount    int64 `json:"entry_count"`
}

type AdjustmentRequest struct {
	ID             string  `json:"id"`
	InvoiceID      string  `json:"invoice_id"`
	AmountDeltaIDR int64   `json:"amount_delta_idr"`
	Reason         string  `json:"reason"`
	Status         string  `json:"status"`
	RequestedBy    string  `json:"requested_by"`
	ReviewedBy     *string `json:"reviewed_by,omitempty"`
}

// Service manages M04 billing, payments (maker-checker), and balanced double-entry ledger.
type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (s *Service) CreateTariff(ctx context.Context, label string, amountIDR int64, effectiveFrom string) (*Tariff, error) {
	if label == "" || amountIDR < 0 || effectiveFrom == "" {
		return nil, fmt.Errorf("%w: label, amount_idr >= 0, effective_from required", ErrInvalidInput)
	}
	var t Tariff
	err := s.pool.QueryRow(ctx, `
		INSERT INTO tariffs (label, amount_idr, effective_from)
		VALUES ($1, $2, $3::date)
		RETURNING id::text, label, amount_idr, effective_from::text
	`, label, amountIDR, effectiveFrom).Scan(&t.ID, &t.Label, &t.AmountIDR, &t.EffectiveFrom)
	return &t, err
}

func (s *Service) CreateSubscription(ctx context.Context, servicePointID, tariffID, startedAt string) (*Subscription, error) {
	if servicePointID == "" || tariffID == "" || startedAt == "" {
		return nil, fmt.Errorf("%w: service_point_id, tariff_id, started_at required", ErrInvalidInput)
	}
	var sub Subscription
	err := s.pool.QueryRow(ctx, `
		INSERT INTO subscriptions (service_point_id, tariff_id, started_at)
		VALUES ($1::uuid, $2::uuid, $3::date)
		RETURNING id::text, service_point_id::text, tariff_id::text, started_at::text, status
	`, servicePointID, tariffID, startedAt).Scan(&sub.ID, &sub.ServicePointID, &sub.TariffID, &sub.StartedAt, &sub.Status)
	return &sub, err
}

// IssueInvoice creates an issued invoice; duplicate (subscription_id, billing_period) returns ErrDuplicateInvoice.
func (s *Service) IssueInvoice(ctx context.Context, actorUserID, subscriptionID, billingPeriod string) (*Invoice, error) {
	if subscriptionID == "" || billingPeriod == "" {
		return nil, fmt.Errorf("%w: subscription_id and billing_period required", ErrInvalidInput)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var amountIDR int64
	err = tx.QueryRow(ctx, `
		SELECT t.amount_idr
		FROM subscriptions sub
		JOIN tariffs t ON t.id = sub.tariff_id
		WHERE sub.id = $1::uuid AND sub.status = 'active'
	`, subscriptionID).Scan(&amountIDR)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	var inv Invoice
	err = tx.QueryRow(ctx, `
		INSERT INTO invoices (subscription_id, billing_period, amount_idr, status)
		VALUES ($1::uuid, $2::date, $3, 'issued')
		RETURNING id::text, subscription_id::text, billing_period::text, amount_idr, status, version
	`, subscriptionID, billingPeriod, amountIDR).Scan(&inv.ID, &inv.SubscriptionID, &inv.BillingPeriod, &inv.AmountIDR, &inv.Status, &inv.Version)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicateInvoice
		}
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_ref, resource_type, resource_id, action, outcome, correlation_id)
		VALUES ($1::uuid, 'invoice', $2::uuid, 'invoice:issue', 'succeeded', $3::uuid)
	`, actorUserID, inv.ID, uuid.NewString()); err != nil {
		return nil, err
	}

	if err := outbox.Enqueue(ctx, tx, "invoice", inv.ID, "billing.invoice_issued", map[string]any{
		"invoice_id": inv.ID,
		"amount_idr": inv.AmountIDR,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &inv, nil
}

func (s *Service) ListInvoices(ctx context.Context) ([]Invoice, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, subscription_id::text, billing_period::text, amount_idr, status, version
		FROM invoices
		ORDER BY billing_period DESC, created_at DESC
		LIMIT 100
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invoice
	for rows.Next() {
		var inv Invoice
		if err := rows.Scan(&inv.ID, &inv.SubscriptionID, &inv.BillingPeriod, &inv.AmountIDR, &inv.Status, &inv.Version); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// RecordPayment records a submitted payment idempotently (FR-012).
func (s *Service) RecordPayment(ctx context.Context, actorUserID, invoiceID, idempotencyKey, method string, amountIDR int64) (*Payment, error) {
	if invoiceID == "" || idempotencyKey == "" || amountIDR <= 0 {
		return nil, fmt.Errorf("%w: invoice_id, idempotency_key, and amount_idr > 0 required", ErrInvalidInput)
	}
	if method == "" {
		method = "cash"
	}

	var p Payment
	err := s.pool.QueryRow(ctx, `
		INSERT INTO payments (invoice_id, idempotency_key, amount_idr, method, status, created_by)
		VALUES ($1::uuid, $2, $3, $4, 'submitted', $5::uuid)
		ON CONFLICT (idempotency_key) DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
		RETURNING id::text, invoice_id::text, idempotency_key, amount_idr, method, status, created_by::text, verified_by::text, version
	`, invoiceID, idempotencyKey, amountIDR, method, actorUserID).Scan(
		&p.ID, &p.InvoiceID, &p.IdempotencyKey, &p.AmountIDR, &p.Method, &p.Status, &p.CreatedBy, &p.VerifiedBy, &p.Version,
	)
	return &p, err
}

// VerifyAndPostPayment enforces distinct Maker-Checker and writes balanced double-entry ledger rows (FR-012, FR-014).
func (s *Service) VerifyAndPostPayment(ctx context.Context, verifierUserID, paymentID string) (*Payment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var p Payment
	err = tx.QueryRow(ctx, `
		SELECT id::text, invoice_id::text, idempotency_key, amount_idr, method, status, created_by::text, verified_by::text, version
		FROM payments
		WHERE id = $1::uuid
		FOR UPDATE
	`, paymentID).Scan(&p.ID, &p.InvoiceID, &p.IdempotencyKey, &p.AmountIDR, &p.Method, &p.Status, &p.CreatedBy, &p.VerifiedBy, &p.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if p.Status != "submitted" {
		return nil, fmt.Errorf("%w: cannot verify payment in state %q", ErrInvalidTransition, p.Status)
	}
	if err := iam.CheckMakerChecker(p.CreatedBy, verifierUserID); err != nil {
		return nil, err
	}

	err = tx.QueryRow(ctx, `
		UPDATE payments
		SET status = 'posted', verified_by = $2::uuid, version = version + 1
		WHERE id = $1::uuid
		RETURNING status, verified_by::text, version
	`, paymentID, verifierUserID).Scan(&p.Status, &p.VerifiedBy, &p.Version)
	if err != nil {
		return nil, err
	}

	// Balanced double-entry postings: +amount Cash (1100), -amount Accounts Receivable (1200) -> net sum == 0.
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_entries (payment_id, amount_delta_idr, account_code, event_type)
		VALUES
			($1::uuid, $2, '1100:CASH', 'payment_posted'),
			($1::uuid, -$2, '1200:AR', 'payment_posted')
	`, p.ID, p.AmountIDR); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE invoices
		SET status = CASE WHEN amount_idr <= (
			SELECT COALESCE(SUM(amount_idr), 0) FROM payments WHERE invoice_id = $1::uuid AND status = 'posted'
		) THEN 'paid' ELSE 'partially_paid' END,
		version = version + 1
		WHERE id = $1::uuid
	`, p.InvoiceID); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_ref, resource_type, resource_id, action, outcome, correlation_id)
		VALUES ($1::uuid, 'payment', $2::uuid, 'payment:verify_post', 'succeeded', $3::uuid)
	`, verifierUserID, p.ID, uuid.NewString()); err != nil {
		return nil, err
	}

	if err := outbox.Enqueue(ctx, tx, "payment", p.ID, "finance.payment_posted", map[string]any{
		"payment_id": p.ID,
		"invoice_id": p.InvoiceID,
		"amount_idr": p.AmountIDR,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &p, nil
}

// ReversePayment posts compensating ledger entries without deleting posted records (FR-013).
func (s *Service) ReversePayment(ctx context.Context, actorUserID, paymentID string) (*Payment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var p Payment
	err = tx.QueryRow(ctx, `
		SELECT id::text, invoice_id::text, idempotency_key, amount_idr, method, status, created_by::text, verified_by::text, version
		FROM payments
		WHERE id = $1::uuid
		FOR UPDATE
	`, paymentID).Scan(&p.ID, &p.InvoiceID, &p.IdempotencyKey, &p.AmountIDR, &p.Method, &p.Status, &p.CreatedBy, &p.VerifiedBy, &p.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if p.Status != "posted" {
		return nil, fmt.Errorf("%w: only posted payments can be reversed", ErrInvalidTransition)
	}

	err = tx.QueryRow(ctx, `
		UPDATE payments SET status = 'reversed', version = version + 1
		WHERE id = $1::uuid
		RETURNING status, version
	`, paymentID).Scan(&p.Status, &p.Version)
	if err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_entries (payment_id, amount_delta_idr, account_code, event_type, reversal_of)
		SELECT payment_id, -amount_delta_idr, account_code, 'payment_reversed', id
		FROM ledger_entries
		WHERE payment_id = $1::uuid AND reversal_of IS NULL
	`, p.ID); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_ref, resource_type, resource_id, action, outcome, correlation_id)
		VALUES ($1::uuid, 'payment', $2::uuid, 'payment:reverse', 'succeeded', $3::uuid)
	`, actorUserID, p.ID, uuid.NewString()); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetLedgerSummary returns the double-entry net balance (invariant: NetBalanceIDR == 0).
func (s *Service) GetLedgerSummary(ctx context.Context) (*LedgerSummary, error) {
	var sum LedgerSummary
	err := s.pool.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(amount_delta_idr), 0),
			COALESCE(SUM(amount_delta_idr) FILTER (WHERE account_code = '1100:CASH'), 0),
			COALESCE(SUM(amount_delta_idr) FILTER (WHERE account_code = '1200:AR'), 0),
			COUNT(*)
		FROM ledger_entries
	`).Scan(&sum.NetBalanceIDR, &sum.CashIDR, &sum.ReceivableIDR, &sum.EntryCount)
	return &sum, err
}

// RequestAdjustment creates a maker-checker financial adjustment request (FR-014).
func (s *Service) RequestAdjustment(ctx context.Context, actorUserID, invoiceID, reason string, amountDeltaIDR int64) (*AdjustmentRequest, error) {
	if invoiceID == "" || reason == "" || amountDeltaIDR == 0 {
		return nil, fmt.Errorf("%w: invoice_id, reason, and non-zero amount_delta_idr required", ErrInvalidInput)
	}
	var adj AdjustmentRequest
	err := s.pool.QueryRow(ctx, `
		INSERT INTO adjustment_requests (invoice_id, amount_delta_idr, reason, status, requested_by)
		VALUES ($1::uuid, $2, $3, 'requested', $4::uuid)
		RETURNING id::text, invoice_id::text, amount_delta_idr, reason, status, requested_by::text, reviewed_by::text
	`, invoiceID, amountDeltaIDR, reason, actorUserID).Scan(
		&adj.ID, &adj.InvoiceID, &adj.AmountDeltaIDR, &adj.Reason, &adj.Status, &adj.RequestedBy, &adj.ReviewedBy,
	)
	return &adj, err
}

// DecideAdjustment enforces distinct Maker-Checker before applying an adjustment (FR-014).
func (s *Service) DecideAdjustment(ctx context.Context, reviewerUserID, adjID, decision string) (*AdjustmentRequest, error) {
	if decision != "approved" && decision != "rejected" {
		return nil, fmt.Errorf("%w: decision must be approved or rejected", ErrInvalidInput)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var adj AdjustmentRequest
	err = tx.QueryRow(ctx, `
		SELECT id::text, invoice_id::text, amount_delta_idr, reason, status, requested_by::text, reviewed_by::text
		FROM adjustment_requests
		WHERE id = $1::uuid
		FOR UPDATE
	`, adjID).Scan(&adj.ID, &adj.InvoiceID, &adj.AmountDeltaIDR, &adj.Reason, &adj.Status, &adj.RequestedBy, &adj.ReviewedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if adj.Status != "requested" {
		return nil, fmt.Errorf("%w: adjustment already %s", ErrInvalidTransition, adj.Status)
	}
	if err := iam.CheckMakerChecker(adj.RequestedBy, reviewerUserID); err != nil {
		return nil, err
	}

	err = tx.QueryRow(ctx, `
		UPDATE adjustment_requests SET status = $2, reviewed_by = $3::uuid
		WHERE id = $1::uuid
		RETURNING status, reviewed_by::text
	`, adjID, decision, reviewerUserID).Scan(&adj.Status, &adj.ReviewedBy)
	if err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_ref, resource_type, resource_id, action, outcome, correlation_id)
		VALUES ($1::uuid, 'adjustment_request', $2::uuid, $3, 'succeeded', $4::uuid)
	`, reviewerUserID, adj.ID, "adjustment:"+decision, uuid.NewString()); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &adj, nil
}
