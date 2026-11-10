-- name: CreateAssignment :one
INSERT INTO patient_assignments (
  tenant_id, patient_id, employee_id, assigned_by,
  assignment_type, valid_from, valid_until
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetAssignment :one
SELECT * FROM patient_assignments
WHERE id = $1
  AND tenant_id = $2;

-- name: RevokeAssignment :one
UPDATE patient_assignments
SET revoked_at = NOW()
WHERE id = $1
  AND tenant_id = $2
  AND revoked_at IS NULL
RETURNING *;

-- name: ListAssignmentsByPatient :many
SELECT * FROM patient_assignments
WHERE tenant_id = $1
  AND patient_id = $2
ORDER BY created_at DESC;

-- name: ListAssignmentsByTenant :many
SELECT * FROM patient_assignments
WHERE tenant_id = $1
ORDER BY created_at DESC;

-- name: IsPatientAssignedToEmployee :one
SELECT EXISTS (
  SELECT 1
  FROM patient_assignments
  WHERE tenant_id = $1
    AND patient_id = $2
    AND employee_id = $3
    AND revoked_at IS NULL
    AND valid_from <= clock_timestamp()
    AND (valid_until IS NULL OR valid_until > clock_timestamp())
);
