# feat: immutable ledger with reversals and cached balances

**Paste into a new GitHub issue.**

| | |
|---|---|
| **Labels** | `backend` · `ledger` · Milestone **M2 Ledger core** |
| **Branch** | `feat/day-11-immutability-balances` |
| **Depends on** | `feat: posting engine that only accepts balanced journals` (merged) |

---

## GOAL

History can never be edited, only reversed; balances are fast to read and always provable.

## WHAT YOU'LL LEARN

- Why financial records are append-only
- Reversal journals and the "reverse once" unique constraint
- Cached balance + version column vs computing from postings
- Optimistic versioning as a second guard

## TASKS

- [ ] Triggers rejecting `UPDATE`/`DELETE` on `journal_entries` and `postings`
- [ ] `ledger.Reverse(ctx, tx, journalID, reason)`; unique `reverses_journal_id`
- [ ] `Post` updates `accounts.balance_minor` and `version`, writes `balance_after_minor` on each posting
- [ ] Non-negative check for accounts with `allow_negative = false`
- [ ] Query: recompute a balance from postings (used by the drift monitor on Day 27)

## DONE WHEN

- [ ] Test: cached balance equals recomputed balance after 100 random journals
- [ ] Test: reversing twice fails; `UPDATE` on postings fails

## OUT OF SCOPE

- Drift monitor job (Day 27)
- Wallet / transfer HTTP APIs (Day 12 / Day 15)
