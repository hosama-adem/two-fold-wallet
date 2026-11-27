package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hosama-adem/twofold/backend/internal/audit"
	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/funding"
	"github.com/hosama-adem/twofold/backend/internal/history"
	"github.com/hosama-adem/twofold/backend/internal/idempotency"
	"github.com/hosama-adem/twofold/backend/internal/kyc"
	"github.com/hosama-adem/twofold/backend/internal/limit"
	"github.com/hosama-adem/twofold/backend/internal/moneyrequest"
	"github.com/hosama-adem/twofold/backend/internal/notification"
	"github.com/hosama-adem/twofold/backend/internal/pin"
	"github.com/hosama-adem/twofold/backend/internal/platform/config"
	"github.com/hosama-adem/twofold/backend/internal/platform/database"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpserver"
	"github.com/hosama-adem/twofold/backend/internal/platform/ratelimit"
	"github.com/hosama-adem/twofold/backend/internal/provider"
	"github.com/hosama-adem/twofold/backend/internal/session"
	"github.com/hosama-adem/twofold/backend/internal/transfer"
	"github.com/hosama-adem/twofold/backend/internal/user"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
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
	wallets := wallet.NewService(pool)
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

	limits := limit.NewService(pool)
	authSvc := auth.NewService(auth.ServiceDeps{
		Users:      users,
		Sessions:   sessions,
		Tokens:     tokens,
		Audit:      audits,
		Pins:       pins,
		Pool:       pool,
		Wallets:    wallets,
		Limits:     limits,
		SMS:        auth.LogSMSSender{Log: log},
		Log:        log,
		RefreshTTL: cfg.RefreshTokenTTL,
	})
	authHandler := auth.NewHandler(authSvc, limiter)
	walletHandler := wallet.NewHandler(wallets, authHandler.RequireAccess(), auth.UserIDFromContext)
	idemStore := idempotency.NewStore(pool)
	transfers := transfer.NewService(pool)
	transferHandler := transfer.NewHandler(
		transfers,
		pins,
		authHandler.RequireAccess(),
		idempotency.Middleware(idemStore, auth.UserIDFromContext),
		auth.UserIDFromContext,
	)
	kycHandler := kyc.NewHandler(
		kyc.NewService(pool),
		authHandler.RequireAccess(),
		auth.UserIDFromContext,
	)
	historyHandler := history.NewHandler(
		history.NewService(pool),
		authHandler.RequireAccess(),
		auth.UserIDFromContext,
	)
	moneyRequestHandler := moneyrequest.NewHandler(
		moneyrequest.NewService(pool, transfers),
		pins,
		authHandler.RequireAccess(),
		idempotency.Middleware(idemStore, auth.UserIDFromContext),
		auth.UserIDFromContext,
	)
	notificationHandler := notification.NewHandler(
		notification.NewService(pool),
		authHandler.RequireAccess(),
		auth.UserIDFromContext,
	)

	var fundingHandler *funding.Handler
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
		log.Warn("provider client disabled", "error", railErr)
	} else {
		fundingHandler = funding.NewHandler(
			funding.NewService(pool, rail, audits, box, log),
			pins,
			authHandler.RequireAccess(),
			idempotency.Middleware(idemStore, auth.UserIDFromContext),
			auth.UserIDFromContext,
		)
	}

	srv := httpserver.New(cfg, pool, log, httpserver.Options{
		Auth:          authHandler,
		Wallets:       walletHandler,
		Transfers:     transferHandler,
		KYC:           kycHandler,
		History:       historyHandler,
		MoneyRequests: moneyRequestHandler,
		Funding:       fundingHandler,
		Notifications: notificationHandler,
	})
	if err := srv.Run(ctx); err != nil {
		log.Error("server", "error", err)
		return 1
	}

	log.Info("shutdown complete")
	return 0
}
