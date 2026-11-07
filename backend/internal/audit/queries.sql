-- name: InsertAuditLog :one
INSERT INTO audit_logs (
    id,
    actor_user_id,
    actor_kind,
    action,
    target_type,
    target_id,
    summary,
    ip,
    user_agent,
    request_id,
    created_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
RETURNING *;
