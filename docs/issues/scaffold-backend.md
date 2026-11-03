# chore: scaffold the backend for Twofold

**Paste this file into a new GitHub issue.**

| | |
|---|---|
| **Labels** | `backend` · `infra` · Milestone **M1 Foundations** |
| **Branch** | `chore/scaffold-backend` |
| **Status** | Implemented on branch |
| **Depends on** | Docs rebrand issue (merged) |

> **Greenfield note:** this repo has no RealLedger code. This issue is **scaffold**, not rename — same layout as PRD §16.2.

---

## GOAL

Create the Go module, Docker Compose, env placeholders, and package layout from PRD §16.2 so every later feature has an obvious home.

## WHAT YOU'LL LEARN

- Go module paths and how they ripple through imports
- Package-by-feature layout vs package-by-layer, and why `internal/` matters
- Docker Compose service / volume / database naming for a clean local stack

## TASKS

- [ ] Module path `github.com/hosama-adem/twofold/backend`; `go mod init` + working `cmd/api` main
- [ ] Compose: container `twofold-db`, volume `twofold-pgdata`, database `twofold`
- [ ] `.env.example`: `DATABASE_URL` for `twofold`; placeholders `JWT_SIGNING_KEY` and `STAFF_JWT_SIGNING_KEY`
- [ ] Create feature packages under `internal/`, each with a `doc.go` (one-sentence job):

  `platform`, `auth`, `user`, `ledger`, `wallet`, `transfer`, `fee`, `limit`, `risk`, `funding`, `provider`, `request`, `merchant`, `idempotency`, `outbox`, `notification`, `integrity`, `audit`, `admin`

- [ ] Put config, database, and HTTP server under `internal/platform/`
- [ ] Makefile targets: `make up`, `make down`, `make run`
- [ ] Health endpoints: `GET /health` → 200, `GET /ready` → 200 when DB is reachable

## DONE WHEN

- [ ] `go build ./...`, `go vet ./...`, `go test ./...`, and lint pass
- [ ] `make up && make run` → `/health` 200 and `/ready` 200
- [ ] No `realledger` string left outside `docs/archive/` (`rg -i realledger`)

## PR checklist

```text
Branch: chore/scaffold-backend
Closes: #<this-issue>
```

## OUT OF SCOPE

- goose migrations
- sqlc / `users` table
- Auth endpoints (OTP, JWT)

## NEXT

`feat: migrations with goose, queries with sqlc, and the users table`  
Branch: `feat/migrations-sqlc-users`
