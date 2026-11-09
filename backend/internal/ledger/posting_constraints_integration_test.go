package ledger_test

import (
	"context"
	"testing"

	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
)

func TestPostingRejectsNonPositiveAmounts(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	account, err := ledgerdb.New(pool).GetAccountByCode(ctx, ledger.CodeFeeRevenue)
	if err != nil {
		t.Fatal(err)
	}
	clearing, err := ledgerdb.New(pool).GetAccountByCode(ctx, ledger.CodeProviderClearingTelebirr)
	if err != nil {
		t.Fatal(err)
	}
	journalID := insertAdjustmentJournal(t, ctx, pool)

	for _, amount := range []int64{0, -1, ledger.MaxAmountMinor + 1} {
		err := insertPosting(ctx, pool, journalID, account.ID, "debit", amount, account.Currency)
		requireViolation(t, err, "23514", "postings_amount_positive")
	}

	// Ceiling amount is legal; pair it so the deferred balance trigger passes.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := insertPostingTx(ctx, tx, journalID, clearing.ID, "debit", ledger.MaxAmountMinor, clearing.Currency); err != nil {
		t.Fatalf("ceiling debit: %v", err)
	}
	if err := insertPostingTx(ctx, tx, journalID, account.ID, "credit", ledger.MaxAmountMinor, account.Currency); err != nil {
		t.Fatalf("ceiling credit: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("ceiling journal commit: %v", err)
	}
}

func TestPostingRejectsUnknownDirection(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	account, err := ledgerdb.New(pool).GetAccountByCode(ctx, ledger.CodeSuspense)
	if err != nil {
		t.Fatal(err)
	}
	journalID := insertAdjustmentJournal(t, ctx, pool)

	for _, direction := range []string{"sideways", "Debit", ""} {
		err := insertPosting(ctx, pool, journalID, account.ID, direction, 100, account.Currency)
		requireViolation(t, err, "23514", "postings_direction_known")
	}
}

func TestPostingCurrencyMustMatchAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	account, err := ledgerdb.New(pool).GetAccountByCode(ctx, ledger.CodeProviderClearingTelebirr)
	if err != nil {
		t.Fatal(err)
	}
	journalID := insertAdjustmentJournal(t, ctx, pool)

	err = insertPosting(ctx, pool, journalID, account.ID, "debit", 100, "USD")
	requireViolation(t, err, "23503", "postings_account_currency_fk")
}

func TestCafeFeeStaysTwoPositiveLines(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	q := ledgerdb.New(pool)

	owner := insertUser(t, ctx, pool)
	selam := insertLiabilityAccount(t, ctx, pool, owner)
	cafe := insertLiabilityAccount(t, ctx, pool, owner)
	fee, err := q.GetAccountByCode(ctx, ledger.CodeFeeRevenue)
	if err != nil {
		t.Fatal(err)
	}

	journalID := insertAdjustmentJournal(t, ctx, pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	if err := insertPostingTx(ctx, tx, journalID, selam, "debit", 300, ledger.CurrencyETB); err != nil {
		t.Fatal(err)
	}
	if err := insertPostingTx(ctx, tx, journalID, cafe, "debit", 3, ledger.CurrencyETB); err != nil {
		t.Fatal(err)
	}
	if err := insertPostingTx(ctx, tx, journalID, cafe, "credit", 300, ledger.CurrencyETB); err != nil {
		t.Fatal(err)
	}
	if err := insertPostingTx(ctx, tx, journalID, fee.ID, "credit", 3, ledger.CurrencyETB); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("balanced café journal: %v", err)
	}

	got, err := q.ListPostingsByJournalID(ctx, journalID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("lines = %d, want 4", len(got))
	}
	var debits, credits int64
	for _, line := range got {
		if line.AmountMinor <= 0 {
			t.Fatalf("amount %d is not positive", line.AmountMinor)
		}
		switch line.Direction {
		case "debit":
			debits += line.AmountMinor
		case "credit":
			credits += line.AmountMinor
		default:
			t.Fatalf("direction %q", line.Direction)
		}
	}
	if debits != 303 || credits != 303 {
		t.Fatalf("debits=%d credits=%d, want 303 and 303", debits, credits)
	}

	cafeLines, err := q.ListPostingsByAccountID(ctx, cafe)
	if err != nil {
		t.Fatal(err)
	}
	if len(cafeLines) != 2 {
		t.Fatalf("café lines = %d, want the fee debit and the sale credit", len(cafeLines))
	}
}
