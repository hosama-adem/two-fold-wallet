package fee

import "time"

// Kind values match fee_rules_kind_known / transactions_kind_known.
const (
	KindP2P             = "p2p"
	KindTopUp           = "top_up"
	KindCashOut         = "cash_out"
	KindMerchantPayment = "merchant_payment"
)

// Rule is one effective-dated fee schedule row.
type Rule struct {
	Kind             string
	Tier             int16
	FlatMinor        int64
	RateBps          int32
	MinMinor         int64
	MaxMinor         int64
	WaiveBelowMinor  int64
	EffectiveFrom    time.Time
}
