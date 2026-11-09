package ledger

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Sentinel validation errors returned by NewJournal and Post.
var (
	ErrEmptyJournal      = errors.New("ledger: journal needs at least two lines")
	ErrCurrencyMismatch  = errors.New("ledger: journal lines must share one currency")
	ErrUnbalanced        = errors.New("ledger: journal debits do not equal credits")
	ErrUnknownKind       = errors.New("ledger: unknown journal kind")
	ErrUnknownDirection  = errors.New("ledger: direction must be debit or credit")
	ErrInvalidAccountID  = errors.New("ledger: account id is required")
)

// Kind is the business reason for a journal entry. Values match
// journal_entries_kind_known. Reversal journals are created by Reverse (Day 11),
// not by callers of Post.
type Kind string

const (
	KindP2P             Kind = "p2p"
	KindTopUp           Kind = "top_up"
	KindCashOut         Kind = "cash_out"
	KindMerchantPayment Kind = "merchant_payment"
	KindMoneyRequest    Kind = "money_request"
	KindAdjustment      Kind = "adjustment"
)

func (k Kind) known() bool {
	switch k {
	case KindP2P, KindTopUp, KindCashOut, KindMerchantPayment, KindMoneyRequest, KindAdjustment:
		return true
	default:
		return false
	}
}

// Line is one posting the caller intends to write. Build it with NewLine.
type Line struct {
	accountID uuid.UUID
	direction Side
	amount    Money
}

// NewLine builds a posting line. direction must be Debit or Credit; amount
// must come from NewMoney.
func NewLine(accountID uuid.UUID, direction Side, amount Money) (Line, error) {
	if accountID == uuid.Nil {
		return Line{}, ErrInvalidAccountID
	}
	if direction != Debit && direction != Credit {
		return Line{}, ErrUnknownDirection
	}
	if amount.IsZero() {
		return Line{}, ErrInvalidMoney
	}
	return Line{accountID: accountID, direction: direction, amount: amount}, nil
}

// AccountID is the account this line posts to.
func (l Line) AccountID() uuid.UUID { return l.accountID }

// Direction is debit or credit.
func (l Line) Direction() Side { return l.direction }

// Amount is the positive money moved on this side.
func (l Line) Amount() Money { return l.amount }

// Journal is a balanced set of lines for one business event. Build it with
// NewJournal so invalid combinations never leave the constructor.
type Journal struct {
	kind          Kind
	lines         []Line
	description   string
	transactionID *uuid.UUID
}

// NewJournal validates kind, line count, single currency, and debit/credit
// equality. The returned value is safe to hand to Post.
func NewJournal(kind Kind, lines []Line) (Journal, error) {
	j := Journal{
		kind:  kind,
		lines: append([]Line(nil), lines...),
	}
	if err := j.validate(); err != nil {
		return Journal{}, err
	}
	return j, nil
}

// WithDescription returns a copy with a human-readable description.
func (j Journal) WithDescription(description string) Journal {
	j.description = description
	return j
}

// WithTransactionID returns a copy linked to a transactions row.
func (j Journal) WithTransactionID(id uuid.UUID) Journal {
	if id == uuid.Nil {
		j.transactionID = nil
		return j
	}
	j.transactionID = &id
	return j
}

// Kind is the journal's business kind.
func (j Journal) Kind() Kind { return j.kind }

// Lines returns a copy of the journal's lines.
func (j Journal) Lines() []Line {
	return append([]Line(nil), j.lines...)
}

// Description is the optional human-readable note.
func (j Journal) Description() string { return j.description }

// TransactionID is the optional link to a transactions row.
func (j Journal) TransactionID() *uuid.UUID { return j.transactionID }

func (j Journal) validate() error {
	if !j.kind.known() {
		return fmt.Errorf("%w: %q", ErrUnknownKind, j.kind)
	}
	if len(j.lines) < 2 {
		return ErrEmptyJournal
	}

	var (
		currency string
		debits   int64
		credits  int64
	)
	for i, line := range j.lines {
		if line.accountID == uuid.Nil {
			return ErrInvalidAccountID
		}
		if line.direction != Debit && line.direction != Credit {
			return ErrUnknownDirection
		}
		if line.amount.IsZero() {
			return ErrInvalidMoney
		}
		if i == 0 {
			currency = line.amount.currency
		} else if line.amount.currency != currency {
			return ErrCurrencyMismatch
		}
		switch line.direction {
		case Debit:
			sum, ok := addMinor(debits, line.amount.amountMinor)
			if !ok {
				return ErrUnbalanced
			}
			debits = sum
		case Credit:
			sum, ok := addMinor(credits, line.amount.amountMinor)
			if !ok {
				return ErrUnbalanced
			}
			credits = sum
		}
	}
	if debits != credits {
		return ErrUnbalanced
	}
	return nil
}

func addMinor(a, b int64) (int64, bool) {
	sum := a + b
	if (b > 0 && sum < a) || (b < 0 && sum > a) {
		return 0, false
	}
	return sum, true
}
