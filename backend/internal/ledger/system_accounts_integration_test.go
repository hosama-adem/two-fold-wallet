package ledger_test

import (
	"context"
	"testing"

	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
)

func TestSystemAccountsMatchTheCatalog(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	q := ledgerdb.New(pool)

	got, err := q.ListSystemAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(ledger.SystemAccounts) {
		t.Fatalf("system accounts = %d, want %d", len(got), len(ledger.SystemAccounts))
	}

	byCode := make(map[string]ledgerdb.Account, len(got))
	for _, account := range got {
		byCode[account.Code] = account
	}
	for _, want := range ledger.SystemAccounts {
		account, ok := byCode[want.Code]
		if !ok {
			t.Fatalf("missing seeded account %s", want.Code)
		}
		if account.Type != string(want.Type) {
			t.Fatalf("%s type = %s, want %s", want.Code, account.Type, want.Type)
		}
		if account.NormalBalance != string(want.Normal) {
			t.Fatalf("%s normal = %s, want %s", want.Code, account.NormalBalance, want.Normal)
		}
		if account.Currency != ledger.CurrencyETB {
			t.Fatalf("%s currency = %s", want.Code, account.Currency)
		}
		if account.AllowNegative != want.AllowNegative {
			t.Fatalf("%s allow_negative = %v", want.Code, account.AllowNegative)
		}
		// The seed writes balance 0 and version 0. This database is shared,
		// and journals cannot be deleted, so a posting may already have
		// moved the cache. This test checks the chart of accounts.
		if account.OwnerUserID != nil {
			t.Fatalf("%s has owner %s", want.Code, account.OwnerUserID)
		}

		again, err := q.GetAccountByCode(ctx, want.Code)
		if err != nil {
			t.Fatal(err)
		}
		if again.ID != account.ID {
			t.Fatalf("%s id drifted between list and get", want.Code)
		}
	}
}
