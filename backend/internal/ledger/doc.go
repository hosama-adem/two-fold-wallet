// Package ledger is the source of truth for accounts, journals, postings, and balances.
//
// A user wallet is a liability on Twofold's books: the balance is money the
// platform owes the customer. Provider clearing accounts are assets. Fees
// land in fee revenue. Every movement is a journal of postings, and each
// posting is a direction (debit or credit) plus a strictly positive amount.
//
// ledger.Post is the only writer of journals and postings. Callers pass a
// Journal value built through NewMoney / NewLine / NewJournal. Postgres still
// enforces positive amounts, known directions, currency match, and — via a
// deferred constraint trigger — that every journal's debits equal credits at
// COMMIT. Cached balance and version updates arrive with reversals.
package ledger
