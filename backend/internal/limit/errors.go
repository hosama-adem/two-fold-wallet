package limit

import (
	"fmt"
	"net/http"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

// Machine-readable limit names on limit_exceeded problems.
const (
	CodeLimitExceeded = "limit_exceeded"

	LimitPerTxn      = "per_txn"
	LimitDaily       = "daily"
	LimitMonthly     = "monthly"
	LimitMaxBalance  = "max_balance"
	LimitCashOut     = "cash_out"
)

func errExceeded(limit string, remainingMinor int64, detail string) *httpx.Error {
	return httpx.New(
		http.StatusUnprocessableEntity,
		CodeLimitExceeded,
		"Limit exceeded",
		detail,
	).WithLimit(limit, remainingMinor)
}

func errPerTxn(amount, cap, remaining int64) *httpx.Error {
	return errExceeded(
		LimitPerTxn,
		remaining,
		fmt.Sprintf(
			"This transfer of %d minor units exceeds your per-transaction limit of %d (remaining %d).",
			amount,
			cap,
			remaining,
		),
	)
}

func errDaily(amount, used, cap, remaining int64) *httpx.Error {
	return errExceeded(
		LimitDaily,
		remaining,
		fmt.Sprintf(
			"This transfer of %d would push today's outgoing to %d, over your daily limit of %d (remaining %d).",
			amount,
			used+amount,
			cap,
			remaining,
		),
	)
}

func errMonthly(amount, used, cap, remaining int64) *httpx.Error {
	return errExceeded(
		LimitMonthly,
		remaining,
		fmt.Sprintf(
			"This transfer of %d would push this month's outgoing to %d, over your monthly limit of %d (remaining %d).",
			amount,
			used+amount,
			cap,
			remaining,
		),
	)
}

func errMaxBalance(credit, balance, cap, remaining int64) *httpx.Error {
	return errExceeded(
		LimitMaxBalance,
		remaining,
		fmt.Sprintf(
			"This credit of %d would take the recipient balance from %d to %d, over their max balance of %d (room left %d).",
			credit,
			balance,
			balance+credit,
			cap,
			remaining,
		),
	)
}

func errCashOutNotAllowed() *httpx.Error {
	return errExceeded(
		LimitCashOut,
		0,
		"Cash-out is not allowed at your KYC tier. Submit identity verification to unlock withdrawals.",
	)
}
