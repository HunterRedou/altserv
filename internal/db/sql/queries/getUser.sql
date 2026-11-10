-- name: ListUsersByTenant :many
SELECT * FROM users
WHERE tenant_id = $1;
