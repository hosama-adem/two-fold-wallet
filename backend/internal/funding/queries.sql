-- name: GetWalletByUserCurrency :one
SELECT * FROM wallets
WHERE user_id = $1 AND currency = $2;

-- name: LockWalletBalance :one
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

-- name: GetUserPhone :one
SELECT phone_e164 FROM users
WHERE id = $1 AND status = 'active' AND phone_verified_at IS NOT NULL;

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

-- name: GetTransactionByID :one
SELECT * FROM transactions WHERE id = $1;

-- name: GetTransactionByIDForUpdate :one
SELECT * FROM transactions WHERE id = $1 FOR UPDATE;

-- name: TransitionTransaction :one
UPDATE transactions
SET status = @to_status,
    completed_at = CASE
        WHEN @to_status = 'completed' THEN now()
        WHEN @to_status IN ('failed', 'expired', 'needs_review') THEN COALESCE(completed_at, now())
        ELSE completed_at
    END
WHERE id = @id
  AND status = @from_status
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

-- name: InsertProviderPayment :one
INSERT INTO provider_payments (
    id, transaction_id, provider, direction, provider_ref, status,
    amount_minor, currency, raw, destination_id, reserve_journal_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
RETURNING *;

-- name: InsertPayoutDestination :one
INSERT INTO payout_destinations (
    id, user_id, provider, kind, label,
    identifier_ciphertext, identifier_nonce, identifier_masked
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: ListPayoutDestinations :many
SELECT * FROM payout_destinations
WHERE user_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC;

-- name: GetPayoutDestination :one
SELECT * FROM payout_destinations
WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL;

-- name: GetPayoutDestinationIncludingDeleted :one
SELECT * FROM payout_destinations
WHERE id = $1 AND user_id = $2;

-- name: SoftDeletePayoutDestination :one
UPDATE payout_destinations
SET deleted_at = now()
WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
RETURNING *;

-- name: GetAvailableBalance :one
SELECT available_minor FROM wallet_balances WHERE wallet_id = $1;

-- name: GetProviderPaymentByRef :one
SELECT * FROM provider_payments
WHERE provider_ref = $1
ORDER BY created_at ASC
LIMIT 1;

-- name: GetProviderPaymentByTxn :one
SELECT * FROM provider_payments
WHERE transaction_id = $1;

-- name: LockProviderPaymentByRef :one
SELECT * FROM provider_payments
WHERE provider_ref = $1
ORDER BY created_at ASC
LIMIT 1
FOR UPDATE;

-- name: UpdateProviderPaymentStatus :one
UPDATE provider_payments
SET status = @status,
    raw = COALESCE(@raw, raw),
    updated_at = now()
WHERE id = @id
  AND status = @from_status
RETURNING *;

-- name: InsertWebhookEvent :execrows
INSERT INTO webhook_events (provider, event_id, payload_hash, transaction_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (provider, event_id) DO NOTHING;

-- name: ListStalePendingTopups :many
SELECT pp.*
FROM provider_payments pp
JOIN transactions t ON t.id = pp.transaction_id
WHERE pp.direction = 'collection'
  AND pp.status = 'pending'
  AND t.status = 'pending'
  AND t.kind = 'top_up'
  AND pp.created_at <= @older_than
ORDER BY pp.created_at ASC
LIMIT @row_limit;
