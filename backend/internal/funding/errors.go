package funding

import (
	"net/http"

	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

func errProviderUnknown(name string) error {
	return httpx.ValidationFailed("Unknown payment provider: " + name + ".")
}

func errAmountInvalid() error {
	return httpx.ValidationFailed("Amount must be a positive amount within limits.")
}

func errCurrencyUnsupported() error {
	return httpx.ValidationFailed("Only ETB top-ups are supported.")
}

func errWalletUnavailable() error {
	return httpx.ValidationFailed("ETB wallet is not available for top-up.")
}

func errTopupNotFound() error {
	return httpx.NotFound("Top-up was not found.")
}

func errTopupForbidden() error {
	return httpx.New(http.StatusForbidden, "forbidden", "Forbidden", "You cannot view this top-up.")
}

func errWebhookUnauthorized() error {
	return httpx.New(http.StatusUnauthorized, "webhook_unauthorized", "Unauthorized",
		"Webhook signature is missing, invalid, or outside the allowed time window.")
}

func errWebhookBadPayload() error {
	return httpx.ValidationFailed("Webhook payload is invalid.")
}
