-- name: DeleteFirm :exec 
DELETE FROM firms
WHERE id = $1;
