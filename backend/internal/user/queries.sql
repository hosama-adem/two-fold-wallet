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

-- name: GetUserByPhoneForUpdate :one
-- Serializes concurrent phone verification for one person.
SELECT * FROM users
WHERE phone_e164 = $1
FOR UPDATE;

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

-- name: UpdateUserPinHash :one
UPDATE users
SET
    pin_hash = $2,
    pin_failed_count = 0,
    pin_locked_until = NULL,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: RecordPinFailure :one
-- Atomically bump the failure counter; lock when the attempt that hits max_failures lands.
UPDATE users
SET
    pin_failed_count = pin_failed_count + 1,
    pin_locked_until = CASE
        WHEN pin_failed_count < sqlc.arg(max_failures)
             AND pin_failed_count + 1 >= sqlc.arg(max_failures)
            THEN sqlc.arg(locked_until)
        ELSE pin_locked_until
    END,
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ResetPinLockout :one
UPDATE users
SET
    pin_failed_count = 0,
    pin_locked_until = NULL,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CreateOTP :one
INSERT INTO otp_codes (
    id,
    phone_e164,
    purpose,
    code_hash,
    attempts,
    expires_at,
    created_at
) VALUES (
    $1, $2, $3, $4, 0, $5, $6
)
RETURNING *;

-- name: GetLatestOTPByPhonePurpose :one
SELECT * FROM otp_codes
WHERE phone_e164 = $1 AND purpose = $2
ORDER BY created_at DESC
LIMIT 1;

-- name: GetLatestActiveOTPByPhonePurpose :one
SELECT * FROM otp_codes
WHERE phone_e164 = $1
  AND purpose = $2
  AND consumed_at IS NULL
ORDER BY created_at DESC
LIMIT 1;

-- name: IncrementOTPAttempts :one
UPDATE otp_codes
SET attempts = attempts + 1
WHERE id = $1
RETURNING *;

-- name: ConsumeOTP :one
UPDATE otp_codes
SET consumed_at = now()
WHERE id = $1
  AND consumed_at IS NULL
RETURNING *;
