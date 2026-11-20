package provider

import "time"

// Status is a provider-side payment state.
type Status string

const (
	StatusPending   Status = "pending"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

// CollectionRequest asks a provider to pull money in (top-up).
type CollectionRequest struct {
	IdempotencyKey string            `json:"idempotency_key"`
	AmountMinor    int64             `json:"amount_minor"`
	Currency       string            `json:"currency"`
	PhoneE164      string            `json:"phone_e164"`
	Reference      string            `json:"reference"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// PayoutRequest asks a provider to push money out (cash-out).
type PayoutRequest struct {
	IdempotencyKey string            `json:"idempotency_key"`
	AmountMinor    int64             `json:"amount_minor"`
	Currency       string            `json:"currency"`
	PhoneE164      string            `json:"phone_e164"`
	Reference      string            `json:"reference"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// CollectionRef is the provider's handle for an inbound collection.
type CollectionRef struct {
	ProviderRef string    `json:"provider_ref"`
	Status      Status    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// PayoutRef is the provider's handle for an outbound payout.
type PayoutRef struct {
	ProviderRef string    `json:"provider_ref"`
	Status      Status    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// PaymentStatus is GetStatus's answer.
type PaymentStatus struct {
	ProviderRef string    `json:"provider_ref"`
	Kind        string    `json:"kind"` // collection | payout
	Status      Status    `json:"status"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	Reference   string    `json:"reference"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// WebhookEvent is what the simulator posts to Twofold.
type WebhookEvent struct {
	EventID     string    `json:"event_id"`
	Provider    string    `json:"provider"`
	ProviderRef string    `json:"provider_ref"`
	Kind        string    `json:"kind"`
	Status      Status    `json:"status"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	Reference   string    `json:"reference"`
	OccurredAt  time.Time `json:"occurred_at"`
}
