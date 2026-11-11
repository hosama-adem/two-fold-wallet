package wallet_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
	"github.com/hosama-adem/twofold/backend/internal/wallet/walletdb"
)

func TestOpenETBIsIdempotentAndAvailableSubtractsHolds(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	userID := insertPendingUser(t, ctx, pool)
	svc := wallet.NewService(pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := svc.OpenETB(ctx, tx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if !opened.Created {
		t.Fatal("first open did not create")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	again, err := svc.OpenETB(ctx, tx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if again.Created || again.WalletID != opened.WalletID || again.AccountID != opened.AccountID {
		t.Fatalf("second open = %+v, first = %+v", again, opened)
	}

	asset := insertOwnedAsset(t, ctx, pool, userID)
	fundWallet(t, ctx, pool, asset, opened.AccountID, 5_000)

	holdID := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `
		INSERT INTO wallet_holds (id, wallet_id, amount_minor, currency, kind, status)
		VALUES ($1, $2, 1600, 'ETB', 'withdrawal', 'pending')
	`, holdID, opened.WalletID); err != nil {
		t.Fatal(err)
	}

	rows, err := walletdb.New(pool).ListBalancesByUserID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("balances = %d", len(rows))
	}
	row := rows[0]
	if row.LedgerBalanceMinor != 5_000 || row.HeldMinor != 1_600 || row.AvailableMinor != 3_400 {
		t.Fatalf("ledger=%d held=%d available=%d, want 5000/1600/3400",
			row.LedgerBalanceMinor, row.HeldMinor, row.AvailableMinor)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE wallets
		SET status = 'frozen', freeze_mode = 'none', updated_at = now()
		WHERE id = $1
	`, opened.WalletID); err == nil {
		t.Fatal("accepted frozen wallet without a freeze mode")
	} else if !isCheck(err) {
		t.Fatalf("freeze consistency: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE wallets
		SET status = 'frozen', freeze_mode = 'debit', frozen_reason = 'court order', updated_at = now()
		WHERE id = $1
	`, opened.WalletID); err != nil {
		t.Fatalf("freeze debit: %v", err)
	}

	rows, err = walletdb.New(pool).ListBalancesByUserID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Status != string(wallet.StatusFrozen) || rows[0].FreezeMode != string(wallet.FreezeDebit) {
		t.Fatalf("frozen row = %+v", rows[0])
	}
	if rows[0].AvailableMinor != 3_400 {
		t.Fatalf("freeze changed available to %d", rows[0].AvailableMinor)
	}
	gate := wallet.Gate{Status: wallet.StatusFrozen, FreezeMode: wallet.FreezeDebit}
	if gate.Allows(wallet.MovementSend) {
		t.Fatal("debit freeze still allows send")
	}
	if !gate.Allows(wallet.MovementReceive) {
		t.Fatal("debit freeze blocked receive")
	}
}

func TestWalletRejectsASecondRowAndANonLiabilityAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	userID := insertPendingUser(t, ctx, pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := wallet.NewService(pool).OpenETB(ctx, tx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	asset := insertOwnedAsset(t, ctx, pool, userID)
	if _, err := pool.Exec(ctx, `
		INSERT INTO wallets (id, user_id, account_id, currency)
		VALUES ($1, $2, $3, 'ETB')
	`, uuid.Must(uuid.NewV7()), userID, asset); err == nil {
		t.Fatal("wallet accepted an asset account")
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO wallets (id, user_id, account_id, currency)
		VALUES ($1, $2, $3, 'ETB')
	`, uuid.Must(uuid.NewV7()), userID, opened.AccountID)
	if err == nil {
		t.Fatal("second wallet for the same user inserted")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("second wallet: %v", err)
	}
}

func insertPendingUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	n, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
	if err != nil {
		t.Fatal(err)
	}
	phone := fmt.Sprintf("+2519%08d", n.Int64())
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, phone_e164, display_name, password_hash, status)
		VALUES ($1, $2, 'Wallet', 'test-hash', 'pending_verification')
	`, id, phone); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertOwnedAsset(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `
		INSERT INTO accounts (
			id, code, type, normal_balance, currency, allow_negative, owner_user_id
		) VALUES ($1, $2, 'asset', 'debit', 'ETB', false, $3)
	`, id, "asset:"+id.String(), owner); err != nil {
		t.Fatal(err)
	}
	return id
}

func fundWallet(t *testing.T, ctx context.Context, pool *pgxpool.Pool, asset, account uuid.UUID, amount int64) {
	t.Helper()
	money, err := ledger.NewMoney(amount, ledger.CurrencyETB)
	if err != nil {
		t.Fatal(err)
	}
	debit, err := ledger.NewLine(asset, ledger.Debit, money)
	if err != nil {
		t.Fatal(err)
	}
	credit, err := ledger.NewLine(account, ledger.Credit, money)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := ledger.NewJournal(ledger.KindAdjustment, []ledger.Line{debit, credit})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := ledger.Post(ctx, tx, journal); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func isCheck(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}
