-- name: SelectFirms :many
SELECT * FROM firms
WHERE status = 'pending';
