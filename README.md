# Twofold

**Every birr, recorded twice.**

A ledger-grade digital wallet for Ethiopia — send, receive, top up, and cash out — with a double-entry ledger underneath that makes it impossible to create or lose money by accident.

> Sandbox only. No real money. Simulated Telebirr / Chapa-style providers. Portfolio engineering project — not an NBE-licensed payment service.

---

## Why this exists

Most student wallets do `UPDATE wallets SET balance = balance - 100`. That works on a laptop and breaks under retries, concurrency, and late webhooks.

Twofold exists to show the parts of a payments system that are hard to fake:

- Double-entry ledger (balances rebuilt from postings)
- Idempotent money APIs
- Ordered locking under concurrent transfers
- Provider webhooks, outbox jobs, drift / reconciliation
- Admin maker-checker, risk holds, audit log

The UI should feel sharp. The backend is why the project exists.

---

## Docs (start here)

| Doc | What it is |
|---|---|
| [`docs/PRD.md`](docs/PRD.md) | Product, ledger, concurrency, API, NFRs |
| [`docs/implementation-plan.md`](docs/implementation-plan.md) | 49 days · 7 milestones · one issue per day |
| [`docs/design-system.md`](docs/design-system.md) | Colour, type, money formatting, components |
| [`docs/brand/palette.html`](docs/brand/palette.html) | Live WCAG contrast check for the palette |
| [`docs/issues/rebrand-to-twofold.md`](docs/issues/rebrand-to-twofold.md) | Day 1 GitHub issue body (paste into GitHub) |
| [`docs/pdf/`](docs/pdf/) | Canonical PDF originals of PRD + plan |

Previous product (RealLedger) notes: [`docs/archive/realledger/`](docs/archive/realledger/).

---

## 7 milestones (49 days → demo)

| Milestone | Focus | Demo |
|---|---|---|
| **M1** Foundations | Auth, OTP, sessions, PIN | Sign up → `/v1/me` |
| **M2** Ledger core | Journals, postings, immutability | Balanced journals + property tests |
| **M3** Money movement | P2P, fees, limits, history | 1,000 concurrent transfers, zero drift |
| **M4** Async & integrity | Providers, webhooks, outbox, drift | Top-up / cash-out via simulator |
| **M5** Customer app | Mobile-first web | Send, top up, receipts, Playwright |
| **M6** Requests, QR & admin | Requests, merchants, staff console | Maker-checker adjustments |
| **M7** Hardening & launch | Risk, OTel, k6, deploy, case study | Live demo + write-up |

---

## Stack (target)

| Layer | Choice |
|---|---|
| API | Go · Gin · pgx · sqlc · goose · Postgres 16 |
| Jobs | Transactional outbox · River |
| Customer web | Next.js · TypeScript · design tokens |
| Admin | Separate staff auth + TOTP MFA |
| Ops | OpenTelemetry · Prometheus · Grafana · k6 |

## Run locally

```bash
cp .env.example .env
./dev.sh up      # Postgres → container twofold-db (host port 5434)
./dev.sh run     # API on :8080
curl -s localhost:8080/health
curl -s localhost:8080/ready
```

`Makefile` mirrors the same targets when GNU make is installed. Package layout lives under `backend/internal/` per PRD §16.2.

---

## Accelerated path (3 days → 1 day)

If you are compressing Week 1 Days 1–3 into a single day:

| Block | Hours | Ship |
|---|---|---|
| **Morning** | ~2–3h | Day 1 docs (this commit) |
| **Afternoon** | ~3–4h | Day 2: Go module + `internal/` packages + Docker/`make` |
| **Evening** | ~3–4h | Day 3: goose migrations + sqlc + `users` / `otp_codes` |

Do not skip **Done when** checks. Cut polish, not correctness gates.

---

## Principles

1. **Correct before fast.** A slow transfer is a bug. A wrong balance is an incident.
2. **The ledger is the truth.** Every balance can be rebuilt from postings.
3. **Nothing is edited; everything is appended.** Mistakes are fixed with reversals.
4. **Every retry is safe.** Any request that moves money can be sent twice.
5. **Show the exact number.** Fees previewed before confirm, enforced at confirm.
6. **Explain every state.** “Pending” always has a reason and a next step.

---

## License / credit

Engineering portfolio project by **Hosama Adem**. Prior RealLedger exploration archived under `docs/archive/realledger/`.
