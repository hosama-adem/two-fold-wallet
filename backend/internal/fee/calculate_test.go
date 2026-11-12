package fee

import (
	"testing"

	"pgregory.net/rapid"
)

func TestCalculateTable(t *testing.T) {
	p2p := Rule{
		Kind:            KindP2P,
		Tier:            0,
		FlatMinor:       0,
		RateBps:         50,
		MinMinor:        50,
		MaxMinor:        1500,
		WaiveBelowMinor: 10_000,
	}

	cases := []struct {
		name   string
		rule   Rule
		amount int64
		want   int64
	}{
		{name: "p2p free under 100 ETB", rule: p2p, amount: 9_999, want: 0},
		{name: "p2p at 100 ETB pays min", rule: p2p, amount: 10_000, want: 50},
		{name: "p2p 0.5% of 200 ETB", rule: p2p, amount: 20_000, want: 100},
		{name: "p2p hits max", rule: p2p, amount: 1_000_000, want: 1500},
		{name: "zero amount", rule: p2p, amount: 0, want: 0},
		{name: "negative amount", rule: p2p, amount: -1, want: 0},

		// Round half up: 15 * 50 / 10000 = 0.075 → 0 before min; but waived? no waive.
		{
			name: "half-up below 0.5 stays 0 then min",
			rule: Rule{FlatMinor: 0, RateBps: 50, MinMinor: 0, MaxMinor: 100, WaiveBelowMinor: 0},
			amount: 99, // 99*50/10000 = 0.495 → 0
			want:   0,
		},
		{
			name: "half-up exactly .5 rounds up",
			rule: Rule{FlatMinor: 0, RateBps: 50, MinMinor: 0, MaxMinor: 100, WaiveBelowMinor: 0},
			amount: 100, // 100*50/10000 = 0.5 → 1
			want:   1,
		},
		{
			name: "half-up just under .5 stays down",
			rule: Rule{FlatMinor: 0, RateBps: 50, MinMinor: 0, MaxMinor: 100, WaiveBelowMinor: 0},
			amount: 99, // 0.495 → 0
			want:   0,
		},
		{
			name: "flat only",
			rule: Rule{FlatMinor: 500, RateBps: 0, MinMinor: 500, MaxMinor: 500},
			amount: 20_000,
			want:   500,
		},
		{
			name: "flat plus rate then clamp max",
			rule: Rule{FlatMinor: 100, RateBps: 100, MinMinor: 0, MaxMinor: 150},
			amount: 20_000, // 100 + round(200)=300 → 150
			want:   150,
		},
		{
			name: "never below min",
			rule: Rule{FlatMinor: 0, RateBps: 1, MinMinor: 40, MaxMinor: 100, WaiveBelowMinor: 0},
			amount: 10_000, // round(1)=1 → min 40
			want:   40,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Calculate(tc.rule, tc.amount)
			if got != tc.want {
				t.Fatalf("Calculate(%+v, %d) = %d, want %d", tc.rule, tc.amount, got, tc.want)
			}
		})
	}
}

func TestPropertyFeeNeverOutsideBounds(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		min := rapid.Int64Range(0, 5_000).Draw(rt, "min")
		max := rapid.Int64Range(min, min+10_000).Draw(rt, "max")
		rule := Rule{
			FlatMinor:       rapid.Int64Range(0, 2_000).Draw(rt, "flat"),
			RateBps:         int32(rapid.Int64Range(0, 10_000).Draw(rt, "bps")),
			MinMinor:        min,
			MaxMinor:        max,
			WaiveBelowMinor: rapid.Int64Range(0, 20_000).Draw(rt, "waive"),
		}
		amount := rapid.Int64Range(0, 5_000_000).Draw(rt, "amount")
		got := Calculate(rule, amount)
		if got < 0 {
			rt.Fatalf("negative fee %d", got)
		}
		if amount > 0 && rule.WaiveBelowMinor > 0 && amount < rule.WaiveBelowMinor {
			if got != 0 {
				rt.Fatalf("waived amount %d produced fee %d", amount, got)
			}
			return
		}
		if amount <= 0 {
			if got != 0 {
				rt.Fatalf("non-positive amount produced fee %d", got)
			}
			return
		}
		if got < rule.MinMinor {
			rt.Fatalf("fee %d below min %d", got, rule.MinMinor)
		}
		if got > rule.MaxMinor {
			rt.Fatalf("fee %d above max %d", got, rule.MaxMinor)
		}
	})
}
