package outbox

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Event represents a row in outbox_events.
type Event struct {
	ID            string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       json.RawMessage
	Retries       int
}

// Handler processes a single outbox event idempotently.
type Handler func(ctx context.Context, ev Event) error

// Enqueue inserts an outbox event inside an active database transaction.
func Enqueue(ctx context.Context, tx pgx.Tx, aggregateType, aggregateID, eventType string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
		VALUES ($1, $2::uuid, $3, $4::jsonb)
	`, aggregateType, aggregateID, eventType, string(raw))
	return err
}

// Worker polls pending outbox events with FOR UPDATE SKIP LOCKED.
type Worker struct {
	pool    *pgxpool.Pool
	handler Handler
}

// NewWorker creates an outbox worker.
func NewWorker(pool *pgxpool.Pool, handler Handler) *Worker {
	return &Worker{pool: pool, handler: handler}
}

// Run polls until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.ProcessBatch(ctx, 10); err != nil && ctx.Err() == nil {
				log.Printf("outbox batch error: %v", err)
			}
		}
	}
}

// ProcessBatch claims up to limit pending events and marks them processed or increments retries.
func (w *Worker) ProcessBatch(ctx context.Context, limit int) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id::text, aggregate_type, aggregate_id::text, event_type, payload, retries
		FROM outbox_events
		WHERE processed_at IS NULL AND retries < 10
		ORDER BY created_at ASC
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`, limit)
	if err != nil {
		return err
	}

	var batch []Event
	for rows.Next() {
		var ev Event
		if err := rows.Scan(&ev.ID, &ev.AggregateType, &ev.AggregateID, &ev.EventType, &ev.Payload, &ev.Retries); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, ev)
	}
	rows.Close()

	for _, ev := range batch {
		if err := w.handler(ctx, ev); err != nil {
			if _, uerr := tx.Exec(ctx, `
				UPDATE outbox_events SET retries = retries + 1, last_error = $2 WHERE id = $1::uuid
			`, ev.ID, err.Error()); uerr != nil {
				return uerr
			}
			continue
		}
		if _, uerr := tx.Exec(ctx, `
			UPDATE outbox_events SET processed_at = now(), last_error = NULL WHERE id = $1::uuid
		`, ev.ID); uerr != nil {
			return uerr
		}
	}

	return tx.Commit(ctx)
}
