package main

 import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hosama-adem/twofold/backend/internal/audit"
	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/pin"
	"github.com/hosama-adem/twofold/backend/internal/platform/config"
	"github.com/hosama-adem/twofold/backend/internal/platform/database"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpserver"
	"github.com/hosama-adem/twofold/backend/internal/platform/ratelimit"
	"github.com/hosama-adem/twofold/backend/internal/session"
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

	tokens, err := auth.NewTokenSigner([]byte(cfg.JWTSigningKey), cfg.JWTKid, cfg.AccessTokenTTL, nil)
	if err != nil {
		log.Error("jwt", "error", err)
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
	sessions := session.NewRepository(pool)
	audits := audit.NewRecorder(pool)
	pins := pin.New(pin.Deps{
		Users: users,
		Audit: audits,
		Match: auth.VerifyPIN,
		Log:   log,
	})
	limiter := ratelimit.NewMemory(ratelimit.Config{
		Burst:  cfg.AuthRateLimitBurst,
		Window: cfg.AuthRateLimitWindow,
	})

	authSvc := auth.NewService(auth.ServiceDeps{
		Users:      users,
		Sessions:   sessions,
		Tokens:     tokens,
		Audit:      audits,
		Pins:       pins,
		Pool:       pool,
		SMS:        auth.LogSMSSender{Log: log},
		Log:        log,
		RefreshTTL: cfg.RefreshTokenTTL,
	})
	authHandler := auth.NewHandler(authSvc, limiter)

	srv := httpserver.New(cfg, pool, log, httpserver.Options{Auth: authHandler})
	if err := srv.Run(ctx); err != nil {
		log.Error("server", "error", err)
		return 1
	}

	log.Info("shutdown complete")
	return 0
}
