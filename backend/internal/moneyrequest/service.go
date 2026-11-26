package moneyrequest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/moneyrequest/moneyrequestdb"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/transfer"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
)

const (
	statusPending = "pending"

	boxIncoming = "incoming"
	boxOutgoing = "outgoing"

	defaultTTL   = 7 * 24 * time.Hour
	maxNoteRunes = 140
	listLimit    = 50
)

// Service owns the money-request state machine (FR-REQ).
type Service struct {
	pool      *pgxpool.Pool
	q         *moneyrequestdb.Queries
	transfers *transfer.Service
}

// NewService builds a money-request service. transfers is required for Pay.
func NewService(pool *pgxpool.Pool, transfers *transfer.Service) *Service {
	return &Service{
		pool:      pool,
		q:         moneyrequestdb.New(pool),
		transfers: transfers,
	}
}

// CreateInput is POST /v1/requests.
type CreateInput struct {
	RequesterID uuid.UUID
	PayerQuery  string
	AmountMinor int64
	Currency    string
	Note        string
}

// PayInput is POST /v1/requests/{id}/pay.
type PayInput struct {
	PayerUserID      uuid.UUID
	RequestID        uuid.UUID
	ExpectedFeeMinor int64
	IdempotencyKey   string
}

// Party is the other side of a request, masked for the caller.
type Party struct {
	DisplayName string  `json:"display_name"`
	Handle      *string `json:"handle,omitempty"`
	PhoneMasked string  `json:"phone_masked"`
}

// Request is the customer-facing money-request record.
type Request struct {
	ID                uuid.UUID  `json:"id"`
	AmountMinor       int64      `json:"amount_minor"`
	Currency          string     `json:"currency"`
	Note              *string    `json:"note,omitempty"`
	Status            string     `json:"status"`
	ExpiresAt         time.Time  `json:"expires_at"`
	CreatedAt         time.Time  `json:"created_at"`
	PaidTransactionID *uuid.UUID `json:"paid_transaction_id,omitempty"`
	Counterparty      Party      `json:"counterparty"`
}

// ListResult is GET /v1/requests.
type ListResult struct {
	Items []Request `json:"items"`
}

// PayResult is the paid request plus the transfer that fulfilled it.
type PayResult struct {
	Request     Request              `json:"request"`
	Transaction transfer.Transaction `json:"transaction"`
}

// Create opens a pending request against a payer (FR-REQ-1).
func (s *Service) Create(ctx context.Context, in CreateInput) (Request, error) {
	if s.pool == nil {
		return Request{}, httpx.Internal("Money request store is not configured.")
	}
	if in.RequesterID == uuid.Nil {
		return Request{}, httpx.ValidationFailed("User id is required.")
	}
	currency := strings.TrimSpace(in.Currency)
	if currency == "" {
		currency = wallet.CurrencyETB
	}
	if currency != wallet.CurrencyETB {
		return Request{}, httpx.ValidationFailed("Currency must be ETB.")
	}
	if in.AmountMinor <= 0 || in.AmountMinor > ledger.MaxAmountMinor {
		return Request{}, httpx.ValidationFailed("Amount must be a positive amount within limits.")
	}
	if utf8.RuneCountInString(in.Note) > maxNoteRunes {
		return Request{}, httpx.ValidationFailed("Note must be at most 140 characters.")
	}

	payer, err := s.resolvePayer(ctx, in.PayerQuery)
	if err != nil {
		return Request{}, err
	}
	if payer.ID == in.RequesterID {
		return Request{}, errSelfRequest()
	}

	id, err := uuid.NewV7()
	if err != nil {
		return Request{}, fmt.Errorf("moneyrequest: id: %w", err)
	}
	now := time.Now().UTC()
	var note *string
	if strings.TrimSpace(in.Note) != "" {
		n := strings.TrimSpace(in.Note)
		note = &n
	}

	row, err := s.q.InsertMoneyRequest(ctx, moneyrequestdb.InsertMoneyRequestParams{
		ID:          id,
		RequesterID: in.RequesterID,
		PayerID:     payer.ID,
		AmountMinor: in.AmountMinor,
		Currency:    currency,
		Note:        note,
		ExpiresAt:   now.Add(defaultTTL),
	})
	if err != nil {
		return Request{}, err
	}
	return s.view(ctx, row, in.RequesterID)
}

// List returns the caller's inbox or outbox (FR-REQ-2).
func (s *Service) List(ctx context.Context, userID uuid.UUID, box string) (ListResult, error) {
	if s.pool == nil {
		return ListResult{}, httpx.Internal("Money request store is not configured.")
	}
	if userID == uuid.Nil {
		return ListResult{}, httpx.ValidationFailed("User id is required.")
	}
	box = strings.TrimSpace(strings.ToLower(box))
	if box == "" {
		box = boxIncoming
	}
	if box != boxIncoming && box != boxOutgoing {
		return ListResult{}, httpx.ValidationFailed("box must be incoming or outgoing.")
	}
	if err := s.expireDue(ctx); err != nil {
		return ListResult{}, err
	}

	var rows []moneyrequestdb.MoneyRequest
	var err error
	switch box {
	case boxIncoming:
		rows, err = s.q.ListIncoming(ctx, moneyrequestdb.ListIncomingParams{
			PayerID:  userID,
			RowLimit: listLimit,
		})
	default:
		rows, err = s.q.ListOutgoing(ctx, moneyrequestdb.ListOutgoingParams{
			RequesterID: userID,
			RowLimit:    listLimit,
		})
	}
	if err != nil {
		return ListResult{}, err
	}

	out := ListResult{Items: make([]Request, 0, len(rows))}
	for _, row := range rows {
		item, err := s.view(ctx, row, userID)
		if err != nil {
			return ListResult{}, err
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// Pay fulfills a pending request with a p2p transfer (FR-REQ-2, FR-REQ-4).
// The request row is locked, the transfer posts, and MarkPaid runs in one tx.
func (s *Service) Pay(ctx context.Context, in PayInput) (PayResult, error) {
	if s.pool == nil {
		return PayResult{}, httpx.Internal("Money request store is not configured.")
	}
	if s.transfers == nil {
		return PayResult{}, httpx.Internal("Transfer service is not configured.")
	}
	if in.PayerUserID == uuid.Nil || in.RequestID == uuid.Nil {
		return PayResult{}, httpx.ValidationFailed("Request id is required.")
	}
	if err := s.expireDue(ctx); err != nil {
		return PayResult{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PayResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := moneyrequestdb.New(tx)
	row, err := q.LockMoneyRequest(ctx, in.RequestID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PayResult{}, errNotFound()
		}
		return PayResult{}, err
	}
	if row.PayerID != in.PayerUserID {
		return PayResult{}, errForbidden()
	}
	if row.Status != statusPending {
		return PayResult{}, errNotPending("This money request is no longer pending.")
	}
	if !row.ExpiresAt.After(time.Now().UTC()) {
		if _, err := q.MarkExpired(ctx, row.ID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return PayResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return PayResult{}, err
		}
		return PayResult{}, errNotPending("This money request has expired.")
	}

	note := ""
	if row.Note != nil {
		note = *row.Note
	}
	xfer, err := s.transfers.TransferInTx(ctx, tx, transfer.Request{
		SenderUserID:     in.PayerUserID,
		RecipientUserID:  row.RequesterID,
		AmountMinor:      row.AmountMinor,
		Currency:         row.Currency,
		Note:             note,
		ExpectedFeeMinor: in.ExpectedFeeMinor,
		IdempotencyKey:   in.IdempotencyKey,
	})
	if err != nil {
		return PayResult{}, err
	}

	paid, err := q.MarkPaid(ctx, moneyrequestdb.MarkPaidParams{
		ID:                row.ID,
		PaidTransactionID: &xfer.Transaction.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PayResult{}, errNotPending("This money request is no longer pending.")
		}
		return PayResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PayResult{}, err
	}

	view, err := s.view(ctx, paid, in.PayerUserID)
	if err != nil {
		return PayResult{}, err
	}
	return PayResult{Request: view, Transaction: xfer.Transaction}, nil
}

// Decline lets the payer reject a pending request (FR-REQ-2).
func (s *Service) Decline(ctx context.Context, payerID, requestID uuid.UUID) (Request, error) {
	if s.pool == nil {
		return Request{}, httpx.Internal("Money request store is not configured.")
	}
	if err := s.expireDue(ctx); err != nil {
		return Request{}, err
	}
	row, err := s.q.MarkDeclined(ctx, moneyrequestdb.MarkDeclinedParams{
		ID:      requestID,
		PayerID: payerID,
	})
	if err != nil {
		return Request{}, s.mapGuardError(ctx, requestID, payerID, false, err)
	}
	return s.view(ctx, row, payerID)
}

// Cancel lets the requester withdraw a pending request (FR-REQ-3).
func (s *Service) Cancel(ctx context.Context, requesterID, requestID uuid.UUID) (Request, error) {
	if s.pool == nil {
		return Request{}, httpx.Internal("Money request store is not configured.")
	}
	if err := s.expireDue(ctx); err != nil {
		return Request{}, err
	}
	row, err := s.q.MarkCancelled(ctx, moneyrequestdb.MarkCancelledParams{
		ID:          requestID,
		RequesterID: requesterID,
	})
	if err != nil {
		return Request{}, s.mapGuardError(ctx, requestID, requesterID, true, err)
	}
	return s.view(ctx, row, requesterID)
}

// ExpireDue marks pending requests past expires_at as expired.
// cmd/worker runs this on a River schedule; list/pay/cancel still call it eagerly.
func (s *Service) ExpireDue(ctx context.Context) error {
	return s.expireDue(ctx)
}

func (s *Service) expireDue(ctx context.Context) error {
	return s.q.ExpireDueRequests(ctx)
}

type partyRow struct {
	ID          uuid.UUID
	DisplayName string
	Handle      *string
	PhoneE164   string
}

func (s *Service) resolvePayer(ctx context.Context, raw string) (partyRow, error) {
	query, err := wallet.ParseLookup(raw)
	if err != nil {
		return partyRow{}, err
	}
	// Reuse the transfer recipient rules: active, verified, open ETB wallet.
	switch query.Kind {
	case wallet.QueryPhone:
		var row partyRow
		err := s.pool.QueryRow(ctx, `
			SELECT u.id, u.display_name, u.handle, u.phone_e164
			FROM users u
			JOIN wallets w ON w.user_id = u.id AND w.currency = 'ETB' AND w.status <> 'closed'
			WHERE u.status = 'active' AND u.phone_verified_at IS NOT NULL AND u.phone_e164 = $1
		`, query.Value).Scan(&row.ID, &row.DisplayName, &row.Handle, &row.PhoneE164)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return partyRow{}, httpx.NotFound("No Twofold user matches that phone or handle.")
			}
			return partyRow{}, err
		}
		return row, nil
	case wallet.QueryHandle:
		var row partyRow
		err := s.pool.QueryRow(ctx, `
			SELECT u.id, u.display_name, u.handle, u.phone_e164
			FROM users u
			JOIN wallets w ON w.user_id = u.id AND w.currency = 'ETB' AND w.status <> 'closed'
			WHERE u.status = 'active' AND u.phone_verified_at IS NOT NULL AND u.handle = $1
		`, query.Value).Scan(&row.ID, &row.DisplayName, &row.Handle, &row.PhoneE164)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return partyRow{}, httpx.NotFound("No Twofold user matches that phone or handle.")
			}
			return partyRow{}, err
		}
		return row, nil
	default:
		return partyRow{}, httpx.ValidationFailed("Enter a phone number or @handle.")
	}
}

func (s *Service) view(ctx context.Context, row moneyrequestdb.MoneyRequest, viewer uuid.UUID) (Request, error) {
	otherID := row.PayerID
	if viewer == row.PayerID {
		otherID = row.RequesterID
	}
	cp, err := s.q.LookupCounterparty(ctx, otherID)
	if err != nil {
		return Request{}, err
	}
	masked, err := wallet.MaskPhone(cp.PhoneE164)
	if err != nil {
		return Request{}, httpx.Internal("Could not mask phone.")
	}
	return Request{
		ID:                row.ID,
		AmountMinor:       row.AmountMinor,
		Currency:          row.Currency,
		Note:              row.Note,
		Status:            row.Status,
		ExpiresAt:         row.ExpiresAt.UTC(),
		CreatedAt:         row.CreatedAt.UTC(),
		PaidTransactionID: row.PaidTransactionID,
		Counterparty: Party{
			DisplayName: cp.DisplayName,
			Handle:      cp.Handle,
			PhoneMasked: masked,
		},
	}, nil
}

func (s *Service) mapGuardError(ctx context.Context, requestID, actor uuid.UUID, asRequester bool, err error) error {
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	row, getErr := s.q.GetMoneyRequest(ctx, requestID)
	if getErr != nil {
		if errors.Is(getErr, pgx.ErrNoRows) {
			return errNotFound()
		}
		return getErr
	}
	if asRequester && row.RequesterID != actor {
		return errForbidden()
	}
	if !asRequester && row.PayerID != actor {
		return errForbidden()
	}
	if row.Status != statusPending {
		return errNotPending("This money request is no longer pending.")
	}
	if !row.ExpiresAt.After(time.Now().UTC()) {
		return errNotPending("This money request has expired.")
	}
	return errNotPending("This money request is no longer pending.")
}
