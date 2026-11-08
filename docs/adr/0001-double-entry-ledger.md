# ADR 0001: Double-entry ledger with direction and positive amounts

**Status:** Accepted  
**Date:** 2026-11-08  
**Context:** M2 ledger core — schema before the posting engine

## Context

Twofold’s balances must be rebuildable from history. Every movement of value is a
journal entry made of two or more postings. We needed to decide how a single
posting stores “which way” money moved, and how the database refuses bad rows
before any Go posting engine exists.

Customer wallets are **liabilities** on Twofold’s books: money in a wallet is
money we owe the user. Provider clearing accounts are **assets**. Fee income is
**revenue**. That mapping drives each account’s normal balance (debit-normal vs
credit-normal) and how a debit or credit changes the cached balance later.

## Decision

1. **Postings carry `direction` (`debit` | `credit`) and a strictly positive
   `amount_minor`.** We do not store signed amounts.
2. **Currency lives on both the account and the posting**, with a composite
   foreign key `(account_id, currency) → accounts(id, currency)` so a posting
   cannot quietly use a different currency than its account.
3. **`allow_negative` defaults to `false`.** User wallets must not go below
   zero on the cached balance; selected system accounts may set
   `allow_negative = true` when a temporary negative is meaningful.
4. **System accounts are seeded in the migration**
   (`provider_clearing:telebirr`, `provider_clearing:chapa`, `payouts_pending`,
   `fee_revenue`, `suspense`) so every environment starts with the same
   clearing and revenue buckets.

## Why direction + positive amount (not signed amounts)

Signed amounts look compact (`-300` vs `+300`), but they hide the story on
mixed journals. A merchant payment fees the café in the same journal that pays
it: credit 300 ETB, debit 3 ETB fee. With direction, both lines stay readable
and sum cleanly as `sum(debits) = sum(credits)` per currency. With signs, one
line is “negative” for reasons that are not “money left the account” in the
accounting sense — they are opposite sides of the same event.

Positive amounts also make overflow and “zero money” checks trivial:
`amount_minor > 0` (and an upper bound) at the database. A signed column would
need extra rules for zero, and apps would disagree about whether `-x` means
credit or debit.

Balance formulas stay explicit at the account:

| Normal balance | Balance meaning        |
|----------------|------------------------|
| Debit          | `debits − credits`     |
| Credit         | `credits − debits`     |

The posting engine (next issue) applies those formulas. The schema only stores
facts the database can already police.

## Consequences

- Inserting `amount_minor <= 0`, an unknown `direction`, or a currency that
  does not match the account fails in Postgres — not later in API code.
- Balanced journals (`sum(debits) = sum(credits)`) are **not** enforced here;
  that deferred constraint lands with `ledger.Post`.
- Immutability triggers and reversals are also deferred; `reverses_journal_id`
  is unique so a journal can be reversed at most once when that work lands.
- Application code must never invent a second way to move money outside
  balanced journals.

## Alternatives considered

- **Signed `amount_minor` only** — rejected: ambiguous on multi-line journals,
  weaker CHECK constraints, harder receipts.
- **Separate debit/credit columns** — rejected: one of the two is always null;
  direction + one amount is clearer.
- **Currency only on the account** — rejected: denormalizing currency onto the
  posting enables the composite FK and keeps history self-describing.
