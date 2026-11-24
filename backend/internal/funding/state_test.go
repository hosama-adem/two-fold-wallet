package funding

import (
	"testing"

	"github.com/hosama-adem/twofold/backend/internal/provider"
)

func TestPlanTransitionHappyAndOutOfOrder(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		from   string
		event  provider.Status
		to     string
		dec    Decision
		wantOK bool
	}{
		{"pending_success", statusPending, provider.StatusSucceeded, statusCompleted, decisionCreditWallet, true},
		{"pending_fail", statusPending, provider.StatusFailed, statusFailed, decisionMarkFailed, true},
		{"fail_then_success", statusFailed, provider.StatusSucceeded, statusNeedsReview, decisionSuspenseLate, true},
		{"expired_then_success", statusExpired, provider.StatusSucceeded, statusNeedsReview, decisionSuspenseLate, true},
		{"completed_ignore_fail", statusCompleted, provider.StatusFailed, "", decisionIgnore, true},
		{"completed_ignore_success", statusCompleted, provider.StatusSucceeded, "", decisionIgnore, true},
		{"needs_review_ignore", statusNeedsReview, provider.StatusSucceeded, "", decisionIgnore, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := planTransition(tc.from, tc.event)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v", ok, tc.wantOK)
			}
			if got.decision != tc.dec {
				t.Fatalf("decision=%s want %s", got.decision, tc.dec)
			}
			if tc.dec != decisionIgnore && got.to != tc.to {
				t.Fatalf("to=%s want %s", got.to, tc.to)
			}
		})
	}
}

func TestPlanWithdrawalTransition(t *testing.T) {
	t.Parallel()
	okSettle, ok := planWithdrawalTransition(statusProcessing, provider.StatusSucceeded)
	if !ok || okSettle.decision != decisionSettlePayout || okSettle.to != statusCompleted {
		t.Fatalf("settle=%+v ok=%v", okSettle, ok)
	}
	okRefund, ok := planWithdrawalTransition(statusProcessing, provider.StatusFailed)
	if !ok || okRefund.decision != decisionRefundPayout || okRefund.to != statusFailed {
		t.Fatalf("refund=%+v ok=%v", okRefund, ok)
	}
	ignore, ok := planWithdrawalTransition(statusCompleted, provider.StatusFailed)
	if !ok || ignore.decision != decisionIgnore {
		t.Fatalf("completed ignore=%+v", ignore)
	}
}
