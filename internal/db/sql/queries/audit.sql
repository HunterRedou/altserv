-- name: CreateAuditEvent :one
INSERT INTO audit_events (
  tenant_id, actor_user_id, action, entity_type, entity_id, request_id, metadata
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListAuditEvents :many
SELECT *
FROM audit_events
WHERE tenant_id = $1
  AND (sqlc.arg(action)::text = '' OR action = sqlc.arg(action)::text)
  AND (sqlc.arg(entity_type)::text = '' OR entity_type = sqlc.arg(entity_type)::text)
  AND (sqlc.narg(entity_id)::uuid IS NULL OR entity_id = sqlc.narg(entity_id)::uuid)
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR occurred_at >= sqlc.narg(from_time)::timestamptz)
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR occurred_at < sqlc.narg(to_time)::timestamptz)
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg(row_limit);
