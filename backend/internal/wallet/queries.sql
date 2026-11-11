-- name: InsertLiabilityAccount :one
INSERT INTO accounts (
    id, code, type, normal_balance, currency, allow_negative, owner_user_id
) VALUES (
    $1, $2, 'liability', 'credit', $3, false, $4
)
RETURNING *;

-- name: InsertWallet :one
INSERT INTO wallets (
    id, user_id, account_id, currency
) VALUES (
    $1, $2, $3, $4
)
RETURNING *;

-- name: GetWalletByUserCurrency :one
SELECT * FROM wallets
WHERE user_id = $1 AND currency = $2;

-- name: ListBalancesByUserID :many
SELECT
    wallet_id,
    user_id,
    account_id,
    currency,
    status,
    freeze_mode,
    frozen_reason,
    ledger_balance_minor,
    held_minor,
    available_minor
FROM wallet_balances
WHERE user_id = $1
ORDER BY currency, wallet_id;

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
