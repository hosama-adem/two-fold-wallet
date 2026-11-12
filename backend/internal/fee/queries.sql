-- name: GetEffectiveFeeRule :one
SELECT *
FROM fee_rules
WHERE kind = $1
  AND tier = $2
  AND effective_from <= $3
ORDER BY effective_from DESC
LIMIT 1;

-- name: GetUserKYCTier :one
SELECT kyc_tier
FROM users
WHERE id = $1;
