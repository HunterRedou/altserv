-- name: SelectPendingTenants :many
SELECT * FROM tenants
WHERE status = 'pending';

-- name: GetTenantStatusForUpdate :one
SELECT status FROM tenants
WHERE id = $1
FOR UPDATE;
