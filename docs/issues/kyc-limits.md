# feat: KYC tiers and transaction limits

**Paste into a new GitHub issue.**

| | |
|---|---|
| **Labels** | `backend` · `security` · Milestone **M3 Money movement** |
| **Branch** | `feat/day-18-kyc-limits` |
| **Depends on** | fees preview (merged) |

---

## GOAL

Enforce PRD §12.1 limits on every money movement, and let users submit KYC. (FR-KYC)

## WHAT YOU'LL LEARN

- Why regulators require tiered KYC
- Rolling daily/monthly windows with efficient SQL
- Checking limits **inside** the money transaction so races cannot slip past the cap

## TASKS

- [x] Migration + seed `tier_limits`; add `kyc_submissions`
- [x] `limit.Check(ctx, tx, user, kind, amount)` — balance cap, per-tx, daily, monthly
- [x] Wire `limit.Check` into the transfer DB transaction (after locks, before journal)
- [x] `POST /v1/kyc/submissions` (data + document keys; file upload later)
- [x] `GET /v1/me` shows limit usage
- [x] Clear errors: `limit_exceeded` with which limit and how much remains

## DONE WHEN

- [x] Boundary tests: limit−1 succeeds, limit succeeds, limit+1 fails
- [x] Recipient balance cap is enforced too
- [x] Concurrent transfers cannot collectively exceed a daily cap

## OUT OF SCOPE

- Admin KYC review UI (admin milestone)
- Document file storage / upload (Day 38)
