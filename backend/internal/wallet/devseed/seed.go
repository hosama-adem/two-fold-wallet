// Package devseed funds one development wallet through ledger.Post.
// It refuses every environment except development. cmd/seed is the only caller.
package devseed

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
	"github.com/hosama-adem/twofold/backend/internal/user/userdb"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
	"github.com/hosama-adem/twofold/backend/internal/wallet/walletdb"
)

const (
	// Phone is the seeded Ethiopian mobile. Login with Password.
	Phone = "+251911000001"
	// Password is the development login. It is not a production secret.
	Password = "dev-seed-password-1"
	// Handle is the @handle lookup key for the seeded user.
	Handle = "devseed"
	// DisplayName is what recipient lookup shows.
	DisplayName = "Dev Seed"
	// AmountMinor is 500.00 ETB, posted once.
	AmountMinor int64 = 50_000
	// Reference is the transactions.reference of that one journal.
	// A second seed sees the row and does not post again.
	Reference = "SEED-DEV-ETB"
)

// Result is what a successful seed left in the database.
type Result struct {
	UserID             uuid.UUID
	WalletID           uuid.UUID
	AccountID          uuid.UUID
	Funded             bool
	LedgerBalanceMinor int64
	AvailableMinor     int64
}

// Run opens the dev user's ETB wallet and, once, posts a top-up journal:
// debit provider_clearing:telebirr, credit the wallet liability.
// appEnv must be "development". Any other value returns before a write.
func Run(ctx context.Context, pool *pgxpool.Pool, appEnv string) (Result, error) {
	if appEnv != "development" {
		return Result{}, fmt.Errorf("wallet seed refused: APP_ENV=%q (only development)", appEnv)
	}
	if pool == nil {
		return Result{}, errors.New("wallet seed: database pool is required")
	}
	res, err := runOnce(ctx, pool)
	if err != nil && isUnique(err) {
		return runOnce(ctx, pool)
	}
	return res, err
}

func runOnce(ctx context.Context, pool *pgxpool.Pool) (Result, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	userID, err := ensureUser(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	opened, err := wallet.NewService(pool).OpenETB(ctx, tx, userID)
	if err != nil {
		return Result{}, err
	}
	funded, err := fund(ctx, tx, userID, opened)
	if err != nil {
		return Result{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, err
	}

	rows, err := walletdb.New(pool).ListWalletBalancesByUser(ctx, userID)
	if err != nil {
		return Result{}, err
	}
	res := Result{
		UserID:    userID,
		WalletID:  opened.WalletID,
		AccountID: opened.AccountID,
		Funded:    funded,
	}
	for _, row := range rows {
		if row.ID == opened.WalletID {
			res.LedgerBalanceMinor = row.LedgerBalanceMinor
			res.AvailableMinor = row.AvailableMinor
			break
		}
	}
	return res, nil
}

func ensureUser(ctx context.Context, tx pgx.Tx) (uuid.UUID, error) {
	q := userdb.New(tx)
	u, err := q.GetUserByPhoneForUpdate(ctx, Phone)
	if err == nil {
		if u.PhoneVerifiedAt == nil || u.Status == "pending_verification" {
			u, err = q.MarkPhoneVerified(ctx, u.ID)
			if err != nil {
				return uuid.Nil, err
			}
		}
		return u.ID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}

	hash, err := auth.HashPassword(Password)
	if err != nil {
		return uuid.Nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	handle := Handle
	created, err := q.CreateUser(ctx, userdb.CreateUserParams{
		ID:           id,
		PhoneE164:    Phone,
		Handle:       &handle,
		DisplayName:  DisplayName,
		PasswordHash: hash,
		KycTier:      0,
		Status:       "pending_verification",
	})
	if err != nil {
		return uuid.Nil, err
	}
	verified, err := q.MarkPhoneVerified(ctx, created.ID)
	if err != nil {
		return uuid.Nil, err
	}
	return verified.ID, nil
}

func fund(ctx context.Context, tx pgx.Tx, userID uuid.UUID, opened wallet.Opened) (bool, error) {
	q := walletdb.New(tx)
	if _, err := q.GetTransactionByReference(ctx, Reference); err == nil {
		return false, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}

	txnID, err := uuid.NewV7()
	if err != nil {
		return false, err
	}
	note := "dev seed"
	initiator := userID
	walletID := opened.WalletID
	if _, err := q.InsertCompletedTopUp(ctx, walletdb.InsertCompletedTopUpParams{
		ID:              txnID,
		Reference:       Reference,
		InitiatorUserID: &initiator,
		ToWalletID:      &walletID,
		AmountMinor:     AmountMinor,
		Currency:        ledger.CurrencyETB,
		Note:            &note,
	}); err != nil {
		return false, err
	}

	clearing, err := ledgerdb.New(tx).GetAccountByCode(ctx, ledger.CodeProviderClearingTelebirr)
	if err != nil {
		return false, fmt.Errorf("wallet seed: clearing account: %w", err)
	}
	money, err := ledger.NewMoney(AmountMinor, ledger.CurrencyETB)
	if err != nil {
		return false, err
	}
	debit, err := ledger.NewLine(clearing.ID, ledger.Debit, money)
	if err != nil {
		return false, err
	}
	credit, err := ledger.NewLine(opened.AccountID, ledger.Credit, money)
	if err != nil {
		return false, err
	}
	journal, err := ledger.NewJournal(ledger.KindTopUp, []ledger.Line{debit, credit})
	if err != nil {
		return false, err
	}
	journal = journal.WithDescription("dev seed").WithTransactionID(txnID)
	if _, err := ledger.Post(ctx, tx, journal); err != nil {
		return false, err
	}
	return true, nil
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
