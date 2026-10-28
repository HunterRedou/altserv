-- name: UpdateFirms :one 
UPDATE firms
SET status='valid'
WHERE id=$1
RETURNING *;
