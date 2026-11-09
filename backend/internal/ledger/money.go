package ledger

import (
	"errors"
	"fmt"
	"regexp"
)

var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

// Money is an amount in minor units of one currency. Fields are unexported so
// every Money value has to come through NewMoney.
type Money struct {
	amountMinor int64
	currency    string
}

// NewMoney builds a Money value. amountMinor must be strictly positive and at
// most MaxAmountMinor. currency must be a three-letter uppercase code.
func NewMoney(amountMinor int64, currency string) (Money, error) {
	if amountMinor <= 0 || amountMinor > MaxAmountMinor {
		return Money{}, fmt.Errorf("ledger: amount %d out of range (1..%d)", amountMinor, MaxAmountMinor)
	}
	if !currencyCode.MatchString(currency) {
		return Money{}, fmt.Errorf("ledger: currency %q must be three uppercase letters", currency)
	}
	return Money{amountMinor: amountMinor, currency: currency}, nil
}

// AmountMinor is the integer minor-unit quantity (santim for ETB).
func (m Money) AmountMinor() int64 { return m.amountMinor }

// Currency is the ISO-style three-letter code.
func (m Money) Currency() string { return m.currency }

// IsZero reports whether m is the zero Money value (not a valid posted amount).
func (m Money) IsZero() bool {
	return m.amountMinor == 0 && m.currency == ""
}

// ErrInvalidMoney is returned when a Line carries an unset Money.
var ErrInvalidMoney = errors.New("ledger: money is unset")
