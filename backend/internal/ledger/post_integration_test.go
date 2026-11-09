package ledger_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
)

func TestPostWritesBalancedJournal(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	q := ledgerdb.New(pool)

	clearing, err := q.GetAccountByCode(ctx, ledger.CodeProviderClearingTelebirr)
	if err != nil {
		t.Fatal(err)
	}
	owner := insertUser(t, ctx, pool)
	wallet := insertLiabilityAccount(t, ctx, pool, owner)

	debit, err := ledger.NewMoney(500, ledger.CurrencyETB)
	if err != nil {
		t.Fatal(err)
	}
	credit, err := ledger.NewMoney(500, ledger.CurrencyETB)
	if err != nil {
		t.Fatal(err)
	}
	lines := []ledger.Line{
		mustNewLine(t, clearing.ID, ledger.Debit, debit),
		mustNewLine(t, wallet, ledger.Credit, credit),
	}
	journal, err := ledger.NewJournal(ledger.KindTopUp, lines)
	if err != nil {
		t.Fatal(err)
	}
	journal = journal.WithDescription("top-up via telebirr")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	journalID, err := ledger.Post(ctx, tx, journal)
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit balanced journal: %v", err)
	}

	got, err := q.GetJournalEntryByID(ctx, journalID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != string(ledger.KindTopUp) {
		t.Fatalf("kind = %q", got.Kind)
	}
	if got.Description == nil || *got.Description != "top-up via telebirr" {
		t.Fatalf("description = %v", got.Description)
	}

	postings, err := q.ListPostingsByJournalID(ctx, journalID)
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 2 {
		t.Fatalf("postings = %d, want 2", len(postings))
	}

	// Day 10 does not update the cache; balance_after is the projected value.
	var debitLine, creditLine ledgerdb.Posting
	for _, p := range postings {
		switch p.Direction {
		case "debit":
			debitLine = p
		case "credit":
			creditLine = p
		}
	}
	if debitLine.BalanceAfterMinor != 500 {
		t.Fatalf("clearing balance_after = %d, want 500", debitLine.BalanceAfterMinor)
	}
	if creditLine.BalanceAfterMinor != 500 {
		t.Fatalf("wallet balance_after = %d, want 500", creditLine.BalanceAfterMinor)
	}

	clearingAfter, err := q.GetAccountByID(ctx, clearing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if clearingAfter.BalanceMinor != clearing.BalanceMinor {
		t.Fatalf("cached clearing balance changed: %d → %d", clearing.BalanceMinor, clearingAfter.BalanceMinor)
	}
}

func TestPostRejectsCurrencyMismatchWithAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	q := ledgerdb.New(pool)

	clearing, err := q.GetAccountByCode(ctx, ledger.CodeProviderClearingChapa)
	if err != nil {
		t.Fatal(err)
	}
	fee, err := q.GetAccountByCode(ctx, ledger.CodeFeeRevenue)
	if err != nil {
		t.Fatal(err)
	}

	// NewJournal only sees the line currencies. USD vs USD is fine there;
	// Post loads the ETB accounts and rejects the mismatch.
	usd := mustNewMoney(t, 100, "USD")
	journal, err := ledger.NewJournal(ledger.KindAdjustment, []ledger.Line{
		mustNewLine(t, clearing.ID, ledger.Debit, usd),
		mustNewLine(t, fee.ID, ledger.Credit, usd),
	})
	if err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	_, err = ledger.Post(ctx, tx, journal)
	if !errors.Is(err, ledger.ErrCurrencyMismatch) {
		t.Fatalf("got %v, want ErrCurrencyMismatch", err)
	}
}

func TestUnbalancedJournalFailsAtCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	account, err := ledgerdb.New(pool).GetAccountByCode(ctx, ledger.CodePayoutsPending)
	if err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	journalID := newID()
	if _, err := tx.Exec(ctx, `
		INSERT INTO journal_entries (id, kind, description)
		VALUES ($1, 'adjustment', 'raw unbalanced probe')
	`, journalID); err != nil {
		t.Fatalf("insert journal: %v", err)
	}
	if err := insertPostingTx(ctx, tx, journalID, account.ID, "debit", 50, account.Currency); err != nil {
		t.Fatalf("insert unbalanced posting: %v", err)
	}

	err = tx.Commit(ctx)
	requireBalancedConstraint(t, err)
}

func mustNewMoney(t *testing.T, amount int64, currency string) ledger.Money {
	t.Helper()
	m, err := ledger.NewMoney(amount, currency)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func mustNewLine(t *testing.T, account uuid.UUID, direction ledger.Side, amount ledger.Money) ledger.Line {
	t.Helper()
	line, err := ledger.NewLine(account, direction, amount)
	if err != nil {
		t.Fatal(err)
	}
	return line
}

func insertPostingTx(ctx context.Context, tx pgx.Tx, journalID, accountID uuid.UUID, direction string, amount int64, currency string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO postings (
			id, journal_id, account_id, direction, amount_minor,
			currency, balance_after_minor, account_version
		) VALUES ($1, $2, $3, $4, $5, $6, 0, 0)
	`, newID(), journalID, accountID, direction, amount, currency)
	return err
}

func requireBalancedConstraint(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("want commit to fail for unbalanced journal")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("want postgres error, got %v", err)
	}
	if pgErr.Code != "23514" {
		t.Fatalf("code = %s, want 23514", pgErr.Code)
	}
	if !strings.Contains(pgErr.Message, "postings_journal_balanced") {
		t.Fatalf("message %q missing postings_journal_balanced", pgErr.Message)
	}
}
