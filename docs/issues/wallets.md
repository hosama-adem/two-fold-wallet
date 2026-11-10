# feat: wallets API with available and ledger balances

**Paste into a new GitHub issue.**

| | |
|---|---|
| **Labels** | `backend` · Milestone **M2 Ledger core** |
| **Branch** | `feat/day-12-wallets` |
| **Depends on** | `feat: immutable ledger with reversals and cached balances` (merged) |

---

## GOAL

Each verified user gets an ETB wallet; `GET /v1/wallets` shows what they can spend (FR-WAL).

## WHAT YOU'LL LEARN

- Ledger balance vs available balance (holds)
- Creating related rows atomically in one transaction
- Freeze modes and where to enforce them

## TASKS

- [x] Migration: `wallets` (`status`, `freeze_mode`, `frozen_reason`)
- [x] Phone verification creates the liability account + wallet in the same DB transaction
- [x] Holds: `available = ledger − pending withdrawals` (column or view)
- [x] `GET /v1/wallets`, `GET /v1/recipients/lookup?q=` with masked names
- [x] Dev-only `make seed` that funds a test wallet through a proper journal

## DONE WHEN

- [x] Test: verify phone creates exactly one wallet even if called twice concurrently
- [x] Lookup never returns the full phone number

## OUT OF SCOPE

- Peer-to-peer transfers (Day 15)
- Idempotency middleware (Day 13)
- Top-ups / withdrawals (Week 4)
