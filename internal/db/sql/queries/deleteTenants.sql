-- name: DeleteTenant :exec 
DELETE FROM tenants
WHERE id = $1;
