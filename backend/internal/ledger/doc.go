// Package ledger is the source of truth for accounts, journals, postings, and balances.
//
// Customer wallets are liabilities: money in a wallet is money Twofold owes the user.
// Postings store direction (debit|credit) and a positive amount_minor — never a signed
// amount. System clearing and revenue accounts are seeded by migration 00005.
package ledger
