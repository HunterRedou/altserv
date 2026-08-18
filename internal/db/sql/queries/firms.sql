-- name: CreateFirm :one 
INSERT INTO firms (id, created_at, updated_at, email, ustId, streetName, plz)
VALUES (
  gen_random_uuid(),
  NOW(),
  NOW(),
  $1,
  $2,
  $3,
  $4
)
RETURNING *;
