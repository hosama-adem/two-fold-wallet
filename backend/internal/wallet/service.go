package wallet

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/wallet/walletdb"
)

// ErrUserNotFound means the user row to lock does not exist.
var ErrUserNotFound = errors.New("wallet: user not found")

// Service reads wallets and opens the ETB wallet inside a caller's transaction.
type Service struct {
	q *walletdb.Queries
}

// NewService builds a wallet service on a pgx pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{q: walletdb.New(pool)}
}

// ProvisionETB implements auth.WalletProvisioner.
// The liability account and the wallet row commit with the caller's transaction
// or not at all. A second call for the same user returns nil and inserts nothing.
func (s *Service) ProvisionETB(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	_, err := s.OpenETB(ctx, tx, userID)
	return err
}

// OpenETB locks the user, then inserts the ETB liability account and wallet
// when that pair does not exist yet. Created is false when the wallet was
// already there. The caller owns the transaction.
func (s *Service) OpenETB(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (Opened, error) {
	if tx == nil {
		return Opened{}, errors.New("wallet: OpenETB requires a transaction")
	}
	if userID == uuid.Nil {
		return Opened{}, errors.New("wallet: user id is required")
	}

	q := s.q.WithTx(tx)
	if _, err := q.LockUser(ctx, userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Opened{}, ErrUserNotFound
		}
		return Opened{}, err
	}

	existing, err := q.GetWalletByUserCurrency(ctx, walletdb.GetWalletByUserCurrencyParams{
		UserID:   userID,
		Currency: ledger.CurrencyETB,
	})
	if err == nil {
		return Opened{WalletID: existing.ID, AccountID: existing.AccountID}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Opened{}, err
	}

	account, err := ensureLiabilityAccount(ctx, q, userID)
	if err != nil {
		return Opened{}, err
	}

	walletID, err := uuid.NewV7()
	if err != nil {
		return Opened{}, err
	}
	row, err := q.InsertWallet(ctx, walletdb.InsertWalletParams{
		ID:        walletID,
		UserID:    userID,
		AccountID: account.ID,
		Currency:  ledger.CurrencyETB,
	})
	if err != nil {
		return Opened{}, err
	}
	return Opened{WalletID: row.ID, AccountID: row.AccountID, Created: true}, nil
}

// ensureLiabilityAccount returns the user's ETB wallet account, inserting it
// when the code is free. A wallet row can disappear (a migration down) while
// the account, which is ledger history's home, stays. Reusing that account
// keeps the code unique and the balance attached to the same bucket.
func ensureLiabilityAccount(ctx context.Context, q *walletdb.Queries, userID uuid.UUID) (walletdb.Account, error) {
	code := AccountCode(userID)
	existing, err := q.GetAccountByCode(ctx, code)
	if err == nil {
		if !liabilityOwnedBy(existing, userID) {
			return walletdb.Account{}, errors.New("wallet: account code is already used by a different account")
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return walletdb.Account{}, err
	}

	accountID, err := uuid.NewV7()
	if err != nil {
		return walletdb.Account{}, err
	}
	owner := userID
	return q.InsertLiabilityAccount(ctx, walletdb.InsertLiabilityAccountParams{
		ID:          accountID,
		Code:        code,
		Currency:    ledger.CurrencyETB,
		OwnerUserID: &owner,
	})
}

func liabilityOwnedBy(account walletdb.Account, userID uuid.UUID) bool {
	return account.Type == string(ledger.Liability) &&
		account.NormalBalance == string(ledger.Credit) &&
		!account.AllowNegative &&
		account.Currency == ledger.CurrencyETB &&
		account.OwnerUserID != nil &&
		*account.OwnerUserID == userID
}

// List returns the caller's wallets with ledger and available balances.
// The query is scoped by userID. It never reads another user's rows.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]Balance, error) {
	if userID == uuid.Nil {
		return nil, httpx.New(http.StatusUnauthorized, "unauthorized", "Unauthorized", "Missing or invalid Authorization header.")
	}
	rows, err := s.q.ListWalletBalancesByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Balance, 0, len(rows))
	for _, row := range rows {
		out = append(out, Balance{
			ID:                     row.ID,
			Currency:               row.Currency,
			Status:                 Status(row.Status),
			FreezeMode:             FreezeMode(row.FreezeMode),
			LedgerBalanceMinor:     row.LedgerBalanceMinor,
			PendingWithdrawalMinor: row.PendingWithdrawalMinor,
			AvailableMinor:         row.AvailableMinor,
		})
	}
	return out, nil
}

// Lookup finds one active verified user by phone or @handle.
// The phone in the result is masked. The raw number is not returned.
func (s *Service) Lookup(ctx context.Context, raw string) (Recipient, error) {
	q := strings.TrimSpace(raw)
	if q == "" || len(q) > 80 {
		return Recipient{}, httpx.ValidationFailed("Enter a phone number or @handle.")
	}

	var (
		display string
		handle  *string
		phone   string
		err     error
	)
	if strings.HasPrefix(q, "@") || !looksLikePhone(q) {
		name, herr := normalizeHandle(q)
		if herr != nil {
			return Recipient{}, httpx.NotFound(recipientMissing)
		}
		row, lerr := s.q.LookupRecipientByHandle(ctx, &name)
		err = lerr
		display, handle, phone = row.DisplayName, row.Handle, row.PhoneE164
	} else {
		normalized, nerr := auth.NormalizePhone(q)
		if nerr != nil {
			return Recipient{}, httpx.NotFound(recipientMissing)
		}
		row, lerr := s.q.LookupRecipientByPhone(ctx, normalized)
		err = lerr
		display, handle, phone = row.DisplayName, row.Handle, row.PhoneE164
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Recipient{}, httpx.NotFound(recipientMissing)
	}
	if err != nil {
		return Recipient{}, err
	}

	masked, err := MaskPhone(phone)
	if err != nil {
		return Recipient{}, httpx.Internal("Could not mask recipient phone.")
	}
	return Recipient{DisplayName: display, Handle: handle, PhoneMasked: masked}, nil
}
