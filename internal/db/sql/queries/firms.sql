-- name: CreateFirm :one 
INSERT INTO firms (id, created_at, updated_at, email, ustId, streetName, plz, hashed_password)
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
