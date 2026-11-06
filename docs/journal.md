# Build journal

Two lines per merged day: what you learned, what surprised you.

## docs: rebrand to Twofold

- Learned: a buildable PRD needs requirement IDs, non-goals, and ledger postings written before code.
- Surprised: the first ship intentionally has zero application code — the product story leads.

## chore: scaffold the backend

- Learned: package-by-feature under `internal/` makes later money domains obvious homes before any SQL lands.
- Surprised: host `:5432` was already owned by another Postgres — published Twofold on `:5434` and verified via `/ready`.

## feat: migrations, sqlc, users

- Learned: Postgres unique/check constraints are the last line of defence — duplicate phones fail at the DB.
- Surprised: Cursor appeared as a GitHub co-author via commit trailers; stripping them + force-push cleaned contributors.

## feat: register-otp

- Learned: anti-enumeration means identical success shapes even when the phone already exists.
- Surprised: OTP cooldown must use the service clock for `created_at`, not DB `now()`, or tests flake.

## feat: login-refresh

- Learned: refresh reuse detection needs `FOR UPDATE` + family revoke, or concurrent refresh races win twice.
- Surprised: Ed25519 PEM in `.env` needs `\n` normalization or local boot fails while CI fixtures pass.

## feat: auth-hardening

- Learned: token-bucket limits per IP *and* phone catch credential stuffing that rotates only one dimension.
- Surprised: append-only `audit_logs` is one trigger away — UPDATE/DELETE fail at Postgres before app code.
