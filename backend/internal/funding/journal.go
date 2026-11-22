package funding

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/ledger"
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
