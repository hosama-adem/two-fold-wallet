package fee

import (
	"math/big"
)

const bpsDenom int64 = 10_000

// Calculate returns the fee in minor units for amount under rule.
//
// Formula (FR-FEE-2):
//
//	fee = clamp(flat + round_half_up(amount * rate_bps / 10_000), min, max)
//
// Integer math only. Never negative. Amounts strictly below waive_below_minor
// are free (FR-FEE-3 P2P under 100 ETB).
func Calculate(rule Rule, amountMinor int64) int64 {
	if amountMinor <= 0 {
		return 0
	}
	if rule.WaiveBelowMinor > 0 && amountMinor < rule.WaiveBelowMinor {
		return 0
	}

	ratePart := roundHalfUpBPS(amountMinor, int64(rule.RateBps))
	fee := addNonNeg(rule.FlatMinor, ratePart)
	fee = clamp(fee, rule.MinMinor, rule.MaxMinor)
	if fee < 0 {
		return 0
	}
	return fee
}

// roundHalfUpBPS is round-half-up of (amount * bps / 10_000) using big.Int so
// MaxAmountMinor * 10000 cannot overflow int64 mid-multiply.
func roundHalfUpBPS(amount, bps int64) int64 {
	if amount == 0 || bps == 0 {
		return 0
	}
	n := new(big.Int).Mul(big.NewInt(amount), big.NewInt(bps))
	n.Add(n, big.NewInt(bpsDenom/2))
	n.Div(n, big.NewInt(bpsDenom))
	if !n.IsInt64() {
		// Past int64: caller clamps to max_minor.
		return int64(^uint64(0) >> 1)
	}
	out := n.Int64()
	if out < 0 {
		return 0
	}
	return out
}

func addNonNeg(a, b int64) int64 {
	if a < 0 {
		a = 0
	}
	if b < 0 {
		b = 0
	}
	sum := a + b
	if sum < a {
		// Overflow: treat as MaxInt64 so clamp to max still works.
		return int64(^uint64(0) >> 1)
	}
	return sum
}

func clamp(v, min, max int64) int64 {
	if min < 0 {
		min = 0
	}
	if max < min {
		max = min
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
