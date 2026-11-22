package funding

import "github.com/hosama-adem/twofold/backend/internal/provider"

// Decision is what ApplyProviderStatus does after a guarded transition.
type Decision string

const (
	decisionIgnore       Decision = "ignore"
	decisionCreditWallet Decision = "credit_wallet"
	decisionMarkFailed   Decision = "mark_failed"
	decisionSuspenseLate Decision = "suspense_late_success"
)

type transition struct {
	to       string
	payStatus string // provider_payments status
	decision Decision
	reason   string
}

// planTransition encodes PRD §10.2 plus late-success-after-failure → suspense.
// Terminal states ignore further provider noise so out-of-order events settle correctly.
func planTransition(txnStatus string, event provider.Status) (transition, bool) {
	switch txnStatus {
	case statusPending:
		switch event {
		case provider.StatusSucceeded:
			return transition{
				to:        statusCompleted,
				payStatus: paySucceeded,
				decision:  decisionCreditWallet,
				reason:    "provider_succeeded",
			}, true
		case provider.StatusFailed:
			return transition{
				to:        statusFailed,
				payStatus: payFailed,
				decision:  decisionMarkFailed,
				reason:    "provider_failed",
			}, true
		default:
			return transition{decision: decisionIgnore}, true
		}
	case statusFailed, statusExpired:
		if event == provider.StatusSucceeded {
			return transition{
				to:        statusNeedsReview,
				payStatus: paySucceeded,
				decision:  decisionSuspenseLate,
				reason:    "late_success_after_" + txnStatus,
			}, true
		}
		return transition{decision: decisionIgnore}, true
	case statusCompleted, statusNeedsReview:
		return transition{decision: decisionIgnore}, true
	default:
		return transition{}, false
	}
}
