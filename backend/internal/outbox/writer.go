package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hosama-adem/twofold/backend/internal/outbox/outboxdb"
)

// Well-known topics. Day 26 maps these to notification templates.
const (
	TopicTransferCompleted        = "transfer.completed"
	TopicTopUpCompleted           = "topup.completed"
	TopicTopUpFailed              = "topup.failed"
	TopicWithdrawalProcessing     = "withdrawal.processing"
	TopicWithdrawalCompleted      = "withdrawal.completed"
	TopicWithdrawalFailedRefunded = "withdrawal.failed_refunded"
)

// Writer inserts outbox rows on a caller-owned transaction.
type Writer struct{}

// NewWriter builds an outbox writer.
func NewWriter() *Writer { return &Writer{} }

// Write inserts one event. Must run inside the business transaction.
func (w *Writer) Write(ctx context.Context, tx pgx.Tx, topic string, payload any) (uuid.UUID, error) {
	if tx == nil {
		return uuid.Nil, fmt.Errorf("outbox: Write requires a transaction")
	}
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return uuid.Nil, fmt.Errorf("outbox: topic is required")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return uuid.Nil, fmt.Errorf("outbox: marshal payload: %w", err)
	}
	if raw == nil {
		raw = []byte("{}")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("outbox: id: %w", err)
	}
	if _, err := outboxdb.New(tx).InsertOutboxEvent(ctx, outboxdb.InsertOutboxEventParams{
		ID:      id,
		Topic:   topic,
		Payload: raw,
	}); err != nil {
		return uuid.Nil, fmt.Errorf("outbox: insert: %w", err)
	}
	return id, nil
}
