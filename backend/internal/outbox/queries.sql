-- name: InsertOutboxEvent :one
INSERT INTO outbox_events (id, topic, payload)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ClaimUndispatched :many
SELECT id, topic, payload, created_at, dispatched_at
FROM outbox_events
WHERE dispatched_at IS NULL
ORDER BY created_at ASC, id ASC
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: MarkDispatched :execrows
UPDATE outbox_events
SET dispatched_at = now()
WHERE id = $1
  AND dispatched_at IS NULL;

-- name: GetOutboxEvent :one
SELECT id, topic, payload, created_at, dispatched_at
FROM outbox_events
WHERE id = $1;

-- name: CountUndispatched :one
SELECT count(*)::bigint
FROM outbox_events
WHERE dispatched_at IS NULL;
