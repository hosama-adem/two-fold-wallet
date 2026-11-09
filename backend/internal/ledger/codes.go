package ledger

// System account codes seeded by migration 00009_seed_system_accounts.
// The CHECK accounts_system_code_iff_ownerless lists the same codes.
const (
	CodeProviderClearingTelebirr = "provider_clearing:telebirr"
	CodeProviderClearingChapa    = "provider_clearing:chapa"
	CodePayoutsPending           = "payouts_pending"
	CodeFeeRevenue               = "fee_revenue"
	CodeSuspense                 = "suspense"
)

// SystemAccount is one platform bucket created by the ledger seed migration.
type SystemAccount struct {
	Code          string
	Type          AccountType
	Normal        Side
	AllowNegative bool
}

// SystemAccounts is the chart the migration inserts. Tests compare the
// database to this list so the SQL seed and the Go names cannot drift quietly.
var SystemAccounts = []SystemAccount{
	{Code: CodeProviderClearingTelebirr, Type: Asset, Normal: Debit, AllowNegative: true},
	{Code: CodeProviderClearingChapa, Type: Asset, Normal: Debit, AllowNegative: true},
	{Code: CodePayoutsPending, Type: Liability, Normal: Credit, AllowNegative: true},
	{Code: CodeFeeRevenue, Type: Revenue, Normal: Credit, AllowNegative: true},
	{Code: CodeSuspense, Type: Equity, Normal: Credit, AllowNegative: true},
}
