-- name: InsertNotification :one
INSERT INTO notifications (
    id, user_id, outbox_event_id, topic, title, body, data
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
ON CONFLICT (user_id, outbox_event_id) DO NOTHING
RETURNING *;

-- name: ListNotifications :many
SELECT id, user_id, outbox_event_id, topic, title, body, data, read_at, created_at
FROM notifications
WHERE user_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- name: CountUnread :one
SELECT count(*)::bigint
FROM notifications
WHERE user_id = $1
  AND read_at IS NULL;

-- name: MarkRead :one
UPDATE notifications
SET read_at = now()
WHERE id = $1
  AND user_id = $2
RETURNING *;

-- name: CountByOutboxEvent :one
SELECT count(*)::bigint
FROM notifications
WHERE outbox_event_id = $1;

-- name: GetWalletUserID :one
SELECT user_id
FROM wallets
WHERE id = $1;

-- name: GetTransactionNotifyMeta :one
SELECT id, kind, status, initiator_user_id, from_wallet_id, to_wallet_id,
       amount_minor, fee_minor, currency, reference
FROM transactions
WHERE id = $1;

-- name: GetUserPhone :one
SELECT phone_e164
FROM users
WHERE id = $1;
