# feat: posting engine that only accepts balanced journals

**Paste into a new GitHub issue.**

| | |
|---|---|
| **Labels** | `backend` · `ledger` · Milestone **M2 Ledger core** |
| **Branch** | `feat/day-10-posting-engine` |
| **Depends on** | `feat: ledger schema — accounts, journals, and postings` (merged) |

---

## GOAL

`ledger.Post(ctx, tx, Journal)` — the only function in the codebase allowed to move money.

## WHAT YOU'LL LEARN

- Validating a journal: at least two lines, one currency, debits equal credits
- Computing a balance change from direction and the account's normal balance
- Writing an API that is hard to misuse (unexported constructors, value types)

## TASKS

- [x] `ledger.Journal`, `ledger.Line`, `ledger.Money` (`int64` minor + currency)
- [x] Validation errors: `ErrUnbalanced`, `ErrCurrencyMismatch`, `ErrEmptyJournal`
- [x] Insert journal + postings in the caller's transaction
- [x] Deferred constraint trigger: every journal's debits equal credits at commit

## DONE WHEN

- [x] Unit tests for each validation error
- [x] Integration test: an unbalanced journal inserted with raw SQL fails at `COMMIT`

## OUT OF SCOPE

- Reversal journals (Day 11)
- Cached balance / version updates (Day 11)
- Transfer / wallet HTTP handlers (Day 12+)
