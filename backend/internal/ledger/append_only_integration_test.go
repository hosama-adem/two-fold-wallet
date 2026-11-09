package ledger_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
)

func TestLedgerHistoryRejectsUpdateDeleteAndTruncate(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	owner := insertUser(t, ctx, pool)
	account := insertLiabilityAccount(t, ctx, pool, owner)
	journalID := insertAdjustmentJournal(t, ctx, pool)
	// A lone line cannot commit: the deferred balance trigger wants debits
	// equal to credits. Debit and credit the same account so the journal
	// balances and the cache is not involved.
	seed, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertPostingTx(ctx, seed, journalID, account, "credit", 25, ledger.CurrencyETB); err != nil {
		t.Fatal(err)
	}
	if err := insertPostingTx(ctx, seed, journalID, account, "debit", 25, ledger.CurrencyETB); err != nil {
		t.Fatal(err)
	}
	if err := seed.Commit(ctx); err != nil {
		t.Fatalf("seed balanced journal: %v", err)
	}

	// A failed statement aborts the transaction. Savepoints let the next
	// mutation run, and rolling the transaction back keeps a missing trigger
	// from truncating the shared database.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	assertRejected := func(table, stmt string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, "SAVEPOINT ledger_mut"); err != nil {
			t.Fatal(err)
		}
		_, err := tx.Exec(ctx, stmt, args...)
		requireAppendOnly(t, err, table)
		if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT ledger_mut"); err != nil {
			t.Fatal(err)
		}
	}

	assertRejected("journal_entries", `UPDATE journal_entries SET description = 'mutated' WHERE id = $1`, journalID)
	assertRejected("journal_entries", `DELETE FROM journal_entries WHERE id = $1`, journalID)
	assertRejected("postings", `UPDATE postings SET amount_minor = 1 WHERE journal_id = $1`, journalID)
	assertRejected("postings", `DELETE FROM postings WHERE journal_id = $1`, journalID)
	assertRejected("postings", `TRUNCATE postings`)
	// Truncating journal_entries alone dies on the foreign key from postings
	// before a statement trigger can run. Truncating both removes that
	// excuse. The trigger still refuses, and it may name either table.
	if _, err := tx.Exec(ctx, "SAVEPOINT ledger_mut"); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `TRUNCATE postings, journal_entries`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23000" ||
		(pgErr.Message != "postings is append-only" && pgErr.Message != "journal_entries is append-only") {
		t.Fatalf("combined truncate: %v", err)
	}
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT ledger_mut"); err != nil {
		t.Fatal(err)
	}

	var amount int64
	if err := tx.QueryRow(ctx, `
		SELECT amount_minor FROM postings
		WHERE journal_id = $1 AND direction = 'credit'
	`, journalID).Scan(&amount); err != nil {
		t.Fatal(err)
	}
	if amount != 25 {
		t.Fatalf("posting amount = %d, want 25", amount)
	}
	var description *string
	if err := tx.QueryRow(ctx, `SELECT description FROM journal_entries WHERE id = $1`, journalID).Scan(&description); err != nil {
		t.Fatal(err)
	}
	if description == nil || *description != "schema probe" {
		t.Fatalf("description = %v", description)
	}
}

func requireAppendOnly(t *testing.T, err error, table string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("want append-only rejection, got %v", err)
	}
	want := table + " is append-only"
	if pgErr.Code != "23000" || pgErr.Message != want {
		t.Fatalf("code=%s message=%q, want 23000 %q", pgErr.Code, pgErr.Message, want)
	}
}
