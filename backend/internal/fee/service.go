package fee

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hosama-adem/twofold/backend/internal/fee/feedb"
	"github.com/hosama-adem/twofold/backend/internal/platform/httpx"
)

// Service loads effective fee rules and computes fees.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewService builds a fee service on a pgx pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{
		pool: pool,
		now:  time.Now,
	}
}

// ForAmount loads the effective rule for kind + the user's KYC tier and
// returns the computed fee in minor units. Missing rules mean fee 0.
func (s *Service) ForAmount(ctx context.Context, userID uuid.UUID, kind string, amountMinor int64) (int64, error) {
	if s.pool == nil {
		return 0, httpx.Internal("Fee store is not configured.")
	}
	q := feedb.New(s.pool)
	tier, err := q.GetUserKYCTier(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, httpx.ValidationFailed("User not found.")
		}
		return 0, err
	}
	rule, ok, err := s.effectiveRule(ctx, q, kind, tier)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}
	return Calculate(rule, amountMinor), nil
}

func (s *Service) effectiveRule(ctx context.Context, q *feedb.Queries, kind string, tier int16) (Rule, bool, error) {
	at := s.now().UTC()
	row, err := q.GetEffectiveFeeRule(ctx, feedb.GetEffectiveFeeRuleParams{
		Kind:          kind,
		Tier:          tier,
		EffectiveFrom: at,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Rule{}, false, nil
		}
		return Rule{}, false, err
	}
	return Rule{
		Kind:            row.Kind,
		Tier:            row.Tier,
		FlatMinor:       row.FlatMinor,
		RateBps:         row.RateBps,
		MinMinor:        row.MinMinor,
		MaxMinor:        row.MaxMinor,
		WaiveBelowMinor: row.WaiveBelowMinor,
		EffectiveFrom:   row.EffectiveFrom,
	}, true, nil
}
