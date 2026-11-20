package support

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openksm/openksm/backend/internal/outbox"
)

var (
	ErrInvalidInput      = errors.New("invalid support/report input")
	ErrInvalidTransition = errors.New("invalid complaint workflow transition")
	ErrNotFound          = errors.New("complaint not found")
)

var allowedComplaintTransitions = map[string]map[string]bool{
	"open":        {"triaged": true, "assigned": true, "escalated": true},
	"triaged":     {"assigned": true, "in_progress": true, "escalated": true},
	"assigned":    {"in_progress": true, "resolved": true, "escalated": true},
	"in_progress": {"resolved": true, "escalated": true},
	"escalated":   {"assigned": true, "in_progress": true, "resolved": true},
	"resolved":    {"closed": true, "reopened": true},
	"closed":      {"reopened": true},
	"reopened":    {"assigned": true, "in_progress": true, "resolved": true, "escalated": true},
}

// CanTransition checks WF-CMP state transition validity.
func CanTransition(fromState, toState string) bool {
	nexts, ok := allowedComplaintTransitions[fromState]
	return ok && nexts[toState]
}

type Complaint struct {
	ID             string    `json:"id"`
	ServicePointID string    `json:"service_point_id,omitempty"`
	Title          string    `json:"title"`
	Description    string    `json:"description"`
	State          string    `json:"state"`
	AssigneeRef    string    `json:"assignee_ref,omitempty"`
	DueAt          time.Time `json:"due_at"`
	Version        int       `json:"version"`
}

type ComplaintEvent struct {
	ID          string    `json:"id"`
	ComplaintID string    `json:"complaint_id"`
	ActorRef    string    `json:"actor_ref"`
	EventType   string    `json:"event_type"`
	Note        string    `json:"note,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// ScopedSummaryReport omits all personal PII fields (FR-018).
type ScopedSummaryReport struct {
	FromDate           string `json:"from_date"`
	ToDate             string `json:"to_date"`
	CustomerCount      int64  `json:"customer_count"`
	CollectionJobCount int64  `json:"collection_job_count"`
	OpenComplaints     int64  `json:"open_complaints"`
	ResolvedComplaints int64  `json:"resolved_complaints"`
	TotalBilledIDR     int64  `json:"total_billed_idr"`
	PostedPaymentsIDR  int64  `json:"posted_payments_idr"`
	CashBalanceIDR     int64  `json:"cash_balance_idr"`
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (s *Service) CreateComplaint(ctx context.Context, actorUserID, servicePointID, title, description string, dueAt time.Time) (*Complaint, error) {
	if title == "" || description == "" {
		return nil, fmt.Errorf("%w: title and description are required", ErrInvalidInput)
	}
	if dueAt.IsZero() {
		dueAt = time.Now().UTC().Add(48 * time.Hour)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var spPtr *string
	if servicePointID != "" {
		spPtr = &servicePointID
	}

	var c Complaint
	err = tx.QueryRow(ctx, `
		INSERT INTO complaints (service_point_id, title, description, state, due_at)
		VALUES ($1::uuid, $2, $3, 'open', $4)
		RETURNING id::text, COALESCE(service_point_id::text,''), title, description, state, COALESCE(assignee_ref::text,''), due_at, version
	`, spPtr, title, description, dueAt).Scan(&c.ID, &c.ServicePointID, &c.Title, &c.Description, &c.State, &c.AssigneeRef, &c.DueAt, &c.Version)
	if err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO complaint_events (complaint_id, actor_ref, event_type, note)
		VALUES ($1::uuid, $2::uuid, 'opened', 'Complaint created')
	`, c.ID, actorUserID); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_ref, resource_type, resource_id, action, outcome, correlation_id)
		VALUES ($1::uuid, 'complaint', $2::uuid, 'complaint:create', 'succeeded', $3::uuid)
	`, actorUserID, c.ID, uuid.NewString()); err != nil {
		return nil, err
	}

	if err := outbox.Enqueue(ctx, tx, "complaint", c.ID, "complaint.opened", map[string]string{
		"complaint_id": c.ID,
		"due_at":       c.DueAt.Format(time.RFC3339),
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Service) TransitionComplaint(ctx context.Context, actorUserID, complaintID, nextState, assigneeRef, note string) (*Complaint, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var c Complaint
	err = tx.QueryRow(ctx, `
		SELECT id::text, COALESCE(service_point_id::text,''), title, description, state, COALESCE(assignee_ref::text,''), due_at, version
		FROM complaints
		WHERE id = $1::uuid
		FOR UPDATE
	`, complaintID).Scan(&c.ID, &c.ServicePointID, &c.Title, &c.Description, &c.State, &c.AssigneeRef, &c.DueAt, &c.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !CanTransition(c.State, nextState) {
		return nil, fmt.Errorf("%w: cannot transition from %q to %q", ErrInvalidTransition, c.State, nextState)
	}

	var assigneePtr *string
	if assigneeRef != "" {
		assigneePtr = &assigneeRef
	} else if c.AssigneeRef != "" {
		assigneePtr = &c.AssigneeRef
	}

	err = tx.QueryRow(ctx, `
		UPDATE complaints
		SET state = $2, assignee_ref = $3::uuid, version = version + 1, updated_at = now()
		WHERE id = $1::uuid
		RETURNING state, COALESCE(assignee_ref::text,''), version
	`, complaintID, nextState, assigneePtr).Scan(&c.State, &c.AssigneeRef, &c.Version)
	if err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO complaint_events (complaint_id, actor_ref, event_type, note)
		VALUES ($1::uuid, $2::uuid, $3, $4)
	`, c.ID, actorUserID, nextState, note); err != nil {
		return nil, err
	}

	if (nextState == "assigned" || nextState == "reopened") && c.AssigneeRef != "" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO tasks (business_type, business_id, assignee_ref, state, priority, due_at)
			VALUES ('complaint', $1::uuid, $2::uuid, 'open', 'high', $3)
		`, c.ID, c.AssigneeRef, c.DueAt); err != nil {
			return nil, err
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (actor_ref, resource_type, resource_id, action, outcome, correlation_id)
		VALUES ($1::uuid, 'complaint', $2::uuid, $3, 'succeeded', $4::uuid)
	`, actorUserID, c.ID, "complaint:"+nextState, uuid.NewString()); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Service) ListComplaints(ctx context.Context) ([]Complaint, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, COALESCE(service_point_id::text,''), title, description, state, COALESCE(assignee_ref::text,''), due_at, version
		FROM complaints
		ORDER BY created_at DESC
		LIMIT 100
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Complaint
	for rows.Next() {
		var c Complaint
		if err := rows.Scan(&c.ID, &c.ServicePointID, &c.Title, &c.Description, &c.State, &c.AssigneeRef, &c.DueAt, &c.Version); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Service) GetTimeline(ctx context.Context, complaintID string) ([]ComplaintEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, complaint_id::text, actor_ref::text, event_type, COALESCE(note,''), created_at
		FROM complaint_events
		WHERE complaint_id = $1::uuid
		ORDER BY created_at ASC
	`, complaintID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ComplaintEvent
	for rows.Next() {
		var ev ComplaintEvent
		if err := rows.Scan(&ev.ID, &ev.ComplaintID, &ev.ActorRef, &ev.EventType, &ev.Note, &ev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// ExportScopedSummary generates a PII-free aggregate report and records an audit event (FR-018).
func (s *Service) ExportScopedSummary(ctx context.Context, actorUserID, fromDate, toDate string) (*ScopedSummaryReport, error) {
	if fromDate == "" || toDate == "" {
		return nil, fmt.Errorf("%w: from and to dates (YYYY-MM-DD) required", ErrInvalidInput)
	}
	rep := ScopedSummaryReport{FromDate: fromDate, ToDate: toDate}
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM customers),
			(SELECT COUNT(*) FROM collection_jobs WHERE scheduled_at::date BETWEEN $1::date AND $2::date),
			(SELECT COUNT(*) FROM complaints WHERE state NOT IN ('resolved', 'closed')),
			(SELECT COUNT(*) FROM complaints WHERE state IN ('resolved', 'closed')),
			(SELECT COALESCE(SUM(amount_idr), 0) FROM invoices WHERE billing_period BETWEEN $1::date AND $2::date),
			(SELECT COALESCE(SUM(amount_idr), 0) FROM payments WHERE status = 'posted' AND created_at::date BETWEEN $1::date AND $2::date),
			(SELECT COALESCE(SUM(amount_delta_idr), 0) FROM ledger_entries WHERE account_code = '1100:CASH' AND created_at::date BETWEEN $1::date AND $2::date)
	`, fromDate, toDate).Scan(
		&rep.CustomerCount,
		&rep.CollectionJobCount,
		&rep.OpenComplaints,
		&rep.ResolvedComplaints,
		&rep.TotalBilledIDR,
		&rep.PostedPaymentsIDR,
		&rep.CashBalanceIDR,
	)
	if err != nil {
		return nil, err
	}

	_, _ = s.pool.Exec(ctx, `
		INSERT INTO audit_events (actor_ref, resource_type, resource_id, action, outcome, correlation_id)
		VALUES ($1::uuid, 'report', $2::uuid, 'report:export', 'succeeded', $2::uuid)
	`, actorUserID, uuid.NewString())

	return &rep, nil
}
