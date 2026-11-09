package ledger_test

import (
	"context"
	"testing"

	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
)

func TestAllowNegativeDefaultsFalseAndRejectsNegativeBalance(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	owner := insertUser(t, ctx, pool)
	id := insertLiabilityAccount(t, ctx, pool, owner)

	account, err := ledgerdb.New(pool).GetAccountByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if account.AllowNegative {
		t.Fatal("allow_negative defaulted to true")
	}
	if account.NormalBalance != string(ledger.Credit) || account.Type != string(ledger.Liability) {
		t.Fatalf("wallet type=%s normal=%s", account.Type, account.NormalBalance)
	}
	if account.OwnerUserID == nil || *account.OwnerUserID != owner {
		t.Fatalf("owner = %v, want %s", account.OwnerUserID, owner)
	}

	_, err = pool.Exec(ctx, `UPDATE accounts SET balance_minor = -1 WHERE id = $1`, id)
	requireViolation(t, err, "23514", "accounts_balance_nonneg_unless_allowed")

	if _, err := pool.Exec(ctx, `UPDATE accounts SET allow_negative = true WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE accounts SET balance_minor = -1 WHERE id = $1`, id); err != nil {
		t.Fatalf("opt-in negative balance: %v", err)
	}
}

func TestInsertNegativeBalanceFailsWhenNotAllowed(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	owner := insertUser(t, ctx, pool)
	id := newID()
	_, err := pool.Exec(ctx, `
		INSERT INTO accounts (
			id, code, type, normal_balance, currency, balance_minor, owner_user_id
		) VALUES ($1, $2, 'liability', 'credit', 'ETB', -1, $3)
	`, id, "wallet:"+id.String(), owner)
	requireViolation(t, err, "23514", "accounts_balance_nonneg_unless_allowed")
}

func TestAccountTypeMustMatchNormalBalance(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	owner := insertUser(t, ctx, pool)

	id := newID()
	_, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, code, type, normal_balance, currency, owner_user_id)
		VALUES ($1, $2, 'asset', 'credit', 'ETB', $3)
	`, id, "wallet:"+id.String(), owner)
	requireViolation(t, err, "23514", "accounts_type_normal_balance_match")

	id = newID()
	_, err = pool.Exec(ctx, `
		INSERT INTO accounts (id, code, type, normal_balance, currency, owner_user_id)
		VALUES ($1, $2, 'liability', 'debit', 'ETB', $3)
	`, id, "wallet:"+id.String(), owner)
	requireViolation(t, err, "23514", "accounts_type_normal_balance_match")
}

func TestOwnerlessAccountMustBeASystemCode(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	id := newID()
	_, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, code, type, normal_balance, currency)
		VALUES ($1, $2, 'liability', 'credit', 'ETB')
	`, id, "wallet:"+id.String())
	requireViolation(t, err, "23514", "accounts_system_code_iff_ownerless")
}

func TestSystemCodeCannotBeOwned(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	owner := insertUser(t, ctx, pool)
	// The code is already taken by the seed, and it is also illegal for a
	// user to own it. Postgres reports the ownership check.
	_, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, code, type, normal_balance, currency, owner_user_id)
		VALUES ($1, $2, 'revenue', 'credit', 'ETB', $3)
	`, newID(), ledger.CodeFeeRevenue, owner)
	requireViolation(t, err, "23514", "accounts_system_code_iff_ownerless")
}

func TestAccountVersionCannotBeNegative(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	owner := insertUser(t, ctx, pool)
	id := newID()
	_, err := pool.Exec(ctx, `
		INSERT INTO accounts (
			id, code, type, normal_balance, currency, version, owner_user_id
		) VALUES ($1, $2, 'liability', 'credit', 'ETB', -1, $3)
	`, id, "wallet:"+id.String(), owner)
	requireViolation(t, err, "23514", "accounts_version_nonneg")
}
