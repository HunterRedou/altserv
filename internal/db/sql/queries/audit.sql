-- name: CreateAuditEvent :one
INSERT INTO audit_events (
  tenant_id, actor_user_id, action, entity_type, entity_id, request_id, metadata
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;
