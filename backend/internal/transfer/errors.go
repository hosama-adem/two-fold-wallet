package transfer

import (
	"fmt"
	"net/http"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

const (
	CodeInsufficientFunds = "insufficient_funds"
	CodeSelfTransfer      = "self_transfer"
	CodeWalletFrozen      = "wallet_frozen"
	CodeFeeChanged        = "fee_changed"
)

func errRecipientNotFound() *httpx.Error {
	return httpx.NotFound("No user matches that phone or handle.")
}

func errSelfTransfer() *httpx.Error {
	return httpx.New(
		http.StatusUnprocessableEntity,
		CodeSelfTransfer,
		"Unprocessable Entity",
		"You cannot transfer to yourself.",
	)
}

func errWalletFrozen(detail string) *httpx.Error {
	return httpx.New(
		http.StatusForbidden,
		CodeWalletFrozen,
		"Forbidden",
		detail,
	)
}

func errInsufficientFunds(available, needed int64, currency string) *httpx.Error {
	return httpx.New(
		http.StatusUnprocessableEntity,
		CodeInsufficientFunds,
		"Insufficient funds",
		fmt.Sprintf(
			"Available balance is %s; this transfer needs %s.",
			formatMoney(available, currency),
			formatMoney(needed, currency),
		),
	)
}

func errFeeChanged(expected, computed int64) *httpx.Error {
	return httpx.New(
		http.StatusConflict,
		CodeFeeChanged,
		"Conflict",
		fmt.Sprintf(
			"Fee changed: you expected %d minor units but the fee is now %d.",
			expected,
			computed,
		),
	)
}

func formatMoney(minor int64, currency string) string {
	sign := ""
	if minor < 0 {
		sign = "-"
		minor = -minor
	}
	return fmt.Sprintf("%s%d.%02d %s", sign, minor/100, minor%100, currency)
}
