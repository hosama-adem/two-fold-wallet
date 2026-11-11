// Package devseed opens a fixed ETB wallet and funds it with a real journal.
// It is a development tool. The command refuses to run when APP_ENV=production.
package devseed

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/auth"
	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
	"github.com/hosama-adem/twofold/backend/internal/user"
	"github.com/hosama-adem/twofold/backend/internal/user/userdb"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
)

const (
	// Phone is the sample customer's number.
	Phone = "+251911000042"
	// Handle is the sample customer's @handle.
	Handle = "devwallet"
	// DisplayName is what recipient lookup shows.
	DisplayName = "Dev Wallet"
	// Password is set only when the sample user is created.
	// A later run does not reset it.
	Password = "dev-wallet-1"
	// AmountMinor is 500.00 ETB, posted as a top-up journal.
	AmountMinor int64 = 50_000
)

// Result is what the seed left in the database.
type Result struct {
	UserID                uuid.UUID
	WalletID              uuid.UUID
	AccountID             uuid.UUID
	CreatedUser           bool
	Funded                bool
	LedgerBalanceMinor    int64
	AvailableBalanceMinor int64
}

// RefuseProduction stops the command from touching a production database.
func RefuseProduction(appEnv string) error {
	if strings.EqualFold(strings.TrimSpace(appEnv), "production") {
		return errors.New("devseed: refusing to run when APP_ENV=production")
	}
	return nil
}

// Run ensures the sample user has an ETB wallet and, when the ledger balance
// is zero, credits it through ledger.Post. A second run does not post again
// while that balance is still above zero.
func Run(ctx context.Context, pool *pgxpool.Pool) (Result, error) {
	if pool == nil {
		return Result{}, errors.New("devseed: pool is required")
	}

	users := user.NewRepository(pool)
	wallets := wallet.NewService(pool)

	existing, created, err := ensureUser(ctx, users)
	if err != nil {
		return Result{}, err
	}
	switch existing.Status {
	case "pending_verification", "active":
	default:
		return Result{}, fmt.Errorf("devseed: user %s is %s", Phone, existing.Status)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if existing.Status == "pending_verification" {
		existing, err = users.MarkPhoneVerifiedTx(ctx, tx, existing.ID)
		if err != nil {
			return Result{}, err
		}
	}

	opened, err := wallets.OpenETB(ctx, tx, existing.ID)
	if err != nil {
		return Result{}, err
	}

	q := ledgerdb.New(tx)
	acct, err := q.GetAccountByID(ctx, opened.AccountID)
	if err != nil {
		return Result{}, err
	}

	funded := false
	if acct.BalanceMinor == 0 {
		if err := fund(ctx, tx, opened.AccountID); err != nil {
			return Result{}, err
		}
		funded = true
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, err
	}

	listed, err := wallets.List(ctx, existing.ID)
	if err != nil {
		return Result{}, err
	}
	var ledgerBalance, available int64
	for _, bal := range listed.Wallets {
		if bal.ID == opened.WalletID {
			ledgerBalance = bal.LedgerBalanceMinor
			available = bal.AvailableBalanceMinor
		}
	}

	return Result{
		UserID:                existing.ID,
		WalletID:              opened.WalletID,
		AccountID:             opened.AccountID,
		CreatedUser:           created,
		Funded:                funded,
		LedgerBalanceMinor:    ledgerBalance,
		AvailableBalanceMinor: available,
	}, nil
}

func ensureUser(ctx context.Context, users *user.Repository) (userdb.User, bool, error) {
	existing, err := users.GetByPhone(ctx, Phone)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, user.ErrNotFound) {
		return userdb.User{}, false, err
	}

	hash, err := auth.HashPassword(Password)
	if err != nil {
		return userdb.User{}, false, err
	}
	handle := Handle
	created, err := users.Create(ctx, user.CreateParams{
		PhoneE164:    Phone,
		Handle:       &handle,
		DisplayName:  DisplayName,
		PasswordHash: hash,
		Status:       "pending_verification",
	})
	if err != nil {
		return userdb.User{}, false, err
	}
	return created, true, nil
}

func fund(ctx context.Context, tx pgx.Tx, walletAccount uuid.UUID) error {
	clearing, err := ledgerdb.New(tx).GetAccountByCode(ctx, ledger.CodeProviderClearingTelebirr)
	if err != nil {
		return fmt.Errorf("devseed: clearing account: %w", err)
	}
	money, err := ledger.NewMoney(AmountMinor, ledger.CurrencyETB)
	if err != nil {
		return err
	}
	debit, err := ledger.NewLine(clearing.ID, ledger.Debit, money)
	if err != nil {
		return err
	}
	credit, err := ledger.NewLine(walletAccount, ledger.Credit, money)
	if err != nil {
		return err
	}
	journal, err := ledger.NewJournal(ledger.KindTopUp, []ledger.Line{debit, credit})
	if err != nil {
		return err
	}
	journal = journal.WithDescription("dev seed: opening balance")
	if _, err := ledger.Post(ctx, tx, journal); err != nil {
		return fmt.Errorf("devseed: post opening journal: %w", err)
	}
	return nil
}
