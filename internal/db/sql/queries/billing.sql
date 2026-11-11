-- name: GetReportForBilling :one
SELECT * FROM reports
WHERE id = $1
  AND tenant_id = $2;

-- name: ListBillingCandidates :many
SELECT
  ra.id AS report_activity_id,
  ra.activity_id,
  ra.quantity,
  ra.performed_at AS report_performed_at,
  ra.sort_order,
  a.status AS activity_status,
  a.service_definition_id,
  a.contract_id,
  a.performed_at AS activity_performed_at,
  a.service_code AS snapshot_service_code,
  a.service_name AS snapshot_service_name,
  a.legal_area AS snapshot_legal_area,
  a.payer_name AS snapshot_payer_name,
  a.contract_reference AS snapshot_contract_reference,
  sd.code AS definition_code,
  sd.name AS definition_name,
  sd.legal_area AS definition_legal_area,
  sd.valid_from AS definition_valid_from,
  sd.valid_until AS definition_valid_until,
  c.payer_name AS contract_payer_name,
  c.contract_reference AS contract_reference,
  c.legal_area AS contract_legal_area,
  c.valid_from AS contract_valid_from,
  c.valid_until AS contract_valid_until
FROM report_activities ra
JOIN activities a
  ON a.id = ra.activity_id
  AND a.tenant_id = ra.tenant_id
LEFT JOIN service_definitions sd ON sd.id = a.service_definition_id
LEFT JOIN tenant_service_contracts c
  ON c.id = a.contract_id
  AND c.tenant_id = a.tenant_id
WHERE ra.report_id = $1
  AND ra.tenant_id = $2
ORDER BY ra.sort_order, ra.performed_at, ra.created_at;

-- name: CreateBillingRun :one
INSERT INTO billing_runs (id, tenant_id, report_id, status, generated_by)
VALUES ($1, $2, $3, 'draft', $4)
RETURNING *;

-- name: GetBillingRun :one
SELECT * FROM billing_runs
WHERE id = $1
  AND tenant_id = $2;

-- name: GetActiveBillingRunByReport :one
SELECT * FROM billing_runs
WHERE tenant_id = $1
  AND report_id = $2
  AND status IN ('draft', 'approved')
ORDER BY generated_at DESC
LIMIT 1;

-- name: ListBillingRunsByReport :many
SELECT * FROM billing_runs
WHERE tenant_id = $1
  AND report_id = $2
ORDER BY generated_at DESC;

-- name: TouchBillingRun :one
UPDATE billing_runs
SET version = version + 1,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
  AND status = 'draft'
  AND version = $3
RETURNING *;

-- name: ApproveBillingRun :one
UPDATE billing_runs
SET status = 'approved',
    approved_at = clock_timestamp(),
    approved_by = $3,
    version = version + 1,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
  AND status = 'draft'
  AND version = $4
RETURNING *;

-- name: VoidBillingRun :one
UPDATE billing_runs
SET status = 'voided',
    voided_at = clock_timestamp(),
    voided_by = $3,
    version = version + 1,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
  AND status IN ('draft', 'approved')
  AND version = $4
RETURNING *;

-- name: DeleteBillingLines :exec
DELETE FROM billing_lines
WHERE run_id = $1
  AND tenant_id = $2;

-- name: DeleteBillingFindings :exec
DELETE FROM billing_findings
WHERE run_id = $1
  AND tenant_id = $2;

-- name: InsertBillingLine :one
INSERT INTO billing_lines (
  tenant_id, run_id, report_activity_id, activity_id, service_definition_id,
  contract_id, quantity, performed_at, service_code, service_name, legal_area,
  payer_name, contract_reference, sort_order
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING *;

-- name: InsertBillingFinding :one
INSERT INTO billing_findings (
  tenant_id, run_id, severity, code, message, activity_id
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListBillingLines :many
SELECT * FROM billing_lines
WHERE run_id = $1
  AND tenant_id = $2
ORDER BY sort_order, performed_at, created_at;

-- name: ListBillingFindings :many
SELECT * FROM billing_findings
WHERE run_id = $1
  AND tenant_id = $2
ORDER BY created_at, id;

-- name: GetBillingLineByActivity :one
SELECT * FROM billing_lines
WHERE tenant_id = $1
  AND activity_id = $2;

-- name: GetBillingServiceDefinition :one
SELECT * FROM service_definitions
WHERE id = $1;

-- name: CreateBillingRule :one
INSERT INTO billing_rules (
  tenant_id, kind, service_definition_id, related_service_definition_id,
  legal_area, description, valid_from, valid_until
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetBillingRule :one
SELECT * FROM billing_rules
WHERE id = $1
  AND tenant_id = $2;

-- name: ListBillingRules :many
SELECT * FROM billing_rules
WHERE tenant_id = $1
ORDER BY kind, service_definition_id, related_service_definition_id, valid_from DESC;
