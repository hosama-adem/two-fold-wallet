-- name: CreateSession :one
INSERT INTO sessions (
    id,
    user_id,
    family_id,
    refresh_hash,
    user_agent,
    ip,
    expires_at,
    created_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: GetSessionByID :one
SELECT * FROM sessions
WHERE id = $1;

-- name: GetSessionByRefreshHash :one
SELECT * FROM sessions
WHERE refresh_hash = $1;

-- name: GetSessionByRefreshHashForUpdate :one
SELECT * FROM sessions
WHERE refresh_hash = $1
FOR UPDATE;

-- name: MarkSessionReplaced :one
UPDATE sessions
SET
    revoked_at = now(),
    replaced_by = $2
WHERE id = $1
  AND revoked_at IS NULL
  AND replaced_by IS NULL
RETURNING *;

-- name: RevokeSession :one
UPDATE sessions
SET revoked_at = now()
WHERE id = $1
  AND revoked_at IS NULL
RETURNING *;

-- name: RevokeSessionFamily :execrows
UPDATE sessions
SET revoked_at = now()
WHERE family_id = $1
  AND revoked_at IS NULL;

-- name: ListActiveSessionsByUser :many
SELECT * FROM sessions
WHERE user_id = $1
  AND revoked_at IS NULL
  AND expires_at > $2
ORDER BY created_at DESC;

-- name: RevokeSessionForUser :one
UPDATE sessions
SET revoked_at = now()
WHERE id = $1
  AND user_id = $2
  AND revoked_at IS NULL
RETURNING *;
