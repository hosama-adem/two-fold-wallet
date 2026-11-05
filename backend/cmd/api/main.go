package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/platform/config"
	"github.com/hosama-adem/twofold/backend/internal/platform/database"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpserver"
	"github.com/hosama-adem/twofold/backend/internal/user"
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

	users := user.NewRepository(pool)
	authSvc := auth.NewService(users, auth.LogSMSSender{Log: log}, log)
	authHandler := auth.NewHandler(authSvc)

	srv := httpserver.New(cfg, pool, log, httpserver.Options{Auth: authHandler})
	if err := srv.Run(ctx); err != nil {
		log.Error("server", "error", err)
		return 1
	}

	log.Info("shutdown complete")
	return 0
}
