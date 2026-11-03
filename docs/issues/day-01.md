# Day 1 — docs: rebrand to Twofold — PRD, plan, and design system

**Paste this file into a new GitHub issue.**

| | |
|---|---|
| **Labels** | `docs` · Milestone **M1 Foundations** |
| **Branch** | `docs/day-01-rebrand-to-twofold` |
| **Date (plan)** | Mon 2 Nov |
| **Accelerated** | Morning block if compressing Days 1–3 into one day |

---

## GOAL

Turn RealLedger into Twofold **on paper** before touching money code: a detailed PRD, a day-by-day plan, a design system, and an archive of the old docs.

## WHAT YOU'LL LEARN

- Writing a PRD that engineers can build from: personas, journeys, requirements with IDs, non-goals
- Writing down the ledger postings for every money flow before writing code
- Picking an accessible colour palette (WCAG contrast ratios)
- Crediting prior art correctly when a project has no licence

## TASKS

- [x] Move RealLedger docs to `docs/archive/realledger/` with a README explaining the pivot
- [x] Write `docs/PRD.md` (product, ledger, concurrency, idempotency, state machines, risk, data model, API, NFRs, testing)
- [x] Write `docs/implementation-plan.md` (49 days, every issue)
- [x] Write `docs/design-system.md` and `docs/brand/palette.html`
- [x] Rewrite the root `README.md` for Twofold
- [x] Keep canonical PDFs under `docs/pdf/`

## DONE WHEN

- [x] PRD + implementation plan live in the repo (markdown + PDF)
- [x] Palette contrast pairs meet WCAG AA (open `docs/brand/palette.html`)
- [x] Archive README explains the RealLedger → Twofold pivot
- [ ] (Later days) `make up && make run` still works — N/A on greenfield Day 1; no app code yet

## PR checklist

```text
Branch: docs/day-01-rebrand-to-twofold
Closes: #<this-issue>
```

## Next (same calendar day if accelerating)

| Block | Day | Issue |
|---|---|---|
| Afternoon | 2 | `chore: reshape/scaffold the backend for Twofold` |
| Evening | 3 | `feat: migrations with goose, sqlc, and the users table` |

See [`docs/implementation-plan.md`](../implementation-plan.md) for full Day 2 / Day 3 bodies.
