package ledger_test

import (
	"context"
	"errors"
	"math/rand/v2"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
)

func TestCachedBalanceMatchesRecomputeAfter100Journals(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	owner := insertUser(t, ctx, pool)
	asset := insertOwnedAccount(t, ctx, pool, owner, "asset", "debit", false)
	wallets := make([]uuid.UUID, 4)
	for i := range wallets {
		wallets[i] = insertLiabilityAccount(t, ctx, pool, owner)
	}

	const opening int64 = 500_000
	fund := []ledger.Line{
		mustNewLine(t, asset, ledger.Debit, mustNewMoney(t, opening*int64(len(wallets)), ledger.CurrencyETB)),
	}
	for _, id := range wallets {
		fund = append(fund, mustNewLine(t, id, ledger.Credit, mustNewMoney(t, opening, ledger.CurrencyETB)))
	}
	funding, err := ledger.NewJournal(ledger.KindAdjustment, fund)
	if err != nil {
		t.Fatal(err)
	}
	commitPost(t, ctx, pool, funding)

	rng := rand.New(rand.NewPCG(11, 100))
	posted := make([]uuid.UUID, 0, 100)
	for len(posted) < 100 {
		from := rng.IntN(len(wallets))
		to := rng.IntN(len(wallets) - 1)
		if to >= from {
			to++
		}
		amount := rng.Int64N(1_000) + 1
		money := mustNewMoney(t, amount, ledger.CurrencyETB)
		journal, err := ledger.NewJournal(ledger.KindP2P, []ledger.Line{
			mustNewLine(t, wallets[from], ledger.Debit, money),
			mustNewLine(t, wallets[to], ledger.Credit, money),
		})
		if err != nil {
			t.Fatal(err)
		}
		posted = append(posted, commitPost(t, ctx, pool, journal))
	}
	if len(posted) != 100 {
		t.Fatalf("posted %d journals", len(posted))
	}

	ids := append([]uuid.UUID{asset}, wallets...)
	assertProvableBalances(t, ctx, pool, ids...)

	q := ledgerdb.New(pool)
	var walletSum int64
	for _, id := range wallets {
		acct, err := q.GetAccountByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		walletSum += acct.BalanceMinor
	}
	assetRow, err := q.GetAccountByID(ctx, asset)
	if err != nil {
		t.Fatal(err)
	}
	if assetRow.BalanceMinor != walletSum || walletSum != opening*int64(len(wallets)) {
		t.Fatalf("closed system: asset=%d wallets=%d opening=%d", assetRow.BalanceMinor, walletSum, opening*int64(len(wallets)))
	}
}

func TestPostRejectsNetNegativeBalance(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	owner := insertUser(t, ctx, pool)
	from := insertLiabilityAccount(t, ctx, pool, owner)
	to := insertLiabilityAccount(t, ctx, pool, owner)

	money := mustNewMoney(t, 10, ledger.CurrencyETB)
	journal, err := ledger.NewJournal(ledger.KindP2P, []ledger.Line{
		mustNewLine(t, from, ledger.Debit, money),
		mustNewLine(t, to, ledger.Credit, money),
	})
	if err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := ledger.Post(ctx, tx, journal); !errors.Is(err, ledger.ErrNegativeBalance) {
		t.Fatalf("got %v, want ErrNegativeBalance", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	assertProvableBalances(t, ctx, pool, from, to)
	for _, id := range []uuid.UUID{from, to} {
		acct, err := ledgerdb.New(pool).GetAccountByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if acct.BalanceMinor != 0 || acct.Version != 0 {
			t.Fatalf("account %s cache = (%d, v%d), want (0, v0)", id, acct.BalanceMinor, acct.Version)
		}
	}
}

func TestPostAllowsIntermediateDipWhenNetIsNonNegative(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	owner := insertUser(t, ctx, pool)
	asset := insertOwnedAccount(t, ctx, pool, owner, "asset", "debit", false)
	selam := insertLiabilityAccount(t, ctx, pool, owner)
	cafe := insertLiabilityAccount(t, ctx, pool, owner)
	fee := insertOwnedAccount(t, ctx, pool, owner, "revenue", "credit", false)

	opening := mustNewMoney(t, 300, ledger.CurrencyETB)
	fund, err := ledger.NewJournal(ledger.KindTopUp, []ledger.Line{
		mustNewLine(t, asset, ledger.Debit, opening),
		mustNewLine(t, selam, ledger.Credit, opening),
	})
	if err != nil {
		t.Fatal(err)
	}
	commitPost(t, ctx, pool, fund)

	// The café is debited the fee before it is credited the sale. The net
	// is +297, which is what allow_negative looks at. The fee line still
	// records the dip.
	sale := mustNewMoney(t, 300, ledger.CurrencyETB)
	feeMoney := mustNewMoney(t, 3, ledger.CurrencyETB)
	journal, err := ledger.NewJournal(ledger.KindMerchantPayment, []ledger.Line{
		mustNewLine(t, selam, ledger.Debit, sale),
		mustNewLine(t, cafe, ledger.Debit, feeMoney),
		mustNewLine(t, cafe, ledger.Credit, sale),
		mustNewLine(t, fee, ledger.Credit, feeMoney),
	})
	if err != nil {
		t.Fatal(err)
	}
	journalID := commitPost(t, ctx, pool, journal)

	postings, err := ledgerdb.New(pool).ListPostingsByJournalID(ctx, journalID)
	if err != nil {
		t.Fatal(err)
	}
	var feeLine, saleLine ledgerdb.Posting
	for _, p := range postings {
		if p.AccountID != cafe {
			continue
		}
		switch p.Direction {
		case "debit":
			feeLine = p
		case "credit":
			saleLine = p
		}
	}
	if feeLine.BalanceAfterMinor != -3 || feeLine.AccountVersion != 1 {
		t.Fatalf("fee line balance_after=%d version=%d, want -3 and 1", feeLine.BalanceAfterMinor, feeLine.AccountVersion)
	}
	if saleLine.BalanceAfterMinor != 297 || saleLine.AccountVersion != 2 {
		t.Fatalf("sale line balance_after=%d version=%d, want 297 and 2", saleLine.BalanceAfterMinor, saleLine.AccountVersion)
	}
	assertProvableBalances(t, ctx, pool, asset, selam, cafe, fee)
}

func TestPostAllowsNegativeWhenAccountOptsIn(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	owner := insertUser(t, ctx, pool)
	wallet := insertOwnedAccount(t, ctx, pool, owner, "liability", "credit", true)
	counter := insertOwnedAccount(t, ctx, pool, owner, "asset", "debit", true)

	money := mustNewMoney(t, 40, ledger.CurrencyETB)
	journal, err := ledger.NewJournal(ledger.KindAdjustment, []ledger.Line{
		mustNewLine(t, wallet, ledger.Debit, money),
		mustNewLine(t, counter, ledger.Credit, money),
	})
	if err != nil {
		t.Fatal(err)
	}
	commitPost(t, ctx, pool, journal)

	acct, err := ledgerdb.New(pool).GetAccountByID(ctx, wallet)
	if err != nil {
		t.Fatal(err)
	}
	if acct.BalanceMinor != -40 {
		t.Fatalf("balance = %d, want -40", acct.BalanceMinor)
	}
	assertProvableBalances(t, ctx, pool, wallet, counter)
}

func TestConcurrentPostsDoNotLoseUpdates(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	owner := insertUser(t, ctx, pool)
	asset := insertOwnedAccount(t, ctx, pool, owner, "asset", "debit", false)
	wallet := insertLiabilityAccount(t, ctx, pool, owner)

	money := mustNewMoney(t, 10, ledger.CurrencyETB)
	journal, err := ledger.NewJournal(ledger.KindTopUp, []ledger.Line{
		mustNewLine(t, asset, ledger.Debit, money),
		mustNewLine(t, wallet, ledger.Credit, money),
	})
	if err != nil {
		t.Fatal(err)
	}

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := pool.Begin(ctx)
			if err != nil {
				errs <- err
				return
			}
			defer tx.Rollback(ctx)
			if _, err := ledger.Post(ctx, tx, journal); err != nil {
				errs <- err
				return
			}
			errs <- tx.Commit(ctx)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	acct, err := ledgerdb.New(pool).GetAccountByID(ctx, wallet)
	if err != nil {
		t.Fatal(err)
	}
	if acct.BalanceMinor != n*10 || acct.Version != n {
		t.Fatalf("wallet cache = (%d, v%d), want (%d, v%d)", acct.BalanceMinor, acct.Version, n*10, n)
	}
	assertProvableBalances(t, ctx, pool, asset, wallet)
}

func assertProvableBalances(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ids ...uuid.UUID) {
	t.Helper()
	q := ledgerdb.New(pool)
	for _, id := range ids {
		acct, err := q.GetAccountByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		recomputed, err := ledger.RecomputeBalance(ctx, pool, id)
		if err != nil {
			t.Fatal(err)
		}
		if acct.BalanceMinor != recomputed {
			t.Fatalf("%s cache %d, recomputed %d", acct.Code, acct.BalanceMinor, recomputed)
		}

		postings, err := q.ListPostingsByAccountID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		sort.Slice(postings, func(i, j int) bool {
			return postings[i].AccountVersion < postings[j].AccountVersion
		})
		var running int64
		var version int64
		for _, p := range postings {
			delta := ledger.BalanceDelta(ledger.Side(acct.NormalBalance), ledger.Side(p.Direction), p.AmountMinor)
			running += delta
			version++
			if p.BalanceAfterMinor != running {
				t.Fatalf("%s posting version %d balance_after %d, running %d", acct.Code, p.AccountVersion, p.BalanceAfterMinor, running)
			}
			if p.AccountVersion != version {
				t.Fatalf("%s posting version %d, want %d", acct.Code, p.AccountVersion, version)
			}
		}
		if acct.BalanceMinor != running || acct.Version != version {
			t.Fatalf("%s cache (%d, v%d), history (%d, v%d)", acct.Code, acct.BalanceMinor, acct.Version, running, version)
		}
	}
}
