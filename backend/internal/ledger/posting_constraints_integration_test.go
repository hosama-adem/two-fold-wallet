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
	journalID := insertAdjustmentJournal(t, ctx, pool)

	for _, amount := range []int64{0, -1, ledger.MaxAmountMinor + 1} {
		err := insertPosting(ctx, pool, journalID, account.ID, "debit", amount, account.Currency)
		requireViolation(t, err, "23514", "postings_amount_positive")
	}

	if err := insertPosting(ctx, pool, journalID, account.ID, "credit", ledger.MaxAmountMinor, account.Currency); err != nil {
		t.Fatalf("ceiling amount should insert: %v", err)
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

func TestUnbalancedJournalStillInserts(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	account, err := ledgerdb.New(pool).GetAccountByCode(ctx, ledger.CodePayoutsPending)
	if err != nil {
		t.Fatal(err)
	}
	journalID := insertAdjustmentJournal(t, ctx, pool)

	// One debit and no credit. The deferred balance trigger is the posting
	// engine's job; this migration must not pretend to enforce it.
	if err := insertPosting(ctx, pool, journalID, account.ID, "debit", 50, account.Currency); err != nil {
		t.Fatalf("one-line journal inserted: %v", err)
	}
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
	if err := insertPosting(ctx, pool, journalID, selam, "debit", 300, ledger.CurrencyETB); err != nil {
		t.Fatal(err)
	}
	if err := insertPosting(ctx, pool, journalID, cafe, "debit", 3, ledger.CurrencyETB); err != nil {
		t.Fatal(err)
	}
	if err := insertPosting(ctx, pool, journalID, cafe, "credit", 300, ledger.CurrencyETB); err != nil {
		t.Fatal(err)
	}
	if err := insertPosting(ctx, pool, journalID, fee.ID, "credit", 3, ledger.CurrencyETB); err != nil {
		t.Fatal(err)
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
