package ledger_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
)

func TestSystemAccountsSeeded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := testutil.Postgres(t)
	q := ledgerdb.New(pool)
	ctx := context.Background()

	got, err := q.ListSystemAccounts(ctx)
	if err != nil {
		t.Fatalf("ListSystemAccounts: %v", err)
	}

	byCode := make(map[string]ledgerdb.Account, len(got))
	for _, a := range got {
		byCode[a.Code] = a
	}

	for _, code := range ledger.SystemAccountCodes {
		a, ok := byCode[code]
		if !ok {
			t.Fatalf("missing system account %q after migrate-up", code)
		}
		if a.Currency != "ETB" {
			t.Fatalf("%s currency = %q, want ETB", code, a.Currency)
		}
		if !a.AllowNegative {
			t.Fatalf("%s allow_negative = false, want true for system accounts", code)
		}
	}
}

func TestPostingRejectsZeroNegativeAndBadDirection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := testutil.Postgres(t)
	q := ledgerdb.New(pool)
	ctx := context.Background()

	account, err := q.GetAccountByCode(ctx, ledger.CodeFeeRevenue)
	if err != nil {
		t.Fatalf("GetAccountByCode: %v", err)
	}

	journalID := uuid.Must(uuid.NewV7())
	_, err = pool.Exec(ctx, `
		INSERT INTO journal_entries (id, kind, description)
		VALUES ($1, 'adjustment', 'constraint probe')
	`, journalID)
	if err != nil {
		t.Fatalf("insert journal: %v", err)
	}

	tryPosting := func(direction string, amount int64) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO postings (
				id, journal_id, account_id, direction, amount_minor,
				currency, balance_after_minor, account_version
			) VALUES ($1, $2, $3, $4, $5, $6, 0, 0)
		`, uuid.Must(uuid.NewV7()), journalID, account.ID, direction, amount, account.Currency)
		return err
	}

	if err := tryPosting("debit", 0); !isCheckViolation(err) {
		t.Fatalf("zero amount error = %v, want check_violation", err)
	}
	if err := tryPosting("credit", -1); !isCheckViolation(err) {
		t.Fatalf("negative amount error = %v, want check_violation", err)
	}
	if err := tryPosting("sideways", 100); !isCheckViolation(err) {
		t.Fatalf("invalid direction error = %v, want check_violation", err)
	}
}

func TestPostingRejectsCurrencyMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := testutil.Postgres(t)
	q := ledgerdb.New(pool)
	ctx := context.Background()

	account, err := q.GetAccountByCode(ctx, ledger.CodeSuspense)
	if err != nil {
		t.Fatalf("GetAccountByCode: %v", err)
	}

	journalID := uuid.Must(uuid.NewV7())
	_, err = pool.Exec(ctx, `
		INSERT INTO journal_entries (id, kind, description)
		VALUES ($1, 'adjustment', 'currency mismatch probe')
	`, journalID)
	if err != nil {
		t.Fatalf("insert journal: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO postings (
			id, journal_id, account_id, direction, amount_minor,
			currency, balance_after_minor, account_version
		) VALUES ($1, $2, $3, 'debit', 100, 'USD', 0, 0)
	`, uuid.Must(uuid.NewV7()), journalID, account.ID)
	if !isForeignKeyViolation(err) {
		t.Fatalf("currency mismatch error = %v, want foreign_key_violation", err)
	}
}

func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
