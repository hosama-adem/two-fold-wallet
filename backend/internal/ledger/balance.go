package ledger

// BalanceDelta is how a posting changes an account's cached balance.
// Same side as the account's normal balance raises the cache; the opposite
// side lowers it. Day 11 writes this into accounts.balance_minor; Post uses
// it today only to fill balance_after_minor on each posting row.
func BalanceDelta(normal, direction Side, amountMinor int64) int64 {
	if IncreasesBalance(normal, direction) {
		return amountMinor
	}
	return -amountMinor
}
