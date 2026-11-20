// Command simulator is a local Telebirr/Chapa stand-in (Day 22 / #51).
//
// It exposes collection/payout initiation and GetStatus, then posts signed
// webhooks according to amount_minor suffix scenarios (see provider docs).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hosama-adem/twofold/backend/internal/provider/simserver"
)

func main() {
	os.Exit(run())
}

func run() int {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	addr := getenv("SIMULATOR_ADDR", ":8091")
	secret := getenv("SIMULATOR_HMAC_SECRET", "dev-simulator-hmac-secret")
	callback := os.Getenv("CALLBACK_URL")
	delay := durationEnv("WEBHOOK_DELAY", 2*time.Second)

	srv := simserver.New(simserver.Config{
		Name:         "simulator",
		HMACSecret:   secret,
		CallbackURL:  callback,
		DefaultDelay: delay,
		Log:          log,
	})

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("simulator listening", "addr", addr, "callback", callback)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
		log.Info("simulator stopped")
		return 0
	case err := <-errCh:
		if err != nil {
			log.Error("simulator", "error", err)
			return 1
		}
		return 0
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func durationEnv(k string, def time.Duration) time.Duration {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
