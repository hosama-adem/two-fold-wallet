package limit

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/limit/limitdb"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
	"github.com/hosama-adem/twofold/backend/internal/wallet"
)

// Caps are the configured ceilings for a KYC tier.
type Caps struct {
	MaxBalanceMinor int64 `json:"max_balance_minor"`
	PerTxnMinor     int64 `json:"per_txn_minor"`
	DailyMinor      int64 `json:"daily_minor"`
	MonthlyMinor    int64 `json:"monthly_minor"`
	CanWithdraw     bool  `json:"can_withdraw"`
}

// Usage is what GET /v1/me shows for limit bars.
type Usage struct {
	Tier               int16 `json:"tier"`
	Caps               Caps  `json:"caps"`
	BalanceMinor       int64 `json:"balance_minor"`
	DailyUsedMinor     int64 `json:"daily_used_minor"`
	MonthlyUsedMinor   int64 `json:"monthly_used_minor"`
	DailyRemaining     int64 `json:"daily_remaining_minor"`
	MonthlyRemaining   int64 `json:"monthly_remaining_minor"`
	BalanceRemaining   int64 `json:"balance_remaining_minor"`
}

// Service loads tier caps and rolling usage for profile views.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewService builds a limit usage reader on a pgx pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now}
}

// ForUser returns caps and Addis-window usage for the profile payload.
func (s *Service) ForUser(ctx context.Context, userID uuid.UUID) (Usage, error) {
	if s == nil || s.pool == nil {
		return Usage{}, httpx.Internal("Limit store is not configured.")
	}
	return usageAt(ctx, s.pool, userID, s.now())
}

func usageAt(ctx context.Context, db DBTX, userID uuid.UUID, now time.Time) (Usage, error) {
	if userID == uuid.Nil {
		return Usage{}, httpx.ValidationFailed("User id is required.")
	}
	q := limitdb.New(db)
	tier, err := q.GetUserKYCTier(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Usage{}, httpx.ValidationFailed("User not found.")
		}
		return Usage{}, err
	}
	caps, err := q.GetTierLimits(ctx, tier)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Usage{}, httpx.Internal("Tier limits are not configured.")
		}
		return Usage{}, err
	}

	uid := userID
	dayUsed, err := q.SumOutgoingDebitsSince(ctx, limitdb.SumOutgoingDebitsSinceParams{
		UserID: &uid,
		Since:  dayStartAddis(now),
	})
	if err != nil {
		return Usage{}, err
	}
	monthUsed, err := q.SumOutgoingDebitsSince(ctx, limitdb.SumOutgoingDebitsSinceParams{
		UserID: &uid,
		Since:  monthStartAddis(now),
	})
	if err != nil {
		return Usage{}, err
	}

	balance, err := q.GetUserLedgerBalance(ctx, limitdb.GetUserLedgerBalanceParams{
		UserID:   userID,
		Currency: wallet.CurrencyETB,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			balance = 0
		} else {
			return Usage{}, err
		}
	}

	dayRem := caps.DailyMinor - dayUsed
	if dayRem < 0 {
		dayRem = 0
	}
	monthRem := caps.MonthlyMinor - monthUsed
	if monthRem < 0 {
		monthRem = 0
	}
	balRem := caps.MaxBalanceMinor - balance
	if balRem < 0 {
		balRem = 0
	}

	return Usage{
		Tier: tier,
		Caps: Caps{
			MaxBalanceMinor: caps.MaxBalanceMinor,
			PerTxnMinor:     caps.PerTxnMinor,
			DailyMinor:      caps.DailyMinor,
			MonthlyMinor:    caps.MonthlyMinor,
			CanWithdraw:     caps.CanWithdraw,
		},
		BalanceMinor:     balance,
		DailyUsedMinor:   dayUsed,
		MonthlyUsedMinor: monthUsed,
		DailyRemaining:   dayRem,
		MonthlyRemaining: monthRem,
		BalanceRemaining: balRem,
	}, nil
}
