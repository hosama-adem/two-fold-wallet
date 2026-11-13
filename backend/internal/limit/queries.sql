-- name: GetTierLimits :one
SELECT tier, max_balance_minor, per_txn_minor, daily_minor, monthly_minor, can_withdraw
FROM tier_limits
WHERE tier = $1;

-- name: GetUserKYCTier :one
SELECT kyc_tier
FROM users
WHERE id = $1;

-- name: SumOutgoingDebitsSince :one
-- Debits on the user's liability accounts = money that left the wallet
-- (includes fees). Window start is an absolute timestamptz (Addis day/month).
SELECT COALESCE(SUM(p.amount_minor), 0)::bigint AS total
FROM postings p
JOIN accounts a ON a.id = p.account_id
WHERE a.owner_user_id = sqlc.arg(user_id)
  AND a.type = 'liability'
  AND p.direction = 'debit'
  AND p.created_at >= sqlc.arg(since);

-- name: GetUserLedgerBalance :one
SELECT COALESCE(a.balance_minor, 0)::bigint AS balance_minor
FROM wallets w
JOIN accounts a ON a.id = w.account_id
WHERE w.user_id = $1
  AND w.currency = $2;
