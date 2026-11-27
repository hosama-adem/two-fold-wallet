package funding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hosama-adem/twofold/backend/internal/fee"
	"github.com/hosama-adem/twofold/backend/internal/funding/fundingdb"
	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/limit"
	"github.com/hosama-adem/twofold/backend/internal/outbox"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/provider"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
)

const (
	kindCashOut          = "cash_out"
	statusProcessing     = "processing"
	directionPayout      = "payout"
	actorUser            = "user"
	eventNotifyReady     = "notify_ready"
)

// WithdrawInput is POST /v1/withdrawals.
type WithdrawInput struct {
	UserID           uuid.UUID
	DestinationID    uuid.UUID
	AmountMinor      int64
	Currency         string
	ExpectedFeeMinor int64
	IdempotencyKey   string
}

// Withdrawal is the customer-facing cash-out record (FR-WDR-4).
type Withdrawal struct {
	ID               uuid.UUID  `json:"id"`
	Reference        string     `json:"reference"`
	Kind             string     `json:"kind"`
	Status           string     `json:"status"`
	AmountMinor      int64      `json:"amount_minor"`
	FeeMinor         int64      `json:"fee_minor"`
	Currency         string     `json:"currency"`
	FromWalletID     uuid.UUID  `json:"from_wallet_id"`
	Provider         string     `json:"provider"`
	ProviderRef      string     `json:"provider_ref"`
	ProviderStatus   string     `json:"provider_status"`
	DestinationID    uuid.UUID  `json:"destination_id"`
	DestinationLabel string     `json:"destination_label"`
	IdentifierMasked string     `json:"identifier_masked"`
	CreatedAt        time.Time  `json:"created_at"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	IdempotencyKey   *string    `json:"idempotency_key,omitempty"`
}

// Withdraw moves wallet → payouts_pending and starts a provider payout (FR-WDR-2).
func (s *Service) Withdraw(ctx context.Context, in WithdrawInput) (Withdrawal, error) {
	if s.pool == nil || s.rail == nil || s.box == nil {
		return Withdrawal{}, httpx.Internal("Withdrawal service is not configured.")
	}
	currency := strings.TrimSpace(in.Currency)
	if currency == "" {
		currency = wallet.CurrencyETB
	}
	if currency != wallet.CurrencyETB {
		return Withdrawal{}, errCurrencyUnsupported()
	}
	if in.AmountMinor <= 0 || in.AmountMinor > ledger.MaxAmountMinor {
		return Withdrawal{}, errAmountInvalid()
	}
	if in.UserID == uuid.Nil || in.DestinationID == uuid.Nil {
		return Withdrawal{}, httpx.ValidationFailed("User and destination are required.")
	}

	dest, plainID, err := s.loadDestinationPlain(ctx, in.UserID, in.DestinationID)
	if err != nil {
		return Withdrawal{}, err
	}

	feeMinor, err := s.fees.ForAmount(ctx, in.UserID, fee.KindCashOut, in.AmountMinor)
	if err != nil {
		return Withdrawal{}, err
	}
	if in.ExpectedFeeMinor != feeMinor {
		return Withdrawal{}, errFeeChanged(in.ExpectedFeeMinor, feeMinor)
	}

	qPool := fundingdb.New(s.pool)
	w, err := qPool.GetWalletByUserCurrency(ctx, fundingdb.GetWalletByUserCurrencyParams{
		UserID:   in.UserID,
		Currency: currency,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Withdrawal{}, errWalletUnavailable()
		}
		return Withdrawal{}, err
	}

	txID, err := uuid.NewV7()
	if err != nil {
		return Withdrawal{}, fmt.Errorf("funding: transaction id: %w", err)
	}
	payID, err := uuid.NewV7()
	if err != nil {
		return Withdrawal{}, fmt.Errorf("funding: payment id: %w", err)
	}
	ref, err := s.allocateCashOutReference(ctx, qPool)
	if err != nil {
		return Withdrawal{}, err
	}

	idemKey := strings.TrimSpace(in.IdempotencyKey)
	payout, err := s.rail.InitiatePayout(ctx, provider.PayoutRequest{
		IdempotencyKey: idemKey,
		AmountMinor:    in.AmountMinor,
		Currency:       currency,
		PhoneE164:      plainID,
		Reference:      ref,
		Metadata: map[string]string{
			"provider":       dest.Provider,
			"transaction_id": txID.String(),
			"destination_id": dest.ID.String(),
		},
	})
	if err != nil {
		return Withdrawal{}, httpx.New(http.StatusBadGateway, "provider_unavailable", "Bad Gateway",
			"Payment provider did not accept the payout.")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Withdrawal{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := fundingdb.New(tx)

	locked, err := q.LockWalletBalance(ctx, w.ID)
	if err != nil {
		return Withdrawal{}, err
	}
	if locked.Status == "closed" || locked.FreezeMode == "full" || locked.FreezeMode == "debit" {
		return Withdrawal{}, errWalletUnavailable()
	}
	available := locked.LedgerBalanceMinor - locked.HeldMinor
	debitTotal := in.AmountMinor + feeMinor
	if available < debitTotal {
		return Withdrawal{}, errInsufficientFunds(available, debitTotal, currency)
	}
	if err := limit.Check(ctx, tx, in.UserID, limit.KindCashOut, in.AmountMinor); err != nil {
		return Withdrawal{}, err
	}

	var idem *string
	if idemKey != "" {
		idem = &idemKey
	}
	fromID := locked.WalletID
	if _, err := q.InsertTransaction(ctx, fundingdb.InsertTransactionParams{
		ID:              txID,
		Reference:       ref,
		Kind:            kindCashOut,
		Status:          statusPending,
		InitiatorUserID: &in.UserID,
		FromWalletID:    &fromID,
		ToWalletID:      nil,
		AmountMinor:     in.AmountMinor,
		FeeMinor:        feeMinor,
		Currency:        currency,
		Note:            nil,
		IdempotencyKey:  idem,
	}); err != nil {
		if isIdempotencyConflict(err) {
			return s.loadExistingWithdrawal(ctx, in.UserID, idemKey)
		}
		return Withdrawal{}, err
	}

	pendingID, err := s.systemAccountID(ctx, tx, ledger.CodePayoutsPending)
	if err != nil {
		return Withdrawal{}, err
	}
	feeAccountID, err := s.systemAccountID(ctx, tx, ledger.CodeFeeRevenue)
	if err != nil {
		return Withdrawal{}, err
	}
	journal, err := buildCashOutReserveJournal(locked.AccountID, pendingID, feeAccountID, in.AmountMinor, feeMinor, currency)
	if err != nil {
		return Withdrawal{}, err
	}
	journal = journal.WithTransactionID(txID).WithDescription("Cash-out reserve " + ref)
	journalID, err := ledger.Post(ctx, tx, journal)
	if err != nil {
		if errors.Is(err, ledger.ErrNegativeBalance) {
			return Withdrawal{}, errInsufficientFunds(available, debitTotal, currency)
		}
		return Withdrawal{}, err
	}

	txn, err := q.TransitionTransaction(ctx, fundingdb.TransitionTransactionParams{
		ID:         txID,
		FromStatus: statusPending,
		ToStatus:   statusProcessing,
	})
	if err != nil {
		return Withdrawal{}, err
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return Withdrawal{}, err
	}
	reason := eventNotifyReady + ":processing"
	if _, err := q.InsertTransactionEvent(ctx, fundingdb.InsertTransactionEventParams{
		ID:            eventID,
		TransactionID: txID,
		FromStatus:    statusPending,
		ToStatus:      statusProcessing,
		Reason:        &reason,
		Actor:         actorUser,
	}); err != nil {
		return Withdrawal{}, err
	}

	raw, _ := json.Marshal(payout)
	destID := dest.ID
	pay, err := q.InsertProviderPayment(ctx, fundingdb.InsertProviderPaymentParams{
		ID:               payID,
		TransactionID:    txID,
		Provider:         dest.Provider,
		Direction:        directionPayout,
		ProviderRef:      payout.ProviderRef,
		Status:           payPending,
		AmountMinor:      in.AmountMinor,
		Currency:         currency,
		Raw:              raw,
		DestinationID:    &destID,
		ReserveJournalID: &journalID,
	})
	if err != nil {
		return Withdrawal{}, err
	}
	if s.outbox != nil {
		payload := map[string]any{
			"transaction_id": txn.ID,
			"reference":      txn.Reference,
			"kind":           txn.Kind,
			"status":         txn.Status,
			"amount_minor":   txn.AmountMinor,
			"fee_minor":      txn.FeeMinor,
			"currency":       txn.Currency,
			"provider":       dest.Provider,
			"provider_ref":   pay.ProviderRef,
		}
		if txn.FromWalletID != nil {
			payload["from_wallet_id"] = *txn.FromWalletID
		}
		if txn.InitiatorUserID != nil {
			payload["initiator_user_id"] = *txn.InitiatorUserID
		}
		if _, err := s.outbox.Write(ctx, tx, outbox.TopicWithdrawalProcessing, payload); err != nil {
			return Withdrawal{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Withdrawal{}, err
	}
	return mapWithdrawal(txn, pay, dest), nil
}

// GetWithdrawal returns a cash-out owned by userID.
func (s *Service) GetWithdrawal(ctx context.Context, userID, id uuid.UUID) (Withdrawal, error) {
	if s.pool == nil {
		return Withdrawal{}, httpx.Internal("Withdrawal service is not configured.")
	}
	q := fundingdb.New(s.pool)
	txn, err := q.GetTransactionByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Withdrawal{}, errWithdrawalNotFound()
		}
		return Withdrawal{}, err
	}
	if txn.Kind != kindCashOut {
		return Withdrawal{}, errWithdrawalNotFound()
	}
	if txn.InitiatorUserID == nil || *txn.InitiatorUserID != userID {
		return Withdrawal{}, errWithdrawalForbidden()
	}
	pay, err := q.GetProviderPaymentByTxn(ctx, txn.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Withdrawal{}, errWithdrawalNotFound()
		}
		return Withdrawal{}, err
	}
	var dest fundingdb.PayoutDestination
	if pay.DestinationID != nil {
		dest, err = q.GetPayoutDestinationIncludingDeleted(ctx, fundingdb.GetPayoutDestinationIncludingDeletedParams{
			ID:     *pay.DestinationID,
			UserID: userID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				dest = fundingdb.PayoutDestination{
					ID:               *pay.DestinationID,
					Label:            "deleted",
					IdentifierMasked: "••••",
				}
			} else {
				return Withdrawal{}, err
			}
		}
	}
	return mapWithdrawal(txn, pay, dest), nil
}

func (s *Service) allocateCashOutReference(ctx context.Context, q *fundingdb.Queries) (string, error) {
	for i := 0; i < maxReferenceTries; i++ {
		ref, err := NewCashOutReference()
		if err != nil {
			return "", err
		}
		if !validCashOutReference(ref) {
			continue
		}
		exists, err := q.ReferenceExists(ctx, ref)
		if err != nil {
			return "", err
		}
		if !exists {
			return ref, nil
		}
	}
	return "", httpx.Internal("Could not allocate a withdrawal reference.")
}

func (s *Service) loadExistingWithdrawal(ctx context.Context, userID uuid.UUID, key string) (Withdrawal, error) {
	var txnID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id FROM transactions
		WHERE initiator_user_id = $1 AND idempotency_key = $2 AND kind = 'cash_out'
		LIMIT 1
	`, userID, key).Scan(&txnID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Withdrawal{}, httpx.Conflict("Idempotency key conflict.")
		}
		return Withdrawal{}, err
	}
	return s.GetWithdrawal(ctx, userID, txnID)
}

func mapWithdrawal(txn fundingdb.Transaction, pay fundingdb.ProviderPayment, dest fundingdb.PayoutDestination) Withdrawal {
	var from uuid.UUID
	if txn.FromWalletID != nil {
		from = *txn.FromWalletID
	}
	var destID uuid.UUID
	if pay.DestinationID != nil {
		destID = *pay.DestinationID
	} else {
		destID = dest.ID
	}
	return Withdrawal{
		ID:               txn.ID,
		Reference:        txn.Reference,
		Kind:             txn.Kind,
		Status:           txn.Status,
		AmountMinor:      txn.AmountMinor,
		FeeMinor:         txn.FeeMinor,
		Currency:         txn.Currency,
		FromWalletID:     from,
		Provider:         pay.Provider,
		ProviderRef:      pay.ProviderRef,
		ProviderStatus:   pay.Status,
		DestinationID:    destID,
		DestinationLabel: dest.Label,
		IdentifierMasked: dest.IdentifierMasked,
		CreatedAt:        txn.CreatedAt,
		CompletedAt:      txn.CompletedAt,
		IdempotencyKey:   txn.IdempotencyKey,
	}
}
