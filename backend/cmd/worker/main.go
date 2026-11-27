package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/hosama-adem/twofold/backend/internal/audit"
	"github.com/hosama-adem/twofold/backend/internal/funding"
	"github.com/hosama-adem/twofold/backend/internal/moneyrequest"
	"github.com/hosama-adem/twofold/backend/internal/notification"
	"github.com/hosama-adem/twofold/backend/internal/outbox"
	"github.com/hosama-adem/twofold/backend/internal/platform/config"
	"github.com/hosama-adem/twofold/backend/internal/platform/database"
	"github.com/hosama-adem/twofold/backend/internal/provider"
	"github.com/hosama-adem/twofold/backend/internal/transfer"
)

func main() {
	os.Exit(run())
}

func run() int {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		log.Error("config", "error", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.NewPool(ctx, cfg)
	if err != nil {
		log.Error("database", "error", err)
		return 1
	}
	defer pool.Close()

	if err := outbox.MigrateRiver(ctx, pool); err != nil {
		log.Error("river migrate", "error", err)
		return 1
	}

	publisher := outbox.NewRiverPublisher()
	dispatcher := outbox.NewDispatcher(pool, publisher, log)

	transfers := transfer.NewService(pool)
	requests := moneyrequest.NewService(pool, transfers)

	var pollStale func(context.Context) (int, error)
	rail, railErr := provider.NewClient(provider.ClientConfig{
		Name:       cfg.ProviderName,
		BaseURL:    cfg.ProviderBaseURL,
		HMACSecret: cfg.ProviderHMACSecret,
	})
	box, boxErr := funding.NewSecretBox(cfg.PayoutEncryptionKey)
	if boxErr != nil {
		log.Warn("payout encryption disabled", "error", boxErr)
	}
	if railErr != nil {
		log.Warn("provider client disabled; top-up poll skipped", "error", railErr)
	} else {
		fundingSvc := funding.NewService(pool, rail, audit.NewRecorder(pool), box, log)
		pollStale = fundingSvc.PollStale
	}

	notifyConsumer := notification.NewConsumer(pool, notification.LogSender{Log: log}, log)

	workers := river.NewWorkers()
	outbox.RegisterWorkers(workers, outbox.WorkerDeps{
		Pool:       pool,
		Dispatcher: dispatcher,
		Deliverer:  notifyConsumer,
		Log:        log,
		ExpireDue:  requests.ExpireDue,
		PollStale:  pollStale,
		Now:        time.Now,
	})

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 4},
		},
		Workers:      workers,
		PeriodicJobs: outbox.PeriodicJobs(),
		Logger:       log,
	})
	if err != nil {
		log.Error("river client", "error", err)
		return 1
	}
	publisher.Bind(client)

	log.Info("worker starting")
	if err := client.Start(ctx); err != nil {
		log.Error("river start", "error", err)
		return 1
	}

	<-ctx.Done()
	log.Info("worker shutting down")
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Stop(stopCtx); err != nil {
		log.Error("river stop", "error", err)
		return 1
	}
	log.Info("shutdown complete")
	return 0
}
