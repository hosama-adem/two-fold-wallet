package devseed_test

import (
	"context"
	"testing"

	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
	"github.com/hosama-adem/twofold/backend/internal/wallet/devseed"
)

func TestSeedFundsThroughAJournalOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()

	var before int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM postings p
		JOIN accounts a ON a.id = p.account_id
		JOIN users u ON u.id = a.owner_user_id
		WHERE u.phone_e164 = $1
	`, devseed.Phone).Scan(&before); err != nil {
		t.Fatal(err)
	}

	first, err := devseed.Run(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	second, err := devseed.Run(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}

	if second.Funded {
		t.Fatal("second seed posted another journal")
	}
	if first.LedgerBalanceMinor != second.LedgerBalanceMinor {
		t.Fatalf("balance changed on rerun: %d -> %d", first.LedgerBalanceMinor, second.LedgerBalanceMinor)
	}
	if first.AvailableBalanceMinor != first.LedgerBalanceMinor {
		t.Fatalf("available %d ledger %d", first.AvailableBalanceMinor, first.LedgerBalanceMinor)
	}
	if first.LedgerBalanceMinor < devseed.AmountMinor {
		t.Fatalf("ledger = %d, want at least %d", first.LedgerBalanceMinor, devseed.AmountMinor)
	}

	var after int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM postings WHERE account_id = $1
	`, first.AccountID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if first.Funded && after != before+1 {
		t.Fatalf("postings before=%d after=%d, want one new credit", before, after)
	}
	if !first.Funded && after != before {
		t.Fatalf("postings changed without a new journal: before=%d after=%d", before, after)
	}

	var kind string
	var direction string
	var amount int64
	err = pool.QueryRow(ctx, `
		SELECT je.kind, p.direction, p.amount_minor
		FROM postings p
		JOIN journal_entries je ON je.id = p.journal_id
		WHERE p.account_id = $1
		ORDER BY p.created_at DESC
		LIMIT 1
	`, first.AccountID).Scan(&kind, &direction, &amount)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "top_up" || direction != "credit" || amount != devseed.AmountMinor {
		t.Fatalf("latest posting kind=%s direction=%s amount=%d", kind, direction, amount)
	}
}
