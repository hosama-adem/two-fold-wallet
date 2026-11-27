package outbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// WorkerDeps wires River workers for cmd/worker.
type WorkerDeps struct {
	Pool       *pgxpool.Pool
	Dispatcher *Dispatcher
	Deliverer  Deliverer
	Log        *slog.Logger
	ExpireDue  func(context.Context) error
	PollStale  func(context.Context) (int, error)
	Now        func() time.Time
}

// RegisterWorkers adds outbox and periodic job handlers to a River workers bundle.
func RegisterWorkers(workers *river.Workers, deps WorkerDeps) {
	river.AddWorker(workers, &DeliverWorker{Deliverer: deps.Deliverer, Log: deps.Log})
	river.AddWorker(workers, &DispatchWorker{Dispatcher: deps.Dispatcher})
	river.AddWorker(workers, &IdempotencyCleanupWorker{Pool: deps.Pool, Now: deps.Now})
	river.AddWorker(workers, &RequestExpiryWorker{Expire: deps.ExpireDue})
	river.AddWorker(workers, &TopUpPollWorker{Poll: deps.PollStale})
}

// PeriodicJobs returns the schedules owned by cmd/worker (PRD §11.2).
func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(5*time.Second),
			func() (river.JobArgs, *river.InsertOpts) { return DispatchArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true},
		),
		river.NewPeriodicJob(
			river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return IdempotencyCleanupArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true},
		),
		river.NewPeriodicJob(
			river.PeriodicInterval(time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return RequestExpiryArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true},
		),
		river.NewPeriodicJob(
			river.PeriodicInterval(5*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return TopUpPollArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true},
		),
	}
}
