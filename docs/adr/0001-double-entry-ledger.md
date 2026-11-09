# ADR 0001: Direction and a positive amount

**Status:** Accepted
**Date:** 2026-11-09

## Context

Twofold's balances have to be rebuildable from history. Every movement of value
is a journal entry made of postings. The schema has to record which way each
line moved, and Postgres has to reject a bad line before any Go posting engine
exists.

From the platform's point of view, money in a customer wallet is money Twofold
owes that customer. Wallets are **liabilities**. Cash sitting at Telebirr or
Chapa is an **asset** (`provider_clearing:*`). Fees we have earned are
**revenue**. Unexplained provider money waits in **suspense** (equity).

That classification picks the normal balance:

| Account type | Normal side | Balance |
|---|---|---|
| Asset, expense | Debit | debits − credits |
| Liability, revenue, equity | Credit | credits − debits |

A credit on a wallet increases what we owe. A debit on clearing increases what
the provider is holding for us. The same word, "credit", means opposite
economic directions on those two accounts. The posting has to say which column
it sits in, and the account has to say which column is its normal side.

## Decision

Each posting stores `direction` (`debit` or `credit`) and a strictly positive
`amount_minor`. We do not store a signed amount.

A top-up of 500 ETB via Telebirr:

| Account | Debit | Credit |
|---|---:|---:|
| `provider_clearing:telebirr` (asset) | 500 | |
| wallet (liability to the user) | | 500 |

A merchant payment of 300 ETB with a 3 ETB fee charged to the café is the case
that decided this:

| Account | Debit | Credit |
|---|---:|---:|
| `wallet:selam` | 300 | |
| `wallet:cafe` | 3 | |
| `wallet:cafe` | | 300 |
| `fee_revenue` | | 3 |

Debits 303, credits 303. The café has two lines in one journal. Both amounts
are positive. Direction is what makes the fee and the sale readable next to
each other.

The database enforces the part it can see without looking at the whole journal:

- `amount_minor > 0` and `amount_minor <= 10^15` (santim). Zero is not a movement.
- `direction` is only `debit` or `credit`.
- The posting's currency matches its account, via
  `(account_id, currency) → accounts(id, currency)`.
- `allow_negative` defaults to `false`, and the cached `balance_minor` cannot
  go below zero unless that flag is set. User wallets leave it false. The
  seeded system accounts set it true, because settlement timing and a fee
  reversal can cross zero for real.
- An account with no owner is one of the seeded system codes, and a system
  code has no owner.
- A journal is a reversal exactly when `reverses_journal_id` is set, and that
  column is unique, so an original journal can be reversed once.

`ledger.Post` is the only writer of journals. A debit raises a debit-normal
balance and a credit raises a credit-normal balance. Same side means the
balance goes up; the opposite side means it goes down.

## Why a signed column loses

A signed `amount_minor` looks smaller: one number instead of a number plus a
word. It breaks on the café. One of those two café lines would be negative, and
the sign would not mean "the balance fell". It would mean "this line is the
other column", which is a different fact, and it depends on which account type
you secretly assumed.

Positive amounts keep the journal invariant obvious: for one journal and one
currency, `sum(debits) = sum(credits)`, both sides totals of positive numbers.
A signed column also makes the overflow check worse. `amount_minor > 0` is a
single predicate. A signed column needs a rule for zero, a rule for magnitude,
and an agreement about whether negative means debit or credit. Callers drift.

Two amount columns (`debit_minor`, `credit_minor`) with one of them always null
was the other option. It stores the same facts as direction plus one amount,
and it invites a row that fills both or neither. One amount column cannot be
on both sides.

## Consequences

- Inserting `0`, a negative amount, an unknown direction, or a currency that
  is not the account's currency fails in Postgres.
- Inserting a negative `balance_minor` on an account with `allow_negative =
  false` fails in Postgres. That flag defaults to false.
- The five system accounts
  (`provider_clearing:telebirr`, `provider_clearing:chapa`, `payouts_pending`,
  `fee_revenue`, `suspense`) exist as soon as migrations finish, with stable
  ids, in ETB.
- Unbalanced journals are rejected at `COMMIT` by the deferred constraint
  trigger `postings_journal_balanced` (migration 00010). Immutability
  triggers arrive with reversals. `wallets` and the foreign keys from
  `transactions.from_wallet_id` / `to_wallet_id` arrive with the wallets API.
- `balance_after_minor` and `account_version` are required on every posting.
  `ledger.Post` writes the projected running balance; updating the cached
  `accounts.balance_minor` / `version` arrives with reversals.

## Alternatives considered

- **Signed `amount_minor`.** Rejected. Ambiguous on a journal that debits and
  credits the same account, and a weaker check than `amount_minor > 0`.
- **Separate debit and credit columns.** Rejected. One of the two is always
  empty, and both-filled rows need yet another check.
- **Currency only on the account.** Rejected. Copying it onto the posting makes
  the composite foreign key possible, and a posting row stays readable on its
  own.
