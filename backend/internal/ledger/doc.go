// Package ledger is the source of truth for accounts, journals, postings, and balances.
//
// A user wallet is a liability on Twofold's books: the balance is money the
// platform owes the customer. Provider clearing accounts are assets. Fees
// land in fee revenue. Every movement is a journal of postings, and each
// posting is a direction (debit or credit) plus a strictly positive amount.
//
// ledger.Post is the only writer of ordinary journals. ledger.Reverse is the
// only writer of a reversal: same lines, directions flipped, and
// reverses_journal_id set. That column is unique, so a journal can be undone
// once. Callers pass a Journal built through NewMoney / NewLine / NewJournal.
//
// Postgres rejects an edit to history, a negative cached balance on an
// account that does not allow it, and — via a deferred constraint trigger —
// a journal whose debits do not equal credits at COMMIT.
//
// accounts.balance_minor and accounts.version are a cache, updated in the
// same transaction as the postings that justify them. Each posting stores
// balance_after_minor and the account version after that line.
// RecomputeBalance rebuilds the cache from postings so a drift monitor can
// prove the two are equal.
package ledger
