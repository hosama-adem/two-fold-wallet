package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/hosama-adem/twofold/backend/internal/idempotency/idempotencydb"
)

var errPublisherUnbound = errors.New("outbox: river publisher not bound")

// DeliverArgs is the River job published for each outbox row.
// Day 26 turns this into a notification. Day 25 only proves delivery.
type DeliverArgs struct {
	EventID uuid.UUID       `json:"event_id"`
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload"`
}

func (DeliverArgs) Kind() string { return "outbox.deliver" }

// DispatchArgs triggers one outbox claim/publish cycle.
type DispatchArgs struct{}

func (DispatchArgs) Kind() string { return "outbox.dispatch" }

// IdempotencyCleanupArgs deletes expired idempotency keys.
type IdempotencyCleanupArgs struct{}

func (IdempotencyCleanupArgs) Kind() string { return "idempotency.cleanup" }

// RequestExpiryArgs expires due money requests.
type RequestExpiryArgs struct{}

func (RequestExpiryArgs) Kind() string { return "request.expiry" }

// TopUpPollArgs polls stale pending top-ups against the provider.
type TopUpPollArgs struct{}

func (TopUpPollArgs) Kind() string { return "topup.poll" }

// RiverPublisher publishes DeliverArgs via River InsertTx.
// Call Bind after river.NewClient so workers can register before the client exists.
type RiverPublisher struct {
	client *river.Client[pgx.Tx]
}

// NewRiverPublisher returns an unbound publisher. Call Bind before dispatch.
func NewRiverPublisher() *RiverPublisher {
	return &RiverPublisher{}
}

// Bind attaches the River client used for InsertTx.
func (p *RiverPublisher) Bind(client *river.Client[pgx.Tx]) {
	p.client = client
}

func (p *RiverPublisher) PublishOutbox(ctx context.Context, tx pgx.Tx, eventID uuid.UUID, topic string, payload json.RawMessage) error {
	if p == nil || p.client == nil {
		return errPublisherUnbound
	}
	_, err := p.client.InsertTx(ctx, tx, DeliverArgs{
		EventID: eventID,
		Topic:   topic,
		Payload: payload,
	}, nil)
	return err
}

// Deliverer turns a claimed outbox event into side effects (notifications).
type Deliverer interface {
	Deliver(ctx context.Context, eventID uuid.UUID, topic string, payload json.RawMessage) error
}

// DeliverWorker runs the notification consumer for each outbox.deliver job.
type DeliverWorker struct {
	river.WorkerDefaults[DeliverArgs]
	Deliverer Deliverer
	Log       *slog.Logger
}

func (w *DeliverWorker) Work(ctx context.Context, job *river.Job[DeliverArgs]) error {
	log := w.Log
	if log == nil {
		log = slog.Default()
	}
	if w.Deliverer == nil {
		log.Info("outbox delivered (no consumer)",
			"event_id", job.Args.EventID,
			"topic", job.Args.Topic,
			"job_id", job.ID,
		)
		return nil
	}
	if err := w.Deliverer.Deliver(ctx, job.Args.EventID, job.Args.Topic, job.Args.Payload); err != nil {
		return err
	}
	log.Info("outbox delivered",
		"event_id", job.Args.EventID,
		"topic", job.Args.Topic,
		"job_id", job.ID,
	)
	return nil
}

// DispatchWorker runs one dispatcher tick.
type DispatchWorker struct {
	river.WorkerDefaults[DispatchArgs]
	Dispatcher *Dispatcher
}

func (w *DispatchWorker) Work(ctx context.Context, _ *river.Job[DispatchArgs]) error {
	if w.Dispatcher == nil {
		return nil
	}
	_, err := w.Dispatcher.RunOnce(ctx)
	return err
}

// IdempotencyCleanupWorker deletes expired idempotency keys.
type IdempotencyCleanupWorker struct {
	river.WorkerDefaults[IdempotencyCleanupArgs]
	Pool *pgxpool.Pool
	Now  func() time.Time
}

func (w *IdempotencyCleanupWorker) Work(ctx context.Context, _ *river.Job[IdempotencyCleanupArgs]) error {
	if w.Pool == nil {
		return nil
	}
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	_, err := idempotencydb.New(w.Pool).DeleteExpired(ctx, now().UTC())
	return err
}

// RequestExpiryWorker expires due money requests.
type RequestExpiryWorker struct {
	river.WorkerDefaults[RequestExpiryArgs]
	Expire func(context.Context) error
}

func (w *RequestExpiryWorker) Work(ctx context.Context, _ *river.Job[RequestExpiryArgs]) error {
	if w.Expire == nil {
		return nil
	}
	return w.Expire(ctx)
}

// TopUpPollWorker polls stale top-ups.
type TopUpPollWorker struct {
	river.WorkerDefaults[TopUpPollArgs]
	Poll func(context.Context) (int, error)
}

func (w *TopUpPollWorker) Work(ctx context.Context, _ *river.Job[TopUpPollArgs]) error {
	if w.Poll == nil {
		return nil
	}
	_, err := w.Poll(ctx)
	return err
}
