-- name: CreateUser :one 
INSERT INTO users (id, created_at, updated_at, name, role, hashed_password, email, tenant_id)
VALUES (
  gen_random_uuid(),
  NOW(),
  NOW(),
  $1,
  $2,
  $3,
  $4,
  $5
)
RETURNING *;

-- name: UpdateUserRole :one
UPDATE users
SET role = $3,
    updated_at = NOW()
WHERE id = $1
  AND tenant_id = $2
RETURNING *;

-- name: CountChefsByTenant :one
SELECT count(*) FROM users
WHERE tenant_id = $1
  AND role = 'chef';
