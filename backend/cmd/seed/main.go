package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/hosama-adem/twofold/backend/internal/platform/migrate"
	"github.com/hosama-adem/twofold/backend/internal/wallet/devseed"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	_ = godotenv.Load()
	_ = godotenv.Load(".env")
	_ = godotenv.Load("../.env")

	appEnv := os.Getenv("APP_ENV")
	if appEnv == "" {
		appEnv = "development"
	}
	if appEnv != "development" {
		return fmt.Errorf("wallet seed refused: APP_ENV=%q (only development)", appEnv)
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if err := migrate.Up(dsn, "migrations"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	res, err := devseed.Run(ctx, pool, appEnv)
	if err != nil {
		return err
	}

	funded := "posted 500.00 ETB through a top-up journal"
	if !res.Funded {
		funded = "journal already posted; balance left as it is"
	}
	fmt.Printf("dev wallet ready\n  phone:     %s\n  password:  %s\n  handle:    @%s\n  ledger:    %d santim\n  available: %d santim\n  %s\n",
		devseed.Phone, devseed.Password, devseed.Handle, res.LedgerBalanceMinor, res.AvailableMinor, funded)
	return nil
}
