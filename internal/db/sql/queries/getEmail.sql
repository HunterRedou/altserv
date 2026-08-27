-- name: GetByEmail :one
SELECT * FROM users
INNER JOIN firms
  ON users.email = firms.email AND users.hashed_password = firms.hashed_password
WHERE users.email = $1;

