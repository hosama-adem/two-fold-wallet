package notification_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/notification"
	"github.com/hosama-adem/twofold/backend/internal/notification/notificationdb"
	"github.com/hosama-adem/twofold/backend/internal/outbox"
	"github.com/hosama-adem/twofold/backend/internal/platform/database/testutil"
)

func TestDeliverSameEventTwiceCreatesOneNotification(t *testing.T) {
	pool := testutil.Postgres(t)
	ctx := context.Background()

	userID := seedUser(t, pool)
	eventID := writeOutbox(t, pool, outbox.TopicTopUpFailed, map[string]any{
		"reference":          "TU-DEDUP1",
		"amount_minor":       5000,
		"currency":           "ETB",
		"initiator_user_id":  userID,
	})

	sms := &notification.RecordingSender{}
	consumer := notification.NewConsumer(pool, sms, nil)

	if err := consumer.Deliver(ctx, eventID, outbox.TopicTopUpFailed, mustJSON(map[string]any{
		"reference":         "TU-DEDUP1",
		"amount_minor":      5000,
		"currency":          "ETB",
		"initiator_user_id": userID,
	})); err != nil {
		t.Fatal(err)
	}
	if err := consumer.Deliver(ctx, eventID, outbox.TopicTopUpFailed, mustJSON(map[string]any{
		"reference":         "TU-DEDUP1",
		"amount_minor":      5000,
		"currency":          "ETB",
		"initiator_user_id": userID,
	})); err != nil {
		t.Fatal(err)
	}

	n, err := notificationdb.New(pool).CountByOutboxEvent(ctx, eventID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("notifications for event=%d want 1", n)
	}
	if sms.Calls != 1 {
		t.Fatalf("critical SMS calls=%d want 1 (second deliver is a no-op)", sms.Calls)
	}

	list, err := notification.NewService(pool).List(ctx, userID, "10")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("list items=%d want 1", len(list.Items))
	}
	if list.UnreadCount != 1 {
		t.Fatalf("unread=%d want 1", list.UnreadCount)
	}
	if list.Items[0].Topic != outbox.TopicTopUpFailed {
		t.Fatalf("topic=%q", list.Items[0].Topic)
	}

	marked, err := notification.NewService(pool).MarkRead(ctx, userID, list.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if marked.ReadAt == nil {
		t.Fatal("expected read_at after mark")
	}
	list, err = notification.NewService(pool).List(ctx, userID, "10")
	if err != nil {
		t.Fatal(err)
	}
	if list.UnreadCount != 0 {
		t.Fatalf("unread after mark=%d want 0", list.UnreadCount)
	}
}

func TestTransferCompletedNotifiesSenderAndRecipient(t *testing.T) {
	pool := testutil.Postgres(t)
	ctx := context.Background()

	senderID := seedUser(t, pool)
	recipientID := seedUser(t, pool)
	fromWallet := seedWallet(t, pool, senderID)
	toWallet := seedWallet(t, pool, recipientID)

	payload := map[string]any{
		"reference":      "TF-NOTIFY1",
		"amount_minor":   2500,
		"fee_minor":      0,
		"currency":       "ETB",
		"from_wallet_id": fromWallet,
		"to_wallet_id":   toWallet,
	}
	eventID := writeOutbox(t, pool, outbox.TopicTransferCompleted, payload)

	consumer := notification.NewConsumer(pool, &notification.RecordingSender{}, nil)
	if err := consumer.Deliver(ctx, eventID, outbox.TopicTransferCompleted, mustJSON(payload)); err != nil {
		t.Fatal(err)
	}
	if err := consumer.Deliver(ctx, eventID, outbox.TopicTransferCompleted, mustJSON(payload)); err != nil {
		t.Fatal(err)
	}

	n, err := notificationdb.New(pool).CountByOutboxEvent(ctx, eventID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("notifications=%d want 2 (sender+recipient, deduped)", n)
	}
}

func writeOutbox(t *testing.T, pool *pgxpool.Pool, topic string, payload any) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id, err := outbox.NewWriter().Write(ctx, tx, topic, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}

func seedUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	phone := uniquePhone(t)
	_, err = pool.Exec(context.Background(), `
		INSERT INTO users (id, phone_e164, display_name, password_hash, status, phone_verified_at, kyc_tier)
		VALUES ($1, $2, $3, $4, 'active', now(), 1)
	`, id, phone, "Notify User", "hash")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func seedWallet(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	accountID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	walletID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	code := "usr_" + strings.ReplaceAll(walletID.String(), "-", "")
	_, err = pool.Exec(ctx, `
		INSERT INTO accounts (id, code, type, normal_balance, currency, allow_negative, owner_user_id)
		VALUES ($1, $2, 'liability', 'credit', 'ETB', false, $3)
	`, accountID, code, userID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO wallets (id, user_id, account_id, currency, status, freeze_mode)
		VALUES ($1, $2, $3, 'ETB', 'active', 'none')
	`, walletID, userID, accountID)
	if err != nil {
		t.Fatal(err)
	}
	return walletID
}

func uniquePhone(t *testing.T) string {
	t.Helper()
	n, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("+2519%08d", n.Int64())
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
