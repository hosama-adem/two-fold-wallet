-- name: InsertMoneyRequest :one
INSERT INTO money_requests (
    id, requester_id, payer_id, amount_minor, currency, note, status, expires_at
) VALUES (
    @id, @requester_id, @payer_id, @amount_minor, @currency, @note, 'pending', @expires_at
)
RETURNING *;

-- name: GetMoneyRequest :one
SELECT * FROM money_requests WHERE id = @id;

-- name: LockMoneyRequest :one
-- Row lock for pay. Callers then TransferInTx and MarkPaid in the same tx.
SELECT * FROM money_requests
WHERE id = @id
FOR UPDATE;

-- name: MarkPaid :one
UPDATE money_requests
SET status = 'paid',
    paid_transaction_id = @paid_transaction_id,
    updated_at = now()
WHERE id = @id
  AND status = 'pending'
  AND expires_at > now()
RETURNING *;

-- name: MarkDeclined :one
UPDATE money_requests
SET status = 'declined',
    updated_at = now()
WHERE id = @id
  AND payer_id = @payer_id
  AND status = 'pending'
  AND expires_at > now()
RETURNING *;

-- name: MarkCancelled :one
UPDATE money_requests
SET status = 'cancelled',
    updated_at = now()
WHERE id = @id
  AND requester_id = @requester_id
  AND status = 'pending'
  AND expires_at > now()
RETURNING *;

-- name: ExpireDueRequests :exec
-- Eager expiry (FR-REQ-3). Day 25 replaces this with a River job; until then
-- list/pay call it so the inbox never shows a stale pending row.
UPDATE money_requests
SET status = 'expired',
    updated_at = now()
WHERE status = 'pending'
  AND expires_at <= now();

-- name: MarkExpired :one
UPDATE money_requests
SET status = 'expired',
    updated_at = now()
WHERE id = @id
  AND status = 'pending'
RETURNING *;

-- name: ListIncoming :many
SELECT * FROM money_requests
WHERE payer_id = @payer_id
ORDER BY created_at DESC, id DESC
LIMIT @row_limit;

-- name: ListOutgoing :many
SELECT * FROM money_requests
WHERE requester_id = @requester_id
ORDER BY created_at DESC, id DESC
LIMIT @row_limit;

-- name: LookupCounterparty :one
SELECT id, display_name, handle, phone_e164
FROM users
WHERE id = @id;
