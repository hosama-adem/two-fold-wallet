package ledger

import "testing"

func TestNormalBalanceFor(t *testing.T) {
	cases := []struct {
		typ  AccountType
		side Side
		ok   bool
	}{
		{Asset, Debit, true},
		{Expense, Debit, true},
		{Liability, Credit, true},
		{Revenue, Credit, true},
		{Equity, Credit, true},
		{AccountType("wallet"), "", false},
	}
	for _, tc := range cases {
		got, ok := NormalBalanceFor(tc.typ)
		if ok != tc.ok || got != tc.side {
			t.Fatalf("NormalBalanceFor(%q) = (%q, %v), want (%q, %v)", tc.typ, got, ok, tc.side, tc.ok)
		}
	}
}

func TestIncreasesBalance(t *testing.T) {
	// Top-up: debit the clearing asset, credit the customer's wallet.
	if !IncreasesBalance(Debit, Debit) {
		t.Fatal("a debit increases a debit-normal clearing account")
	}
	if !IncreasesBalance(Credit, Credit) {
		t.Fatal("a credit increases a credit-normal wallet")
	}
	if IncreasesBalance(Credit, Debit) {
		t.Fatal("a debit decreases a wallet liability")
	}
	if IncreasesBalance(Debit, Credit) {
		t.Fatal("a credit decreases an asset")
	}
	if IncreasesBalance("", Debit) {
		t.Fatal("a blank normal balance does not increase")
	}
}

func TestSystemAccountCatalog(t *testing.T) {
	if len(SystemAccounts) != 5 {
		t.Fatalf("catalog length = %d, want 5", len(SystemAccounts))
	}
	seen := make(map[string]bool, len(SystemAccounts))
	for _, a := range SystemAccounts {
		if seen[a.Code] {
			t.Fatalf("duplicate system code %q", a.Code)
		}
		seen[a.Code] = true
		side, ok := NormalBalanceFor(a.Type)
		if !ok || side != a.Normal {
			t.Fatalf("%s normal = %q (ok=%v), type %s wants %q", a.Code, a.Normal, ok, a.Type, side)
		}
		if !a.AllowNegative {
			t.Fatalf("%s allow_negative = false, system accounts opt in", a.Code)
		}
	}
	for _, code := range []string{
		CodeProviderClearingTelebirr,
		CodeProviderClearingChapa,
		CodePayoutsPending,
		CodeFeeRevenue,
		CodeSuspense,
	} {
		if !seen[code] {
			t.Fatalf("catalog missing %s", code)
		}
	}
}

func TestMaxAmountMinorIsTenToTheFifteen(t *testing.T) {
	if MaxAmountMinor != 1_000_000_000_000_000 {
		t.Fatalf("MaxAmountMinor = %d", MaxAmountMinor)
	}
}
