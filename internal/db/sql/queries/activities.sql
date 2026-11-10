-- name: CreateActivity :one
INSERT INTO activities (
  id, tenant_id, patient_id, employee_id, service_definition_id, contract_id,
  performed_at, duration_minutes, note_enc, note_key_version, status,
  service_code, service_name, legal_area, contract_reference, payer_name,
  created_by
)
VALUES (
  $1, $2, $3, $4, $5, $6,
  $7, $8, $9, $10, $11,
  $12, $13, $14, $15, $16,
  $17
)
RETURNING *;

-- name: GetActivity :one
SELECT * FROM activities
WHERE id = $1
  AND tenant_id = $2;

-- name: ListActivitiesByPatient :many
SELECT * FROM activities
WHERE tenant_id = $1
  AND patient_id = $2
ORDER BY performed_at DESC, created_at DESC
LIMIT sqlc.arg(max_results);

-- name: ListActivities :many
SELECT * FROM activities
WHERE tenant_id = $1
ORDER BY performed_at DESC, created_at DESC
LIMIT sqlc.arg(max_results);
