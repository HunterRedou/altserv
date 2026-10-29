-- name: GetByEmail :one
SELECT * FROM users
INNER JOIN firms
  ON firms.id = users.firm_id
WHERE users.email = $1;
