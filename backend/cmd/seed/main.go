package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/hosama-adem/twofold/backend/internal/wallet/devseed"
)

func main() {
	_ = godotenv.Load()
	_ = godotenv.Load("../.env")

	if err := devseed.RefuseProduction(os.Getenv("APP_ENV")); err != nil {
		fail(err)
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fail(fmt.Errorf("DATABASE_URL is required"))
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail(err)
	}
	defer pool.Close()

	result, err := devseed.Run(ctx, pool)
	if err != nil {
		fail(err)
	}

	fmt.Println("twofold dev wallet")
	fmt.Printf("  phone      %s\n", devseed.Phone)
	fmt.Printf("  handle     @%s\n", devseed.Handle)
	if result.CreatedUser {
		fmt.Printf("  password   %s\n", devseed.Password)
	} else {
		fmt.Println("  password   unchanged")
	}
	fmt.Printf("  ledger     %s\n", formatETB(result.LedgerBalanceMinor))
	fmt.Printf("  available  %s\n", formatETB(result.AvailableBalanceMinor))
	if result.Funded {
		fmt.Println("  journal    top_up  Dr provider_clearing:telebirr  Cr wallet")
	} else {
		fmt.Println("  journal    already funded, nothing posted")
	}
}

func formatETB(minor int64) string {
	sign := ""
	if minor < 0 {
		sign = "-"
		minor = -minor
	}
	return fmt.Sprintf("%s%d.%02d ETB", sign, minor/100, minor%100)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
