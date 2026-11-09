package ledger_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/hosama-adem/twofold/backend/internal/ledger"
	"github.com/hosama-adem/twofold/backend/internal/ledger/ledgerdb"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
)

func TestTransactionAmountAndFeeChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		INSERT INTO transactions (id, reference, kind, status, amount_minor, currency)
		VALUES ($1, $2, 'top_up', 'pending', 0, 'ETB')
	`, newID(), "ref-"+newID().String())
	requireViolation(t, err, "23514", "transactions_amount_positive")

	_, err = pool.Exec(ctx, `
		INSERT INTO transactions (id, reference, kind, status, amount_minor, fee_minor, currency)
		VALUES ($1, $2, 'p2p', 'pending', 100, -1, 'ETB')
	`, newID(), "ref-"+newID().String())
	requireViolation(t, err, "23514", "transactions_fee_nonneg")

	_, err = pool.Exec(ctx, `
		INSERT INTO transactions (id, reference, kind, status, amount_minor, currency)
		VALUES ($1, $2, 'cash_out', 'pending', $3, 'ETB')
	`, newID(), "ref-"+newID().String(), ledger.MaxAmountMinor+1)
	requireViolation(t, err, "23514", "transactions_amount_positive")
}

func TestTransactionWalletsMustDiffer(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	same := newID()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO transactions (
			id, reference, kind, status, from_wallet_id, to_wallet_id, amount_minor, currency
		) VALUES ($1, $2, 'p2p', 'pending', $3, $3, 100, 'ETB')
	`, newID(), "ref-"+newID().String(), same)
	requireViolation(t, err, "23514", "transactions_wallets_distinct")
}

func TestIdempotencyKeyIsUniquePerInitiator(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	userA := insertUser(t, ctx, pool)
	userB := insertUser(t, ctx, pool)
	key := uuid.NewString()

	insert := func(user uuid.UUID, reference, idem string) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO transactions (
				id, reference, kind, status, initiator_user_id, amount_minor, currency, idempotency_key
			) VALUES ($1, $2, 'p2p', 'pending', $3, 250, 'ETB', $4)
		`, newID(), reference, user, idem)
		return err
	}

	if err := insert(userA, "ref-"+newID().String(), key); err != nil {
		t.Fatal(err)
	}
	err := insert(userA, "ref-"+newID().String(), key)
	requireViolation(t, err, "23505", "transactions_user_idempotency_key_idx")

	if err := insert(userB, "ref-"+newID().String(), key); err != nil {
		t.Fatalf("same key for a different user should insert: %v", err)
	}

	err = insert(userA, "ref-"+newID().String(), "not-a-uuid")
	requireViolation(t, err, "23514", "transactions_idempotency_key_uuid")
}

func TestTransactionStatusEventMustMove(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.Postgres(t)
	ctx := context.Background()
	txID := newID()
	reference := "ref-" + newID().String()
	_, err := pool.Exec(ctx, `
		INSERT INTO transactions (id, reference, kind, status, amount_minor, fee_minor, currency)
		VALUES ($1, $2, 'top_up', 'pending', $3, 0, 'ETB')
	`, txID, reference, ledger.MaxAmountMinor)
	if err != nil {
		t.Fatalf("ceiling transaction: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO transaction_events (id, transaction_id, from_status, to_status, actor)
		VALUES ($1, $2, 'pending', 'pending', 'system')
	`, newID(), txID)
	requireViolation(t, err, "23514", "transaction_events_status_moves")

	_, err = pool.Exec(ctx, `
		INSERT INTO transaction_events (id, transaction_id, from_status, to_status, actor)
		VALUES ($1, $2, 'pending', 'processing', 'wizard')
	`, newID(), txID)
	requireViolation(t, err, "23514", "transaction_events_actor_known")

	eventID := newID()
	_, err = pool.Exec(ctx, `
		INSERT INTO transaction_events (id, transaction_id, from_status, to_status, reason, actor)
		VALUES ($1, $2, 'pending', 'processing', 'provider accepted', 'provider')
	`, eventID, txID)
	if err != nil {
		t.Fatal(err)
	}

	q := ledgerdb.New(pool)
	got, err := q.GetTransactionByReference(ctx, reference)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != txID || got.AmountMinor != ledger.MaxAmountMinor || got.FeeMinor != 0 {
		t.Fatalf("transaction = %+v", got)
	}
	events, err := q.ListTransactionEventsByTransactionID(ctx, txID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ID != eventID || events[0].ToStatus != "processing" {
		t.Fatalf("events = %+v", events)
	}
}
