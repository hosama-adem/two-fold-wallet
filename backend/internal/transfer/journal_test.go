package transfer

import (
	"testing"

	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/ledger"
)

func TestBuildP2PJournalFeeZero(t *testing.T) {
	from, to, feeAcct := uuid.New(), uuid.New(), uuid.New()
	j, err := buildP2PJournal(from, to, feeAcct, 100, 0, ledger.CurrencyETB)
	if err != nil {
		t.Fatal(err)
	}
	lines := j.Lines()
	if len(lines) != 2 {
		t.Fatalf("lines=%d", len(lines))
	}
	if lines[0].Direction() != ledger.Debit || lines[0].Amount().AmountMinor() != 100 {
		t.Fatalf("debit=%+v", lines[0])
	}
	if lines[1].Direction() != ledger.Credit || lines[1].Amount().AmountMinor() != 100 {
		t.Fatalf("credit=%+v", lines[1])
	}
}

func TestBuildP2PJournalWithFee(t *testing.T) {
	// PRD §7.3 shape: Dr 102 / Cr 100 + Cr fee_revenue 2
	from, to, feeAcct := uuid.New(), uuid.New(), uuid.New()
	j, err := buildP2PJournal(from, to, feeAcct, 100, 2, ledger.CurrencyETB)
	if err != nil {
		t.Fatal(err)
	}
	lines := j.Lines()
	if len(lines) != 3 {
		t.Fatalf("lines=%d", len(lines))
	}
	if lines[0].AccountID() != from || lines[0].Direction() != ledger.Debit || lines[0].Amount().AmountMinor() != 102 {
		t.Fatalf("debit=%+v", lines[0])
	}
	if lines[1].AccountID() != to || lines[1].Direction() != ledger.Credit || lines[1].Amount().AmountMinor() != 100 {
		t.Fatalf("credit recv=%+v", lines[1])
	}
	if lines[2].AccountID() != feeAcct || lines[2].Direction() != ledger.Credit || lines[2].Amount().AmountMinor() != 2 {
		t.Fatalf("credit fee=%+v", lines[2])
	}
}
