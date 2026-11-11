-- name: ClaimKey :one
-- Insert a fresh in_progress row, or reclaim an expired one.
-- RETURNING is empty when the key already exists and is still live.
INSERT INTO idempotency_keys (
    user_id,
    key,
    request_hash,
    state,
    created_at,
    expires_at
) VALUES (
    $1, $2, $3, 'in_progress', $4, $5
)
ON CONFLICT (user_id, key) DO UPDATE
SET
    request_hash = EXCLUDED.request_hash,
    state = 'in_progress',
    response_code = NULL,
    response_body = NULL,
    response_content_type = NULL,
    created_at = EXCLUDED.created_at,
    expires_at = EXCLUDED.expires_at
WHERE idempotency_keys.expires_at <= EXCLUDED.created_at
RETURNING *;

-- name: GetKey :one
SELECT *
FROM idempotency_keys
WHERE user_id = $1 AND key = $2;

-- name: CompleteKey :one
UPDATE idempotency_keys
SET
    state = 'completed',
    response_code = $3,
    response_body = $4,
    response_content_type = $5
WHERE user_id = $1
  AND key = $2
  AND state = 'in_progress'
RETURNING *;

-- name: ReleaseKey :exec
DELETE FROM idempotency_keys
WHERE user_id = $1
  AND key = $2
  AND state = 'in_progress';

-- name: DeleteExpired :execrows
DELETE FROM idempotency_keys
WHERE expires_at <= $1;
