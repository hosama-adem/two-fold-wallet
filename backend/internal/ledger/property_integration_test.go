package ledger_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"pgregory.net/rapid"

	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
)

// propertyWorld is a closed set of accounts the generators post against.
type propertyWorld struct {
	pool    *pgxpool.Pool
	asset   uuid.UUID
	wallets []uuid.UUID
	empty   uuid.UUID // always-zero liability for overdraft draws
	normal  map[uuid.UUID]ledger.Side
}

func newPropertyWorld(t *testing.T, wallets int) propertyWorld {
	t.Helper()
	pool := testutil.Postgres(t)
	ctx := context.Background()
	owner := insertUser(t, ctx, pool)
	asset := insertOwnedAccount(t, ctx, pool, owner, "asset", "debit", false)
	ids := make([]uuid.UUID, wallets)
	for i := range ids {
		ids[i] = insertLiabilityAccount(t, ctx, pool, owner)
	}
	empty := insertLiabilityAccount(t, ctx, pool, owner)

	const opening int64 = 5_000_000
	fund := []ledger.Line{
		mustNewLine(t, asset, ledger.Debit, mustNewMoney(t, opening*int64(wallets), ledger.CurrencyETB)),
	}
	for _, id := range ids {
		fund = append(fund, mustNewLine(t, id, ledger.Credit, mustNewMoney(t, opening, ledger.CurrencyETB)))
	}
	funding, err := ledger.NewJournal(ledger.KindAdjustment, fund)
	if err != nil {
		t.Fatal(err)
	}
	commitPost(t, ctx, pool, funding)

	all := append([]uuid.UUID{asset, empty}, ids...)
	normal := make(map[uuid.UUID]ledger.Side, len(all))
	q := ledgerdb.New(pool)
	for _, id := range all {
		acct, err := q.GetAccountByID(ctx, id)
		if err != nil {
			t.Fatalf("load normal: %v", err)
		}
		normal[id] = ledger.Side(acct.NormalBalance)
	}

	return propertyWorld{pool: pool, asset: asset, wallets: ids, empty: empty, normal: normal}
}

func (w propertyWorld) allIDs() []uuid.UUID {
	return append([]uuid.UUID{w.asset, w.empty}, w.wallets...)
}

func snapshotBalancesDB(t testing.TB, ctx context.Context, db ledgerdb.DBTX, ids []uuid.UUID) map[uuid.UUID]int64 {
	t.Helper()
	q := ledgerdb.New(db)
	out := make(map[uuid.UUID]int64, len(ids))
	for _, id := range ids {
		acct, err := q.GetAccountByID(ctx, id)
		if err != nil {
			t.Fatalf("snapshot %s: %v", id, err)
		}
		out[id] = acct.BalanceMinor
	}
	return out
}

func touchedAccountIDs(j ledger.Journal) []uuid.UUID {
	lines := j.Lines()
	seen := make(map[uuid.UUID]struct{}, len(lines))
	out := make([]uuid.UUID, 0, len(lines))
	for _, line := range lines {
		id := line.AccountID()
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func assertCacheMatchesRecomputeDB(t testing.TB, ctx context.Context, db ledgerdb.DBTX, ids ...uuid.UUID) {
	t.Helper()
	q := ledgerdb.New(db)
	for _, id := range ids {
		acct, err := q.GetAccountByID(ctx, id)
		if err != nil {
			t.Fatalf("get account: %v", err)
		}
		recomputed, err := ledger.RecomputeBalance(ctx, db, id)
		if err != nil {
			t.Fatalf("recompute: %v", err)
		}
		if acct.BalanceMinor != recomputed {
			t.Fatalf("%s cache %d, recomputed %d", acct.Code, acct.BalanceMinor, recomputed)
		}
		if !acct.AllowNegative && acct.BalanceMinor < 0 {
			t.Fatalf("%s balance %d is negative", acct.Code, acct.BalanceMinor)
		}
	}
}

func genWorldTransfer(rt *rapid.T, wallets []uuid.UUID) ledger.Journal {
	if len(wallets) < 2 {
		rt.Fatalf("need at least two wallets")
	}
	fromIdx := rapid.IntRange(0, len(wallets)-1).Draw(rt, "from")
	toIdx := rapid.IntRange(0, len(wallets)-2).Draw(rt, "to")
	if toIdx >= fromIdx {
		toIdx++
	}
	amount := rapid.Int64Range(1, 5_000).Draw(rt, "amount_minor")
	money := mustMoneyProp(rt, amount)
	j, err := ledger.NewJournal(ledger.KindP2P, []ledger.Line{
		mustLineProp(rt, wallets[fromIdx], ledger.Debit, money),
		mustLineProp(rt, wallets[toIdx], ledger.Credit, money),
	})
	if err != nil {
		rt.Fatalf("valid transfer: %v", err)
	}
	return j
}

func genWorldTopUp(rt *rapid.T, asset uuid.UUID, wallets []uuid.UUID) ledger.Journal {
	wallet := wallets[rapid.IntRange(0, len(wallets)-1).Draw(rt, "wallet")]
	amount := rapid.Int64Range(1, 5_000).Draw(rt, "amount_minor")
	money := mustMoneyProp(rt, amount)
	j, err := ledger.NewJournal(ledger.KindTopUp, []ledger.Line{
		mustLineProp(rt, asset, ledger.Debit, money),
		mustLineProp(rt, wallet, ledger.Credit, money),
	})
	if err != nil {
		rt.Fatalf("valid top-up: %v", err)
	}
	return j
}

func genWorldJournal(rt *rapid.T, w propertyWorld) ledger.Journal {
	if rapid.Bool().Draw(rt, "top_up") {
		return genWorldTopUp(rt, w.asset, w.wallets)
	}
	return genWorldTransfer(rt, w.wallets)
}

func mustMoneyProp(rt *rapid.T, amount int64) ledger.Money {
	m, err := ledger.NewMoney(amount, ledger.CurrencyETB)
	if err != nil {
		rt.Fatalf("NewMoney: %v", err)
	}
	return m
}

func mustLineProp(rt *rapid.T, account uuid.UUID, direction ledger.Side, amount ledger.Money) ledger.Line {
	line, err := ledger.NewLine(account, direction, amount)
	if err != nil {
		rt.Fatalf("NewLine: %v", err)
	}
	return line
}

func applyExpected(expected map[uuid.UUID]int64, normals map[uuid.UUID]ledger.Side, j ledger.Journal) {
	for _, line := range j.Lines() {
		id := line.AccountID()
		expected[id] += ledger.BalanceDelta(normals[id], line.Direction(), line.Amount().AmountMinor())
	}
}

// TestPropertyLedgerInvariants posts 1,000 random journals inside one
// transaction. After every post the in-tx cache must match the running
// Σ BalanceDelta model and stay non-negative. A full RecomputeBalance pass
// at the end proves the cache still equals the posting sum.
func TestPropertyLedgerInvariants(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	w := newPropertyWorld(t, 4)
	ctx := context.Background()
	ids := w.allIDs()

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	expected := snapshotBalancesDB(t, ctx, tx, ids)
	q := ledgerdb.New(tx)

	rapid.Check(t, func(rt *rapid.T) {
		j := genWorldJournal(rt, w)
		if _, err := ledger.Post(ctx, tx, j); err != nil {
			rt.Fatalf("Post: %v", err)
		}
		applyExpected(expected, w.normal, j)

		for _, id := range touchedAccountIDs(j) {
			acct, err := q.GetAccountByID(ctx, id)
			if err != nil {
				rt.Fatalf("get account: %v", err)
			}
			if acct.BalanceMinor != expected[id] {
				rt.Fatalf("%s cache %d, expected %d", acct.Code, acct.BalanceMinor, expected[id])
			}
			if acct.BalanceMinor < 0 {
				rt.Fatalf("%s balance %d is negative", acct.Code, acct.BalanceMinor)
			}
		}
	})

	assertCacheMatchesRecomputeDB(t, ctx, tx, ids...)
	for _, id := range ids {
		acct, err := q.GetAccountByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if acct.BalanceMinor != expected[id] {
			t.Fatalf("final %s cache %d, model %d", acct.Code, acct.BalanceMinor, expected[id])
		}
	}
}

// TestPropertyReversePostRestoresBalances: reverse(post(j)) is a balance no-op
// against real Postgres. One shared transaction, snapshot only the touched
// accounts — enough to catch a broken Reverse without blowing the 60s budget.
func TestPropertyReversePostRestoresBalances(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	w := newPropertyWorld(t, 4)
	ctx := context.Background()

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	rapid.Check(t, func(rt *rapid.T) {
		j := genWorldJournal(rt, w)
		touched := touchedAccountIDs(j)
		before := snapshotBalancesDB(t, ctx, tx, touched)

		journalID, err := ledger.Post(ctx, tx, j)
		if err != nil {
			rt.Fatalf("Post: %v", err)
		}
		if _, err := ledger.Reverse(ctx, tx, journalID, "property reverse"); err != nil {
			rt.Fatalf("Reverse: %v", err)
		}

		after := snapshotBalancesDB(t, ctx, tx, touched)
		for _, id := range touched {
			if before[id] != after[id] {
				rt.Fatalf("account %s: before %d after reverse %d", id, before[id], after[id])
			}
		}
	})
}

// TestPropertyOverdraftRejected: any debit that would take a zero-balance
// non-negative liability below zero fails with ErrNegativeBalance.
func TestPropertyOverdraftRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	w := newPropertyWorld(t, 2)
	ctx := context.Background()

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	rapid.Check(t, func(rt *rapid.T) {
		sink := w.wallets[rapid.IntRange(0, len(w.wallets)-1).Draw(rt, "sink")]
		amount := rapid.Int64Range(1, 5_000).Draw(rt, "overdraft_amount")
		money := mustMoneyProp(rt, amount)
		j, err := ledger.NewJournal(ledger.KindP2P, []ledger.Line{
			mustLineProp(rt, w.empty, ledger.Debit, money),
			mustLineProp(rt, sink, ledger.Credit, money),
		})
		if err != nil {
			rt.Fatalf("overdraft journal: %v", err)
		}

		if _, err := tx.Exec(ctx, "SAVEPOINT overdraft"); err != nil {
			rt.Fatalf("savepoint: %v", err)
		}
		_, err = ledger.Post(ctx, tx, j)
		if _, rbErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT overdraft"); rbErr != nil {
			rt.Fatalf("rollback savepoint: %v", rbErr)
		}
		if !errors.Is(err, ledger.ErrNegativeBalance) {
			rt.Fatalf("overdraft Post: %v, want ErrNegativeBalance", err)
		}
	})

	// One end check: the empty wallet is still zero and the world is untouched.
	empty, err := ledgerdb.New(tx).GetAccountByID(ctx, w.empty)
	if err != nil {
		t.Fatal(err)
	}
	if empty.BalanceMinor != 0 {
		t.Fatalf("empty wallet balance %d after rejected overdrafts", empty.BalanceMinor)
	}
}
