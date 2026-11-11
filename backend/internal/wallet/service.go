package wallet

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/wallet/walletdb"
)

// CurrencyETB is the only wallet currency this product opens.
const CurrencyETB = "ETB"

// Service reads balances and opens the wallet that backs a verified phone.
type Service struct {
	pool *pgxpool.Pool
}

// NewService builds a wallet service on a pgx pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// Opened is the liability account and the wallet row created together.
type Opened struct {
	WalletID  uuid.UUID
	AccountID uuid.UUID
	Currency  string
	Created   bool
}

// OpenETB inserts the customer's ETB liability account and wallet.
// It is idempotent when called again for the same user inside a later
// transaction. The caller must hold a lock that serializes the first insert;
// phone verification locks the user row.
func (s *Service) OpenETB(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (Opened, error) {
	if tx == nil {
		return Opened{}, errors.New("wallet: OpenETB requires a transaction")
	}
	if userID == uuid.Nil {
		return Opened{}, errors.New("wallet: user id is required")
	}

	q := walletdb.New(tx)
	existing, err := q.GetWalletByUserCurrency(ctx, walletdb.GetWalletByUserCurrencyParams{
		UserID:   userID,
		Currency: CurrencyETB,
	})
	if err == nil {
		return Opened{
			WalletID:  existing.ID,
			AccountID: existing.AccountID,
			Currency:  existing.Currency,
			Created:   false,
		}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Opened{}, fmt.Errorf("wallet: lookup: %w", err)
	}

	accountID, err := uuid.NewV7()
	if err != nil {
		return Opened{}, fmt.Errorf("wallet: account id: %w", err)
	}
	walletID, err := uuid.NewV7()
	if err != nil {
		return Opened{}, fmt.Errorf("wallet: wallet id: %w", err)
	}

	owner := userID
	if _, err := q.InsertLiabilityAccount(ctx, walletdb.InsertLiabilityAccountParams{
		ID:          accountID,
		Code:        AccountCode(userID),
		Currency:    CurrencyETB,
		OwnerUserID: &owner,
	}); err != nil {
		return Opened{}, fmt.Errorf("wallet: liability account: %w", err)
	}

	row, err := q.InsertWallet(ctx, walletdb.InsertWalletParams{
		ID:        walletID,
		UserID:    userID,
		AccountID: accountID,
		Currency:  CurrencyETB,
	})
	if err != nil {
		return Opened{}, fmt.Errorf("wallet: insert: %w", err)
	}
	return Opened{
		WalletID:  row.ID,
		AccountID: row.AccountID,
		Currency:  row.Currency,
		Created:   true,
	}, nil
}

// Balance is one wallet as the app should show it.
// Available is the ledger cache minus pending withdrawal holds.
type Balance struct {
	ID                    uuid.UUID `json:"id"`
	Currency              string    `json:"currency"`
	Status                string    `json:"status"`
	FreezeMode            string    `json:"freeze_mode"`
	FrozenReason          *string   `json:"frozen_reason,omitempty"`
	LedgerBalanceMinor    int64     `json:"ledger_balance_minor"`
	AvailableBalanceMinor int64     `json:"available_balance_minor"`
	HeldMinor             int64     `json:"held_minor"`
}

// ListResult is GET /v1/wallets.
type ListResult struct {
	Wallets []Balance `json:"wallets"`
}

// List returns the caller's wallets. It does not apply Allows: a frozen
// customer can still read the number they cannot spend.
func (s *Service) List(ctx context.Context, userID uuid.UUID) (ListResult, error) {
	if s.pool == nil {
		return ListResult{}, httpx.Internal("Wallet store is not configured.")
	}
	if userID == uuid.Nil {
		return ListResult{}, httpx.ValidationFailed("User id is required.")
	}
	rows, err := walletdb.New(s.pool).ListBalancesByUserID(ctx, userID)
	if err != nil {
		return ListResult{}, err
	}
	out := make([]Balance, 0, len(rows))
	for _, row := range rows {
		out = append(out, Balance{
			ID:                    row.WalletID,
			Currency:              row.Currency,
			Status:                row.Status,
			FreezeMode:            row.FreezeMode,
			FrozenReason:          row.FrozenReason,
			LedgerBalanceMinor:    row.LedgerBalanceMinor,
			AvailableBalanceMinor: row.AvailableMinor,
			HeldMinor:             row.HeldMinor,
		})
	}
	return ListResult{Wallets: out}, nil
}

// Recipient is the confirmation card for a send. The phone is masked.
type Recipient struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"display_name"`
	Handle      *string   `json:"handle,omitempty"`
	PhoneMasked string    `json:"phone_masked"`
}

// Lookup finds an active wallet holder by phone or @handle.
// The raw phone number is not part of the result.
func (s *Service) Lookup(ctx context.Context, raw string) (Recipient, error) {
	if s.pool == nil {
		return Recipient{}, httpx.Internal("Wallet store is not configured.")
	}
	query, err := ParseLookup(raw)
	if err != nil {
		return Recipient{}, err
	}

	q := walletdb.New(s.pool)
	var found recipientRow
	switch query.Kind {
	case QueryPhone:
		row, err := q.LookupRecipientByPhone(ctx, query.Value)
		if err != nil {
			return Recipient{}, lookupError(err)
		}
		found = recipientRow{id: row.ID, displayName: row.DisplayName, handle: row.Handle, phone: row.PhoneE164}
	case QueryHandle:
		handle := query.Value
		row, err := q.LookupRecipientByHandle(ctx, &handle)
		if err != nil {
			return Recipient{}, lookupError(err)
		}
		found = recipientRow{id: row.ID, displayName: row.DisplayName, handle: row.Handle, phone: row.PhoneE164}
	default:
		return Recipient{}, httpx.ValidationFailed("Enter a phone number or @handle.")
	}

	masked, err := MaskPhone(found.phone)
	if err != nil {
		return Recipient{}, httpx.Internal("Could not mask phone.")
	}
	return Recipient{
		ID:          found.id,
		DisplayName: redactPhone(found.displayName, found.phone, masked),
		Handle:      found.handle,
		PhoneMasked: masked,
	}, nil
}

type recipientRow struct {
	id          uuid.UUID
	displayName string
	handle      *string
	phone       string
}

func lookupError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NotFound("No user matches that phone or handle.")
	}
	return err
}

// redactPhone strips every form of the stored number out of a display name
// so a lookup response cannot smuggle the phone through another field.
func redactPhone(name, e164, masked string) string {
	if e164 == "" {
		return name
	}
	name = strings.ReplaceAll(name, e164, masked)
	if len(e164) > 4 {
		national := e164[4:]
		name = strings.ReplaceAll(name, e164[1:], masked)
		name = strings.ReplaceAll(name, "0"+national, masked)
		name = strings.ReplaceAll(name, national, masked)
	}
	return name
}
