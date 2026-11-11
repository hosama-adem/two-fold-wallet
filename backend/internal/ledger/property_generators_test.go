package ledger

import (
	"github.com/google/uuid"
	"pgregory.net/rapid"
)

// knownKinds is every Kind NewJournal accepts (reversals are Reverse-only).
var knownKinds = []Kind{
	KindP2P,
	KindTopUp,
	KindCashOut,
	KindMerchantPayment,
	KindMoneyRequest,
	KindAdjustment,
}

// genAccountIDs draws n distinct account UUIDs the rest of a case can share.
func genAccountIDs(t *rapid.T, n int) []uuid.UUID {
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = uuid.Must(uuid.NewV7())
	}
	return ids
}

// genAmountMinor draws a posting amount that NewMoney accepts.
// Kept modest so thousands of cases stay fast and balances stay readable.
func genAmountMinor(t *rapid.T) int64 {
	return rapid.Int64Range(1, 50_000).Draw(t, "amount_minor")
}

// genKind draws a non-reversal journal kind.
func genKind(t *rapid.T) Kind {
	return rapid.SampledFrom(knownKinds).Draw(t, "kind")
}

// genValidTwoLineJournal is a simple balanced transfer: one debit, one credit,
// same amount, two accounts. The smallest journal the ledger will post.
func genValidTwoLineJournal(t *rapid.T) Journal {
	ids := genAccountIDs(t, 2)
	amount := mustMoneyRapid(t, genAmountMinor(t))
	lines := []Line{
		mustLineRapid(t, ids[0], Debit, amount),
		mustLineRapid(t, ids[1], Credit, amount),
	}
	j, err := NewJournal(genKind(t), lines)
	if err != nil {
		t.Fatalf("valid two-line journal: %v", err)
	}
	return j
}

// genValidMultiLineJournal draws 2–4 balanced lines. Extra debit/credit pairs
// keep the journal balanced while exercising multi-posting paths (fees, splits).
func genValidMultiLineJournal(t *rapid.T) Journal {
	nPairs := rapid.IntRange(1, 2).Draw(t, "pairs")
	ids := genAccountIDs(t, nPairs*2)
	lines := make([]Line, 0, nPairs*2)
	for i := 0; i < nPairs; i++ {
		amount := mustMoneyRapid(t, genAmountMinor(t))
		lines = append(lines,
			mustLineRapid(t, ids[i*2], Debit, amount),
			mustLineRapid(t, ids[i*2+1], Credit, amount),
		)
	}
	// Occasionally reorder so applyLines sees credits before debits.
	if rapid.Bool().Draw(t, "shuffle") {
		for i := len(lines) - 1; i > 0; i-- {
			j := rapid.IntRange(0, i).Draw(t, "swap")
			lines[i], lines[j] = lines[j], lines[i]
		}
	}
	j, err := NewJournal(genKind(t), lines)
	if err != nil {
		t.Fatalf("valid multi-line journal: %v", err)
	}
	return j
}

// genValidJournal draws either a two-line or multi-line balanced journal.
func genValidJournal(t *rapid.T) Journal {
	if rapid.Bool().Draw(t, "two_line") {
		return genValidTwoLineJournal(t)
	}
	return genValidMultiLineJournal(t)
}

// genUnbalancedLines draws two lines with different amounts so NewJournal must
// reject them. Used to prove the constructor is the first gate, not Post alone.
func genUnbalancedLines(t *rapid.T) []Line {
	ids := genAccountIDs(t, 2)
	debitAmt := genAmountMinor(t)
	creditAmt := genAmountMinor(t)
	for creditAmt == debitAmt {
		creditAmt = genAmountMinor(t)
	}
	return []Line{
		mustLineRapid(t, ids[0], Debit, mustMoneyRapid(t, debitAmt)),
		mustLineRapid(t, ids[1], Credit, mustMoneyRapid(t, creditAmt)),
	}
}

// genSingleLine draws one posting — below the two-line minimum.
func genSingleLine(t *rapid.T) []Line {
	ids := genAccountIDs(t, 1)
	return []Line{
		mustLineRapid(t, ids[0], Debit, mustMoneyRapid(t, genAmountMinor(t))),
	}
}

// genCurrencyMismatchLines draws a balanced shape with disagreeing currencies.
func genCurrencyMismatchLines(t *rapid.T) []Line {
	ids := genAccountIDs(t, 2)
	amount := genAmountMinor(t)
	return []Line{
		mustLineRapid(t, ids[0], Debit, mustMoneyRapid(t, amount)),
		mustLineRapid(t, ids[1], Credit, mustMoneyRapidCurrency(t, amount, "USD")),
	}
}

func mustMoneyRapid(t *rapid.T, amount int64) Money {
	return mustMoneyRapidCurrency(t, amount, CurrencyETB)
}

func mustMoneyRapidCurrency(t *rapid.T, amount int64, currency string) Money {
	m, err := NewMoney(amount, currency)
	if err != nil {
		t.Fatalf("NewMoney(%d, %s): %v", amount, currency, err)
	}
	return m
}

func mustLineRapid(t *rapid.T, account uuid.UUID, direction Side, amount Money) Line {
	line, err := NewLine(account, direction, amount)
	if err != nil {
		t.Fatalf("NewLine: %v", err)
	}
	return line
}

// sumSides returns total debit and credit minor units for a line set.
func sumSides(lines []Line) (debits, credits int64) {
	for _, line := range lines {
		switch line.Direction() {
		case Debit:
			debits += line.Amount().AmountMinor()
		case Credit:
			credits += line.Amount().AmountMinor()
		}
	}
	return debits, credits
}

// flipLines returns a copy with every direction inverted — the Reverse shape.
func flipLines(t *rapid.T, lines []Line) []Line {
	out := make([]Line, len(lines))
	for i, line := range lines {
		flipped, err := opposite(line.Direction())
		if err != nil {
			t.Fatalf("opposite: %v", err)
		}
		out[i] = mustLineRapid(t, line.AccountID(), flipped, line.Amount())
	}
	return out
}
