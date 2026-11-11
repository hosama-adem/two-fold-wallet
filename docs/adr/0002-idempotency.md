# ADR 0002: Idempotency keys with request hashing and replay

**Status:** Accepted
**Date:** 2026-11-11

## Context

Networks flake. Mobile clients retry. Load balancers time out. A user taps
Send twice. None of those events may move money twice.

Twofold's contract (PRD §9) is:

- Every money-moving (or create) endpoint requires an `Idempotency-Key`
  header — a client-generated UUID v4, one per user intent.
- Keys are scoped per user and kept for 24 hours.
- Same key + same request → the original response, with
  `Idempotent-Replayed: true`.
- Same key + a different body → `422 idempotency_key_reused`.
- A second request while the first is still running →
  `409 request_in_progress` with `Retry-After: 1`.

The ledger also has domain uniqueness (`transactions` unique on
`(initiator_user_id, idempotency_key)`). That is a second layer. This ADR is
about the HTTP replay layer that sits in front of it.

## Decision

### Table

`idempotency_keys` is keyed by `(user_id, key)`:

| Column | Role |
|---|---|
| `request_hash` | `sha256(method + path + canonical JSON body)` |
| `state` | `in_progress` or `completed` |
| `response_code` / `response_body` / `response_content_type` | stored only when `completed` |
| `expires_at` | `created_at + 24h` |

Canonical JSON means object keys sorted recursively so `{"a":1,"b":2}` and
`{"b":2,"a":1}` hash the same. Empty bodies hash as an empty payload.

### Claim

```sql
INSERT … state='in_progress'
ON CONFLICT (user_id, key) DO UPDATE
SET … (reset to in_progress)
WHERE idempotency_keys.expires_at <= EXCLUDED.created_at
RETURNING *
```

- A row comes back → this request won the claim (fresh or expired reclaim).
- No row → a live key exists; load it and branch on hash / state.

### Middleware

Routes opt in by mounting `idempotency.Middleware` **after** auth:

1. Require `Idempotency-Key` (UUID v4).
2. Hash method + path + body; restore the body for the handler.
3. Claim the row.
4. On conflict: replay, `422`, or `409` as above.
5. On win: wrap the response writer, run the handler, then:
   - status `< 500` → `Complete` (store status + body + content type)
   - status `>= 500` or panic → `Release` (delete in_progress) so a retry
     may claim again

We do **not** cache 5xx. A failed attempt must be retryable with the same key.

`response_content_type` is a small extension beyond the PRD column list so
`application/problem+json` replays correctly next to `application/json`.

### Cleanup

`Store.DeleteExpired(asOf)` deletes rows with `expires_at <= asOf`. The
background job that calls it lands on Day 25. Expired rows are also
reclaimable on the next claim, so cleanup is hygiene, not correctness.

### Two layers (and Day 15)

The learning goal “claim the key and the business write in one transaction”
is what transfers will do: `ClaimTx` / `CompleteTx` exist so a money handler
can own the key inside the same Postgres transaction as the journal. The
middleware shipped on Day 13 is the HTTP envelope — claim before the
handler, complete after a successful response — and is enough to prove
replay / `422` / `409` before transfers exist. Domain uniqueness on
`transactions.idempotency_key` remains the third net.

## Consequences

- Mounting the middleware on a route makes the header mandatory.
- Clients must generate the key once per intent and reuse it on retry —
  never per click.
- A crash after the business commit but before `Complete` leaves the key
  `in_progress` until expiry or reclaim; clients see `409` and retry later.
- Day 15 wires this middleware onto `POST /v1/transfers`. Day 25 schedules
  `DeleteExpired`.

## Alternatives considered

- **Rely only on `transactions` unique constraint.** Rejected. A unique
  violation becomes a 500 unless every handler translates it, and there is
  nothing to replay the original body from.
- **Cache 5xx responses.** Rejected. Retries would keep getting the same
  failure forever for a transient outage.
- **Hash the raw body bytes.** Rejected. Clients and proxies reorder JSON
  keys; canonicalization matches intent, not serialization accidents.
