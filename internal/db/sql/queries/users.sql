-- name: CreateUser :one 
INSERT INTO users (id, created_at, updated_at, name, is_admin, is_teamhead, hashed_password, email, firm_id)
VALUES (
  gen_random_uuid(),
  NOW(),
  NOW(),
  $1,
  $2,
  $3,
  $4,
  $5,
  $6
)
RETURNING *;
