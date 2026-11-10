-- name: GetByEmail :one
SELECT * FROM users
INNER JOIN tenants
  ON tenants.id = users.tenant_id
WHERE users.email = $1;
