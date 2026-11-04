-- name: CreateUser :one
INSERT INTO users (
    id,
    phone_e164,
    handle,
    display_name,
    password_hash,
    kyc_tier,
    status
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users
WHERE id = $1;

-- name: GetUserByPhone :one
SELECT * FROM users
WHERE phone_e164 = $1;

-- name: MarkPhoneVerified :one
UPDATE users
SET
    phone_verified_at = now(),
    status = CASE
        WHEN status = 'pending_verification' THEN 'active'
        ELSE status
    END,
    updated_at = now()
WHERE id = $1
RETURNING *;
