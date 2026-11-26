package funding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/audit"
	"github.com/hosama-adem/twofold/backend/internal/fee"
	"github.com/hosama-adem/twofold/backend/internal/funding/fundingdb"
	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
	"github.com/hosama-adem/twofold/backend/internal/outbox"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/provider"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
)

const (
	kindTopUp = "top_up"

	statusPending     = "pending"
	statusCompleted   = "completed"
	statusFailed      = "failed"
	statusExpired     = "expired"
	statusNeedsReview = "needs_review"

	payPending   = "pending"
	paySucceeded = "succeeded"
	payFailed    = "failed"
	payExpired   = "expired"

	providerTelebirr  = "telebirr"
	providerChapa     = "chapa"
	providerSimulator = "simulator"

	directionCollection = "collection"
	actorProvider       = "provider"
	actorSystem         = "system"

	maxReferenceTries = 8
	staleAfter        = 30 * time.Minute
	pollBatchLimit    = int32(50)

	auditLateSuccess = "topup.late_success"
)

// Service owns top-ups, withdrawals, destinations, and provider webhooks.
type Service struct {
	pool   *pgxpool.Pool
	rail   provider.Provider
	audit  *audit.Recorder
	fees   *fee.Service
	box    *SecretBox
	outbox *outbox.Writer
	log    *slog.Logger
	now    func() time.Time
}

// NewService builds a funding service. box may be nil if withdrawals are disabled.
func NewService(pool *pgxpool.Pool, rail provider.Provider, audits *audit.Recorder, box *SecretBox, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		pool:   pool,
		rail:   rail,
		audit:  audits,
		fees:   fee.NewService(pool),
		box:    box,
		outbox: outbox.NewWriter(),
		log:    log,
		now:    time.Now,
	}
}

// CreateInput is POST /v1/topups.
type CreateInput struct {
	UserID         uuid.UUID
	Provider       string
	AmountMinor    int64
	Currency       string
	IdempotencyKey string
}

// Checkout is the provider-facing handle returned to the client.
type Checkout struct {
	Provider    string `json:"provider"`
	ProviderRef string `json:"provider_ref"`
	Status      string `json:"status"`
}

// TopUp is the customer-facing top-up record.
type TopUp struct {
	ID             uuid.UUID  `json:"id"`
	Reference      string     `json:"reference"`
	Kind           string     `json:"kind"`
	Status         string     `json:"status"`
	AmountMinor    int64      `json:"amount_minor"`
	FeeMinor       int64      `json:"fee_minor"`
	Currency       string     `json:"currency"`
	ToWalletID     uuid.UUID  `json:"to_wallet_id"`
	Provider       string     `json:"provider"`
	ProviderRef    string     `json:"provider_ref"`
	ProviderStatus string     `json:"provider_status"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	IdempotencyKey *string    `json:"idempotency_key,omitempty"`
}

// CreateResult is POST /v1/topups response.
type CreateResult struct {
	TopUp    TopUp    `json:"top_up"`
	Checkout Checkout `json:"checkout"`
}

// Create starts a pending top-up and initiates a provider collection (FR-TOP-1).
func (s *Service) Create(ctx context.Context, in CreateInput) (CreateResult, error) {
	if s.pool == nil || s.rail == nil {
		return CreateResult{}, httpx.Internal("Top-up service is not configured.")
	}
	providerName, err := normalizeProvider(in.Provider)
	if err != nil {
		return CreateResult{}, err
	}
	currency := strings.TrimSpace(in.Currency)
	if currency == "" {
		currency = wallet.CurrencyETB
	}
	if currency != wallet.CurrencyETB {
		return CreateResult{}, errCurrencyUnsupported()
	}
	if in.AmountMinor <= 0 || in.AmountMinor > ledger.MaxAmountMinor {
		return CreateResult{}, errAmountInvalid()
	}
	if in.UserID == uuid.Nil {
		return CreateResult{}, httpx.ValidationFailed("User id is required.")
	}

	q := fundingdb.New(s.pool)
	phone, err := q.GetUserPhone(ctx, in.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CreateResult{}, httpx.ValidationFailed("Verified phone is required for top-up.")
		}
		return CreateResult{}, err
	}
	w, err := q.GetWalletByUserCurrency(ctx, fundingdb.GetWalletByUserCurrencyParams{
		UserID:   in.UserID,
		Currency: currency,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CreateResult{}, errWalletUnavailable()
		}
		return CreateResult{}, err
	}
	if w.Status == "closed" || w.FreezeMode == "full" {
		return CreateResult{}, errWalletUnavailable()
	}

	txID, err := uuid.NewV7()
	if err != nil {
		return CreateResult{}, fmt.Errorf("funding: transaction id: %w", err)
	}
	payID, err := uuid.NewV7()
	if err != nil {
		return CreateResult{}, fmt.Errorf("funding: payment id: %w", err)
	}
	ref, err := s.allocateReference(ctx, q)
	if err != nil {
		return CreateResult{}, err
	}

	idemKey := strings.TrimSpace(in.IdempotencyKey)
	coll, err := s.rail.InitiateCollection(ctx, provider.CollectionRequest{
		IdempotencyKey: idemKey,
		AmountMinor:    in.AmountMinor,
		Currency:       currency,
		PhoneE164:      phone,
		Reference:      ref,
		Metadata: map[string]string{
			"provider":       providerName,
			"transaction_id": txID.String(),
		},
	})
	if err != nil {
		return CreateResult{}, httpx.New(http.StatusBadGateway, "provider_unavailable", "Bad Gateway",
			"Payment provider did not accept the top-up.")
	}

	raw, _ := json.Marshal(coll)
	toWallet := w.ID
	var idem *string
	if idemKey != "" {
		idem = &idemKey
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreateResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tq := fundingdb.New(tx)
	txn, err := tq.InsertTransaction(ctx, fundingdb.InsertTransactionParams{
		ID:              txID,
		Reference:       ref,
		Kind:            kindTopUp,
		Status:          statusPending,
		InitiatorUserID: &in.UserID,
		FromWalletID:    nil,
		ToWalletID:      &toWallet,
		AmountMinor:     in.AmountMinor,
		FeeMinor:        0,
		Currency:        currency,
		Note:            nil,
		IdempotencyKey:  idem,
	})
	if err != nil {
		if isIdempotencyConflict(err) {
			return s.loadExistingByIdempotency(ctx, in.UserID, idemKey)
		}
		return CreateResult{}, err
	}

	pay, err := tq.InsertProviderPayment(ctx, fundingdb.InsertProviderPaymentParams{
		ID:               payID,
		TransactionID:    txID,
		Provider:         providerName,
		Direction:        directionCollection,
		ProviderRef:      coll.ProviderRef,
		Status:           payPending,
		AmountMinor:      in.AmountMinor,
		Currency:         currency,
		Raw:              raw,
		DestinationID:    nil,
		ReserveJournalID: nil,
	})
	if err != nil {
		return CreateResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateResult{}, err
	}

	return CreateResult{
		TopUp:    mapTopUp(txn, pay),
		Checkout: Checkout{Provider: providerName, ProviderRef: pay.ProviderRef, Status: string(coll.Status)},
	}, nil
}

// Get returns a top-up owned by userID (FR-TOP GET).
func (s *Service) Get(ctx context.Context, userID, topUpID uuid.UUID) (TopUp, error) {
	if s.pool == nil {
		return TopUp{}, httpx.Internal("Top-up service is not configured.")
	}
	q := fundingdb.New(s.pool)
	txn, err := q.GetTransactionByID(ctx, topUpID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TopUp{}, errTopupNotFound()
		}
		return TopUp{}, err
	}
	if txn.Kind != kindTopUp {
		return TopUp{}, errTopupNotFound()
	}
	if txn.InitiatorUserID == nil || *txn.InitiatorUserID != userID {
		return TopUp{}, errTopupForbidden()
	}
	pay, err := q.GetProviderPaymentByTxn(ctx, txn.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TopUp{}, errTopupNotFound()
		}
		return TopUp{}, err
	}
	return mapTopUp(txn, pay), nil
}

// PollStale checks pending top-ups older than 30 minutes against GetStatus
// (FR-TOP-4). Day 25's worker calls this; tests call it directly.
func (s *Service) PollStale(ctx context.Context) (int, error) {
	if s.pool == nil || s.rail == nil {
		return 0, httpx.Internal("Top-up service is not configured.")
	}
	olderThan := s.now().UTC().Add(-staleAfter)
	rows, err := fundingdb.New(s.pool).ListStalePendingTopups(ctx, fundingdb.ListStalePendingTopupsParams{
		OlderThan: olderThan,
		RowLimit:  pollBatchLimit,
	})
	if err != nil {
		return 0, err
	}
	applied := 0
	for _, pay := range rows {
		st, err := s.rail.GetStatus(ctx, pay.ProviderRef)
		if err != nil {
			s.log.Warn("topup poll get status", "provider_ref", pay.ProviderRef, "error", err)
			continue
		}
		switch st.Status {
		case provider.StatusSucceeded, provider.StatusFailed:
			res, err := s.applyStatus(ctx, applyInput{
				ProviderPath: pay.Provider,
				EventID:      "poll_" + pay.ProviderRef + "_" + string(st.Status),
				ProviderRef:  pay.ProviderRef,
				Status:       st.Status,
				AmountMinor:  st.AmountMinor,
				Currency:     st.Currency,
				Payload:      mustJSON(st),
				Actor:        actorSystem,
				ReasonPrefix: "poll_",
			})
			if err != nil {
				s.log.Warn("topup poll apply", "provider_ref", pay.ProviderRef, "error", err)
				continue
			}
			if !res.Duplicate && res.Decision != decisionIgnore {
				applied++
			}
		case provider.StatusPending:
			if err := s.expirePending(ctx, pay); err != nil {
				s.log.Warn("topup poll expire", "provider_ref", pay.ProviderRef, "error", err)
				continue
			}
			applied++
		}
	}
	return applied, nil
}

func (s *Service) expirePending(ctx context.Context, pay fundingdb.ProviderPayment) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := fundingdb.New(tx)

	txn, err := q.GetTransactionByIDForUpdate(ctx, pay.TransactionID)
	if err != nil {
		return err
	}
	if txn.Status != statusPending {
		return nil
	}
	if _, err := q.LockProviderPaymentByRef(ctx, pay.ProviderRef); err != nil {
		return err
	}
	updated, err := q.TransitionTransaction(ctx, fundingdb.TransitionTransactionParams{
		ID:         txn.ID,
		FromStatus: statusPending,
		ToStatus:   statusExpired,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if _, err := q.UpdateProviderPaymentStatus(ctx, fundingdb.UpdateProviderPaymentStatusParams{
		ID:         pay.ID,
		FromStatus: payPending,
		Status:     payExpired,
		Raw:        pay.Raw,
	}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	reason := "poll_expired"
	if _, err := q.InsertTransactionEvent(ctx, fundingdb.InsertTransactionEventParams{
		ID:            eventID,
		TransactionID: updated.ID,
		FromStatus:    statusPending,
		ToStatus:      statusExpired,
		Reason:        &reason,
		Actor:         actorSystem,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) allocateReference(ctx context.Context, q *fundingdb.Queries) (string, error) {
	for i := 0; i < maxReferenceTries; i++ {
		ref, err := NewReference()
		if err != nil {
			return "", err
		}
		if !validReference(ref) {
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
	return "", httpx.Internal("Could not allocate a top-up reference.")
}

func (s *Service) loadExistingByIdempotency(ctx context.Context, userID uuid.UUID, key string) (CreateResult, error) {
	var txnID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id FROM transactions
		WHERE initiator_user_id = $1 AND idempotency_key = $2 AND kind = 'top_up'
		LIMIT 1
	`, userID, key).Scan(&txnID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CreateResult{}, httpx.Conflict("Idempotency key conflict.")
		}
		return CreateResult{}, err
	}
	top, err := s.Get(ctx, userID, txnID)
	if err != nil {
		return CreateResult{}, err
	}
	return CreateResult{
		TopUp: top,
		Checkout: Checkout{
			Provider:    top.Provider,
			ProviderRef: top.ProviderRef,
			Status:      top.ProviderStatus,
		},
	}, nil
}

func normalizeProvider(name string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case providerTelebirr:
		return providerTelebirr, nil
	case providerChapa:
		return providerChapa, nil
	case providerSimulator:
		return providerSimulator, nil
	default:
		return "", errProviderUnknown(name)
	}
}

func mapTopUp(txn fundingdb.Transaction, pay fundingdb.ProviderPayment) TopUp {
	var to uuid.UUID
	if txn.ToWalletID != nil {
		to = *txn.ToWalletID
	}
	return TopUp{
		ID:             txn.ID,
		Reference:      txn.Reference,
		Kind:           txn.Kind,
		Status:         txn.Status,
		AmountMinor:    txn.AmountMinor,
		FeeMinor:       txn.FeeMinor,
		Currency:       txn.Currency,
		ToWalletID:     to,
		Provider:       pay.Provider,
		ProviderRef:    pay.ProviderRef,
		ProviderStatus: pay.Status,
		CreatedAt:      txn.CreatedAt,
		CompletedAt:    txn.CompletedAt,
		IdempotencyKey: txn.IdempotencyKey,
	}
}

func isIdempotencyConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		(pgErr.ConstraintName == "transactions_user_idempotency_key_idx" ||
			strings.Contains(pgErr.ConstraintName, "idempotency"))
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// accountID helpers used by webhook apply.
func (s *Service) systemAccountID(ctx context.Context, tx pgx.Tx, code string) (uuid.UUID, error) {
	acct, err := ledgerdb.New(tx).GetAccountByCode(ctx, code)
	if err != nil {
		return uuid.Nil, fmt.Errorf("funding: system account %s: %w", code, err)
	}
	return acct.ID, nil
}
