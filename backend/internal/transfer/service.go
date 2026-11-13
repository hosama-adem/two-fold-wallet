package transfer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/fee"
	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
	"github.com/hosama-adem/twofold/backend/internal/limit"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/transfer/transferdb"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
)

const (
	kindP2P           = "p2p"
	statusPending     = "pending"
	statusCompleted   = "completed"
	actorUser         = "user"
	maxNoteRunes      = 140
	maxReferenceTries = 8
)

// Service posts peer-to-peer transfers.
type Service struct {
	pool *pgxpool.Pool
	fees *fee.Service
}

// NewService builds a transfer service on a pgx pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{
		pool: pool,
		fees: fee.NewService(pool),
	}
}

// Request is a validated send intent.
type Request struct {
	SenderUserID     uuid.UUID
	RecipientQuery   string
	AmountMinor      int64
	Currency         string
	Note             string
	ExpectedFeeMinor int64
	IdempotencyKey   string
}

// Result is the completed transfer the API returns.
type Result struct {
	Transaction Transaction `json:"transaction"`
}

// Transaction is the customer-facing money movement record.
type Transaction struct {
	ID             uuid.UUID  `json:"id"`
	Reference      string     `json:"reference"`
	Kind           string     `json:"kind"`
	Status         string     `json:"status"`
	AmountMinor    int64      `json:"amount_minor"`
	FeeMinor       int64      `json:"fee_minor"`
	Currency       string     `json:"currency"`
	Note           *string    `json:"note,omitempty"`
	FromWalletID   uuid.UUID  `json:"from_wallet_id"`
	ToWalletID     uuid.UUID  `json:"to_wallet_id"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	IdempotencyKey *string    `json:"idempotency_key,omitempty"`
}

// PreviewRequest is a dry-run send quote.
type PreviewRequest struct {
	SenderUserID   uuid.UUID
	RecipientQuery string
	AmountMinor    int64
	Currency       string
}

// PreviewResult is what the confirm screen shows. No money moves.
type PreviewResult struct {
	AmountMinor     int64             `json:"amount_minor"`
	FeeMinor        int64             `json:"fee_minor"`
	TotalDebitMinor int64             `json:"total_debit_minor"`
	Currency        string            `json:"currency"`
	Recipient       PreviewRecipient  `json:"recipient"`
}

// PreviewRecipient is the identity card on the confirm screen.
type PreviewRecipient struct {
	DisplayName string  `json:"display_name"`
	Handle      *string `json:"handle,omitempty"`
	PhoneMasked string  `json:"phone_masked"`
}

type lockedWallet struct {
	WalletID           uuid.UUID
	UserID             uuid.UUID
	AccountID          uuid.UUID
	Currency           string
	Status             string
	FreezeMode         string
	LedgerBalanceMinor int64
	HeldMinor          int64
	AvailableMinor     int64
}

type resolvedRecipient struct {
	UserID      uuid.UUID
	DisplayName string
	Handle      *string
	PhoneE164   string
}

// Preview returns amount, fee, total debit, and recipient display — and moves
// nothing (FR-TRF-2).
func (s *Service) Preview(ctx context.Context, in PreviewRequest) (PreviewResult, error) {
	if s.pool == nil {
		return PreviewResult{}, httpx.Internal("Transfer store is not configured.")
	}
	if err := validatePreview(in); err != nil {
		return PreviewResult{}, err
	}

	recipient, err := s.resolveRecipient(ctx, in.RecipientQuery)
	if err != nil {
		return PreviewResult{}, err
	}
	if recipient.UserID == in.SenderUserID {
		return PreviewResult{}, errSelfTransfer()
	}

	feeMinor, err := s.fees.ForAmount(ctx, in.SenderUserID, kindP2P, in.AmountMinor)
	if err != nil {
		return PreviewResult{}, err
	}

	masked, err := wallet.MaskPhone(recipient.PhoneE164)
	if err != nil {
		return PreviewResult{}, httpx.Internal("Could not mask phone.")
	}

	return PreviewResult{
		AmountMinor:     in.AmountMinor,
		FeeMinor:        feeMinor,
		TotalDebitMinor: in.AmountMinor + feeMinor,
		Currency:        in.Currency,
		Recipient: PreviewRecipient{
			DisplayName: recipient.DisplayName,
			Handle:      recipient.Handle,
			PhoneMasked: masked,
		},
	}, nil
}

// Transfer moves amount_minor from the sender's ETB wallet to the recipient.
//
// Inside one database transaction:
//  1. Resolve the recipient (phone or @handle).
//  2. Lock both wallets (and their accounts) in sorted wallet-id order.
//  3. Check freeze, self-send, available balance, and KYC tier limits.
//  4. Insert transactions as pending, post the p2p journal, mark completed.
//
// The fee is computed from fee_rules. expected_fee_minor must match or the
// call returns 409 fee_changed and moves nothing (FR-TRF-3, PRD §7.3).
// limit.Check / CheckReceive run after locks and before the journal so races
// cannot slip past a daily or max-balance cap (PRD §12.1).
func (s *Service) Transfer(ctx context.Context, in Request) (Result, error) {
	if s.pool == nil {
		return Result{}, httpx.Internal("Transfer store is not configured.")
	}
	if err := validateRequest(in); err != nil {
		return Result{}, err
	}

	recipient, err := s.resolveRecipient(ctx, in.RecipientQuery)
	if err != nil {
		return Result{}, err
	}
	if recipient.UserID == in.SenderUserID {
		return Result{}, errSelfTransfer()
	}

	feeMinor, err := s.fees.ForAmount(ctx, in.SenderUserID, kindP2P, in.AmountMinor)
	if err != nil {
		return Result{}, err
	}
	if in.ExpectedFeeMinor != feeMinor {
		return Result{}, errFeeChanged(in.ExpectedFeeMinor, feeMinor)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := transferdb.New(tx)

	fromWallet, err := q.GetWalletByUserCurrency(ctx, transferdb.GetWalletByUserCurrencyParams{
		UserID:   in.SenderUserID,
		Currency: in.Currency,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, httpx.ValidationFailed("Sender has no ETB wallet.")
		}
		return Result{}, err
	}
	toWallet, err := q.GetWalletByUserCurrency(ctx, transferdb.GetWalletByUserCurrencyParams{
		UserID:   recipient.UserID,
		Currency: in.Currency,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, errRecipientNotFound()
		}
		return Result{}, err
	}

	locked, err := lockWalletsOrdered(ctx, q, fromWallet.ID, toWallet.ID)
	if err != nil {
		return Result{}, err
	}
	from := locked[fromWallet.ID]
	to := locked[toWallet.ID]

	if err := checkSender(from); err != nil {
		return Result{}, err
	}
	if err := checkRecipient(to); err != nil {
		return Result{}, err
	}

	debitTotal := in.AmountMinor + feeMinor
	if from.AvailableMinor < debitTotal {
		return Result{}, errInsufficientFunds(from.AvailableMinor, debitTotal, in.Currency)
	}

	if err := limit.Check(ctx, tx, in.SenderUserID, kindP2P, in.AmountMinor); err != nil {
		return Result{}, err
	}
	if err := limit.CheckReceive(ctx, tx, to.UserID, to.LedgerBalanceMinor, in.AmountMinor); err != nil {
		return Result{}, err
	}

	txID, err := uuid.NewV7()
	if err != nil {
		return Result{}, fmt.Errorf("transfer: transaction id: %w", err)
	}
	ref, err := allocateReference(ctx, q)
	if err != nil {
		return Result{}, err
	}

	var note *string
	if in.Note != "" {
		n := in.Note
		note = &n
	}
	var idem *string
	if in.IdempotencyKey != "" {
		k := in.IdempotencyKey
		idem = &k
	}
	fromID := from.WalletID
	toID := to.WalletID

	if _, err := q.InsertTransaction(ctx, transferdb.InsertTransactionParams{
		ID:              txID,
		Reference:       ref,
		Kind:            kindP2P,
		Status:          statusPending,
		InitiatorUserID: &in.SenderUserID,
		FromWalletID:    &fromID,
		ToWalletID:      &toID,
		AmountMinor:     in.AmountMinor,
		FeeMinor:        feeMinor,
		Currency:        in.Currency,
		Note:            note,
		IdempotencyKey:  idem,
	}); err != nil {
		return Result{}, mapInsertError(err)
	}

	feeAccountID, err := feeRevenueAccountID(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	journal, err := buildP2PJournal(from.AccountID, to.AccountID, feeAccountID, in.AmountMinor, feeMinor, in.Currency)
	if err != nil {
		return Result{}, err
	}
	journal = journal.WithTransactionID(txID).WithDescription("P2P " + ref)
	if _, err := ledger.Post(ctx, tx, journal); err != nil {
		if errors.Is(err, ledger.ErrNegativeBalance) {
			return Result{}, errInsufficientFunds(from.AvailableMinor, debitTotal, in.Currency)
		}
		return Result{}, err
	}

	eventID, err := uuid.NewV7()
	if err != nil {
		return Result{}, fmt.Errorf("transfer: event id: %w", err)
	}
	reason := "transfer completed"
	if _, err := q.InsertTransactionEvent(ctx, transferdb.InsertTransactionEventParams{
		ID:            eventID,
		TransactionID: txID,
		FromStatus:    statusPending,
		ToStatus:      statusCompleted,
		Reason:        &reason,
		Actor:         actorUser,
	}); err != nil {
		return Result{}, fmt.Errorf("transfer: event: %w", err)
	}

	completed, err := q.CompleteTransaction(ctx, txID)
	if err != nil {
		return Result{}, fmt.Errorf("transfer: complete: %w", err)
	}
	if completed.Status != statusCompleted {
		return Result{}, httpx.Internal("Transfer did not complete.")
	}

	if err := tx.Commit(ctx); err != nil {
		return Result{}, err
	}
	return Result{Transaction: transactionFrom(completed)}, nil
}

func validatePreview(in PreviewRequest) error {
	if in.SenderUserID == uuid.Nil {
		return httpx.ValidationFailed("User id is required.")
	}
	if strings.TrimSpace(in.RecipientQuery) == "" {
		return httpx.ValidationFailed("Recipient is required.")
	}
	if in.Currency != wallet.CurrencyETB && in.Currency != ledger.CurrencyETB {
		return httpx.ValidationFailed("Currency must be ETB.")
	}
	if in.AmountMinor <= 0 || in.AmountMinor > ledger.MaxAmountMinor {
		return httpx.ValidationFailed("Amount must be a positive amount within limits.")
	}
	return nil
}

func validateRequest(in Request) error {
	if in.SenderUserID == uuid.Nil {
		return httpx.ValidationFailed("User id is required.")
	}
	if strings.TrimSpace(in.RecipientQuery) == "" {
		return httpx.ValidationFailed("Recipient is required.")
	}
	if in.Currency != wallet.CurrencyETB && in.Currency != ledger.CurrencyETB {
		return httpx.ValidationFailed("Currency must be ETB.")
	}
	if in.AmountMinor <= 0 || in.AmountMinor > ledger.MaxAmountMinor {
		return httpx.ValidationFailed("Amount must be a positive amount within limits.")
	}
	if in.ExpectedFeeMinor < 0 || in.ExpectedFeeMinor > ledger.MaxAmountMinor {
		return httpx.ValidationFailed("expected_fee_minor must be zero or a positive amount within limits.")
	}
	if utf8.RuneCountInString(in.Note) > maxNoteRunes {
		return httpx.ValidationFailed("Note must be at most 140 characters.")
	}
	return nil
}

func (s *Service) resolveRecipient(ctx context.Context, raw string) (resolvedRecipient, error) {
	query, err := wallet.ParseLookup(raw)
	if err != nil {
		return resolvedRecipient{}, err
	}
	q := transferdb.New(s.pool)
	switch query.Kind {
	case wallet.QueryPhone:
		row, err := q.LookupRecipientByPhone(ctx, query.Value)
		if err != nil {
			return resolvedRecipient{}, lookupError(err)
		}
		return resolvedRecipient{
			UserID:      row.ID,
			DisplayName: row.DisplayName,
			Handle:      row.Handle,
			PhoneE164:   row.PhoneE164,
		}, nil
	case wallet.QueryHandle:
		handle := query.Value
		row, err := q.LookupRecipientByHandle(ctx, &handle)
		if err != nil {
			return resolvedRecipient{}, lookupError(err)
		}
		return resolvedRecipient{
			UserID:      row.ID,
			DisplayName: row.DisplayName,
			Handle:      row.Handle,
			PhoneE164:   row.PhoneE164,
		}, nil
	default:
		return resolvedRecipient{}, httpx.ValidationFailed("Enter a phone number or @handle.")
	}
}

func lookupError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return errRecipientNotFound()
	}
	return err
}

func lockWalletsOrdered(ctx context.Context, q *transferdb.Queries, a, b uuid.UUID) (map[uuid.UUID]lockedWallet, error) {
	ids := []uuid.UUID{a, b}
	slices.SortFunc(ids, func(x, y uuid.UUID) int {
		return bytes.Compare(x[:], y[:])
	})
	out := make(map[uuid.UUID]lockedWallet, 2)
	for _, id := range ids {
		row, err := q.LockWalletBalance(ctx, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, errRecipientNotFound()
			}
			return nil, fmt.Errorf("transfer: lock wallet: %w", err)
		}
		out[id] = lockedWallet{
			WalletID:           row.WalletID,
			UserID:             row.UserID,
			AccountID:          row.AccountID,
			Currency:           row.Currency,
			Status:             row.Status,
			FreezeMode:         row.FreezeMode,
			LedgerBalanceMinor: row.LedgerBalanceMinor,
			HeldMinor:          row.HeldMinor,
			AvailableMinor:     row.LedgerBalanceMinor - row.HeldMinor,
		}
	}
	return out, nil
}

func checkSender(w lockedWallet) error {
	if w.Status == "closed" {
		return errWalletFrozen("Your wallet is closed and cannot send money.")
	}
	if w.Status == "frozen" || w.FreezeMode == "debit" || w.FreezeMode == "full" {
		return errWalletFrozen("Your wallet is frozen and cannot send money.")
	}
	if w.Status != "active" || w.FreezeMode != "none" {
		return errWalletFrozen("Your wallet cannot send money right now.")
	}
	return nil
}

func checkRecipient(w lockedWallet) error {
	if w.Status == "closed" {
		return errRecipientNotFound()
	}
	if w.FreezeMode == "full" {
		return errWalletFrozen("The recipient wallet is frozen and cannot receive money.")
	}
	return nil
}

// buildP2PJournal matches PRD §7.3:
//
//	fee 0: Debit sender amount / Credit recipient amount
//	fee n: Debit sender amount+n / Credit recipient amount / Credit fee_revenue n
//
// The fee_revenue line is a posting, not a locked row (ADR 0003, PRD §8.2).
func buildP2PJournal(fromAccount, toAccount, feeAccount uuid.UUID, amount, feeMinor int64, currency string) (ledger.Journal, error) {
	if feeMinor < 0 {
		return ledger.Journal{}, httpx.Internal("Fee cannot be negative.")
	}
	if feeMinor == 0 {
		money, err := ledger.NewMoney(amount, currency)
		if err != nil {
			return ledger.Journal{}, httpx.ValidationFailed("Amount must be a positive amount within limits.")
		}
		debit, err := ledger.NewLine(fromAccount, ledger.Debit, money)
		if err != nil {
			return ledger.Journal{}, err
		}
		credit, err := ledger.NewLine(toAccount, ledger.Credit, money)
		if err != nil {
			return ledger.Journal{}, err
		}
		return ledger.NewJournal(ledger.KindP2P, []ledger.Line{debit, credit})
	}

	debitTotal, ok := addMinor(amount, feeMinor)
	if !ok {
		return ledger.Journal{}, httpx.ValidationFailed("Amount plus fee is out of range.")
	}
	debitMoney, err := ledger.NewMoney(debitTotal, currency)
	if err != nil {
		return ledger.Journal{}, httpx.ValidationFailed("Amount plus fee must be a positive amount within limits.")
	}
	recvMoney, err := ledger.NewMoney(amount, currency)
	if err != nil {
		return ledger.Journal{}, httpx.ValidationFailed("Amount must be a positive amount within limits.")
	}
	feeMoney, err := ledger.NewMoney(feeMinor, currency)
	if err != nil {
		return ledger.Journal{}, httpx.ValidationFailed("Fee must be a positive amount within limits.")
	}
	if feeAccount == uuid.Nil {
		return ledger.Journal{}, httpx.Internal("Fee revenue account is not configured.")
	}

	debit, err := ledger.NewLine(fromAccount, ledger.Debit, debitMoney)
	if err != nil {
		return ledger.Journal{}, err
	}
	creditRecv, err := ledger.NewLine(toAccount, ledger.Credit, recvMoney)
	if err != nil {
		return ledger.Journal{}, err
	}
	creditFee, err := ledger.NewLine(feeAccount, ledger.Credit, feeMoney)
	if err != nil {
		return ledger.Journal{}, err
	}
	return ledger.NewJournal(ledger.KindP2P, []ledger.Line{debit, creditRecv, creditFee})
}

func feeRevenueAccountID(ctx context.Context, tx pgx.Tx) (uuid.UUID, error) {
	acct, err := ledgerdb.New(tx).GetAccountByCode(ctx, ledger.CodeFeeRevenue)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, httpx.Internal("Fee revenue account is not configured.")
		}
		return uuid.Nil, err
	}
	return acct.ID, nil
}

func addMinor(a, b int64) (int64, bool) {
	sum := a + b
	if (b > 0 && sum < a) || (b < 0 && sum > a) {
		return 0, false
	}
	return sum, true
}

func allocateReference(ctx context.Context, q *transferdb.Queries) (string, error) {
	for i := 0; i < maxReferenceTries; i++ {
		ref, err := NewReference()
		if err != nil {
			return "", err
		}
		exists, err := q.ReferenceExists(ctx, ref)
		if err != nil {
			return "", fmt.Errorf("transfer: reference check: %w", err)
		}
		if !exists {
			return ref, nil
		}
	}
	return "", httpx.Internal("Could not allocate a transfer reference.")
}

func transactionFrom(row transferdb.Transaction) Transaction {
	out := Transaction{
		ID:             row.ID,
		Reference:      row.Reference,
		Kind:           row.Kind,
		Status:         row.Status,
		AmountMinor:    row.AmountMinor,
		FeeMinor:       row.FeeMinor,
		Currency:       row.Currency,
		Note:           row.Note,
		CreatedAt:      row.CreatedAt,
		CompletedAt:    row.CompletedAt,
		IdempotencyKey: row.IdempotencyKey,
	}
	if row.FromWalletID != nil {
		out.FromWalletID = *row.FromWalletID
	}
	if row.ToWalletID != nil {
		out.ToWalletID = *row.ToWalletID
	}
	return out
}

func mapInsertError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		if pgErr.ConstraintName == "transactions_user_idempotency_key_idx" {
			return httpx.Conflict("A transfer with this Idempotency-Key already exists.")
		}
		if pgErr.ConstraintName == "transactions_reference_unique" {
			return httpx.Internal("Transfer reference collision.")
		}
	}
	return fmt.Errorf("transfer: insert: %w", err)
}
