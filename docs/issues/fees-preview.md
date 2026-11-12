# feat: fee rules, transfer preview, and expected-fee check

**Paste into a new GitHub issue.**

| | |
|---|---|
| **Labels** | `backend` · Milestone **M3 Money movement** |
| **Branch** | `feat/day-17-fees-preview` |
| **Depends on** | peer-to-peer transfers + concurrency suite (merged) |

---

## GOAL

Fees are configurable, integer-exact, and always shown before confirm. Transfers post the fee line and reject a stale preview. (FR-FEE, FR-TRF-2/3, PRD §7.3)

## WHAT YOU'LL LEARN

- Basis points and round-half-up in minor units (no floats)
- Effective-dated fee configuration
- Why the client sends `expected_fee_minor` and the server returns `409 fee_changed` on mismatch

## TASKS

- [x] Migration + seed `fee_rules` (flat, `rate_bps`, min, max, waive_below — per kind and tier)
- [x] `fee.Calculate(kind, tier, amount)` with table tests for rounding edges
- [x] `POST /v1/transfers/preview` → amount, fee, total debit, recipient display name (no money moved)
- [x] Transfers debit `amount + fee`, credit recipient `amount`, credit `fee_revenue` `fee`
- [x] Reject when `expected_fee_minor` ≠ computed fee (`409 fee_changed`)
- [x] Hot system accounts stay unlocked when the fee line posts (ADR 0003)

## DONE WHEN

- [x] Property: fee never exceeds max, never below min, never negative
- [x] Happy-path journal matches PRD §7.3 P2P with fee (e.g. Dr 102 / Cr 100 + Cr fee_revenue 2)
- [x] Preview moves nothing; transfer with wrong expected fee moves nothing

## OUT OF SCOPE

- KYC tiers / limits (Day 18)
- Merchant fee schedule (later)
