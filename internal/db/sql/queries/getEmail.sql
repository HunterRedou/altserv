-- name: GetByEmail :one
SELECT * FROM users
INNER JOIN firms
  ON users.email = firms.email
WHERE users.email = $1;

