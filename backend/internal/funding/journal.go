package funding

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

func clearingCode(providerName string) (string, error) {
	switch providerName {
	case providerTelebirr, providerSimulator:
		return ledger.CodeProviderClearingTelebirr, nil
	case providerChapa:
		return ledger.CodeProviderClearingChapa, nil
	default:
		return "", fmt.Errorf("funding: no clearing account for %q", providerName)
	}
}

func buildTopUpJournal(clearingID, walletID uuid.UUID, amountMinor int64, currency string) (ledger.Journal, error) {
	money, err := ledger.NewMoney(amountMinor, currency)
	if err != nil {
		return ledger.Journal{}, errAmountInvalid()
	}
	debit, err := ledger.NewLine(clearingID, ledger.Debit, money)
	if err != nil {
		return ledger.Journal{}, err
	}
	credit, err := ledger.NewLine(walletID, ledger.Credit, money)
	if err != nil {
		return ledger.Journal{}, err
	}
	return ledger.NewJournal(ledger.KindTopUp, []ledger.Line{debit, credit})
}

func buildCashOutReserveJournal(walletID, pendingID, feeAccountID uuid.UUID, amount, feeMinor int64, currency string) (ledger.Journal, error) {
	if feeMinor < 0 {
		return ledger.Journal{}, httpx.Internal("Fee cannot be negative.")
	}
	if feeMinor == 0 {
		money, err := ledger.NewMoney(amount, currency)
		if err != nil {
			return ledger.Journal{}, errAmountInvalid()
		}
		debit, err := ledger.NewLine(walletID, ledger.Debit, money)
		if err != nil {
			return ledger.Journal{}, err
		}
		credit, err := ledger.NewLine(pendingID, ledger.Credit, money)
		if err != nil {
			return ledger.Journal{}, err
		}
		return ledger.NewJournal(ledger.KindCashOut, []ledger.Line{debit, credit})
	}
	total, ok := addMinor(amount, feeMinor)
	if !ok {
		return ledger.Journal{}, errAmountInvalid()
	}
	debitMoney, err := ledger.NewMoney(total, currency)
	if err != nil {
		return ledger.Journal{}, errAmountInvalid()
	}
	pendingMoney, err := ledger.NewMoney(amount, currency)
	if err != nil {
		return ledger.Journal{}, errAmountInvalid()
	}
	feeMoney, err := ledger.NewMoney(feeMinor, currency)
	if err != nil {
		return ledger.Journal{}, errAmountInvalid()
	}
	debit, err := ledger.NewLine(walletID, ledger.Debit, debitMoney)
	if err != nil {
		return ledger.Journal{}, err
	}
	creditPending, err := ledger.NewLine(pendingID, ledger.Credit, pendingMoney)
	if err != nil {
		return ledger.Journal{}, err
	}
	creditFee, err := ledger.NewLine(feeAccountID, ledger.Credit, feeMoney)
	if err != nil {
		return ledger.Journal{}, err
	}
	return ledger.NewJournal(ledger.KindCashOut, []ledger.Line{debit, creditPending, creditFee})
}

func buildCashOutSettleJournal(pendingID, clearingID uuid.UUID, amountMinor int64, currency string) (ledger.Journal, error) {
	money, err := ledger.NewMoney(amountMinor, currency)
	if err != nil {
		return ledger.Journal{}, errAmountInvalid()
	}
	debit, err := ledger.NewLine(pendingID, ledger.Debit, money)
	if err != nil {
		return ledger.Journal{}, err
	}
	credit, err := ledger.NewLine(clearingID, ledger.Credit, money)
	if err != nil {
		return ledger.Journal{}, err
	}
	return ledger.NewJournal(ledger.KindCashOut, []ledger.Line{debit, credit})
}

func addMinor(a, b int64) (int64, bool) {
	sum := a + b
	if (b > 0 && sum < a) || (b < 0 && sum > a) {
		return 0, false
	}
	return sum, true
}

func buildSuspenseJournal(clearingID, suspenseID uuid.UUID, amountMinor int64, currency string) (ledger.Journal, error) {
	money, err := ledger.NewMoney(amountMinor, currency)
	if err != nil {
		return ledger.Journal{}, errAmountInvalid()
	}
	debit, err := ledger.NewLine(clearingID, ledger.Debit, money)
	if err != nil {
		return ledger.Journal{}, err
	}
	credit, err := ledger.NewLine(suspenseID, ledger.Credit, money)
	if err != nil {
		return ledger.Journal{}, err
	}
	// Late provider money is unexplained until finance reviews — adjustment kind
	// keeps top_up journals reserved for real wallet credits (FR-TOP-5).
	return ledger.NewJournal(ledger.KindAdjustment, []ledger.Line{debit, credit})
}
