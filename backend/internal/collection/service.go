package collection

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openksm/openksm/backend/internal/outbox"
)

var (
	ErrInvalidInput = errors.New("invalid collection input")
)

type Customer struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	PhoneMasked string `json:"phone_masked,omitempty"`
	Status      string `json:"status"`
}

type ServicePoint struct {
	ID            string `json:"id"`
	CustomerID    string `json:"customer_id"`
	Label         string `json:"label"`
	AddressText   string `json:"address_text"`
	ServiceAreaID string `json:"service_area_id,omitempty"`
	Status        string `json:"status"`
}

type CollectionJob struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	AssignedUserRef string    `json:"assigned_user_ref"`
	ServiceAreaID   string    `json:"service_area_id,omitempty"`
	State           string    `json:"state"`
	ScheduledAt     time.Time `json:"scheduled_at"`
	Version         int       `json:"version"`
}

type OfflinePickupEvent struct {
	StopID         string    `json:"stop_id"`
	EventType      string    `json:"event_type"` // completed, failed_pickup, rescheduled
	IdempotencyKey string    `json:"idempotency_key"`
	OccurredAt     time.Time `json:"occurred_at"`
	Reason         string    `json:"reason,omitempty"`
}

type SyncResult struct {
	IdempotencyKey string `json:"idempotency_key"`
	Status         string `json:"status"` // applied | duplicate | error
	Detail         string `json:"detail,omitempty"`
}

type TaskItem struct {
	ID           string `json:"id"`
	BusinessType string `json:"business_type"`
	BusinessID   string `json:"business_id"`
	AssigneeRef  string `json:"assignee_ref"`
	State        string `json:"state"`
	Priority     string `json:"priority"`
}

// Service manages M03 customers, service points, collection jobs, and offline sync.
type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (s *Service) CreateCustomer(ctx context.Context, displayName, phoneMasked string) (*Customer, error) {
	if displayName == "" {
		return nil, fmt.Errorf("%w: display_name required", ErrInvalidInput)
	}
	var c Customer
	err := s.pool.QueryRow(ctx, `
		INSERT INTO customers (display_name, phone_masked)
		VALUES ($1, $2)
		RETURNING id::text, display_name, COALESCE(phone_masked,''), status
	`, displayName, phoneMasked).Scan(&c.ID, &c.DisplayName, &c.PhoneMasked, &c.Status)
	return &c, err
}

func (s *Service) ListCustomers(ctx context.Context) ([]Customer, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, display_name, COALESCE(phone_masked,''), status
		FROM customers
		ORDER BY created_at DESC
		LIMIT 100
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Customer
	for rows.Next() {
		var c Customer
		if err := rows.Scan(&c.ID, &c.DisplayName, &c.PhoneMasked, &c.Status); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Service) ListServicePoints(ctx context.Context) ([]ServicePoint, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id::text, label, address_text, COALESCE(service_area_id::text,''), status
		FROM service_points
		ORDER BY created_at DESC
		LIMIT 100
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServicePoint
	for rows.Next() {
		var sp ServicePoint
		if err := rows.Scan(&sp.ID, &sp.CustomerID, &sp.Label, &sp.AddressText, &sp.ServiceAreaID, &sp.Status); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

func (s *Service) CreateServicePoint(ctx context.Context, customerID, label, addressText, serviceAreaID string) (*ServicePoint, error) {
	if customerID == "" || label == "" || addressText == "" {
		return nil, fmt.Errorf("%w: customer_id, label, and address_text required", ErrInvalidInput)
	}
	var saPtr *string
	if serviceAreaID != "" {
		saPtr = &serviceAreaID
	}
	var sp ServicePoint
	err := s.pool.QueryRow(ctx, `
		INSERT INTO service_points (customer_id, label, address_text, service_area_id)
		VALUES ($1::uuid, $2, $3, $4::uuid)
		RETURNING id::text, customer_id::text, label, address_text, COALESCE(service_area_id::text,''), status
	`, customerID, label, addressText, saPtr).Scan(&sp.ID, &sp.CustomerID, &sp.Label, &sp.AddressText, &sp.ServiceAreaID, &sp.Status)
	return &sp, err
}

func (s *Service) CreateJob(ctx context.Context, actorUserID, title, assignedUserRef, serviceAreaID string, scheduledAt time.Time, stopPointIDs []string) (*CollectionJob, error) {
	if title == "" || assignedUserRef == "" || len(stopPointIDs) == 0 {
		return nil, fmt.Errorf("%w: title, assigned_user_ref, and at least one stop are required", ErrInvalidInput)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var saPtr *string
	if serviceAreaID != "" {
		saPtr = &serviceAreaID
	}
	var job CollectionJob
	err = tx.QueryRow(ctx, `
		INSERT INTO collection_jobs (title, assigned_user_ref, service_area_id, state, scheduled_at)
		VALUES ($1, $2::uuid, $3::uuid, 'assigned', $4)
		RETURNING id::text, title, assigned_user_ref::text, COALESCE(service_area_id::text,''), state, scheduled_at, version
	`, title, assignedUserRef, saPtr, scheduledAt).Scan(
		&job.ID, &job.Title, &job.AssignedUserRef, &job.ServiceAreaID, &job.State, &job.ScheduledAt, &job.Version,
	)
	if err != nil {
		return nil, err
	}

	for i, spID := range stopPointIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO collection_stops (job_id, service_point_id, seq)
			VALUES ($1::uuid, $2::uuid, $3)
		`, job.ID, spID, i+1); err != nil {
			return nil, err
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO tasks (business_type, business_id, assignee_ref, state, priority, due_at)
		VALUES ('collection_job', $1::uuid, $2::uuid, 'open', 'normal', $3)
	`, job.ID, assignedUserRef, scheduledAt); err != nil {
		return nil, err
	}

	if err := outbox.Enqueue(ctx, tx, "collection_job", job.ID, "collection.job_assigned", map[string]string{
		"job_id":            job.ID,
		"assigned_user_ref": assignedUserRef,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &job, nil
}

// ListAssignedJobs enforces FR-008: officers only see jobs assigned to them.
func (s *Service) ListAssignedJobs(ctx context.Context, userRefID string) ([]CollectionJob, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, title, assigned_user_ref::text, COALESCE(service_area_id::text,''), state, scheduled_at, version
		FROM collection_jobs
		WHERE assigned_user_ref = $1::uuid
		ORDER BY scheduled_at ASC
	`, userRefID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CollectionJob
	for rows.Next() {
		var j CollectionJob
		if err := rows.Scan(&j.ID, &j.Title, &j.AssignedUserRef, &j.ServiceAreaID, &j.State, &j.ScheduledAt, &j.Version); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// SyncPickupEvents processes offline-queued pickup events idempotently (FR-009, FR-010).
func (s *Service) SyncPickupEvents(ctx context.Context, actorUserID string, events []OfflinePickupEvent) ([]SyncResult, error) {
	results := make([]SyncResult, 0, len(events))
	for _, ev := range events {
		if ev.IdempotencyKey == "" || ev.StopID == "" || (ev.EventType != "completed" && ev.EventType != "failed_pickup" && ev.EventType != "rescheduled") {
			results = append(results, SyncResult{IdempotencyKey: ev.IdempotencyKey, Status: "error", Detail: "invalid event fields"})
			continue
		}

		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return nil, err
		}

		var insertedID string
		err = tx.QueryRow(ctx, `
			INSERT INTO pickup_events (stop_id, event_type, idempotency_key, occurred_at, reason, created_by)
			VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid)
			ON CONFLICT (idempotency_key) DO NOTHING
			RETURNING id::text
		`, ev.StopID, ev.EventType, ev.IdempotencyKey, ev.OccurredAt, ev.Reason, actorUserID).Scan(&insertedID)

		if err != nil && err.Error() == "no rows in result set" {
			_ = tx.Rollback(ctx)
			results = append(results, SyncResult{IdempotencyKey: ev.IdempotencyKey, Status: "duplicate"})
			continue
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			results = append(results, SyncResult{IdempotencyKey: ev.IdempotencyKey, Status: "error", Detail: err.Error()})
			continue
		}

		if _, err := tx.Exec(ctx, `UPDATE collection_stops SET state = $2 WHERE id = $1::uuid`, ev.StopID, ev.EventType); err != nil {
			_ = tx.Rollback(ctx)
			return nil, err
		}

		if ev.EventType == "failed_pickup" {
			if _, err := tx.Exec(ctx, `
				INSERT INTO tasks (business_type, business_id, assignee_ref, state, priority)
				VALUES ('pickup_exception', $1::uuid, $2::uuid, 'open', 'high')
			`, ev.StopID, actorUserID); err != nil {
				_ = tx.Rollback(ctx)
				return nil, err
			}
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_events (actor_ref, resource_type, resource_id, action, outcome, correlation_id)
			VALUES ($1::uuid, 'collection_stop', $2::uuid, $3, 'succeeded', $4::uuid)
		`, actorUserID, ev.StopID, "pickup:"+ev.EventType, uuid.NewString()); err != nil {
			_ = tx.Rollback(ctx)
			return nil, err
		}

		if err := outbox.Enqueue(ctx, tx, "collection_stop", ev.StopID, "collection.pickup_"+ev.EventType, map[string]string{
			"stop_id":         ev.StopID,
			"idempotency_key": ev.IdempotencyKey,
		}); err != nil {
			_ = tx.Rollback(ctx)
			return nil, err
		}

		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		results = append(results, SyncResult{IdempotencyKey: ev.IdempotencyKey, Status: "applied"})
	}
	return results, nil
}

// ListTasks returns tasks assigned to the user (FR-016).
func (s *Service) ListTasks(ctx context.Context, userRefID string) ([]TaskItem, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, business_type, business_id::text, assignee_ref::text, state, priority
		FROM tasks
		WHERE assignee_ref = $1::uuid
		ORDER BY created_at DESC
		LIMIT 100
	`, userRefID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TaskItem
	for rows.Next() {
		var t TaskItem
		if err := rows.Scan(&t.ID, &t.BusinessType, &t.BusinessID, &t.AssigneeRef, &t.State, &t.Priority); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
