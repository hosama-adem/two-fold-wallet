package notification

import (
	"fmt"
	"strings"

	"github.com/hosama-adem/twofold/backend/internal/outbox"
)

// rendered is user-facing copy for one notification row.
type rendered struct {
	Title    string
	Body     string
	Critical bool // when true, also send SMS
}

func render(topic string, amountMinor int64, currency, reference string, role string) (rendered, bool) {
	currency = strings.TrimSpace(currency)
	if currency == "" {
		currency = "ETB"
	}
	amount := formatETB(amountMinor, currency)
	ref := strings.TrimSpace(reference)

	switch topic {
	case outbox.TopicTransferCompleted:
		switch role {
		case "recipient":
			return rendered{
				Title: "Money received",
				Body:  fmt.Sprintf("You received %s. Ref %s.", amount, ref),
			}, true
		case "sender":
			return rendered{
				Title: "Transfer sent",
				Body:  fmt.Sprintf("You sent %s. Ref %s.", amount, ref),
			}, true
		}
	case outbox.TopicTopUpCompleted:
		return rendered{
			Title: "Top-up completed",
			Body:  fmt.Sprintf("Your top-up of %s is complete. Ref %s.", amount, ref),
		}, true
	case outbox.TopicTopUpFailed:
		return rendered{
			Title:    "Top-up failed",
			Body:     fmt.Sprintf("Your top-up of %s failed. Ref %s.", amount, ref),
			Critical: true,
		}, true
	case outbox.TopicWithdrawalProcessing:
		return rendered{
			Title: "Cash-out processing",
			Body:  fmt.Sprintf("Your cash-out of %s is processing. Ref %s.", amount, ref),
		}, true
	case outbox.TopicWithdrawalCompleted:
		return rendered{
			Title: "Cash-out completed",
			Body:  fmt.Sprintf("Your cash-out of %s is complete. Ref %s.", amount, ref),
		}, true
	case outbox.TopicWithdrawalFailedRefunded:
		return rendered{
			Title:    "Cash-out failed",
			Body:     fmt.Sprintf("Your cash-out of %s failed and was refunded. Ref %s.", amount, ref),
			Critical: true,
		}, true
	}
	return rendered{}, false
}

func formatETB(amountMinor int64, currency string) string {
	sign := ""
	if amountMinor < 0 {
		sign = "-"
		amountMinor = -amountMinor
	}
	major := amountMinor / 100
	minor := amountMinor % 100
	return fmt.Sprintf("%s%d.%02d %s", sign, major, minor, currency)
}
