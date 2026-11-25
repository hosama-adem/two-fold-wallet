package funding

import "github.com/hosama-adem/twofold/backend/internal/provider"

// Decision is what ApplyProviderStatus does after a guarded transition.
type Decision string

const (
	decisionIgnore         Decision = "ignore"
	decisionCreditWallet   Decision = "credit_wallet"
	decisionMarkFailed     Decision = "mark_failed"
	decisionSuspenseLate   Decision = "suspense_late_success"
	decisionSettlePayout   Decision = "settle_payout"
	decisionRefundPayout   Decision = "refund_payout"
)

type transition struct {
	to       string
	payStatus string // provider_payments status
	decision Decision
	reason   string
}

// planTopUpTransition encodes PRD §10.2 plus late-success-after-failure → suspense.
func planTopUpTransition(txnStatus string, event provider.Status) (transition, bool) {
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

// planWithdrawalTransition encodes PRD §10.3 (processing → completed | failed).
func planWithdrawalTransition(txnStatus string, event provider.Status) (transition, bool) {
	switch txnStatus {
	case statusProcessing:
		switch event {
		case provider.StatusSucceeded:
			return transition{
				to:        statusCompleted,
				payStatus: paySucceeded,
				decision:  decisionSettlePayout,
				reason:    eventNotifyReady + ":completed",
			}, true
		case provider.StatusFailed:
			return transition{
				to:        statusFailed,
				payStatus: payFailed,
				decision:  decisionRefundPayout,
				reason:    eventNotifyReady + ":failed_refunded",
			}, true
		default:
			return transition{decision: decisionIgnore}, true
		}
	case statusFailed:
		if event == provider.StatusSucceeded {
			return transition{
				to:        statusNeedsReview,
				payStatus: paySucceeded,
				decision:  decisionSuspenseLate,
				reason:    "late_payout_success_after_failed",
			}, true
		}
		return transition{decision: decisionIgnore}, true
	case statusCompleted, statusNeedsReview:
		return transition{decision: decisionIgnore}, true
	default:
		return transition{}, false
	}
}

// planTransition is kept for top-up unit tests.
func planTransition(txnStatus string, event provider.Status) (transition, bool) {
	return planTopUpTransition(txnStatus, event)
}
