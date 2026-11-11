-- name: GetWalletByUserCurrency :one
SELECT * FROM wallets
WHERE user_id = $1 AND currency = $2;

-- name: LockWalletBalance :one
-- Locks the wallet and its liability account together.
-- Callers must invoke this for every wallet in sorted wallet-id order.
SELECT
    w.id AS wallet_id,
    w.user_id,
    w.account_id,
    w.currency,
    w.status,
    w.freeze_mode,
    w.frozen_reason,
    a.balance_minor AS ledger_balance_minor,
    COALESCE((
        SELECT SUM(h.amount_minor)
        FROM wallet_holds h
        WHERE h.wallet_id = w.id AND h.status = 'pending'
    ), 0)::bigint AS held_minor
FROM wallets w
JOIN accounts a ON a.id = w.account_id
WHERE w.id = $1
FOR UPDATE OF w, a;

-- name: LookupRecipientByPhone :one
SELECT u.id, u.display_name, u.handle, u.phone_e164
FROM users u
JOIN wallets w
  ON w.user_id = u.id
 AND w.currency = 'ETB'
 AND w.status <> 'closed'
WHERE u.status = 'active'
  AND u.phone_verified_at IS NOT NULL
  AND u.phone_e164 = $1;

-- name: LookupRecipientByHandle :one
SELECT u.id, u.display_name, u.handle, u.phone_e164
FROM users u
JOIN wallets w
  ON w.user_id = u.id
 AND w.currency = 'ETB'
 AND w.status <> 'closed'
WHERE u.status = 'active'
  AND u.phone_verified_at IS NOT NULL
  AND u.handle = $1;

-- name: InsertTransaction :one
INSERT INTO transactions (
    id,
    reference,
    kind,
    status,
    initiator_user_id,
    from_wallet_id,
    to_wallet_id,
    amount_minor,
    fee_minor,
    currency,
    note,
    idempotency_key
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
)
RETURNING *;

-- name: CompleteTransaction :one
UPDATE transactions
SET status = 'completed',
    completed_at = now()
WHERE id = $1
  AND status = 'pending'
RETURNING *;

-- name: InsertTransactionEvent :one
INSERT INTO transaction_events (
    id, transaction_id, from_status, to_status, reason, actor
) VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: ReferenceExists :one
SELECT EXISTS(
    SELECT 1 FROM transactions WHERE reference = $1
)::bool AS present;
