// Package ledger is the source of truth for accounts, journals, postings, and balances.
//
// A user wallet is a liability on Twofold's books: the balance is money the
// platform owes the customer. Provider clearing accounts are assets. Fees
// land in fee revenue. Every movement is a journal of postings, and each
// posting is a direction (debit or credit) plus a strictly positive amount.
//
// This package names those types and reads the tables. The only writer of
// journals, ledger.Post, arrives with the posting engine. Until then Postgres
// is the enforcement: positive amounts, known directions, currency match, and
// a cached balance that cannot go negative unless the account allows it.
package ledger
