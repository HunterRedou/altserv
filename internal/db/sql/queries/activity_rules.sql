-- name: CreateActivityServiceRule :one
INSERT INTO activity_service_rules (
  tenant_id, service_definition_id, contract_id, activity_label,
  legal_area, criteria, valid_from, valid_until
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListActivityServiceRules :many
SELECT * FROM activity_service_rules
WHERE tenant_id = $1
ORDER BY activity_label, valid_from DESC;
