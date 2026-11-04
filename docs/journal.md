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
