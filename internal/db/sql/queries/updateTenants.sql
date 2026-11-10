-- name: UpdateTenantStatus :one 
UPDATE tenants
SET status = $2,
    updated_at = NOW()
WHERE id = $1
RETURNING *;
