package ledger

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func mustMoney(t *testing.T, amount int64, currency string) Money {
	t.Helper()
	m, err := NewMoney(amount, currency)
	if err != nil {
		t.Fatalf("NewMoney: %v", err)
	}
	return m
}

func mustLine(t *testing.T, account uuid.UUID, direction Side, amount Money) Line {
	t.Helper()
	line, err := NewLine(account, direction, amount)
	if err != nil {
		t.Fatalf("NewLine: %v", err)
	}
	return line
}

func TestNewJournalRejectsEmpty(t *testing.T) {
	a := uuid.Must(uuid.NewV7())
	etb := mustMoney(t, 100, CurrencyETB)

	_, err := NewJournal(KindAdjustment, nil)
	if !errors.Is(err, ErrEmptyJournal) {
		t.Fatalf("nil lines: %v", err)
	}

	_, err = NewJournal(KindAdjustment, []Line{
		mustLine(t, a, Debit, etb),
	})
	if !errors.Is(err, ErrEmptyJournal) {
		t.Fatalf("one line: %v", err)
	}
}

func TestNewJournalRejectsCurrencyMismatch(t *testing.T) {
	a := uuid.Must(uuid.NewV7())
	b := uuid.Must(uuid.NewV7())

	_, err := NewJournal(KindAdjustment, []Line{
		mustLine(t, a, Debit, mustMoney(t, 100, CurrencyETB)),
		mustLine(t, b, Credit, mustMoney(t, 100, "USD")),
	})
	if !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("got %v, want ErrCurrencyMismatch", err)
	}
}

func TestNewJournalRejectsUnbalanced(t *testing.T) {
	a := uuid.Must(uuid.NewV7())
	b := uuid.Must(uuid.NewV7())

	_, err := NewJournal(KindAdjustment, []Line{
		mustLine(t, a, Debit, mustMoney(t, 100, CurrencyETB)),
		mustLine(t, b, Credit, mustMoney(t, 90, CurrencyETB)),
	})
	if !errors.Is(err, ErrUnbalanced) {
		t.Fatalf("got %v, want ErrUnbalanced", err)
	}
}

func TestNewJournalAcceptsBalanced(t *testing.T) {
	a := uuid.Must(uuid.NewV7())
	b := uuid.Must(uuid.NewV7())
	c := uuid.Must(uuid.NewV7())

	j, err := NewJournal(KindMerchantPayment, []Line{
		mustLine(t, a, Debit, mustMoney(t, 300, CurrencyETB)),
		mustLine(t, b, Debit, mustMoney(t, 3, CurrencyETB)),
		mustLine(t, b, Credit, mustMoney(t, 300, CurrencyETB)),
		mustLine(t, c, Credit, mustMoney(t, 3, CurrencyETB)),
	})
	if err != nil {
		t.Fatalf("balanced café journal: %v", err)
	}
	if j.Kind() != KindMerchantPayment {
		t.Fatalf("kind = %q", j.Kind())
	}
	if len(j.Lines()) != 4 {
		t.Fatalf("lines = %d", len(j.Lines()))
	}

	j = j.WithDescription("café + fee")
	if j.Description() != "café + fee" {
		t.Fatalf("description = %q", j.Description())
	}
}

func TestNewMoneyRejectsBadAmounts(t *testing.T) {
	for _, amount := range []int64{0, -1, MaxAmountMinor + 1} {
		if _, err := NewMoney(amount, CurrencyETB); err == nil {
			t.Fatalf("amount %d should fail", amount)
		}
	}
	if _, err := NewMoney(1, "etb"); err == nil {
		t.Fatal("lowercase currency should fail")
	}
}

func TestBalanceDelta(t *testing.T) {
	if got := BalanceDelta(Debit, Debit, 500); got != 500 {
		t.Fatalf("debit on debit-normal = %d", got)
	}
	if got := BalanceDelta(Credit, Credit, 500); got != 500 {
		t.Fatalf("credit on credit-normal = %d", got)
	}
	if got := BalanceDelta(Credit, Debit, 100); got != -100 {
		t.Fatalf("debit on wallet = %d", got)
	}
	if got := BalanceDelta(Debit, Credit, 100); got != -100 {
		t.Fatalf("credit on asset = %d", got)
	}
}
