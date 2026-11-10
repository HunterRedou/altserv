-- name: SelectPendingTenants :many
SELECT * FROM tenants
WHERE status = 'pending';
