# ADR 0004: Transactional outbox before River

**Status:** Accepted  
**Date:** 2026-11-25

## Context

After a transfer completes we want a notification. After a top-up settles we
want the same. The naïve path is: commit the money, then call the queue.

That is the dual-write problem. If the process dies between commit and
enqueue, the money moved and the side effect never happened. If we enqueue
before commit and the money rolls back, a worker fires on a world that never
existed.

PRD §11.1 requires the opposite: side effects exist only when the business
transaction commits, and they are never lost after that commit.

## Decision

### 1. Write `outbox_events` in the same Postgres transaction

When a money path finishes (transfer completed, top-up settled, withdrawal
refunded), it inserts a row into `outbox_events` before `COMMIT`. Same
connection. Same atomicity. Rollback deletes the event. Commit keeps it.

Payload is JSON. Topic is a stable string (`transfer.completed`,
`topup.completed`, `withdrawal.failed_refunded`). Day 26 turns topics into
notifications. Day 25 only guarantees delivery into River.

### 2. Dispatch with `FOR UPDATE SKIP LOCKED`

A dispatcher (periodic River job + `cmd/worker`) claims a batch:

```sql
SELECT … FROM outbox_events
WHERE dispatched_at IS NULL
ORDER BY created_at, id
FOR UPDATE SKIP LOCKED
LIMIT $1
```

Two workers cannot claim the same row. Each claimed row is published to River
inside that transaction, then `dispatched_at` is set. Crash after commit and
before dispatch leaves the row undispatched — the next tick delivers it.
That is the done-when for Day 25.

### 3. River is the worker runtime, not the source of truth

River holds retries, backoff, and dead letters. The outbox is the durable
intent log. Consumers must be idempotent (at-least-once). Duplicate delivery
is possible if a worker finishes after we marked dispatched but before the
client ack — Day 26 dedupes by event id.

### 4. Periodic jobs live on the worker binary

`cmd/worker` runs:

- outbox dispatch
- idempotency key cleanup
- money-request expiry
- stale top-up status polling

The API process does not run these loops. `make worker` starts the binary.

## Consequences

- Money handlers grow one insert. They never talk to River directly for
  notifications.
- Ops watch `outbox_events` lag (`dispatched_at IS NULL`) and River dead
  letters.
- Day 26 can change notification shape without touching ledger code.

## Alternatives considered

**Enqueue River inside the money transaction.** Works for jobs that only need
Postgres visibility, but couples every money package to River and still needs
an outbox if any consumer is outside our DB.

**Outbox table only, no River.** Possible for a laptop demo. River gives
retries and schedules we already promised in PRD §11.2.
