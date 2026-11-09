package ledger_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
)

func TestReverseTwiceFailsAndRestoresBalance(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	owner := insertUser(t, ctx, pool)
	asset := insertOwnedAccount(t, ctx, pool, owner, "asset", "debit", false)
	from := insertLiabilityAccount(t, ctx, pool, owner)
	to := insertLiabilityAccount(t, ctx, pool, owner)

	opening := mustNewMoney(t, 1_000, ledger.CurrencyETB)
	fund, err := ledger.NewJournal(ledger.KindTopUp, []ledger.Line{
		mustNewLine(t, asset, ledger.Debit, mustNewMoney(t, 2_000, ledger.CurrencyETB)),
		mustNewLine(t, from, ledger.Credit, opening),
		mustNewLine(t, to, ledger.Credit, opening),
	})
	if err != nil {
		t.Fatal(err)
	}
	commitPost(t, ctx, pool, fund)

	moved := mustNewMoney(t, 400, ledger.CurrencyETB)
	transfer, err := ledger.NewJournal(ledger.KindP2P, []ledger.Line{
		mustNewLine(t, from, ledger.Debit, moved),
		mustNewLine(t, to, ledger.Credit, moved),
	})
	if err != nil {
		t.Fatal(err)
	}
	transfer = transfer.WithDescription("p2p")
	transferID := commitPost(t, ctx, pool, transfer)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	reversalID, err := ledger.Reverse(ctx, tx, transferID, "  customer asked  ")
	if err != nil {
		t.Fatalf("first reverse: %v", err)
	}
	if _, err := ledger.Reverse(ctx, tx, transferID, "again"); !errors.Is(err, ledger.ErrAlreadyReversed) {
		t.Fatalf("second reverse in same tx: %v, want ErrAlreadyReversed", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit first reversal: %v", err)
	}

	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := ledger.Reverse(ctx, tx, transferID, "after commit"); !errors.Is(err, ledger.ErrAlreadyReversed) {
		t.Fatalf("reverse after commit: %v, want ErrAlreadyReversed", err)
	}

	q := ledgerdb.New(pool)
	got, err := q.GetJournalEntryByID(ctx, reversalID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != string(ledger.KindReversal) {
		t.Fatalf("kind = %q", got.Kind)
	}
	if got.ReversesJournalID == nil || *got.ReversesJournalID != transferID {
		t.Fatalf("reverses = %v, want %s", got.ReversesJournalID, transferID)
	}
	if got.Description == nil || *got.Description != "customer asked" {
		t.Fatalf("description = %v", got.Description)
	}

	original, err := q.ListPostingsByJournalID(ctx, transferID)
	if err != nil {
		t.Fatal(err)
	}
	flipped, err := q.ListPostingsByJournalID(ctx, reversalID)
	if err != nil {
		t.Fatal(err)
	}
	if len(original) != 2 || len(flipped) != 2 {
		t.Fatalf("lines original=%d reversal=%d", len(original), len(flipped))
	}
	for i := range original {
		if flipped[i].AccountID != original[i].AccountID || flipped[i].AmountMinor != original[i].AmountMinor {
			t.Fatalf("reversal line %d = %+v, original %+v", i, flipped[i], original[i])
		}
		if flipped[i].Direction == original[i].Direction {
			t.Fatalf("line %d direction was not flipped", i)
		}
		want := "credit"
		if original[i].Direction == "credit" {
			want = "debit"
		}
		if flipped[i].Direction != want {
			t.Fatalf("line %d direction = %s, want %s", i, flipped[i].Direction, want)
		}
	}

	fromRow, err := q.GetAccountByID(ctx, from)
	if err != nil {
		t.Fatal(err)
	}
	toRow, err := q.GetAccountByID(ctx, to)
	if err != nil {
		t.Fatal(err)
	}
	if fromRow.BalanceMinor != 1_000 || toRow.BalanceMinor != 1_000 {
		t.Fatalf("balances from=%d to=%d, want 1000 and 1000", fromRow.BalanceMinor, toRow.BalanceMinor)
	}
	assertProvableBalances(t, ctx, pool, asset, from, to)
}

func TestReverseRejectsBlankReasonAndUnknownJournal(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	if _, err := ledger.Reverse(ctx, tx, newID(), "   "); !errors.Is(err, ledger.ErrReasonRequired) {
		t.Fatalf("blank reason: %v", err)
	}
	if _, err := ledger.Reverse(ctx, tx, newID(), "missing"); !errors.Is(err, ledger.ErrJournalNotFound) {
		t.Fatalf("missing journal: %v", err)
	}
}

func TestReverseRejectsWhenItWouldOverdraw(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	owner := insertUser(t, ctx, pool)
	asset := insertOwnedAccount(t, ctx, pool, owner, "asset", "debit", true)
	wallet := insertLiabilityAccount(t, ctx, pool, owner)

	money := mustNewMoney(t, 100, ledger.CurrencyETB)
	topUp, err := ledger.NewJournal(ledger.KindTopUp, []ledger.Line{
		mustNewLine(t, asset, ledger.Debit, money),
		mustNewLine(t, wallet, ledger.Credit, money),
	})
	if err != nil {
		t.Fatal(err)
	}
	topUpID := commitPost(t, ctx, pool, topUp)

	spend, err := ledger.NewJournal(ledger.KindP2P, []ledger.Line{
		mustNewLine(t, wallet, ledger.Debit, money),
		mustNewLine(t, asset, ledger.Credit, money),
	})
	if err != nil {
		t.Fatal(err)
	}
	commitPost(t, ctx, pool, spend)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := ledger.Reverse(ctx, tx, topUpID, "provider failed"); !errors.Is(err, ledger.ErrNegativeBalance) {
		t.Fatalf("got %v, want ErrNegativeBalance", err)
	}
	assertProvableBalances(t, ctx, pool, asset, wallet)
}
