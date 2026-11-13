-- name: InsertSubmission :one
INSERT INTO kyc_submissions (
    id,
    user_id,
    target_tier,
    data,
    document_keys,
    status
) VALUES (
    $1, $2, $3, $4, $5, 'submitted'
)
RETURNING id, user_id, target_tier, data, document_keys, status, reviewed_by, reviewed_at, reason, created_at;

-- name: GetUserKYCTier :one
SELECT kyc_tier
FROM users
WHERE id = $1;
