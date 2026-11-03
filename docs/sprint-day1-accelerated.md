# Accelerated Day — compress plan Days 1–3 into one calendar day

You asked to run the first **three plan-days** in **one real day**. Keep the gates; cut ceremony.

## What you are building

**Twofold** — sandbox Ethiopian wallet. Balances come from a double-entry ledger. No real money. Portfolio-grade correctness (idempotency, concurrency, webhooks) is the point.

Full story: [`PRD.md`](PRD.md) · calendar: [`implementation-plan.md`](implementation-plan.md).

## Today’s three ships

| Clock | Plan day | Outcome |
|---|---|---|
| **Morning (~2–3h)** | Day 1 | Docs committed: PRD, plan, design system, README, archive |
| **Afternoon (~3–4h)** | Day 2 | Go module `github.com/hosama-adem/twofold/backend`, Docker Postgres, `internal/*` packages with `doc.go`, `/health` + `/ready` |
| **Evening (~3–4h)** | Day 3 | goose migrations for `users` + `otp_codes`, sqlc queries, integration test that duplicate phones are rejected |

This repo started **empty** (no RealLedger code to rename). Day 2 is therefore **scaffold**, not rename — same package layout as the plan, without hunting leftover `realledger` strings in code.

## Order of work (do not reorder)

1. **Docs first** — so every later PR can cite FR-IDs and package homes. ← you are here after the first commit
2. **Empty packages + platform wiring** — so Day 3 SQL has a place to live
3. **Schema + sqlc** — identity tables before auth endpoints (Days 4–6)

## Explicit non-goals for today

- No OTP send endpoint yet (Day 5)
- No JWT login yet (Day 6)
- No ledger tables yet (Day 8+)
- No frontend yet (Day 29+)

## Definition of done for the accelerated day

- [ ] Day 1 files merged (this morning’s commit)
- [ ] `go build ./...` and health endpoints green
- [ ] `make migrate-up` / `migrate-down` / `migrate-up` clean
- [ ] Test proves unique phone constraint
- [ ] Three journal lines in `docs/journal.md` (one per plan-day)

## Issue titles to open on GitHub

1. `docs: rebrand to Twofold — PRD, plan, and design system` — body: [`issues/day-01.md`](issues/day-01.md)
2. `chore: scaffold the backend for Twofold` — body from plan Day 2 (adapt “rename” → “create”)
3. `feat: migrations with goose, queries with sqlc, and the users table` — body from plan Day 3
