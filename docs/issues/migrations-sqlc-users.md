# feat: migrations with goose, queries with sqlc, and the users table

**Paste into a new GitHub issue.**

| | |
|---|---|
| **Labels** | `backend` · Milestone **M1 Foundations** |
| **Branch** | `feat/migrations-sqlc-users` |
| **Depends on** | `chore: scaffold the backend for Twofold` (merged) |

---

## GOAL

A repeatable way to change the schema and type-safe SQL, starting with `users` and `otp_codes`.

## WHAT YOU'LL LEARN

- Versioned migrations: up/down, why you never edit a merged migration
- sqlc: write SQL, get typed Go; why not an ORM for money code
- Postgres constraints as the last line of defence (unique, check, not null)
- `citext`, `timestamptz`, UUIDv7 and why time-ordered IDs help indexes

## TASKS

- [x] Goose-format migrations + `migrate-up` / `migrate-down` / `migrate-new` (`Makefile` + `./dev.sh`)
- [x] Migration: `users` and `otp_codes` per PRD §14.3 (E.164 check, `kyc_tier` 0–2)
- [x] `sqlc.yaml` + `internal/user/queries.sql` (create, get by phone, get by id, mark phone verified)
- [x] `generate` / CI fails if generated `userdb` is stale
- [x] Integration tests against Postgres (`DATABASE_URL`) + CI Postgres service

## DONE WHEN

- [x] migrate up → down → up covered in CI
- [x] Repository test proves duplicate phone numbers are rejected by the database
- [x] CI workflow runs vet + migrate + tests

## OUT OF SCOPE

- Register / OTP HTTP endpoints
- JWT login
- Wallet creation
