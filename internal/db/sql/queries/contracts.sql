-- name: CreateTenantServiceContract :one
INSERT INTO tenant_service_contracts (
  tenant_id, service_definition_id, payer_name, contract_reference,
  legal_area, valid_from, valid_until
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetTenantServiceContract :one
SELECT * FROM tenant_service_contracts
WHERE id = $1
  AND tenant_id = $2;

-- name: ListTenantServiceContracts :many
SELECT * FROM tenant_service_contracts
WHERE tenant_id = $1
ORDER BY valid_from DESC, payer_name;
