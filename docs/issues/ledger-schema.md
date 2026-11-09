# feat: ledger schema — accounts, journals, and postings

**Paste into a new GitHub issue.**

| | |
|---|---|
| **Labels** | `backend` · `ledger` · `good-first-read` · Milestone **M2 Ledger core** |
| **Branch** | `feat/day-09-ledger-schema` |
| **Depends on** | `feat: transaction PIN with lockout` (merged) |

---

## GOAL

The tables from PRD §7 and §14 with the database enforcing as much of the accounting as it can — before any Go posting engine exists.

## WHAT YOU'LL LEARN

- Double-entry: assets vs liabilities, normal balances, debit/credit direction
- Why wallets are liabilities on Twofold's books
- Encoding invariants as constraints: `amount_minor > 0`, currency match, enums
- Seeding system accounts in a migration

## TASKS

- [ ] Migration: `accounts`, `journal_entries`, `postings`, `transactions`, `transaction_events`
- [ ] Checks: positive amounts, valid direction, `allow_negative = false` by default
- [ ] Seed system accounts: `provider_clearing:telebirr`, `provider_clearing:chapa`, `payouts_pending`, `fee_revenue`, `suspense`
- [ ] Write `docs/adr/0001-double-entry-ledger.md` in your own words

## DONE WHEN

- [ ] Inserting a zero or negative posting fails at the database
- [ ] ADR explains direction + positive amount vs signed amounts

## OUT OF SCOPE

- `ledger.Post` / posting engine (Day 10)
- Reversals and cached balance updates (Day 11)
- Wallets HTTP API (Day 12)
