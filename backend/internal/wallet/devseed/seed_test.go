package devseed_test

import (
	"context"
	"strings"
	"testing"

	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
	"github.com/hosama-adem/twofold/backend/internal/wallet/devseed"
)

func TestSeedRefusesNonDevelopment(t *testing.T) {
	t.Parallel()
	for _, env := range []string{"", "production", "test", "staging"} {
		if _, err := devseed.Run(context.Background(), nil, env); err == nil {
			t.Fatalf("APP_ENV=%q was accepted", env)
		} else if !strings.Contains(err.Error(), "refused") {
			t.Fatalf("APP_ENV=%q: %v", env, err)
		}
	}
}

func TestSeedFundsOnceThroughAJournal(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()

	first, err := devseed.Run(ctx, pool, "development")
	if err != nil {
		t.Fatal(err)
	}
	second, err := devseed.Run(ctx, pool, "development")
	if err != nil {
		t.Fatal(err)
	}
	if second.Funded {
		t.Fatal("second seed posted another journal")
	}
	if first.WalletID != second.WalletID || first.UserID != second.UserID {
		t.Fatalf("seed identity changed: %+v then %+v", first, second)
	}
	if second.LedgerBalanceMinor != second.AvailableMinor {
		t.Fatalf("available %d != ledger %d with no hold", second.AvailableMinor, second.LedgerBalanceMinor)
	}
	if second.LedgerBalanceMinor < devseed.AmountMinor {
		t.Fatalf("ledger = %d, want at least %d", second.LedgerBalanceMinor, devseed.AmountMinor)
	}

	var journals int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM journal_entries j
		JOIN transactions t ON t.id = j.transaction_id
		WHERE t.reference = $1
		  AND j.kind = 'top_up'
		  AND t.kind = 'top_up'
		  AND t.status = 'completed'
	`, devseed.Reference).Scan(&journals); err != nil {
		t.Fatal(err)
	}
	if journals != 1 {
		t.Fatalf("seed journals = %d, want 1", journals)
	}

	rows, err := pool.Query(ctx, `
		SELECT a.code, p.direction, p.amount_minor
		FROM postings p
		JOIN accounts a ON a.id = p.account_id
		JOIN journal_entries j ON j.id = p.journal_id
		JOIN transactions t ON t.id = j.transaction_id
		WHERE t.reference = $1
		ORDER BY p.direction
	`, devseed.Reference)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	type line struct {
		code      string
		direction string
		amount    int64
	}
	var lines []line
	for rows.Next() {
		var ln line
		if err := rows.Scan(&ln.code, &ln.direction, &ln.amount); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, ln)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("seed postings = %+v", lines)
	}
	var debit, credit line
	for _, ln := range lines {
		if ln.amount != devseed.AmountMinor {
			t.Fatalf("posting amount = %d", ln.amount)
		}
		switch ln.direction {
		case "debit":
			debit = ln
		case "credit":
			credit = ln
		default:
			t.Fatalf("direction %q", ln.direction)
		}
	}
	if debit.code != "provider_clearing:telebirr" {
		t.Fatalf("debit account = %s", debit.code)
	}
	if credit.code != "wallet:"+second.UserID.String() {
		t.Fatalf("credit account = %s", credit.code)
	}
}
