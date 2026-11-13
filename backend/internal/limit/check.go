package limit

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hosama-adem/twofold/backend/internal/limit/limitdb"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

// KindCashOut is the withdrawal movement that tier 0 cannot perform.
const KindCashOut = "cash_out"

// DBTX is the sqlc/pgx query surface Check needs (pool or transaction).
type DBTX interface {
	limitdb.DBTX
}

// Check enforces outgoing tier caps for kind + amountMinor inside an open
// money transaction (after locks, before journal).
//
// Order: cash-out permission → per-transaction → daily → monthly.
// Usage is liability debit postings since the Addis Ababa day/month start.
func Check(ctx context.Context, db DBTX, userID uuid.UUID, kind string, amountMinor int64) error {
	return checkAt(ctx, db, userID, kind, amountMinor, time.Now())
}

func checkAt(ctx context.Context, db DBTX, userID uuid.UUID, kind string, amountMinor int64, now time.Time) error {
	if userID == uuid.Nil {
		return httpx.ValidationFailed("User id is required.")
	}
	if amountMinor <= 0 {
		return httpx.ValidationFailed("Amount must be positive.")
	}

	q := limitdb.New(db)
	tier, err := q.GetUserKYCTier(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ValidationFailed("User not found.")
		}
		return err
	}
	caps, err := q.GetTierLimits(ctx, tier)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.Internal("Tier limits are not configured.")
		}
		return err
	}

	if kind == KindCashOut && !caps.CanWithdraw {
		return errCashOutNotAllowed()
	}

	if amountMinor > caps.PerTxnMinor {
		remaining := caps.PerTxnMinor
		if remaining < 0 {
			remaining = 0
		}
		return errPerTxn(amountMinor, caps.PerTxnMinor, remaining)
	}

	uid := userID
	dayUsed, err := q.SumOutgoingDebitsSince(ctx, limitdb.SumOutgoingDebitsSinceParams{
		UserID: &uid,
		Since:  dayStartAddis(now),
	})
	if err != nil {
		return err
	}
	dayRemaining := caps.DailyMinor - dayUsed
	if dayRemaining < 0 {
		dayRemaining = 0
	}
	if amountMinor > dayRemaining {
		return errDaily(amountMinor, dayUsed, caps.DailyMinor, dayRemaining)
	}

	monthUsed, err := q.SumOutgoingDebitsSince(ctx, limitdb.SumOutgoingDebitsSinceParams{
		UserID: &uid,
		Since:  monthStartAddis(now),
	})
	if err != nil {
		return err
	}
	monthRemaining := caps.MonthlyMinor - monthUsed
	if monthRemaining < 0 {
		monthRemaining = 0
	}
	if amountMinor > monthRemaining {
		return errMonthly(amountMinor, monthUsed, caps.MonthlyMinor, monthRemaining)
	}

	return nil
}

// CheckReceive enforces the recipient's max-balance cap for an incoming credit.
// balanceMinor is the locked ledger balance before this credit posts.
func CheckReceive(ctx context.Context, db DBTX, userID uuid.UUID, balanceMinor, creditMinor int64) error {
	if userID == uuid.Nil {
		return httpx.ValidationFailed("User id is required.")
	}
	if creditMinor <= 0 {
		return httpx.ValidationFailed("Amount must be positive.")
	}
	if balanceMinor < 0 {
		return httpx.Internal("Ledger balance cannot be negative for a liability check.")
	}

	q := limitdb.New(db)
	tier, err := q.GetUserKYCTier(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ValidationFailed("User not found.")
		}
		return err
	}
	caps, err := q.GetTierLimits(ctx, tier)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.Internal("Tier limits are not configured.")
		}
		return err
	}

	room := caps.MaxBalanceMinor - balanceMinor
	if room < 0 {
		room = 0
	}
	if creditMinor > room {
		return errMaxBalance(creditMinor, balanceMinor, caps.MaxBalanceMinor, room)
	}
	return nil
}
