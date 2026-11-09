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

## feat: append-only audit wrap

- Learned: a useful auth row is the actor, the action, the target, and the request id. `login.failure` always says `invalid_credentials`, even when the phone is unknown, so the log is not a second enumeration oracle. Password, OTP, refresh token, and PIN are not summary fields — `audit.Record` rejects those keys and the error names the field, never the value.
- Surprised: the UPDATE/DELETE trigger is real (Postgres raises `audit_logs is append-only`), and it still misses `TRUNCATE`. Row triggers do not fire for it, so one statement could empty the trail.
- Redo: append-only is three triggers (update, delete, truncate) plus the secret-key check in the recorder. A comment that says "please don't update" would not have survived the mutation test. `pin_set_before` stays, because that flag is the before/after, not the PIN.

## feat: transaction PIN set and change

- Learned: session proves identity; PIN proves intent. Set uses password step-up, change uses old PIN + the same lockout path money movement will call — wrong `old_pin` burns tries, malformed `old_pin` does not.
- Surprised: weak-PIN rejection must stay *before* any argon2 work, or a timing gap between "weak" and "strong-but-wrong-password" becomes a free oracle.

## feat: PIN lockout and verify helper

- Learned: PIN lockout must not soft-lock login — `pin.Verify` only gates money movement. Atomic `UPDATE … RETURNING` keeps concurrent wrong verifies from skipping the 5th-failure lock.
- Surprised: while locked, skipping argon2 is not just a perf win — a Match call would still burn CPU for an attacker who already knows the account is locked.

## feat: ledger schema

- Learned: a wallet is a liability. Crediting it means Twofold owes the user more. A posting keeps a direction and a positive `amount_minor`, so a café credited 300 and debited a fee of 3 is two readable lines, and `sum(debits) = sum(credits)` compares two positive totals.
- Surprised: Postgres already rejects a negative cached balance when `allow_negative` is false, and that flag defaults to false. The five system accounts opt in, because a fee reversal or a provider settlement can cross zero before the books catch up. The balanced-journal trigger is still absent on purpose. That check belongs to the posting engine.

## feat: posting engine that only accepts balanced journals

- Learned: `ledger.Post` is the only writer — `NewMoney` / `NewLine` / `NewJournal` hide the fields so callers cannot assemble an unbalanced journal without hitting `ErrEmptyJournal`, `ErrCurrencyMismatch`, or `ErrUnbalanced` first. Postgres still has the last word: a deferred constraint trigger rejects the same mistake at `COMMIT` even if someone inserts with raw SQL.
- Surprised: an immediate trigger makes double-entry impossible (the first line always looks unbalanced). `DEFERRABLE INITIALLY DEFERRED` is what lets you insert debit then credit inside one transaction and only check when the thought is finished.
