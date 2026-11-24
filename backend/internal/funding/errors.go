package funding

import (
	"fmt"
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
	return httpx.ValidationFailed("Only ETB is supported.")
}

func errWalletUnavailable() error {
	return httpx.ValidationFailed("ETB wallet is not available.")
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

func errDestinationIdentifier() error {
	return httpx.ValidationFailed("Destination identifier is required.")
}

func errDestinationNotFound() error {
	return httpx.NotFound("Payout destination was not found.")
}

func errWithdrawalNotFound() error {
	return httpx.NotFound("Withdrawal was not found.")
}

func errWithdrawalForbidden() error {
	return httpx.New(http.StatusForbidden, "forbidden", "Forbidden", "You cannot view this withdrawal.")
}

func errFeeChanged(expected, actual int64) error {
	return httpx.New(http.StatusConflict, "fee_changed", "Conflict",
		fmt.Sprintf("Fee changed: you expected %d minor units but the fee is now %d.", expected, actual))
}

func errInsufficientFunds(available, needed int64, currency string) error {
	return httpx.New(http.StatusUnprocessableEntity, "insufficient_funds", "Unprocessable Entity",
		fmt.Sprintf("Insufficient funds: available %d %s, need %d.", available, currency, needed))
}
