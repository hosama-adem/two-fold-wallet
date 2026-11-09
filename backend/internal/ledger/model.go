package ledger

// CurrencyETB is the only currency the product moves today.
// The columns accept any three-letter code so a posting can be rejected for
// disagreeing with its account, not for being an unknown ISO code.
const CurrencyETB = "ETB"

// MaxAmountMinor is the largest single amount Postgres will store: 10^15 santim.
// One birr is 100 santim, so this ceiling is 10^13 ETB. It matches
// postings_amount_positive and transactions_amount_positive.
const MaxAmountMinor int64 = 1_000_000_000_000_000

// AccountType is the role of a bucket on Twofold's books.
type AccountType string

const (
	Asset     AccountType = "asset"
	Liability AccountType = "liability"
	Revenue   AccountType = "revenue"
	Expense   AccountType = "expense"
	Equity    AccountType = "equity"
)

// Side is one column of a T-account. Posting direction and an account's
// normal balance use the same two words.
type Side string

const (
	Debit  Side = "debit"
	Credit Side = "credit"
)

// NormalBalanceFor matches accounts_type_normal_balance_match.
// Assets and expenses grow when they are debited. Liabilities, revenue, and
// equity grow when they are credited. A user wallet is a liability: a credit
// is money Twofold owes the customer.
func NormalBalanceFor(t AccountType) (Side, bool) {
	switch t {
	case Asset, Expense:
		return Debit, true
	case Liability, Revenue, Equity:
		return Credit, true
	default:
		return "", false
	}
}

// IncreasesBalance reports whether a posting on this side raises the cached
// balance. The posting engine will apply it; the schema only stores the two
// sides. A debit raises a debit-normal account. A credit raises a credit-normal
// account, including every user wallet.
func IncreasesBalance(normal, direction Side) bool {
	return normal != "" && normal == direction
}
