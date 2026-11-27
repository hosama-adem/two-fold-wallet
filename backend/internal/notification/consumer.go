package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/notification/notificationdb"
	"github.com/hosama-adem/twofold/backend/internal/outbox"
)

// eventPayload is the JSON shape money paths write into outbox_events.
type eventPayload struct {
	TransactionID   uuid.UUID  `json:"transaction_id"`
	Reference       string     `json:"reference"`
	AmountMinor     int64      `json:"amount_minor"`
	FeeMinor        int64      `json:"fee_minor"`
	Currency        string     `json:"currency"`
	FromWalletID    *uuid.UUID `json:"from_wallet_id"`
	ToWalletID      *uuid.UUID `json:"to_wallet_id"`
	InitiatorUserID *uuid.UUID `json:"initiator_user_id"`
	Kind            string     `json:"kind"`
	Status          string     `json:"status"`
}

// Consumer turns a claimed outbox event into notification rows (FR-NOT-3).
type Consumer struct {
	pool   *pgxpool.Pool
	sender Sender
	log    *slog.Logger
}

// NewConsumer builds an idempotent outbox → notification consumer.
func NewConsumer(pool *pgxpool.Pool, sender Sender, log *slog.Logger) *Consumer {
	if log == nil {
		log = slog.Default()
	}
	if sender == nil {
		sender = LogSender{Log: log}
	}
	return &Consumer{pool: pool, sender: sender, log: log}
}

// Deliver inserts notifications for the event. Safe to call twice — the unique
// (user_id, outbox_event_id) constraint drops duplicates.
func (c *Consumer) Deliver(ctx context.Context, eventID uuid.UUID, topic string, payload json.RawMessage) error {
	if c == nil || c.pool == nil {
		return fmt.Errorf("notification: consumer not configured")
	}
	if eventID == uuid.Nil {
		return fmt.Errorf("notification: event id is required")
	}

	var body eventPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &body); err != nil {
			return fmt.Errorf("notification: payload: %w", err)
		}
	}
	if err := c.enrichFromTransaction(ctx, &body); err != nil {
		return err
	}

	targets, err := c.resolveTargets(ctx, topic, body)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		c.log.Info("notification skipped (no recipients)", "event_id", eventID, "topic", topic)
		return nil
	}

	data := payload
	if len(data) == 0 {
		data = []byte("{}")
	}

	for _, t := range targets {
		msg, ok := render(topic, body.AmountMinor, body.Currency, body.Reference, t.role)
		if !ok {
			continue
		}
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		_, err = notificationdb.New(c.pool).InsertNotification(ctx, notificationdb.InsertNotificationParams{
			ID:            id,
			UserID:        t.userID,
			OutboxEventID: eventID,
			Topic:         topic,
			Title:         msg.Title,
			Body:          msg.Body,
			Data:          data,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// ON CONFLICT DO NOTHING — already delivered for this user.
				continue
			}
			return fmt.Errorf("notification: insert: %w", err)
		}
		if msg.Critical {
			if smsErr := c.alertSMS(ctx, t.userID, msg.Body); smsErr != nil {
				c.log.Warn("notification sms failed", "user_id", t.userID, "error", smsErr)
			}
		}
	}
	return nil
}

type target struct {
	userID uuid.UUID
	role   string
}

func (c *Consumer) enrichFromTransaction(ctx context.Context, body *eventPayload) error {
	if body.TransactionID == uuid.Nil {
		return nil
	}
	if body.Reference != "" && body.AmountMinor > 0 && body.Currency != "" {
		return nil
	}
	meta, err := notificationdb.New(c.pool).GetTransactionNotifyMeta(ctx, body.TransactionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if body.Reference == "" {
		body.Reference = meta.Reference
	}
	if body.AmountMinor == 0 {
		body.AmountMinor = meta.AmountMinor
	}
	if body.Currency == "" {
		body.Currency = meta.Currency
	}
	if body.FromWalletID == nil {
		body.FromWalletID = meta.FromWalletID
	}
	if body.ToWalletID == nil {
		body.ToWalletID = meta.ToWalletID
	}
	return nil
}

func (c *Consumer) resolveTargets(ctx context.Context, topic string, body eventPayload) ([]target, error) {
	q := notificationdb.New(c.pool)

	switch topic {
	case outbox.TopicTransferCompleted:
		var out []target
		if body.ToWalletID != nil && *body.ToWalletID != uuid.Nil {
			uid, err := q.GetWalletUserID(ctx, *body.ToWalletID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
			if err == nil {
				out = append(out, target{userID: uid, role: "recipient"})
			}
		}
		if body.FromWalletID != nil && *body.FromWalletID != uuid.Nil {
			uid, err := q.GetWalletUserID(ctx, *body.FromWalletID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
			if err == nil {
				out = append(out, target{userID: uid, role: "sender"})
			}
		}
		return out, nil

	case outbox.TopicTopUpCompleted, outbox.TopicTopUpFailed,
		outbox.TopicWithdrawalProcessing, outbox.TopicWithdrawalCompleted,
		outbox.TopicWithdrawalFailedRefunded:
		uid, err := c.initiatorUser(ctx, body)
		if err != nil {
			return nil, err
		}
		if uid == uuid.Nil {
			return nil, nil
		}
		return []target{{userID: uid, role: "owner"}}, nil
	default:
		c.log.Info("notification unknown topic", "topic", topic)
		return nil, nil
	}
}

func (c *Consumer) initiatorUser(ctx context.Context, body eventPayload) (uuid.UUID, error) {
	if body.InitiatorUserID != nil && *body.InitiatorUserID != uuid.Nil {
		return *body.InitiatorUserID, nil
	}
	q := notificationdb.New(c.pool)
	if body.ToWalletID != nil && *body.ToWalletID != uuid.Nil {
		uid, err := q.GetWalletUserID(ctx, *body.ToWalletID)
		if err == nil {
			return uid, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, err
		}
	}
	if body.FromWalletID != nil && *body.FromWalletID != uuid.Nil {
		uid, err := q.GetWalletUserID(ctx, *body.FromWalletID)
		if err == nil {
			return uid, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, err
		}
	}
	if body.TransactionID == uuid.Nil {
		return uuid.Nil, nil
	}
	meta, err := q.GetTransactionNotifyMeta(ctx, body.TransactionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, nil
		}
		return uuid.Nil, err
	}
	if meta.InitiatorUserID != nil {
		return *meta.InitiatorUserID, nil
	}
	if meta.ToWalletID != nil {
		return q.GetWalletUserID(ctx, *meta.ToWalletID)
	}
	if meta.FromWalletID != nil {
		return q.GetWalletUserID(ctx, *meta.FromWalletID)
	}
	return uuid.Nil, nil
}

func (c *Consumer) alertSMS(ctx context.Context, userID uuid.UUID, body string) error {
	phone, err := notificationdb.New(c.pool).GetUserPhone(ctx, userID)
	if err != nil {
		return err
	}
	return c.sender.SendSMS(ctx, phone, body)
}
