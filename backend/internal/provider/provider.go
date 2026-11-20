package provider

import (
	"context"
	"net/http"
)

// Provider is the payment-rail port. Day 23+ top-ups and withdrawals call this;
// they never import a Telebirr or Chapa SDK.
type Provider interface {
	Name() string
	InitiateCollection(ctx context.Context, req CollectionRequest) (CollectionRef, error)
	InitiatePayout(ctx context.Context, req PayoutRequest) (PayoutRef, error)
	GetStatus(ctx context.Context, ref string) (PaymentStatus, error)
	// VerifyWebhook checks HMAC signature + timestamp window before the body
	// is trusted. It does not apply business state — that is Day 23.
	VerifyWebhook(headers http.Header, body []byte) error
}
