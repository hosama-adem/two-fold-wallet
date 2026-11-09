package ledger

// Stable system account codes seeded by migration 00005_ledger.
const (
	CodeProviderClearingTelebirr = "provider_clearing:telebirr"
	CodeProviderClearingChapa    = "provider_clearing:chapa"
	CodePayoutsPending           = "payouts_pending"
	CodeFeeRevenue               = "fee_revenue"
	CodeSuspense                 = "suspense"
)

// SystemAccountCodes is every platform account created by the ledger migration.
var SystemAccountCodes = []string{
	CodeProviderClearingTelebirr,
	CodeProviderClearingChapa,
	CodePayoutsPending,
	CodeFeeRevenue,
	CodeSuspense,
}
