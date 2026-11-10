-- name: GetUserByID :one
SELECT * FROM users
WHERE id = $1
  AND tenant_id = $2;
