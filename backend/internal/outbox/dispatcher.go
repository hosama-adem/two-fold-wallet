package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/outbox/outboxdb"
)

const defaultClaimBatch = int32(50)

// Publisher enqueues a claimed outbox row onto River (or a test fake).
type Publisher interface {
	PublishOutbox(ctx context.Context, tx pgx.Tx, eventID uuid.UUID, topic string, payload json.RawMessage) error
}

// Dispatcher claims undispatched rows and publishes them.
type Dispatcher struct {
	pool      *pgxpool.Pool
	publisher Publisher
	log       *slog.Logger
	batch     int32
}

// NewDispatcher builds a SKIP LOCKED outbox dispatcher.
func NewDispatcher(pool *pgxpool.Pool, publisher Publisher, log *slog.Logger) *Dispatcher {
	if log == nil {
		log = slog.Default()
	}
	return &Dispatcher{
		pool:      pool,
		publisher: publisher,
		log:       log,
		batch:     defaultClaimBatch,
	}
}

// RunOnce claims up to batch undispatched events, publishes each to River,
// and marks dispatched_at. Safe under concurrent workers.
func (d *Dispatcher) RunOnce(ctx context.Context) (int, error) {
	if d.pool == nil || d.publisher == nil {
		return 0, fmt.Errorf("outbox: dispatcher not configured")
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := outboxdb.New(tx).ClaimUndispatched(ctx, d.batch)
	if err != nil {
		return 0, fmt.Errorf("outbox: claim: %w", err)
	}
	if len(rows) == 0 {
		return 0, tx.Commit(ctx)
	}

	for _, row := range rows {
		if err := d.publisher.PublishOutbox(ctx, tx, row.ID, row.Topic, row.Payload); err != nil {
			return 0, fmt.Errorf("outbox: publish %s: %w", row.ID, err)
		}
		n, err := outboxdb.New(tx).MarkDispatched(ctx, row.ID)
		if err != nil {
			return 0, fmt.Errorf("outbox: mark dispatched: %w", err)
		}
		if n != 1 {
			return 0, fmt.Errorf("outbox: mark dispatched affected %d rows for %s", n, row.ID)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	d.log.Info("outbox dispatched", "count", len(rows))
	return len(rows), nil
}
