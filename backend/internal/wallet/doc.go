// Package wallet is the customer-facing ETB wallet.
//
// A wallet is not a balance column. It is one liability account — money
// Twofold owes the user — plus the status, freeze mode, and reason a person
// or a compliance officer needs to see. ledger.Post remains the only writer
// of money. Opening a wallet inserts the account at zero; it does not post.
//
// Two numbers:
//
//	ledger balance     accounts.balance_minor, rebuildable from postings
//	available balance  ledger − pending cash-outs (view wallet_balances)
//
// Pending cash-outs are transactions with kind cash_out and status pending,
// processing, or needs_review. Week 4 is what creates those rows. Until one
// exists, available and ledger are the same number. The view is the formula
// so a cached available column cannot drift from the transactions table.
//
// Freeze is stored here and enforced later, inside the money transaction:
//
//	outbound  cannot send or cash out; can still receive
//	full      cannot send, cash out, or receive
//	closed    no movement, and only when the ledger balance is already zero
//
// Gate.Allows is the check Day 15 (transfers) and Week 4 (cash-out) call
// while they hold the wallet row. GET /v1/wallets does not apply it: a frozen
// user can still read what they hold. The customer response omits
// frozen_reason; that text is for the compliance freeze API, not for lookup.
package wallet
