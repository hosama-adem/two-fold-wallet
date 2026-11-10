-- name: LockUser :one
SELECT id, status FROM users
WHERE id = $1
FOR UPDATE;

-- name: GetAccountByCode :one
SELECT * FROM accounts
WHERE code = $1;

-- name: InsertLiabilityAccount :one
INSERT INTO accounts (
    id,
    code,
    type,
    normal_balance,
    currency,
    allow_negative,
    owner_user_id
) VALUES (
    $1, $2, 'liability', 'credit', $3, false, $4
)
RETURNING *;

-- name: InsertWallet :one
INSERT INTO wallets (
    id,
    user_id,
    account_id,
    currency,
    status,
    freeze_mode
) VALUES (
    $1, $2, $3, $4, 'active', 'none'
)
RETURNING *;

-- name: GetWalletByUserCurrency :one
SELECT * FROM wallets
WHERE user_id = $1 AND currency = $2;

-- name: ListWalletBalancesByUser :many
SELECT
    id,
    user_id,
    account_id,
    currency,
    status,
    freeze_mode,
    ledger_balance_minor,
    pending_withdrawal_minor,
    available_minor
FROM wallet_balances
WHERE user_id = $1
ORDER BY currency;

-- name: LookupRecipientByPhone :one
SELECT display_name, handle, phone_e164
FROM users
WHERE phone_e164 = $1
  AND status = 'active'
  AND phone_verified_at IS NOT NULL;

-- name: LookupRecipientByHandle :one
SELECT display_name, handle, phone_e164
FROM users
WHERE handle = $1
  AND status = 'active'
  AND phone_verified_at IS NOT NULL;

-- name: GetTransactionByReference :one
SELECT id FROM transactions
WHERE reference = $1;

-- name: InsertCompletedTopUp :one
INSERT INTO transactions (
    id,
    reference,
    kind,
    status,
    initiator_user_id,
    to_wallet_id,
    amount_minor,
    fee_minor,
    currency,
    note,
    completed_at
) VALUES (
    $1, $2, 'top_up', 'completed', $3, $4, $5, 0, $6, $7, now()
)
RETURNING id;
