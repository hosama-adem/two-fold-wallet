package ledger_test

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
)

func newID() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}

func requireViolation(t *testing.T, err error, code, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("want postgres %s (%s), got %v", code, constraint, err)
	}
	if pgErr.Code != code || pgErr.ConstraintName != constraint {
		t.Fatalf("got %s (%s), want %s (%s)", pgErr.Code, pgErr.ConstraintName, code, constraint)
	}
}

func insertUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := newID()
	n, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
	if err != nil {
		t.Fatal(err)
	}
	phone := fmt.Sprintf("+2519%08d", n.Int64())
	_, err = pool.Exec(ctx, `
		INSERT INTO users (id, phone_e164, display_name, password_hash)
		VALUES ($1, $2, 'Ledger', 'test-hash')
	`, id, phone)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

func insertLiabilityAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner uuid.UUID) uuid.UUID {
	t.Helper()
	id := newID()
	_, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, code, type, normal_balance, currency, owner_user_id)
		VALUES ($1, $2, 'liability', 'credit', 'ETB', $3)
	`, id, "wallet:"+id.String(), owner)
	if err != nil {
		t.Fatalf("insert liability: %v", err)
	}
	return id
}

func insertAdjustmentJournal(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := newID()
	_, err := pool.Exec(ctx, `
		INSERT INTO journal_entries (id, kind, description)
		VALUES ($1, 'adjustment', 'schema probe')
	`, id)
	if err != nil {
		t.Fatalf("insert journal: %v", err)
	}
	return id
}

func insertPosting(ctx context.Context, pool *pgxpool.Pool, journalID, accountID uuid.UUID, direction string, amount int64, currency string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO postings (
			id, journal_id, account_id, direction, amount_minor,
			currency, balance_after_minor, account_version
		) VALUES ($1, $2, $3, $4, $5, $6, 0, 0)
	`, newID(), journalID, accountID, direction, amount, currency)
	return err
}
