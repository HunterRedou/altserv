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
